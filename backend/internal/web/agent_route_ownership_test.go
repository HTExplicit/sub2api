//go:build embed

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAgentRegisteredAPIRoutesOwnTheirResponses(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, path := range []string{"/chat/completions", "/embeddings", "/messages/count_tokens", "/tts", "/future-api/execute"} {
			t.Run(path+map[bool]string{false: "/settings", true: "/legacy"}[legacy], func(t *testing.T) {
				router := gin.New()
				if legacy {
					router.Use(ServeEmbeddedFrontend())
				} else {
					server, err := NewFrontendServer(&mockSettingsProvider{settings: map[string]string{}})
					require.NoError(t, err)
					router.Use(server.Middleware())
				}
				called := false
				router.POST(path, func(c *gin.Context) {
					called = true
					c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"type": "authentication_error"}})
				})
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
				require.True(t, called, "a matched API must never be intercepted by the SPA")
				require.Equal(t, http.StatusUnauthorized, rec.Code)
				require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
			})
		}
	}
}

func TestAgentFrontendFallbackUsesRouteTableAndMethods(t *testing.T) {
	router := gin.New()
	router.POST("/chat/completions", func(c *gin.Context) { c.JSON(401, gin.H{"error": "auth"}) })
	router.POST("/future-api/execute", func(c *gin.Context) { c.JSON(401, gin.H{"error": "auth"}) })
	router.GET("/v1/models", func(c *gin.Context) { c.JSON(401, gin.H{"error": "auth"}) })
	server, err := NewFrontendServer(&mockSettingsProvider{settings: map[string]string{}})
	require.NoError(t, err)
	RegisterFrontendFallback(router, server.Middleware())
	for _, tc := range []struct {
		method, path string
		status       int
		html         bool
	}{
		{http.MethodPost, "/chat/completions", 401, false},
		{http.MethodGet, "/chat/completions", 405, false},
		{http.MethodPost, "/v1/models", 405, false},
		{http.MethodGet, "/chat/missing", 404, false},
		{http.MethodGet, "/future-api/missing", 404, false},
		{http.MethodGet, "/v1/missing", 404, false},
		{http.MethodPost, "/unknown", 404, false},
		{http.MethodPost, "//chat/completions", 404, false},
		{http.MethodGet, "/dashboard", 200, true},
		{http.MethodGet, "/", 200, true},
		{http.MethodHead, "/dashboard", 200, true},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			require.Equal(t, tc.status, rec.Code)
			if tc.html {
				require.Contains(t, rec.Header().Get("Content-Type"), "text/html")
			} else {
				require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
			}
		})
	}
}
