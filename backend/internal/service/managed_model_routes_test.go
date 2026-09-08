package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func managedRouteTestFixture(groupID int64, upstream string) (*Group, *Account) {
	public := "gpt-5.6-sol"
	selector := ManagedModelSelector(groupID, public)
	account := &Account{ID: 41, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, GroupIDs: []int64{groupID},
		Credentials: map[string]any{"api_key": "synthetic-not-a-real-key", "base_url": "https://upstream.example/v1", "model_mapping": map[string]any{public: "private-ssvip", selector: upstream}}}
	endpoints := []string{CompositeRouteEndpointResponses, CompositeRouteEndpointMessages, CompositeRouteEndpointCountTokens, CompositeRouteEndpointChatCompletions, ManagedModelEndpointResponsesWebSocket}
	group := &Group{ID: groupID, Platform: PlatformOpenAI, Status: StatusActive, AllowMessagesDispatch: true,
		ModelAllowlist:              GroupModelAllowlist{Enabled: true, Models: []string{public}},
		MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{ExactModelMappings: map[string]string{public: selector}},
		ManagedModelRoutes: ManagedModelRoutesConfig{Version: 1, Enabled: true, Routes: []ManagedModelRoute{{PublicModel: public, Aliases: []string{"gpt-5.6"}, Selector: selector, TargetPlatform: PlatformOpenAI, Endpoints: endpoints,
			Accounts: []ManagedModelRouteAccount{{AccountID: account.ID, UpstreamModel: upstream, AccountFingerprint: ManagedModelAccountFingerprint(account), Endpoints: endpoints}}}}}}
	return group, account
}

func TestManagedModelRoutesResolveAndPreserveAliasSemantics(t *testing.T) {
	group, _ := managedRouteTestFixture(23, "gpt-5.6-sol")
	for _, tc := range []struct {
		name, model string
		wantError   bool
	}{
		{"standard", "gpt-5.6-sol", false},
		{"case and whitespace", "  GPT-5.6-SOL\t", false},
		{"explicit alias", "gpt-5.6", false},
		{"official effort alias", "openai/gpt-5.6-sol-high", false},
		{"internal selector", group.ManagedModelRoutes.Routes[0].Selector, true},
		{"unknown", "private-ssvip", true},
		{"unknown namespace", "other/gpt-5.6-sol-high", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := ResolveManagedModelRoute(group, tc.model, CompositeRouteEndpointResponses)
			if tc.wantError {
				require.ErrorIs(t, err, ErrManagedModelRouteUnavailable)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "gpt-5.6-sol", request.Route.PublicModel)
		})
	}
	body, request, err := PrepareManagedModelRequest(group, CompositeRouteEndpointResponses, []byte(`{"model":"openai/gpt-5.6-sol-high","input":[],"unknown":{"large":9007199254740993}}`), "")
	require.NoError(t, err)
	require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(body, "model").String())
	require.Equal(t, "high", gjson.GetBytes(body, "reasoning.effort").String())
	require.Equal(t, "9007199254740993", gjson.GetBytes(body, "unknown.large").Raw)
	require.NotNil(t, request)
	body, _, err = PrepareManagedModelRequest(group, CompositeRouteEndpointMessages, []byte(`{"Model":"gpt-5.6-sol-high","messages":[],"output_config":{"effort":"low"}}`), "")
	require.NoError(t, err)
	require.Equal(t, "low", gjson.GetBytes(body, "output_config.effort").String())
	require.False(t, gjson.GetBytes(body, "Model").Exists())
	for _, body := range []string{`{"model":"gpt-5.6-sol","Model":"gpt-5.6-sol"}`, `{"model":"gpt-5.6-sol","model":"private-ssvip"}`, `{"model":null}`, `{}`} {
		_, _, err := PrepareManagedModelRequest(group, CompositeRouteEndpointResponses, []byte(body), "")
		require.ErrorIs(t, err, ErrManagedModelRouteUnavailable)
	}
	_, err = ResolveManagedModelRoute(group, "gpt-5.6-sol", "images")
	require.ErrorIs(t, err, ErrManagedModelRouteUnavailable)
	group.ManagedModelRoutes.Routes = append(group.ManagedModelRoutes.Routes, group.ManagedModelRoutes.Routes[0])
	_, err = ResolveManagedModelRoute(group, "gpt-5.6-sol", CompositeRouteEndpointResponses)
	require.ErrorIs(t, err, ErrManagedModelRouteUnavailable)
}

func TestManagedModelRoutesSeparatePublicTiersWithoutChangingPrivateMapping(t *testing.T) {
	standard, account := managedRouteTestFixture(23, "gpt-5.6-sol")
	vip, _ := managedRouteTestFixture(33, "gpt-5.6-sol-ssvip")
	mapping, ok := account.Credentials["model_mapping"].(map[string]any)
	require.True(t, ok)
	mapping[vip.ManagedModelRoutes.Routes[0].Selector] = "gpt-5.6-sol-ssvip"
	for _, group := range []*Group{standard, vip} {
		request, err := ResolveManagedModelRoute(group, "gpt-5.6-sol", CompositeRouteEndpointResponses)
		require.NoError(t, err)
		ctx := WithManagedModelRequest(context.Background(), request)
		require.True(t, ManagedModelAccountAllowed(ctx, account, request.Route.Selector))
		require.False(t, ManagedModelAccountAllowed(ctx, account, "gpt-5.6-sol"), "must never fall back to private original mapping")
	}
	require.Equal(t, "private-ssvip", account.GetMappedModel("gpt-5.6-sol"))
	request, err := ResolveManagedModelRoute(standard, "gpt-5.6-sol", CompositeRouteEndpointResponses)
	require.NoError(t, err)
	ctx := WithManagedModelRequest(context.Background(), request)
	mapping[request.Route.Selector] = "gpt-5.6-sol-ssvip"
	account.modelMappingCacheReady = false
	require.False(t, ManagedModelAccountAllowed(ctx, account, request.Route.Selector), "manual target changes must invalidate publication")
}

func TestManagedModelRoutesFingerprintAndMemberGuard(t *testing.T) {
	group, account := managedRouteTestFixture(23, "gpt-5.6-sol")
	request, err := ResolveManagedModelRoute(group, "gpt-5.6-sol", CompositeRouteEndpointResponses)
	require.NoError(t, err)
	ctx := WithManagedModelRequest(context.Background(), request)
	original := ManagedModelAccountFingerprint(account)
	account.Priority = 500
	account.GroupIDs = []int64{23, 12}
	account.Extra = map[string]any{"quota_used_usd": 100}
	require.Equal(t, original, ManagedModelAccountFingerprint(account))
	require.True(t, ManagedModelAccountAllowed(ctx, account, request.Route.Selector))
	account.Extra["openai_responses_mode"] = "force_chat_completions"
	require.NotEqual(t, original, ManagedModelAccountFingerprint(account))
	require.False(t, ManagedModelAccountAllowed(ctx, account, request.Route.Selector))
	delete(account.Extra, "openai_responses_mode")
	account.Extra["openai_passthrough"] = true
	require.False(t, ManagedModelAccountAllowed(ctx, account, request.Route.Selector))
	delete(account.Extra, "openai_passthrough")
	account.ID++
	require.False(t, ManagedModelAccountAllowed(ctx, account, request.Route.Selector))
	account.ID--
	request.Route.Accounts[0].Endpoints = []string{CompositeRouteEndpointMessages}
	ctx = WithManagedModelRequest(context.Background(), request)
	require.False(t, ManagedModelAccountAllowed(ctx, account, request.Route.Selector))
	require.True(t, ManagedModelAccountAllowed(context.Background(), account, "private-ssvip"), "unmanaged callers are unchanged")
}

func TestManagedModelRoutesFingerprintPinsProxyAndAuthScheme(t *testing.T) {
	_, account := managedRouteTestFixture(23, "gpt-5.6-sol")
	proxyID := int64(8)
	account.ProxyID = &proxyID
	require.Empty(t, ManagedModelAccountFingerprint(account), "incomplete proxy hydration cannot certify a transport")
	account.Proxy = &Proxy{ID: proxyID, Protocol: "http", Host: "proxy.example", Port: 8080, Username: "synthetic", Password: "not-a-real-password", Status: StatusActive}
	original := ManagedModelAccountFingerprint(account)
	require.NotEmpty(t, original)
	account.Proxy.Host = "other-proxy.example"
	require.NotEqual(t, original, ManagedModelAccountFingerprint(account), "same proxy ID does not imply the same transport")
	account.Proxy.Host = "proxy.example"
	require.Equal(t, original, ManagedModelAccountFingerprint(account))
	account.Extra = map[string]any{"anthropic_apikey_auth_scheme": "bearer"}
	require.NotEqual(t, original, ManagedModelAccountFingerprint(account))
}

type managedLatestAccountRepo struct {
	AccountRepository
	account *Account
}

func (r *managedLatestAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func TestManagedModelRoutesFinalForwardChecksLatestAccount(t *testing.T) {
	group, account := managedRouteTestFixture(23, "gpt-5.6-sol")
	request, err := ResolveManagedModelRoute(group, "gpt-5.6-sol", CompositeRouteEndpointResponses)
	require.NoError(t, err)
	ctx := WithManagedModelRequest(context.Background(), request)
	latest := *account
	latest.Credentials = map[string]any{"api_key": "changed-synthetic-key", "base_url": "https://upstream.example/v1", "model_mapping": account.Credentials["model_mapping"]}
	repo := &managedLatestAccountRepo{account: &latest}
	require.ErrorIs(t, validateManagedForwardAccount(ctx, repo, account, request.Route.Selector), ErrManagedModelRouteUnavailable)
	repo.account = account
	require.NoError(t, validateManagedForwardAccount(ctx, repo, account, request.Route.Selector))
	account.GroupIDs = []int64{999}
	require.ErrorIs(t, validateManagedForwardAccount(ctx, repo, account, request.Route.Selector), ErrManagedModelRouteUnavailable, "removing the member must revoke its published route")
	require.NoError(t, validateManagedForwardAccount(context.Background(), nil, account, "unmanaged"))
}

func TestManagedModelRoutesEffectiveCatalogNeverExposesSelectors(t *testing.T) {
	group, _ := managedRouteTestFixture(23, "gpt-5.6-sol")
	group.ModelAllowlist = GroupModelAllowlist{}
	allowlist := EffectiveManagedModelAllowlist(group)
	require.True(t, allowlist.Enabled)
	require.Equal(t, []string{"gpt-5.6-sol"}, allowlist.FilterForListing([]string{"gpt-5.6-sol", "private-ssvip", group.ManagedModelRoutes.Routes[0].Selector}))
	encoded, err := json.Marshal(allowlist)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "s2pub-")
}

func TestManagedModelRoutesCompiledChannelAndMessagesMustAgree(t *testing.T) {
	group, _ := managedRouteTestFixture(23, "gpt-5.6-sol")
	selector := group.ManagedModelRoutes.Routes[0].Selector
	channel := Channel{ID: 7, Status: StatusActive, GroupIDs: []int64{group.ID}, BillingModelSource: BillingModelSourceRequested,
		ModelMapping: map[string]map[string]string{PlatformOpenAI: {"gpt-5.6-sol": selector}}}
	channels := &ChannelService{}
	channels.cache.Store(populateChannelCache([]Channel{channel}, map[int64]string{group.ID: PlatformOpenAI}))
	svc := &GatewayService{channelService: channels}
	request, err := ResolveManagedModelRoute(group, "gpt-5.6-sol", CompositeRouteEndpointMessages)
	require.NoError(t, err)
	require.NoError(t, svc.ValidateManagedModelCompilation(context.Background(), group, request))
	group.MessagesDispatchModelConfig.ExactModelMappings["gpt-5.6-sol"] = "private-ssvip"
	require.ErrorIs(t, svc.ValidateManagedModelCompilation(context.Background(), group, request), ErrManagedModelRouteUnavailable)
	group.MessagesDispatchModelConfig.ExactModelMappings["gpt-5.6-sol"] = selector
	channel.BillingModelSource = BillingModelSourceChannelMapped
	channels.cache.Store(populateChannelCache([]Channel{channel}, map[int64]string{group.ID: PlatformOpenAI}))
	require.ErrorIs(t, svc.ValidateManagedModelCompilation(context.Background(), group, request), ErrManagedModelRouteUnavailable)
}
