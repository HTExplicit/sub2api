package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSystemPromptRoutesRequireAdminAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{
		SystemPrompt: adminhandler.NewSystemPromptHandler(nil),
	}}
	adminAuth := servermiddleware.AdminAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			servermiddleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authorization required")
			return
		}
		servermiddleware.AbortWithError(c, http.StatusForbidden, "FORBIDDEN", "Admin access required")
	})
	auditLog := servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepUp := servermiddleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterAdminRoutes(router.Group("/api/v1"), handlers, adminAuth, auditLog, stepUp, nil, nil)

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/system-prompts"},
		{http.MethodPut, "/api/v1/admin/system-prompts"},
		{http.MethodPut, "/api/v1/admin/system-prompts/bindings"},
	} {
		for _, testCase := range []struct {
			name       string
			auth       string
			wantStatus int
		}{
			{name: "unauthenticated", wantStatus: http.StatusUnauthorized},
			{name: "non-admin", auth: "Bearer user-token", wantStatus: http.StatusForbidden},
		} {
			t.Run(route.method+" "+route.path+" "+testCase.name, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(route.method, route.path, nil)
				if testCase.auth != "" {
					request.Header.Set("Authorization", testCase.auth)
				}
				router.ServeHTTP(recorder, request)
				require.Equal(t, testCase.wantStatus, recorder.Code)
			})
		}
	}
}
