//go:build unit

package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPelicanRoutesRetireBorrowing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	noop := func(c *gin.Context) { c.Next() }
	RegisterAdminRoutes(router.Group("/api/v1"), &handler.Handlers{Admin: &handler.AdminHandlers{}},
		middleware.AdminAuthMiddleware(noop), middleware.AuditLogMiddleware(noop), middleware.StepUpAuthMiddleware(noop), nil,
		middleware.NewPanelRateLimiter(nil, nil))
	registered := map[string]bool{}
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	require.True(t, registered["POST /api/v1/admin/pelican-tests/tasks"])
	require.True(t, registered["GET /api/v1/admin/pelican-tests/tasks/:id"])
	require.True(t, registered["GET /api/v1/pelican-tests/preview/:cap/index.html"])
	for _, endpoint := range []struct{ method, path string }{
		{"GET", "/api/v1/admin/codex-gateway-borrow/config"}, {"PUT", "/api/v1/admin/codex-gateway-borrow/config"},
		{"GET", "/api/v1/admin/codex-gateway-borrow/status"}, {"POST", "/api/v1/admin/codex-gateway-borrow/prepare"},
		{"POST", "/api/v1/admin/codex-gateway-borrow/verify"}, {"POST", "/api/v1/admin/codex-gateway-borrow/diagnose"},
		{"POST", "/api/v1/admin/codex-gateway-borrow/tests"}, {"GET", "/api/v1/admin/codex-gateway-borrow/tests"},
		{"GET", "/api/v1/admin/codex-gateway-borrow/tests/one"}, {"GET", "/api/v1/codex-gateway-borrow/preview/one/index.html"},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(endpoint.method, endpoint.path, nil))
		require.Equal(t, http.StatusNotFound, response.Code, endpoint.method+" "+endpoint.path)
	}
}
