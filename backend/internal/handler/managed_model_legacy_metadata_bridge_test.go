//go:build unit

package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestManagedModelV2LegacyCountTokensUsesExistingBridge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const groupID int64 = 55100
	const public, wire = "claude-fable-5.1", "retained-fable-5.1-wire"
	selector := service.ManagedModelSelector(groupID, public)
	account := service.Account{ID: 55201, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		GroupIDs: []int64{groupID}, Credentials: map[string]any{"api_key": "offline-key", "base_url": "https://fixture.invalid", "model_mapping": map[string]any{selector: wire, public: "private-vip-wire"}}}
	upstream := &cindyCountTokensUpstream{}
	h, _, _ := newCindyCountTokensHandler(t, []service.Account{account}, upstream)
	seed, _ := newCindyCountTokensContext(t, groupID, false, true, "")
	apiKey, _ := middleware2.GetAPIKeyFromContext(seed)
	subject, _ := middleware2.GetAuthSubjectFromContext(seed)
	endpoints := []string{service.CompositeRouteEndpointCountTokens}
	apiKey.Group.ManagedModelRoutes = service.ManagedModelRoutesConfig{Version: 2, Enabled: true, Routes: []service.ManagedModelRoute{{PublicModel: public, Endpoints: endpoints,
		Branches: []service.ManagedModelRouteBranch{{Selector: selector, TargetPlatform: service.PlatformOpenAI, Endpoints: endpoints,
			Accounts: []service.ManagedModelRouteAccount{{AccountID: account.ID, UpstreamModel: wire, AccountFingerprint: service.ManagedModelAccountFingerprint(&account), Endpoints: endpoints}}}}}}}
	// An unrelated legacy dispatch cannot replace the selected retained target.
	apiKey.Group.MessagesDispatchModelConfig.ExactModelMappings = map[string]string{selector: "private-vip-wire"}
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware2.ContextKeyUser), subject)
	})
	engine.Use(middleware2.GroupModelAllowlist())
	engine.Use((&GatewayHandler{}).ManagedModelV2(h))
	engine.POST("/v1/messages/count_tokens", h.CountTokens)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{"model":"claude-fable-5.1","messages":[{"role":"user","content":"offline"}]}`))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	calls := upstream.snapshots()
	require.Len(t, calls, 1, "the existing bridge executes once; no generative request or handler re-entry")
	require.Equal(t, account.ID, calls[0].accountID)
	require.Equal(t, "/v1/responses/input_tokens", calls[0].path)
	require.Equal(t, wire, gjson.GetBytes(calls[0].body, "model").String())
	require.NotContains(t, w.Body.String(), "s2pub-")
	require.Equal(t, "private-vip-wire", apiKey.Group.MessagesDispatchModelConfig.ExactModelMappings[selector])
}

func TestManagedModelV2LegacyMetadataNoAccountErrorUsesPublicName(t *testing.T) {
	_, fixture, _ := retainedManagedModelWSFixture()
	group := fixture.group
	request, err := service.ResolveManagedModelRoute(group, "gpt-5.4", service.CompositeRouteEndpointCountTokens)
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	c.Request = c.Request.WithContext(service.WithManagedModelRequest(c.Request.Context(), request))
	diag := &fakeDiagnoser{resp: service.ModelAvailabilityDiagnosis{HasAccountsInPool: true, HasModelSupport: false}}
	apiKey := &service.APIKey{GroupID: &group.ID, Group: group}
	classification := classifyNoAccountErrorFromGin(c, diag, apiKey, request.RoutingModel(), managedModelMetadataPublicName(c, request.RoutingModel()), service.PlatformOpenAI)
	require.Equal(t, http.StatusNotFound, classification.Status)
	require.Contains(t, classification.Message, "gpt-5.4")
	require.NotContains(t, classification.Message, "s2pub-")
	require.Equal(t, request.RoutingModel(), diag.calls[0].Model, "diagnosis still uses the actual retained route")
}
