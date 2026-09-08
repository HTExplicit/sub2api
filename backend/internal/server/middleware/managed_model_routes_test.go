package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestManagedModelRoutesHTTPAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	publicModel := "gpt-5.6-sol"
	group := &service.Group{ID: 23, Platform: service.PlatformOpenAI, Status: service.StatusActive,
		ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{publicModel}},
		ManagedModelRoutes: service.ManagedModelRoutesConfig{Version: 1, Enabled: true, Routes: []service.ManagedModelRoute{{PublicModel: publicModel, Aliases: []string{"gpt-5.6"},
			Selector: service.ManagedModelSelector(23, publicModel), TargetPlatform: service.PlatformOpenAI,
			Endpoints: []string{"responses", "messages", "count_tokens", "chat_completions"}, Accounts: []service.ManagedModelRouteAccount{{AccountID: 1, UpstreamModel: publicModel, AccountFingerprint: "test-only", Endpoints: []string{"responses"}}}}}}}
	for _, tc := range []struct {
		name, path, body string
		status           int
	}{
		{"canonical", "/v1/responses", `{"model":"gpt-5.6-sol","input":[]}`, 200},
		{"case and trim", "/v1/responses", `{"model":"  GPT-5.6-SOL  ","input":[]}`, 200},
		{"declared alias", "/v1/responses", `{"model":"gpt-5.6","input":[]}`, 200},
		{"effort alias", "/v1/responses", `{"model":"gpt-5.6-sol-high","input":[]}`, 200},
		{"unknown", "/v1/responses", `{"model":"private-only","input":[]}`, 404},
		{"direct selector", "/v1/responses", `{"model":"` + group.ManagedModelRoutes.Routes[0].Selector + `","input":[]}`, 404},
		{"duplicate parser keys", "/v1/responses", `{"model":"gpt-5.6-sol","Model":"gpt-5.6-sol"}`, 404},
		{"unpublished media endpoint", "/v1/images/generations", `{"model":"gpt-5.6-sol"}`, 404},
		{"legacy compact uses the published responses route", "/v1/responses/compact", `{"model":"gpt-5.6-sol"}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) { c.Set(string(ContextKeyAPIKey), &service.APIKey{Group: group}); c.Next() })
			router.Use(GroupModelAllowlist())
			called := false
			router.POST(tc.path, func(c *gin.Context) {
				called = true
				request, ok := service.ManagedModelRequestFromContext(c.Request.Context())
				require.True(t, ok)
				require.Equal(t, publicModel, request.Route.PublicModel)
				body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
				require.NoError(t, err)
				require.Equal(t, publicModel, gjson.GetBytes(body, "model").String())
				if tc.name == "effort alias" {
					require.Equal(t, "high", gjson.GetBytes(body, "reasoning.effort").String())
				}
				c.Status(http.StatusOK)
			})
			request := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, tc.status, response.Code)
			require.Equal(t, tc.status == 200, called, "rejected requests must not reach a forwarding handler")
			require.NotContains(t, response.Body.String(), "s2pub-")
		})
	}
}
