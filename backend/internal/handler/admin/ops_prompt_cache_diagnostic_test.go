//go:build unit

package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPromptCacheDiagnosticAdminValidationAndLifecycle(t *testing.T) {
	h := NewOpsHandler(&service.OpsService{})
	router := gin.New()
	router.POST("/diagnostics", h.StartPromptCacheDiagnostic)
	router.GET("/diagnostics/:id", h.GetPromptCacheDiagnostic)
	router.DELETE("/diagnostics/:id", h.StopPromptCacheDiagnostic)
	for _, body := range []string{`{`, `{}`, `{"secret":"private-value"}`, strings.Repeat("x", 4097), `{} {}`} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/diagnostics", strings.NewReader(body)))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.NotContains(t, w.Body.String(), "private-value")
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/diagnostics", strings.NewReader(`{"api_key_id":8999,"session_id":"11111111-1111-4111-8111-111111111111","model":"gpt-diagnostic-test","max_requests":2,"ttl_seconds":60}`)))
	require.Equal(t, http.StatusOK, w.Code)
	var out struct {
		Data service.PromptCacheDiagnosticView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.NotEmpty(t, out.Data.ID)
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		w = httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, "/diagnostics/"+out.Data.ID, nil))
		require.Equal(t, http.StatusOK, w.Code)
	}
	require.Contains(t, w.Body.String(), `"status":"stopped"`)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/diagnostics/not-enrolled", nil))
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestPromptCacheDiagnosticAdminRequiresMonitoring(t *testing.T) {
	svc := &service.OpsService{}
	svc.SetMonitoringEnabled(false)
	for _, h := range []*OpsHandler{NewOpsHandler(nil), NewOpsHandler(svc)} {
		for _, handler := range []gin.HandlerFunc{h.StartPromptCacheDiagnostic, h.GetPromptCacheDiagnostic, h.StopPromptCacheDiagnostic} {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/diagnostics", strings.NewReader(`{}`))
			handler(c)
			require.GreaterOrEqual(t, w.Code, 400)
		}
	}
}
