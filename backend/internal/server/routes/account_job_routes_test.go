package routes

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountJobRoutesExposeSlimContractWithoutImportPreview(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	adminAccount := adminhandler.NewAccountHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{
		Account: adminAccount, AccountJob: adminhandler.NewAccountJobHandler(nil),
	}}
	adminAuth := servermiddleware.AdminAuthMiddleware(func(c *gin.Context) { c.Next() })
	auditLog := servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepUp := servermiddleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterAdminRoutes(router.Group("/api/v1"), handlers, adminAuth, auditLog, stepUp, nil, nil)

	registered := make(map[string]struct{})
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = struct{}{}
	}
	for _, expected := range []string{
		http.MethodGet + " /api/v1/admin/account-jobs",
		http.MethodGet + " /api/v1/admin/account-jobs/:id",
		http.MethodGet + " /api/v1/admin/account-jobs/:id/items",
		http.MethodPost + " /api/v1/admin/account-jobs/:id/cancel",
		http.MethodPost + " /api/v1/admin/account-jobs/:id/retry-failed",
		http.MethodPost + " /api/v1/admin/accounts/duplicates/review",
		http.MethodPost + " /api/v1/admin/accounts/duplicates/merge",
	} {
		_, ok := registered[expected]
		require.True(t, ok, expected)
	}
	_, previewRegistered := registered[http.MethodPost+" /api/v1/admin/accounts/data/preview"]
	require.False(t, previewRegistered)
}
