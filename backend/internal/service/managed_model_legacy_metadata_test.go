package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func managedLegacyMetadataV2Fixture() (*Group, *Account) {
	group, account := managedRouteTestFixture(23, "verified-legacy-wire-model")
	group.ManagedModelRoutes.Version = ManagedModelRoutesVersion
	route := &group.ManagedModelRoutes.Routes[0]
	legacy := ManagedModelRouteBranches(*route)[0]
	selector := ManagedModelBranchSelector(group.ID, route.PublicModel, PlatformOpenAI, CompositeRouteEndpointResponses, "new-generative-wire-model")
	account.Credentials["model_mapping"].(map[string]any)[selector] = "new-generative-wire-model"
	newBranch := ManagedModelRouteBranch{Selector: selector, TargetPlatform: PlatformOpenAI, UpstreamProtocol: CompositeRouteEndpointResponses,
		Endpoints: []string{CompositeRouteEndpointResponses}, Accounts: []ManagedModelRouteAccount{{AccountID: account.ID, UpstreamModel: "new-generative-wire-model",
			AccountFingerprint: ManagedModelAccountFingerprint(account), Endpoints: []string{CompositeRouteEndpointResponses}}}}
	route.Selector, route.TargetPlatform, route.Accounts = "", "", nil
	route.QuotaPlatform = PlatformOpenAI
	route.Branches = []ManagedModelRouteBranch{newBranch, legacy}
	return group, account
}

func TestManagedModelV2LegacyMetadataPinsOnlyPublishedLegacyPath(t *testing.T) {
	for _, endpoint := range []string{CompositeRouteEndpointCountTokens, ManagedModelEndpointResponsesWebSocket} {
		t.Run(endpoint, func(t *testing.T) {
			group, account := managedLegacyMetadataV2Fixture()
			group.Platform = PlatformComposite
			before, err := json.Marshal(group.ManagedModelRoutes)
			require.NoError(t, err)
			request, err := ResolveManagedModelRoute(group, "gpt-5.6", endpoint)
			require.NoError(t, err)
			require.True(t, IsManagedModelLegacyMetadataRequest(request))
			legacy := group.ManagedModelRoutes.Routes[0].Branches[1]
			require.Equal(t, legacy.Selector, request.RoutingModel())
			require.Equal(t, legacy.Selector, request.Route.Selector, "old metadata consumers receive the exact retained projection")
			require.Equal(t, legacy.TargetPlatform, request.TargetPlatform())
			require.Equal(t, legacy.Accounts, request.Route.Accounts)
			require.Equal(t, "gpt-5.6-sol", request.Route.PublicModel)
			require.Equal(t, PlatformOpenAI, request.QuotaPlatform)
			require.Len(t, request.Route.Branches, 2, "compilation still validates the published graph")
			ctx := WithManagedModelRequest(context.Background(), request)
			require.True(t, ManagedModelAccountAllowed(ctx, account, legacy.Selector))
			require.False(t, ManagedModelAccountAllowed(ctx, account, request.Route.Branches[0].Selector), "a same-account generative target cannot borrow legacy metadata")
			after, err := json.Marshal(group.ManagedModelRoutes)
			require.NoError(t, err)
			require.Equal(t, before, after, "the stored route and private settings are never rewritten during resolution")
		})
	}
}

func TestManagedModelV2LegacyMetadataCannotBeInferredOrDuplicated(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*ManagedModelRoute)
	}{
		{"legacy endpoint withdrawn", func(route *ManagedModelRoute) {
			route.Branches[1].Endpoints = []string{CompositeRouteEndpointResponses}
		}},
		{"only new generative branch", func(route *ManagedModelRoute) { route.Branches = route.Branches[:1] }},
		{"new branch claims metadata", func(route *ManagedModelRoute) {
			route.Branches[0].Endpoints = append(route.Branches[0].Endpoints, CompositeRouteEndpointCountTokens)
		}},
		{"duplicate legacy path", func(route *ManagedModelRoute) { route.Branches = append(route.Branches, route.Branches[1]) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group, _ := managedLegacyMetadataV2Fixture()
			tc.edit(&group.ManagedModelRoutes.Routes[0])
			_, err := ResolveManagedModelRoute(group, "gpt-5.6-sol", CompositeRouteEndpointCountTokens)
			require.ErrorIs(t, err, ErrManagedModelRouteUnavailable)
		})
	}
	group, account := managedLegacyMetadataV2Fixture()
	group.ManagedModelRoutes.Routes[0].Branches[1].Accounts[0].Endpoints = []string{CompositeRouteEndpointResponses}
	request, err := ResolveManagedModelRoute(group, "gpt-5.6-sol", CompositeRouteEndpointCountTokens)
	require.NoError(t, err)
	require.False(t, ManagedModelAccountAllowed(WithManagedModelRequest(context.Background(), request), account, request.RoutingModel()), "the branch declaration cannot invent account-level metadata support")
}

func TestManagedModelV2LegacyMetadataLeavesGenerationAndV1Unchanged(t *testing.T) {
	group, _ := managedLegacyMetadataV2Fixture()
	request, err := ResolveManagedModelRoute(group, "gpt-5.6-sol", CompositeRouteEndpointResponses)
	require.NoError(t, err)
	require.Nil(t, request.Branch, "generation retains all branches for its ordinary v2 selector")
	require.Empty(t, request.Route.Selector)
	require.False(t, IsManagedModelLegacyMetadataRequest(request))
	for _, version := range []int{1, ManagedModelRoutesVersion} {
		group, _ = managedRouteTestFixture(23, "verified-legacy-wire-model")
		group.ManagedModelRoutes.Version = version
		request, err = ResolveManagedModelRoute(group, "gpt-5.6-sol", CompositeRouteEndpointCountTokens)
		require.NoError(t, err)
		require.Nil(t, request.Branch, "scalar publications already have a complete legacy path")
		require.Equal(t, ManagedModelSelector(group.ID, "gpt-5.6-sol"), request.RoutingModel())
		require.False(t, IsManagedModelLegacyMetadataRequest(request))
	}
}

func TestManagedModelV2LegacyMetadataNativeCountUsesExactMappingForEachAuthType(t *testing.T) {
	for _, authType := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken} {
		t.Run(authType, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			const groupID int64 = 23
			const public, wireModel = "claude-fable-5.1", "retained-claude-fable-5.1-wire"
			selector := ManagedModelSelector(groupID, public)
			account := &Account{ID: 41, Platform: PlatformAnthropic, Type: authType, Status: StatusActive, Schedulable: true, GroupIDs: []int64{groupID},
				Credentials: map[string]any{"api_key": "offline-key", "access_token": "offline-token", "base_url": "https://fixture.invalid",
					"model_mapping": map[string]any{selector: wireModel, public: "private-wire"}}}
			endpoints := []string{CompositeRouteEndpointCountTokens}
			group := &Group{ID: groupID, Platform: PlatformAnthropic, ManagedModelRoutes: ManagedModelRoutesConfig{Version: 2, Enabled: true,
				Routes: []ManagedModelRoute{{PublicModel: public, Endpoints: endpoints, Branches: []ManagedModelRouteBranch{{Selector: selector, TargetPlatform: PlatformAnthropic,
					Endpoints: endpoints, Accounts: []ManagedModelRouteAccount{{AccountID: account.ID, UpstreamModel: wireModel, AccountFingerprint: ManagedModelAccountFingerprint(account), Endpoints: endpoints}}}}}}}}
			request, err := ResolveManagedModelRoute(group, public, CompositeRouteEndpointCountTokens)
			require.NoError(t, err)
			ctx := WithManagedModelBranch(WithManagedModelRequest(context.Background(), request), *request.Branch)
			body := []byte(`{"model":"` + selector + `","messages":[{"role":"user","content":"offline"}]}`)
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
			require.NoError(t, err)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", bytes.NewReader(body)).WithContext(ctx)
			// Avoid OAuth mimicry in this focused exact-routing regression.
			c.Request.Header.Set("User-Agent", "claude-cli/2.1.1 (external, cli)")
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"input_tokens":17}`))}}
			svc := &GatewayService{cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}}, accountRepo: &managedLatestAccountRepo{account: account}, httpUpstream: upstream}
			err = svc.ForwardCountTokens(ctx, c, account, parsed)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, rec.Code)
			require.Equal(t, wireModel, gjson.GetBytes(upstream.lastBody, "model").String())
			require.NotContains(t, rec.Body.String(), "s2pub-")
			require.NotContains(t, string(upstream.lastBody), "private-wire")
		})
	}
}

func TestManagedModelV2LegacyMetadataStaleCountValidationWritesNotFound(t *testing.T) {
	for _, platform := range []string{PlatformAnthropic, PlatformOpenAI} {
		t.Run(platform, func(t *testing.T) {
			group, account := managedLegacyMetadataV2Fixture()
			request, err := ResolveManagedModelRoute(group, "gpt-5.6-sol", CompositeRouteEndpointCountTokens)
			require.NoError(t, err)
			ctx := WithManagedModelRequest(context.Background(), request)
			body := []byte(`{"model":"` + request.RoutingModel() + `","messages":[]}`)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", bytes.NewReader(body)).WithContext(ctx)
			latest := *account
			latest.Schedulable = false
			repo := &managedLatestAccountRepo{account: &latest}
			if platform == PlatformAnthropic {
				parsed, parseErr := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
				require.NoError(t, parseErr)
				err = (&GatewayService{accountRepo: repo}).ForwardCountTokens(ctx, c, account, parsed)
			} else {
				err = (&OpenAIGatewayService{accountRepo: repo}).ForwardCountTokensAsAnthropic(ctx, c, account, body, "")
			}
			require.ErrorIs(t, err, ErrManagedModelRouteUnavailable)
			require.Equal(t, http.StatusNotFound, rec.Code, "final identity failure must not become an empty 200")
			require.Equal(t, "not_found_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
			require.NotContains(t, rec.Body.String(), "s2pub-")
		})
	}
}
