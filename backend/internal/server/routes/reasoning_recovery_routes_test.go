package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Both reasoning recovery routes sit behind admin authentication, and neither
// asks for a TOTP step-up: an admin request reaches the handler, which here has
// no service and answers 503.
func TestReasoningRecoveryRoutesRequireAdminAuthenticationWithoutStepUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{
		ReasoningRecovery: adminhandler.NewReasoningRecoveryHandler(nil),
	}}
	adminAuth := servermiddleware.AdminAuthMiddleware(func(c *gin.Context) {
		switch c.GetHeader("Authorization") {
		case "":
			servermiddleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authorization required")
		case "Bearer admin-token":
			c.Next()
		default:
			servermiddleware.AbortWithError(c, http.StatusForbidden, "FORBIDDEN", "Admin access required")
		}
	})
	auditLog := servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepUp := servermiddleware.StepUpAuthMiddleware(func(c *gin.Context) {
		servermiddleware.AbortWithError(c, http.StatusPreconditionRequired, "STEP_UP_REQUIRED", "TOTP step-up required")
	})
	RegisterAdminRoutes(router.Group("/api/v1"), handlers, adminAuth, auditLog, stepUp, nil, nil)

	for _, route := range []struct{ method, body string }{
		{http.MethodGet, ""},
		{http.MethodPut, `{"enabled":false}`},
	} {
		for _, testCase := range []struct {
			name       string
			auth       string
			wantStatus int
		}{
			{name: "unauthenticated", wantStatus: http.StatusUnauthorized},
			{name: "non-admin", auth: "Bearer user-token", wantStatus: http.StatusForbidden},
			{name: "admin", auth: "Bearer admin-token", wantStatus: http.StatusServiceUnavailable},
		} {
			t.Run(route.method+" "+testCase.name, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(route.method, "/api/v1/admin/reasoning-recovery", strings.NewReader(route.body))
				if testCase.auth != "" {
					request.Header.Set("Authorization", testCase.auth)
				}
				router.ServeHTTP(recorder, request)
				require.Equal(t, testCase.wantStatus, recorder.Code)
			})
		}
	}
}
