package middleware

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestLoginProtectionGlobalRoutesDisableAndStorageFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	server := miniredis.RunT(t)
	server.SetTime(time.Now())
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1, DialTimeout: 20 * time.Millisecond})
	t.Cleanup(func() { _ = client.Close() })
	settings := service.NewSettingService(&panelRateLimitStubRepo{}, &config.Config{})
	guard := service.NewLoginProtectionService(repository.NewLoginProtectionStore(client), settings, nil)
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.Use(LoginProtection(guard))
	router.Any("/api/v1/auth/login", func(c *gin.Context) { c.Status(204) })
	router.Any("/api/v1/auth/login/2fa", func(c *gin.Context) { c.Status(204) })
	router.Any("/api/v1/admin/settings", func(c *gin.Context) { c.Status(204) })
	router.Any("/v1/responses", func(c *gin.Context) { c.Status(204) })
	router.NoRoute(func(c *gin.Context) { c.Status(200) })
	request := func(ip, method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = ip + ":1234"
		req.Header.Set("X-Forwarded-For", "198.51.100.7")
		if path == "/v1/responses" {
			req.Header.Set("Upgrade", "websocket")
			req.Header.Set("Authorization", "Bearer valid-test-key")
		}
		result := httptest.NewRecorder()
		router.ServeHTTP(result, req)
		return result
	}
	for range 6 {
		_, err := guard.RecordFailure(ctx, "203.0.113.8")
		require.NoError(t, err)
	}
	for _, path := range []string{"/", "/assets/app.js", "/api/v1/admin/settings", "/v1/responses"} {
		result := request("203.0.113.8", "GET", path)
		require.Equal(t, 429, result.Code)
		require.Contains(t, result.Body.String(), "LOGIN_IP_BANNED")
		require.NotEmpty(t, result.Header().Get("Retry-After"))
	}
	require.Equal(t, 204, request("203.0.113.9", "GET", "/v1/responses").Code)
	options := service.DefaultLoginProtectionSettings()
	options.Enabled = false
	require.NoError(t, settings.SetLoginProtectionSettings(ctx, options))
	require.Equal(t, 200, request("203.0.113.8", "GET", "/").Code)
	options.Enabled = true
	require.NoError(t, settings.SetLoginProtectionSettings(ctx, options))
	require.Equal(t, 429, request("203.0.113.8", "GET", "/").Code)
	server.Close()
	require.Equal(t, 429, request("203.0.113.8", "GET", "/").Code)
	require.Equal(t, 204, request("203.0.113.9", "GET", "/v1/responses").Code)
	for _, path := range []string{"/api/v1/auth/login", "/api/v1/auth/login/2fa"} {
		require.Equal(t, http.StatusServiceUnavailable, request("203.0.113.9", "POST", path).Code)
	}
}

func TestLoginProtectionDoesNotCancelAdmittedStream(t *testing.T) {
	server := miniredis.RunT(t)
	server.SetTime(time.Now())
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	guard := service.NewLoginProtectionService(repository.NewLoginProtectionStore(client), nil, nil)
	started, finish := make(chan struct{}), make(chan struct{})
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Request.RemoteAddr = "203.0.113.8:1234"; c.Next() })
	require.NoError(t, router.SetTrustedProxies(nil))
	router.Use(LoginProtection(guard))
	router.GET("/stream", func(c *gin.Context) {
		c.Status(200)
		_, _ = c.Writer.WriteString("started")
		c.Writer.Flush()
		close(started)
		<-finish
		if c.Request.Context().Err() == nil {
			_, _ = c.Writer.WriteString("finished")
		}
	})
	httpServer := httptest.NewServer(router)
	defer httpServer.Close()
	result, err := http.Get(httpServer.URL + "/stream")
	require.NoError(t, err)
	defer func() { _ = result.Body.Close() }()
	<-started
	for range 6 {
		_, err := guard.RecordFailure(context.Background(), "203.0.113.8")
		require.NoError(t, err)
	}
	blocked, err := http.Get(httpServer.URL + "/stream")
	require.NoError(t, err)
	_ = blocked.Body.Close()
	require.Equal(t, 429, blocked.StatusCode)
	close(finish)
	body, err := io.ReadAll(result.Body)
	require.NoError(t, err)
	require.Equal(t, 200, result.StatusCode)
	require.Equal(t, "startedfinished", string(body))
}
