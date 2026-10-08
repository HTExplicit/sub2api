//go:build unit

package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func newCodexGatewayBorrowRepoTest(t *testing.T) (*codexGatewayBorrowTestRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &codexGatewayBorrowTestRepository{db: db}, mock
}

func codexGatewayBorrowTaskTestRows(task *service.CodexGatewayBorrowTestTask) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "client_task_id", "created_by", "request_hash", "status", "prompt", "created_at", "expires_at", "started_at", "finished_at", "total", "completed", "error", "generation_timeout_seconds", "execution_mode"}).AddRow(
		task.ID, task.ClientTaskID, task.CreatedBy, task.RequestHash, task.Status, task.Prompt, task.CreatedAt, task.ExpiresAt, nil, nil, task.Total, task.Completed, []byte(task.Error), task.GenerationTimeoutSeconds, task.ExecutionMode)
}

func codexGatewayBorrowResultTestRows(result *service.CodexGatewayBorrowTestResult) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "task_id", "ordinal", "account_id", "account_name", "model_id", "upstream_model", "effort", "status", "raw_answer", "raw_response", "raw_html", "html", "error", "started_at", "finished_at", "duration_ms", "expires_at", "platform", "actual_endpoint", "actual_protocol", "actual_transport", "borrow_applied", "queue_duration_ms", "preparation_duration_ms", "generation_duration_ms", "generation_started_at"}).AddRow(
		result.ID, result.TaskID, result.Ordinal, result.AccountID, result.AccountName, result.ModelID, result.UpstreamModel, result.Effort, result.Status, []byte(result.RawAnswer), []byte(result.RawResponse), []byte(result.RawHTML), []byte(result.HTML), []byte(result.Error), result.StartedAt, result.FinishedAt, result.DurationMS, result.ExpiresAt,
		result.Platform, result.ActualEndpoint, result.ActualProtocol, result.ActualTransport, result.BorrowApplied, result.QueueDurationMS, result.PreparationDurationMS, result.GenerationDurationMS, result.GenerationStartedAt)
}

func codexGatewayBorrowRepoFixture(t *testing.T) *service.CodexGatewayBorrowTestTask {
	t.Helper()
	now := time.Now().UTC()
	clientID, err := uuid.NewV7()
	require.NoError(t, err)
	task := &service.CodexGatewayBorrowTestTask{ID: uuid.NewString(), ClientTaskID: clientID.String(), CreatedBy: 7, RequestHash: "hash", Status: "pending", Prompt: service.CodexGatewayBorrowPelicanPrompt, CreatedAt: now, ExpiresAt: now.Add(service.CodexGatewayBorrowTestTTL), Total: 1,
		GenerationTimeoutSeconds: service.PelicanDefaultGenerationTimeoutSeconds, ExecutionMode: service.PelicanExecutionModeAccount}
	task.Results = []*service.CodexGatewayBorrowTestResult{{ID: uuid.NewString(), TaskID: task.ID, Ordinal: 1, AccountID: 42, AccountName: "paused target", Platform: "openai", ModelID: "gpt-6-astra", Effort: "high", Status: "pending", ExpiresAt: task.ExpiresAt}}
	return task
}

func TestCodexGatewayBorrowTestRepositoryCreateAtomicAndReplayReadsWithoutWrites(t *testing.T) {
	repo, mock := newCodexGatewayBorrowRepoTest(t)
	task := codexGatewayBorrowRepoFixture(t)
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO codex_gateway_borrow_test_tasks").WithArgs(task.ID, task.ClientTaskID, task.CreatedBy, task.RequestHash, task.Prompt, task.Total, task.CreatedAt, task.ExpiresAt, task.GenerationTimeoutSeconds, task.ExecutionMode).WillReturnRows(codexGatewayBorrowTaskTestRows(task))
	result := task.Results[0]
	mock.ExpectExec("INSERT INTO codex_gateway_borrow_test_results").WithArgs(result.ID, task.ID, 1, result.AccountID, result.AccountName, result.ModelID, result.Effort, result.Platform).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	stored, replayed, err := repo.Create(context.Background(), task)
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, task.ID, stored.ID)
	require.Len(t, stored.Results, 1)
	require.Equal(t, task.GenerationTimeoutSeconds, stored.GenerationTimeoutSeconds)
	require.Equal(t, task.ExecutionMode, stored.ExecutionMode)
	// A repeated client UUID gets the original full observation, not another child.
	task.Status = "complete"
	result.Status, result.RawAnswer, result.RawResponse, result.Error = "complete", "<html>answer\x00tail</html>", "full raw stream\x00end", "untruncated upstream diagnostic"
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO codex_gateway_borrow_test_tasks").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("FROM codex_gateway_borrow_test_tasks WHERE scope='manual_pelican' AND client_task_id=\\$1 FOR UPDATE").WithArgs(task.ClientTaskID).WillReturnRows(codexGatewayBorrowTaskTestRows(task))
	mock.ExpectCommit()
	mock.ExpectQuery("FROM codex_gateway_borrow_test_tasks WHERE scope='manual_pelican'.*expires_at>CURRENT_TIMESTAMP").WithArgs(task.ID).WillReturnRows(codexGatewayBorrowTaskTestRows(task))
	mock.ExpectQuery("FROM codex_gateway_borrow_test_results r JOIN codex_gateway_borrow_test_tasks t.*t.expires_at>CURRENT_TIMESTAMP").WithArgs(task.ID).WillReturnRows(codexGatewayBorrowResultTestRows(result))
	stored, replayed, err = repo.Create(context.Background(), task)
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, result.RawAnswer, stored.Results[0].RawAnswer)
	require.Equal(t, result.RawResponse, stored.Results[0].RawResponse)
	require.Equal(t, result.Error, stored.Results[0].Error)
	require.Equal(t, result.Platform, stored.Results[0].Platform)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCodexGatewayBorrowTestRepositoryRejectsConflictingAndExpiredReplay(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "conflict", true: "expired"}[expired], func(t *testing.T) {
			repo, mock := newCodexGatewayBorrowRepoTest(t)
			task := codexGatewayBorrowRepoFixture(t)
			original := *task
			if expired {
				original.CreatedAt = time.Now().Add(-25 * time.Hour)
				original.ExpiresAt = original.CreatedAt.Add(service.CodexGatewayBorrowTestTTL)
			} else {
				original.RequestHash = "different"
			}
			mock.ExpectBegin()
			mock.ExpectQuery("INSERT INTO codex_gateway_borrow_test_tasks").WillReturnRows(sqlmock.NewRows([]string{"id"}))
			mock.ExpectQuery("client_task_id=\\$1 FOR UPDATE").WillReturnRows(codexGatewayBorrowTaskTestRows(&original))
			mock.ExpectRollback()
			_, replayed, err := repo.Create(context.Background(), task)
			require.True(t, replayed)
			if expired {
				require.ErrorIs(t, err, service.ErrCodexGatewayBorrowTestExpired)
			} else {
				require.ErrorIs(t, err, service.ErrCodexGatewayBorrowTestReplayConflict)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCodexGatewayBorrowTestRepositoryExpiredReadsAndScopedCleanup(t *testing.T) {
	repo, mock := newCodexGatewayBorrowRepoTest(t)
	id := uuid.NewString()
	mock.ExpectQuery("FROM codex_gateway_borrow_test_tasks WHERE scope='manual_pelican'.*expires_at>CURRENT_TIMESTAMP").WithArgs(id).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	_, err := repo.Get(context.Background(), id)
	require.ErrorIs(t, err, service.ErrCodexGatewayBorrowTestNotFound)
	mock.ExpectQuery("WHERE t.scope='manual_pelican' AND r.id=\\$1 AND t.expires_at>CURRENT_TIMESTAMP").WithArgs(id).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	_, err = repo.GetResult(context.Background(), id)
	require.ErrorIs(t, err, service.ErrCodexGatewayBorrowTestNotFound)
	now := time.Now().UTC()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM codex_gateway_borrow_test_tasks WHERE scope='manual_pelican' AND expires_at<=$1")).WithArgs(now).WillReturnResult(sqlmock.NewResult(0, 2))
	require.NoError(t, repo.CleanupExpired(context.Background(), now))
	require.NoError(t, mock.ExpectationsWereMet())
	data, err := migrations.FS.ReadFile("275_codex_gateway_borrow_tests.sql")
	require.NoError(t, err)
	require.Contains(t, string(data), "task_id UUID NOT NULL REFERENCES codex_gateway_borrow_test_tasks(id) ON DELETE CASCADE")
	require.Contains(t, string(data), "client_task_id UUID NOT NULL UNIQUE")
	require.Contains(t, string(data), "expires_at - created_at = INTERVAL '24 hours'")
	require.Contains(t, string(data), "raw_answer BYTEA NOT NULL")
	data, err = migrations.FS.ReadFile("276_pelican_test_execution.sql")
	require.NoError(t, err)
	require.Contains(t, string(data), "generation_timeout_seconds BETWEEN 60 AND 1800")
	require.Contains(t, string(data), "DEFAULT 'legacy_cache'")
	require.NotContains(t, string(data), "DROP")
}

func TestCodexGatewayBorrowTestRepositorySavesFullRawValuesAndMarksRestartIncomplete(t *testing.T) {
	repo, mock := newCodexGatewayBorrowRepoTest(t)
	task := codexGatewayBorrowRepoFixture(t)
	result := task.Results[0]
	result.Status, result.UpstreamModel, result.RawAnswer, result.RawResponse, result.RawHTML, result.HTML, result.Error = "incomplete", "reported", "answer\x00tail", "SSE\x00raw", "source\x00raw", "html\x00raw", "API returned 403: entire original body\x00tail"
	started := time.Now().UTC()
	result.ActualEndpoint, result.ActualProtocol, result.ActualTransport, result.BorrowApplied = "https://upstream.test/responses", "responses", "http", true
	result.QueueDurationMS, result.PreparationDurationMS, result.GenerationDurationMS, result.GenerationStartedAt = 1200, 2300, 3400, &started
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE codex_gateway_borrow_test_results r.*t.expires_at>CURRENT_TIMESTAMP").WithArgs(result.ID, result.TaskID, result.AccountName, result.UpstreamModel, result.Effort, result.Status, []byte(result.RawAnswer), []byte(result.RawResponse), []byte(result.RawHTML), []byte(result.HTML), []byte(result.Error), nil, nil, int64(0),
		result.Platform, result.ActualEndpoint, result.ActualProtocol, result.ActualTransport, result.BorrowApplied, result.QueueDurationMS, result.PreparationDurationMS, result.GenerationDurationMS, result.GenerationStartedAt).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE codex_gateway_borrow_test_tasks t.*completed=.*status NOT IN").WithArgs(task.ID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.SaveResult(context.Background(), result))
	mock.ExpectQuery("WHERE t.scope='manual_pelican' AND r.id=\\$1 AND t.expires_at>CURRENT_TIMESTAMP").WithArgs(result.ID).WillReturnRows(codexGatewayBorrowResultTestRows(result))
	stored, err := repo.GetResult(context.Background(), result.ID)
	require.NoError(t, err)
	result.PreviewUnavailable = "test incomplete"
	require.Equal(t, result, stored, "actual invocation and phase durations round-trip alongside untruncated raw observations")
	// The reader derives the preview reason from the terminal status.
	require.Equal(t, "test incomplete", stored.PreviewUnavailable)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE codex_gateway_borrow_test_results r.*status='incomplete'.*t.scope='manual_pelican'.*r.status IN").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE codex_gateway_borrow_test_tasks t.*status='incomplete'.*t.scope='manual_pelican'").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.MarkInterrupted(context.Background(), time.Now().UTC()))
	require.NoError(t, mock.ExpectationsWereMet())
}
