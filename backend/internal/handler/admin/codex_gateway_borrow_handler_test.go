//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type codexGatewayBorrowHandlerTestRepo struct {
	task    *service.CodexGatewayBorrowTestTask
	result  *service.CodexGatewayBorrowTestResult
	creates int
	writes  int
	reads   int
}

func (r *codexGatewayBorrowHandlerTestRepo) Create(context.Context, *service.CodexGatewayBorrowTestTask) (*service.CodexGatewayBorrowTestTask, bool, error) {
	r.creates++
	return r.task, true, nil
}
func (r *codexGatewayBorrowHandlerTestRepo) Get(context.Context, string) (*service.CodexGatewayBorrowTestTask, error) {
	r.reads++
	return r.task, nil
}
func (r *codexGatewayBorrowHandlerTestRepo) GetResult(context.Context, string) (*service.CodexGatewayBorrowTestResult, error) {
	r.reads++
	return r.result, nil
}
func (r *codexGatewayBorrowHandlerTestRepo) List(context.Context, int, int) (*service.CodexGatewayBorrowTestList, error) {
	return &service.CodexGatewayBorrowTestList{Items: []*service.CodexGatewayBorrowTestTask{r.task}}, nil
}
func (r *codexGatewayBorrowHandlerTestRepo) StartTask(context.Context, string, time.Time) error {
	r.writes++
	return nil
}
func (r *codexGatewayBorrowHandlerTestRepo) SaveResult(context.Context, *service.CodexGatewayBorrowTestResult) error {
	r.writes++
	return nil
}
func (r *codexGatewayBorrowHandlerTestRepo) FinishTask(context.Context, string, string, string, time.Time) error {
	r.writes++
	return nil
}
func (r *codexGatewayBorrowHandlerTestRepo) MarkInterrupted(context.Context, time.Time) error {
	return nil
}
func (r *codexGatewayBorrowHandlerTestRepo) CleanupExpired(context.Context, time.Time) error {
	return nil
}

func newCodexGatewayBorrowHandlerFixture(t *testing.T) (*CodexGatewayBorrowHandler, *codexGatewayBorrowHandlerTestRepo) {
	t.Helper()
	now := time.Now().UTC()
	client, err := uuid.NewV7()
	require.NoError(t, err)
	result := &service.CodexGatewayBorrowTestResult{ID: uuid.NewString(), Status: "complete", AccountID: 42, ModelID: "gpt-6-astra", Effort: "high", RawAnswer: "<html><script>animate()</script></html>", RawHTML: "<html><script>animate()</script></html>", HTML: "<html><script>animate()</script></html>", Error: "original upstream error token=admin-visible\x00tail", ExpiresAt: now.Add(time.Hour)}
	task := &service.CodexGatewayBorrowTestTask{ID: uuid.NewString(), ClientTaskID: client.String(), Status: "complete", Total: 1, Completed: 1, CreatedAt: now, ExpiresAt: result.ExpiresAt, Results: []*service.CodexGatewayBorrowTestResult{result}}
	result.TaskID = task.ID
	repo := &codexGatewayBorrowHandlerTestRepo{task: task, result: result}
	runner := service.NewCodexGatewayBorrowTestRunner(repo, nil, nil)
	preview := service.NewCodexGatewayBorrowPreviewService(repo)
	t.Cleanup(runner.Stop)
	t.Cleanup(preview.Stop)
	return NewCodexGatewayBorrowHandler(nil, runner, preview), repo
}

func TestCodexGatewayBorrowHandlerPreviewUsesOnlyCapabilityAndOverridesCSP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, repo := newCodexGatewayBorrowHandlerFixture(t)
	decorated := h.preview.DecorateResult(repo.result)
	r := gin.New()
	r.Use(middleware.SecurityHeaders(config.CSPConfig{Enabled: true}, nil))
	r.GET("/api/v1/codex-gateway-borrow/preview/:cap/index.html", h.ServePreview)
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "main app") })
	w := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, decorated.PreviewURL, nil)
	request.Header.Set("Authorization", "Bearer SHOULD-NOT-APPEAR")
	request.Header.Set("Cookie", "admin_jwt=SHOULD-NOT-APPEAR")
	r.ServeHTTP(w, request)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, repo.result.HTML, w.Body.String())
	require.Equal(t, service.CodexGatewayBorrowPreviewCSP, w.Header().Get("Content-Security-Policy"))
	require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, "no-referrer", w.Header().Get("Referrer-Policy"))
	require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "SAMEORIGIN", w.Header().Get("X-Frame-Options"))
	require.NotContains(t, w.Body.String(), "SHOULD-NOT-APPEAR")
	require.NotContains(t, w.Body.String(), "bridge_token")
	main := httptest.NewRecorder()
	r.ServeHTTP(main, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Contains(t, main.Header().Get("Content-Security-Policy"), "'nonce-")
	require.NotEqual(t, service.CodexGatewayBorrowPreviewCSP, main.Header().Get("Content-Security-Policy"))
	reads := repo.reads
	unknown := httptest.NewRecorder()
	r.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/api/v1/codex-gateway-borrow/preview/"+strings.Repeat("x", 43)+"/index.html", nil))
	require.Equal(t, http.StatusGone, unknown.Code)
	require.Equal(t, reads, repo.reads)
}

func TestCodexGatewayBorrowHandlerReadAndSSEReplayPreserveFullErrorsWithoutDispatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, repo := newCodexGatewayBorrowHandlerFixture(t)
	r := gin.New()
	r.GET("/tests/:id", h.GetTest)
	r.POST("/tests", h.StartTests)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tests/"+repo.task.ID, nil))
	require.Equal(t, http.StatusOK, w.Code)
	var envelope struct {
		Data service.CodexGatewayBorrowTestTask `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.Equal(t, repo.result.Error, envelope.Data.Results[0].Error)
	require.NotEmpty(t, envelope.Data.Results[0].PreviewURL)
	requestBody := `{"client_task_id":"` + repo.task.ClientTaskID + `","targets":[{"account_id":42,"model_id":"gpt-6-astra"}]}`
	stream := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/tests", strings.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(stream, request)
	require.Equal(t, http.StatusOK, stream.Code)
	require.Equal(t, "text/event-stream", stream.Header().Get("Content-Type"))
	require.Contains(t, stream.Body.String(), `"type":"task_start"`)
	require.Contains(t, stream.Body.String(), `"type":"task_complete"`)
	require.NotContains(t, stream.Body.String(), `"type":"result_started"`)
	require.Contains(t, stream.Body.String(), "original upstream error token=admin-visible")
	require.Equal(t, 1, repo.creates)
	require.Zero(t, repo.writes, "SSE replay must never start or regenerate an existing task")
}
