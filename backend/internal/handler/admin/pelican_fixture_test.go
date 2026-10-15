//go:build unit

package admin

import (
	"context"
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

type pelicanHandlerTestRepo struct {
	task    *service.PelicanTestTask
	result  *service.PelicanTestResult
	creates int
	writes  int
	reads   int
}

func (r *pelicanHandlerTestRepo) Create(context.Context, *service.PelicanTestTask) (*service.PelicanTestTask, bool, error) {
	r.creates++
	return r.task, true, nil
}
func (r *pelicanHandlerTestRepo) Get(context.Context, string) (*service.PelicanTestTask, error) {
	r.reads++
	return r.task, nil
}
func (r *pelicanHandlerTestRepo) GetResult(context.Context, string) (*service.PelicanTestResult, error) {
	r.reads++
	return r.result, nil
}
func (r *pelicanHandlerTestRepo) List(context.Context, int, int) (*service.PelicanTestList, error) {
	return &service.PelicanTestList{Items: []*service.PelicanTestTask{r.task}}, nil
}
func (r *pelicanHandlerTestRepo) StartTask(context.Context, string, time.Time) error {
	r.writes++
	return nil
}
func (r *pelicanHandlerTestRepo) SaveResult(context.Context, *service.PelicanTestResult) error {
	r.writes++
	return nil
}
func (r *pelicanHandlerTestRepo) FinishTask(context.Context, string, string, string, time.Time) error {
	r.writes++
	return nil
}
func (r *pelicanHandlerTestRepo) MarkInterrupted(context.Context, time.Time) error {
	return nil
}
func (r *pelicanHandlerTestRepo) CleanupExpired(context.Context, time.Time) error {
	return nil
}

func newPelicanHandlerFixture(t *testing.T) (*PelicanTestHandler, *pelicanHandlerTestRepo) {
	t.Helper()
	now := time.Now().UTC()
	client, err := uuid.NewV7()
	require.NoError(t, err)
	result := &service.PelicanTestResult{ID: uuid.NewString(), Status: "complete", AccountID: 42, ModelID: "gpt-6-astra", Effort: "high", RawAnswer: "<html><script>animate()</script></html>", RawHTML: "<html><script>animate()</script></html>", HTML: "<html><script>animate()</script></html>", Error: "original upstream error token=admin-visible\x00tail", ExpiresAt: now.Add(time.Hour)}
	task := &service.PelicanTestTask{ID: uuid.NewString(), ClientTaskID: client.String(), Status: "complete", Total: 1, Completed: 1, CreatedAt: now, ExpiresAt: result.ExpiresAt, Results: []*service.PelicanTestResult{result}}
	result.TaskID = task.ID
	repo := &pelicanHandlerTestRepo{task: task, result: result}
	runner := service.NewPelicanTestRunner(repo, nil, nil)
	preview := service.NewPelicanPreviewService(repo)
	t.Cleanup(runner.Stop)
	t.Cleanup(preview.Stop)
	return NewPelicanTestHandler(nil, runner, preview), repo
}

func TestPelicanHandlerPreviewUsesOnlyCapabilityAndOverridesCSP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, repo := newPelicanHandlerFixture(t)
	decorated := h.preview.DecorateResult(repo.result)
	r := gin.New()
	r.Use(middleware.SecurityHeaders(config.CSPConfig{Enabled: true}, nil))
	r.GET("/api/v1/pelican-tests/preview/:cap/index.html", h.ServePreview)
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "main app") })
	w := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, decorated.PreviewURL, nil)
	request.Header.Set("Authorization", "Bearer SHOULD-NOT-APPEAR")
	request.Header.Set("Cookie", "admin_jwt=SHOULD-NOT-APPEAR")
	r.ServeHTTP(w, request)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, repo.result.HTML, w.Body.String())
	require.Equal(t, service.PelicanPreviewCSP, w.Header().Get("Content-Security-Policy"))
	require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, "no-referrer", w.Header().Get("Referrer-Policy"))
	require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "SAMEORIGIN", w.Header().Get("X-Frame-Options"))
	require.NotContains(t, w.Body.String(), "SHOULD-NOT-APPEAR")
	require.NotContains(t, w.Body.String(), "bridge_token")
	main := httptest.NewRecorder()
	r.ServeHTTP(main, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Contains(t, main.Header().Get("Content-Security-Policy"), "'nonce-")
	require.NotEqual(t, service.PelicanPreviewCSP, main.Header().Get("Content-Security-Policy"))
	reads := repo.reads
	unknown := httptest.NewRecorder()
	r.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/api/v1/pelican-tests/preview/"+strings.Repeat("x", 43)+"/index.html", nil))
	require.Equal(t, http.StatusGone, unknown.Code)
	require.Equal(t, reads, repo.reads)
}
