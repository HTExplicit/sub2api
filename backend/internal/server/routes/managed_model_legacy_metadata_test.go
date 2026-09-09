package routes

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCompositeTargetPreservesRetainedManagedMetadataPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const public = "claude-fable-5.1"
	const groupID int64 = 23
	selector := service.ManagedModelSelector(groupID, public)
	group := &service.Group{ID: groupID, Platform: service.PlatformComposite, Status: service.StatusActive,
		ManagedModelRoutes: service.ManagedModelRoutesConfig{Version: 2, Enabled: true, Routes: []service.ManagedModelRoute{{PublicModel: public,
			QuotaPlatform: service.PlatformAnthropic, Endpoints: []string{service.CompositeRouteEndpointCountTokens},
			Branches: []service.ManagedModelRouteBranch{{Selector: selector, TargetPlatform: service.PlatformOpenAI, Endpoints: []string{service.CompositeRouteEndpointCountTokens},
				Accounts: []service.ManagedModelRouteAccount{{AccountID: 41, UpstreamModel: "legacy-wire", AccountFingerprint: "offline", Endpoints: []string{service.CompositeRouteEndpointCountTokens}}}}}}}}}
	// A generic/manual resolver would send this name to Anthropic instead.
	resolver := service.NewCompositeRouteResolver(compositeRouteRepoStub{routes: []service.CompositeModelRoute{{ID: 1, GroupID: groupID, Enabled: true,
		PublicModel: public, MatchType: service.CompositeRouteMatchExact, Endpoint: service.CompositeRouteEndpointCountTokens,
		TargetPlatform: service.PlatformAnthropic, UpstreamModel: "manual-target"}}})
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
	})
	engine.Use(servermiddleware.GroupModelAllowlist())
	engine.Use((&handler.GatewayHandler{}).ManagedModelV2(nil))
	engine.Use(compositeTargetPlatformMiddleware(resolver))
	called := 0
	engine.POST("/v1/messages/count_tokens", func(c *gin.Context) {
		called++
		require.Equal(t, service.PlatformOpenAI, getGroupPlatform(c))
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		require.Equal(t, selector, gjson.GetBytes(body, "model").String())
		publicModel, ok := service.RequestedPublicModelFromContext(c.Request.Context())
		require.True(t, ok)
		require.Equal(t, public, publicModel)
		apiKey, _ := servermiddleware.GetAPIKeyFromContext(c)
		require.Equal(t, service.PlatformAnthropic, service.QuotaPlatform(c.Request.Context(), apiKey))
		c.Status(http.StatusNoContent)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{"model":"claude-fable-5.1","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Equal(t, 1, called)
	require.Equal(t, "", group.ManagedModelRoutes.Routes[0].Selector)
	require.Equal(t, service.PlatformComposite, group.Platform)
}
