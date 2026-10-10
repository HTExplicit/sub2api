//go:build unit

package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type pelicanSummaryHandlerRepo struct {
	*codexGatewayBorrowHandlerTestRepo
}

func (r *pelicanSummaryHandlerRepo) GetTaskSnapshot(context.Context, string) (*service.PelicanTaskSnapshot, error) {
	copy := *r.task
	copy.Results = nil
	return &service.PelicanTaskSnapshot{CodexGatewayBorrowTestTask: &copy, Counts: map[string]int{"complete": 1}}, nil
}
func (r *pelicanSummaryHandlerRepo) ListTaskResults(context.Context, string, service.PelicanResultFilter) (*service.PelicanResultPage, error) {
	return &service.PelicanResultPage{Items: []*service.PelicanResultSummary{{ID: r.result.ID, TaskID: r.task.ID, Status: "complete", HasPreview: true}}, Total: 1, Page: 1, PageSize: 24}, nil
}
func TestPelicanBackgroundReadEndpointsNeverGenerateOrSendRawInProgress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	legacy, base := newCodexGatewayBorrowHandlerFixture(t)
	repo := &pelicanSummaryHandlerRepo{base}
	runner := service.NewCodexGatewayBorrowTestRunner(repo, nil, nil)
	t.Cleanup(runner.Stop)
	h := NewPelicanTestHandler(nil, runner, legacy.preview)
	router := gin.New()
	router.GET("/tasks/:id", h.GetTask)
	router.GET("/tasks/:id/results", h.GetTaskResults)
	router.GET("/tasks/:id/results/:result_id", h.GetTaskResult)
	router.GET("/tasks/:id/events", h.ObserveTask)
	for _, suffix := range []string{"", "/results", "/events"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tasks/"+base.task.ID+suffix, nil))
		require.Equal(t, http.StatusOK, w.Code)
		require.NotContains(t, w.Body.String(), "raw_answer")
		require.NotContains(t, w.Body.String(), "admin-visible")
		require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
	}
	full := httptest.NewRecorder()
	router.ServeHTTP(full, httptest.NewRequest(http.MethodGet, "/tasks/"+base.task.ID+"/results/"+base.result.ID, nil))
	require.Equal(t, http.StatusOK, full.Code)
	require.Contains(t, full.Body.String(), "admin-visible")
	base.task.Status = "running"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	disconnected := httptest.NewRecorder()
	router.ServeHTTP(disconnected, httptest.NewRequest(http.MethodGet, "/tasks/"+base.task.ID+"/events", nil).WithContext(ctx))
	require.Equal(t, "running", base.task.Status)
	require.Zero(t, base.creates)
	require.Zero(t, base.writes)
}
