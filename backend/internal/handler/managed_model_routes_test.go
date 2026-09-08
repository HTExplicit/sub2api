package handler

import (
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

func TestManagedModelHTTPFirstPublicationCannotUseUnmanagedAuthSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, source, _ := newManagedModelWSFixture()
	source.group.ManagedModelRoutes.Routes[0].Endpoints = []string{"responses"}
	source.group.ManagedModelRoutes.Routes[0].Accounts[0].Endpoints = []string{"responses"}
	cached := &service.Group{ID: source.group.ID, Platform: service.PlatformOpenAI, Status: service.StatusActive}
	for _, tc := range []struct {
		model  string
		status int
	}{{" PUBLIC-GPT ", 200}, {"private-vip-model", 404}} {
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: cached})
			c.Next()
		})
		router.Use(managedModelRouteGuard(source, 1024), middleware2.GroupModelAllowlist(1024))
		called := false
		router.POST("/v1/responses", func(c *gin.Context) {
			called = true
			key, _ := middleware2.GetAPIKeyFromContext(c)
			require.True(t, key.Group.ManagedModelRoutes.Enabled)
			request, ok := service.ManagedModelRequestFromContext(c.Request.Context())
			require.True(t, ok)
			require.Equal(t, "PUBLIC-GPT", request.SubmittedModel, "allowlist must not normalize a prepared request twice")
			body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
			require.NoError(t, err)
			require.Equal(t, "gpt-5.4", gjson.GetBytes(body, "model").String())
			c.Status(http.StatusOK)
		})
		request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"`+tc.model+`","input":[]}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, tc.status, response.Code)
		require.Equal(t, tc.status == 200, called)
	}
	require.False(t, cached.ManagedModelRoutes.Enabled, "gateway refresh must not mutate the shared cached/admin group object")
	require.Positive(t, source.groupReads)
}

func TestManagedModelHTTPRefreshesCatalogIdentityButDoesNotTouchPrivateOrCindy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, source, _ := newManagedModelWSFixture()
	for _, tc := range []struct {
		name, platform string
		exclusive      bool
		managed        bool
	}{{"public stale flag", service.PlatformOpenAI, false, true}, {"private", service.PlatformOpenAI, true, false}, {"cindy", service.PlatformCindy, false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			before := source.groupReads
			cached := &service.Group{ID: 23, Platform: tc.platform, IsExclusive: tc.exclusive}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: cached})
				c.Next()
			})
			router.Use(managedModelRouteGuard(source, 1024))
			router.GET("/v1/models", func(c *gin.Context) {
				key, _ := middleware2.GetAPIKeyFromContext(c)
				require.Equal(t, tc.managed, key.Group.ManagedModelRoutes.Enabled)
				if tc.managed {
					require.Equal(t, []string{"gpt-5.4"}, key.Group.ModelAllowlist.Models)
				}
				c.Status(http.StatusOK)
			})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
			require.Equal(t, http.StatusOK, response.Code)
			require.Equal(t, tc.managed, source.groupReads > before)
		})
	}
}
