package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func retainedManagedModelWSFixture() (*managedModelWSGuard, *managedModelWSFixture, *service.Account) {
	guard, fixture, account := newManagedModelWSFixture()
	route := &fixture.group.ManagedModelRoutes.Routes[0]
	legacy := service.ManagedModelRouteBranches(*route)[0]
	legacy.Endpoints = append(legacy.Endpoints, service.CompositeRouteEndpointCountTokens)
	legacy.Accounts[0].Endpoints = append(legacy.Accounts[0].Endpoints, service.CompositeRouteEndpointCountTokens)
	selector := service.ManagedModelBranchSelector(fixture.group.ID, route.PublicModel, service.PlatformOpenAI, service.CompositeRouteEndpointResponses, "new-generative-target")
	account.Credentials["model_mapping"].(map[string]any)[selector] = "new-generative-target"
	newBranch := service.ManagedModelRouteBranch{Selector: selector, TargetPlatform: service.PlatformOpenAI, UpstreamProtocol: service.CompositeRouteEndpointResponses,
		Endpoints: []string{service.CompositeRouteEndpointResponses}, Accounts: []service.ManagedModelRouteAccount{{AccountID: account.ID, UpstreamModel: "new-generative-target",
			AccountFingerprint: service.ManagedModelAccountFingerprint(account), Endpoints: []string{service.CompositeRouteEndpointResponses}}}}
	route.Endpoints = []string{service.ManagedModelEndpointResponsesWebSocket, service.CompositeRouteEndpointCountTokens, service.CompositeRouteEndpointResponses}
	route.Branches = []service.ManagedModelRouteBranch{newBranch, legacy}
	route.Selector, route.TargetPlatform, route.Accounts = "", "", nil
	return guard, fixture, account
}

func TestManagedModelV2LegacyCountTokensReachesExistingHandlerOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, fixture, _ := retainedManagedModelWSFixture()
	group := fixture.group
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: group}) })
	engine.Use(middleware2.GroupModelAllowlist())
	engine.Use((&GatewayHandler{}).ManagedModelV2(nil))
	called := 0
	engine.POST("/v1/messages/count_tokens", func(c *gin.Context) {
		called++
		request, managed := service.ManagedModelRequestFromContext(c.Request.Context())
		require.True(t, managed)
		require.True(t, service.IsManagedModelLegacyMetadataRequest(request))
		body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
		require.NoError(t, err)
		require.Equal(t, request.RoutingModel(), gjson.GetBytes(body, "model").String())
		require.Equal(t, "unchanged", gjson.GetBytes(body, "messages.0.content").String())
		public, ok := service.RequestedPublicModelFromContext(c.Request.Context())
		require.True(t, ok)
		require.Equal(t, "gpt-5.4", public)
		c.JSON(http.StatusOK, gin.H{"input_tokens": 7})
	})
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{"model":"public-gpt","messages":[{"role":"user","content":"unchanged"}]}`)))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, called)
	require.NotContains(t, w.Body.String(), "s2pub-")
}

func TestManagedModelV2LegacyWSKeepsAccountAndRefreshesEachTurn(t *testing.T) {
	guard, fixture, account := retainedManagedModelWSFixture()
	ctx := context.Background()
	body, request, _, err := guard.prepareFrame(ctx, 1, []byte(`{"model":"public-gpt"}`), "", nil)
	require.NoError(t, err)
	require.True(t, service.IsManagedModelLegacyMetadataRequest(request))
	require.Equal(t, "gpt-5.4", gjson.GetBytes(body, "model").String())
	require.NoError(t, guard.mappedTurn(ctx, 1, request, account))
	publicBody, _, _, err := guard.validatePayload(ctx, 1, []byte(`{"type":"response.create","model":"verified-wire-model"}`), "gpt-5.4", account)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.4", gjson.GetBytes(publicBody, "model").String())
	require.NoError(t, guard.validateTurn(ctx, 1, account))
	body, _, _, err = guard.prepareFrame(ctx, 2, []byte(`{"type":"session.update","session":{"model":"public-gpt"}}`), "gpt-5.4", account)
	require.NoError(t, err)
	require.Equal(t, "verified-wire-model", gjson.GetBytes(body, "session.model").String())
	_, _, _, err = guard.validatePayload(ctx, 2, []byte(`{"type":"response.create","model":"new-generative-target"}`), "gpt-5.4", account)
	require.ErrorIs(t, err, service.ErrManagedModelRouteUnavailable)
	fixture.group.ManagedModelRoutes.Routes[0].Branches = fixture.group.ManagedModelRoutes.Routes[0].Branches[:1]
	require.ErrorIs(t, guard.validateTurn(ctx, 1, account), service.ErrManagedModelRouteUnavailable, "withdrawing legacy WS closes the turn instead of switching to generative evidence")
}
