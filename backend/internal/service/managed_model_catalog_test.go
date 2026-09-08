package service

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type managedCatalogRepo struct {
	AccountRepository
	accounts []Account
}

func (r *managedCatalogRepo) ListSchedulableByGroupID(context.Context, int64) ([]Account, error) {
	return r.accounts, nil
}
func (r *managedCatalogRepo) ListModelAvailabilityCandidates(context.Context, *int64, []string, bool) ([]Account, error) {
	return r.accounts, nil
}

func TestManagedModelCatalogUsesLivePublishedMembersWithoutPrivateCanonicalKeys(t *testing.T) {
	group, account := managedRouteTestFixture(23, "dmx/sol-ssvip")
	mapping, ok := account.Credentials["model_mapping"].(map[string]any)
	require.True(t, ok)
	delete(mapping, "gpt-5.6-sol")
	account.Extra = map[string]any{ModelContextOverridesExtraKey: map[string]int64{"dmx/sol-ssvip": 333333}}
	repo := &managedCatalogRepo{accounts: []Account{*account}}
	channels := &ChannelService{}
	channels.cache.Store(populateChannelCache([]Channel{{ID: 7, Status: StatusActive, GroupIDs: []int64{group.ID}, BillingModelSource: BillingModelSourceRequested,
		ModelMapping: map[string]map[string]string{PlatformOpenAI: {"gpt-5.6-sol": group.ManagedModelRoutes.Routes[0].Selector}}}}, map[int64]string{group.ID: PlatformOpenAI}))
	svc := &GatewayService{accountRepo: repo, channelService: channels}
	ordinary, err := svc.ManagedPublicModelIDs(context.Background(), group, "")
	require.NoError(t, err)
	codex, err := svc.ManagedPublicModelIDs(context.Background(), group, CompositeRouteEndpointResponses)
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-5.6-sol"}, ordinary)
	require.Equal(t, ordinary, codex)
	body, err := svc.BuildCodexModelsManifestForGroup(context.Background(), group, "", codex)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(body, "models.0.slug").String())
	require.Equal(t, int64(333333), gjson.GetBytes(body, "models.0.context_window").Int())
	require.NotContains(t, string(body), "s2pub-")
	require.NotContains(t, string(body), "dmx/sol-ssvip")
	currentMapping, ok := account.Credentials["model_mapping"].(map[string]any)
	require.True(t, ok)
	_, privateKeyCreated := currentMapping["gpt-5.6-sol"]
	require.False(t, privateKeyCreated)
	repo.accounts[0].Schedulable = false
	models, err := svc.ManagedPublicModelIDs(context.Background(), group, "")
	require.NoError(t, err)
	require.Empty(t, models, "no live member must not fall back to an advertised static route")
	repo.accounts[0].Schedulable = true
	repo.accounts[0].Credentials = map[string]any{"api_key": "changed-key", "model_mapping": account.Credentials["model_mapping"]}
	models, err = svc.ManagedPublicModelIDs(context.Background(), group, "")
	require.NoError(t, err)
	require.Empty(t, models, "stale fingerprints must disappear from both catalog shapes")
}

func TestManagedModelCompactCannotRetryThroughPrivateFallback(t *testing.T) {
	group, account := managedRouteTestFixture(23, "gpt-5.6-sol")
	selector := group.ManagedModelRoutes.Routes[0].Selector
	account.Credentials["compact_model_mapping"] = map[string]any{selector: "private-compact-model"}
	request, err := ResolveManagedModelRoute(group, "gpt-5.6-sol", CompositeRouteEndpointResponses)
	require.NoError(t, err)
	ctx := WithManagedModelRequest(context.Background(), request)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses/compact", nil).WithContext(ctx)
	body := []byte(`{"model":"gpt-5.6-sol","input":[]}`)
	svc := &OpenAIGatewayService{}
	_, _, retry := svc.prepareOpenAICompactFallbackRetry(c, account, selector, body, 400, "model not found", []byte(`{"error":{"code":"model_not_found"}}`), false)
	require.False(t, retry, "managed compact cannot switch to a preserved private compact-only target")
	_, normal := resolveOpenAIForwardMappedModels(account, selector, false)
	require.Equal(t, "gpt-5.6-sol", normal)
	c.Request = c.Request.WithContext(context.Background())
	_, target, retry := svc.prepareOpenAICompactFallbackRetry(c, account, selector, body, 400, "model not found", []byte(`{"error":{"code":"model_not_found"}}`), false)
	require.True(t, retry, "unmanaged compact retains its existing configured fallback")
	require.Equal(t, "private-compact-model", target)
}

func TestManagedModelCatalogCapacityIncludesTemporarilyCoolingPublishedMember(t *testing.T) {
	group, first := managedRouteTestFixture(23, "verified-wire")
	first.Extra = map[string]any{ModelContextOverridesExtraKey: map[string]int64{"verified-wire": 333333}}
	second := *first
	second.ID = 42
	reset := time.Now().Add(time.Hour)
	second.RateLimitResetAt = &reset
	second.Extra = map[string]any{ModelContextOverridesExtraKey: map[string]int64{"verified-wire": 111111}}
	member := group.ManagedModelRoutes.Routes[0].Accounts[0]
	member.AccountID = second.ID
	member.AccountFingerprint = ManagedModelAccountFingerprint(&second)
	group.ManagedModelRoutes.Routes[0].Accounts = append(group.ManagedModelRoutes.Routes[0].Accounts, member)
	repo := &managedCatalogRepo{accounts: []Account{*first, second}}
	channels := &ChannelService{}
	channels.cache.Store(populateChannelCache([]Channel{{ID: 7, Status: StatusActive, GroupIDs: []int64{group.ID}, BillingModelSource: BillingModelSourceRequested,
		ModelMapping: map[string]map[string]string{PlatformOpenAI: {"gpt-5.6-sol": group.ManagedModelRoutes.Routes[0].Selector}}}}, map[int64]string{group.ID: PlatformOpenAI}))
	svc := &GatewayService{accountRepo: repo, channelService: channels}
	models, err := svc.ManagedPublicModelIDs(context.Background(), group, CompositeRouteEndpointResponses)
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-5.6-sol"}, models)
	body, err := svc.BuildCodexModelsManifestForGroup(context.Background(), group, "", models)
	require.NoError(t, err)
	require.Equal(t, int64(111111), gjson.GetBytes(body, "models.0.context_window").Int(), "temporary cooldown must not widen a published model's planning capacity")
	repo.accounts[0].RateLimitResetAt = &reset
	models, err = svc.ManagedPublicModelIDs(context.Background(), group, CompositeRouteEndpointResponses)
	require.NoError(t, err)
	require.Empty(t, models, "but a route with no presently usable members is not advertised")
}
