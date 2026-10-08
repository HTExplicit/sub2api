//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type pelicanTestHandlerLocalAccountReader struct {
	service.AccountRepository
	reads int
}

type pelicanTestHandlerReplayCaptureRepo struct {
	*codexGatewayBorrowHandlerTestRepo
	incoming *service.CodexGatewayBorrowTestTask
}

func (r *pelicanTestHandlerReplayCaptureRepo) Create(ctx context.Context, task *service.CodexGatewayBorrowTestTask) (*service.CodexGatewayBorrowTestTask, bool, error) {
	r.incoming = task
	return r.codexGatewayBorrowHandlerTestRepo.Create(ctx, task)
}

func (r *pelicanTestHandlerLocalAccountReader) GetByIDs(_ context.Context, _ []int64) ([]*service.Account, error) {
	r.reads++
	return []*service.Account{{ID: 42, Name: "paused account", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusDisabled}}, nil
}

func TestPelicanTestHandlerOptionsDefaultsAndSelectedLocalModels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	accounts := &pelicanTestHandlerLocalAccountReader{}
	h := NewPelicanTestHandler(service.NewPelicanTestCatalog(accounts), nil, nil)
	router := gin.New()
	router.GET("/options", h.GetOptions)
	router.POST("/options", h.GetAccountOptions)
	request := func(method, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/options", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, r)
		return w
	}
	defaults := request(http.MethodGet, "")
	require.Equal(t, http.StatusOK, defaults.Code)
	require.Zero(t, accounts.reads)
	require.Contains(t, defaults.Body.String(), `"default_generation_timeout_seconds":600`)
	require.Equal(t, "private, no-store", defaults.Header().Get("Cache-Control"))
	selected := request(http.MethodPost, `{"account_ids":[42,999]}`)
	require.Equal(t, http.StatusOK, selected.Code)
	var envelope struct {
		Data service.PelicanTestOptions `json:"data"`
	}
	require.NoError(t, json.Unmarshal(selected.Body.Bytes(), &envelope))
	require.Equal(t, 1, accounts.reads)
	require.Len(t, envelope.Data.Accounts, 2)
	require.Equal(t, service.StatusDisabled, envelope.Data.Accounts[0].Status)
	require.Equal(t, "missing", envelope.Data.Accounts[1].Status)
	ids := make([]string, 0, len(envelope.Data.Accounts[0].Models))
	for _, model := range envelope.Data.Accounts[0].Models {
		ids = append(ids, model.ID)
	}
	require.Contains(t, ids, "gpt-6-astra")
	require.Contains(t, ids, "gpt-6.1-sol")
	invalid := request(http.MethodPost, `{"account_ids":[0]}`)
	require.Equal(t, http.StatusBadRequest, invalid.Code)
	require.Equal(t, 1, accounts.reads)
}

func TestPelicanTestHandlerSSEReplayAndHistoryNeverGenerateAndPreserveRawContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	legacy, repo := newCodexGatewayBorrowHandlerFixture(t)
	h := NewPelicanTestHandler(service.NewPelicanTestCatalog(nil), legacy.runner, legacy.preview)
	router := gin.New()
	router.GET("/tests/:id", h.GetTest)
	router.GET("/tests", h.ListTests)
	router.POST("/tests", h.StartTests)
	router.GET("/api/v1/pelican-tests/preview/:cap/index.html", h.ServePreview)
	history := httptest.NewRecorder()
	router.ServeHTTP(history, httptest.NewRequest(http.MethodGet, "/tests/last", nil))
	require.Equal(t, http.StatusOK, history.Code)
	var envelope struct {
		Data service.CodexGatewayBorrowTestTask `json:"data"`
	}
	require.NoError(t, json.Unmarshal(history.Body.Bytes(), &envelope))
	require.Equal(t, repo.result.Error, envelope.Data.Results[0].Error)
	require.Equal(t, repo.result.RawAnswer, envelope.Data.Results[0].RawAnswer)
	require.True(t, strings.HasPrefix(envelope.Data.Results[0].PreviewURL, "/api/v1/pelican-tests/preview/"))
	preview := httptest.NewRecorder()
	router.ServeHTTP(preview, httptest.NewRequest(http.MethodGet, envelope.Data.Results[0].PreviewURL, nil))
	require.Equal(t, http.StatusOK, preview.Code)
	require.Equal(t, repo.result.HTML, preview.Body.String())
	require.Equal(t, service.CodexGatewayBorrowPreviewCSP, preview.Header().Get("Content-Security-Policy"))
	stream := httptest.NewRecorder()
	body := fmt.Sprintf(`{"client_task_id":%q,"generation_timeout_seconds":600,"targets":[{"account_id":42,"model_id":"gpt-6-astra"}]}`, repo.task.ClientTaskID)
	request := httptest.NewRequest(http.MethodPost, "/tests", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(stream, request)
	require.Equal(t, http.StatusOK, stream.Code)
	require.Equal(t, "text/event-stream", stream.Header().Get("Content-Type"))
	require.Contains(t, stream.Body.String(), `"type":"task_start"`)
	require.Contains(t, stream.Body.String(), `"type":"task_complete"`)
	require.NotContains(t, stream.Body.String(), `"type":"result_started"`)
	require.Contains(t, stream.Body.String(), "original upstream error token=admin-visible")
	require.Equal(t, 1, repo.creates)
	require.Zero(t, repo.writes)
}

func TestPelicanTestHandlerForcesAccountModeAndFixedPromptBeforeReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, existing := newCodexGatewayBorrowHandlerFixture(t)
	repo := &pelicanTestHandlerReplayCaptureRepo{codexGatewayBorrowHandlerTestRepo: existing}
	runner := service.NewCodexGatewayBorrowTestRunner(repo, nil, nil)
	preview := service.NewCodexGatewayBorrowPreviewService(repo)
	t.Cleanup(runner.Stop)
	t.Cleanup(preview.Stop)
	h := NewPelicanTestHandler(service.NewPelicanTestCatalog(nil), runner, preview)
	router := gin.New()
	router.POST("/tests", h.StartTests)
	body := fmt.Sprintf(`{"client_task_id":%q,"standalone":false,"execution_mode":"legacy_cache","prompt":"client modified prompt","targets":[{"account_id":42,"model_id":"gpt-6-astra"}]}`, existing.task.ClientTaskID)
	request := httptest.NewRequest(http.MethodPost, "/tests", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, request)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, repo.incoming)
	require.Equal(t, service.PelicanExecutionModeAccount, repo.incoming.ExecutionMode)
	require.Equal(t, service.CodexGatewayBorrowPelicanPrompt, repo.incoming.Prompt)
	require.Equal(t, 600, repo.incoming.GenerationTimeoutSeconds)
	require.Equal(t, int64(42), repo.incoming.Results[0].AccountID)
	require.Empty(t, repo.incoming.Results[0].Effort, "the generator applies only the selected model's supported default")
	require.Zero(t, existing.writes, "replay must not dispatch the forced-account execution")
}

func TestPelicanTestHandlerRejectsGenerationBudgetBeforeCreate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	legacy, repo := newCodexGatewayBorrowHandlerFixture(t)
	h := NewPelicanTestHandler(service.NewPelicanTestCatalog(nil), legacy.runner, legacy.preview)
	router := gin.New()
	router.POST("/tests", h.StartTests)
	for _, seconds := range []int{59, 1801} {
		w := httptest.NewRecorder()
		body := fmt.Sprintf(`{"client_task_id":%q,"generation_timeout_seconds":%d,"targets":[{"account_id":42,"model_id":"gpt-6-astra"}]}`, repo.task.ClientTaskID, seconds)
		r := httptest.NewRequest(http.MethodPost, "/tests", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, r)
		require.Equal(t, http.StatusBadRequest, w.Code)
	}
	require.Zero(t, repo.creates, "invalid budget must not leave a pending task")
	require.Zero(t, repo.writes)
}
