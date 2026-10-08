//go:build integration

package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

// The real repository opens its own transactions, so a private schema keeps
// its expiry cleanup separate from every other integration fixture and ledger.
// Copy the harness connection configuration without displaying its credentials.
func codexGatewayBorrowPrivateIntegrationDB(t *testing.T, ctx context.Context) *sql.DB {
	t.Helper()
	conn, err := integrationDB.Conn(ctx)
	require.NoError(t, err)
	var connectionConfig *pgx.ConnConfig
	err = conn.Raw(func(driverConnection any) error {
		connection, ok := driverConnection.(*stdlib.Conn)
		if !ok {
			return errors.New("integration harness must use its pgx SQL driver")
		}
		connectionConfig = connection.Conn().Config().Copy()
		return nil
	})
	closeErr := conn.Close()
	require.NoError(t, err)
	require.NoError(t, closeErr)

	schema := "codex_borrow_it_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	_, err = integrationDB.ExecContext(ctx, "CREATE SCHEMA "+quotedSchema)
	require.NoError(t, err)
	var privateDB *sql.DB
	t.Cleanup(func() {
		if privateDB != nil {
			require.NoError(t, privateDB.Close())
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// This exact random schema was created above solely for this test. The
		// shared public schema and its diagnostic rows are never deleted.
		_, cleanupErr := integrationDB.ExecContext(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE")
		require.NoError(t, cleanupErr)
	})
	if connectionConfig.RuntimeParams == nil {
		connectionConfig.RuntimeParams = make(map[string]string)
	}
	connectionConfig.RuntimeParams["search_path"] = schema
	privateDB = stdlib.OpenDB(*connectionConfig)
	require.NoError(t, privateDB.PingContext(ctx))
	ddl, err := migrations.FS.ReadFile("275_codex_gateway_borrow_tests.sql")
	require.NoError(t, err)
	_, err = privateDB.ExecContext(ctx, string(ddl))
	require.NoError(t, err, "apply the original observation schema inside the private fixture")
	// Put one legacy record in the original schema before applying the new
	// migration. Its existing output and retention must survive unchanged.
	legacyTask, legacyResult := uuid.NewString(), uuid.NewString()
	legacyClient, err := uuid.NewV7()
	require.NoError(t, err)
	created := time.Now().UTC().Truncate(time.Microsecond)
	legacyAnswer := []byte("legacy original answer\x00tail")
	_, err = privateDB.ExecContext(ctx, `INSERT INTO codex_gateway_borrow_test_tasks
        (id,client_task_id,created_by,request_hash,status,prompt,total,created_at,expires_at)
        VALUES ($1,$2,7,'legacy-hash','complete',$3,1,$4,$5)`, legacyTask, legacyClient.String(), service.CodexGatewayBorrowPelicanPrompt, created, created.Add(service.CodexGatewayBorrowTestTTL))
	require.NoError(t, err)
	_, err = privateDB.ExecContext(ctx, `INSERT INTO codex_gateway_borrow_test_results
        (id,task_id,ordinal,account_id,model_id,effort,status,raw_answer,error)
        VALUES ($1,$2,1,42,'gpt-6-astra','high','complete',$3,$3)`, legacyResult, legacyTask, legacyAnswer)
	require.NoError(t, err)
	ddl, err = migrations.FS.ReadFile("276_pelican_test_execution.sql")
	require.NoError(t, err)
	_, err = privateDB.ExecContext(ctx, string(ddl))
	require.NoError(t, err, "apply the actual additive execution migration")
	var answer, originalError []byte
	var executionMode string
	var timeout int
	var retention int64
	err = privateDB.QueryRowContext(ctx, `SELECT r.raw_answer,r.error,t.execution_mode,t.generation_timeout_seconds,
        EXTRACT(EPOCH FROM (t.expires_at-t.created_at))::BIGINT
        FROM codex_gateway_borrow_test_results r JOIN codex_gateway_borrow_test_tasks t ON t.id=r.task_id
        WHERE r.id=$1`, legacyResult).Scan(&answer, &originalError, &executionMode, &timeout, &retention)
	require.NoError(t, err)
	require.Equal(t, legacyAnswer, answer)
	require.Equal(t, legacyAnswer, originalError)
	require.Equal(t, service.PelicanExecutionModeLegacyCache, executionMode)
	require.Equal(t, 90, timeout, "historical observations retain their actual legacy budget")
	require.Equal(t, int64(service.CodexGatewayBorrowTestTTL/time.Second), retention)
	_, err = privateDB.ExecContext(ctx, `DELETE FROM codex_gateway_borrow_test_tasks WHERE id=$1`, legacyTask)
	require.NoError(t, err)
	return privateDB
}

func codexGatewayBorrowIntegrationTask(t *testing.T, accountID int64) *service.CodexGatewayBorrowTestTask {
	t.Helper()
	clientID, err := uuid.NewV7()
	require.NoError(t, err)
	created := time.Now().UTC().Truncate(time.Microsecond)
	task := &service.CodexGatewayBorrowTestTask{
		ID: uuid.NewString(), ClientTaskID: clientID.String(), CreatedBy: 7,
		RequestHash: strings.Repeat("a", 64), Status: "pending", Prompt: service.CodexGatewayBorrowPelicanPrompt,
		CreatedAt: created, ExpiresAt: created.Add(service.CodexGatewayBorrowTestTTL), Total: 1,
		GenerationTimeoutSeconds: service.PelicanDefaultGenerationTimeoutSeconds, ExecutionMode: service.PelicanExecutionModeAccount,
	}
	task.Results = []*service.CodexGatewayBorrowTestResult{{
		ID: uuid.NewString(), TaskID: task.ID, Ordinal: 1, AccountID: accountID,
		AccountName: "private integration fixture", Platform: "openai", ModelID: "gpt-6-astra", Effort: "high",
		Status: "pending", ExpiresAt: task.ExpiresAt,
	}}
	return task
}

func TestCodexGatewayBorrowTestRepositoryIntegrationLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := codexGatewayBorrowPrivateIntegrationDB(t, ctx)
	repo := NewCodexGatewayBorrowTestRepository(db)
	task := codexGatewayBorrowIntegrationTask(t, 987654321)

	// No account fixture is needed: these are retained observations, not an
	// account relationship or a model request. The genuine schema enforces this.
	stored, replayed, err := repo.Create(ctx, task)
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, task.ID, stored.ID)
	require.Equal(t, "pending", stored.Status)
	require.Len(t, stored.Results, 1)
	require.Equal(t, service.PelicanDefaultGenerationTimeoutSeconds, stored.GenerationTimeoutSeconds)
	require.Equal(t, service.PelicanExecutionModeAccount, stored.ExecutionMode)
	require.Equal(t, service.CodexGatewayBorrowTestTTL, stored.ExpiresAt.Sub(stored.CreatedAt))

	started := time.Now().UTC().Truncate(time.Microsecond)
	require.NoError(t, repo.StartTask(ctx, task.ID, started))
	result := task.Results[0]
	finished := started.Add(250 * time.Millisecond)
	result.Status, result.UpstreamModel = "complete", "gpt-6-astra-reported"
	result.StartedAt, result.FinishedAt, result.DurationMS = &started, &finished, 250
	generationStarted := started.Add(50 * time.Millisecond)
	result.GenerationStartedAt = &generationStarted
	result.QueueDurationMS, result.PreparationDurationMS, result.GenerationDurationMS = 20, 30, 200
	result.ActualEndpoint, result.ActualProtocol, result.ActualTransport, result.BorrowApplied = "https://local.test/responses", "responses", "http", true
	result.RawAnswer = "```svg\n<svg><script>animatePelican()</script>鹈鹕\x00tail</svg>\n```"
	result.RawResponse = "data: {\"original\":\"full stream\"}\n\n\x00end"
	result.RawHTML = "<svg><script>animatePelican()</script>鹈鹕\x00tail</svg>"
	result.HTML = "<html><body>" + result.RawHTML + "</body></html>"
	result.Error = "original upstream diagnostic token=admin-visible\x00untruncated tail"
	require.NoError(t, repo.SaveResult(ctx, result))
	const taskError = "task observation\x00original diagnostic"
	require.NoError(t, repo.FinishTask(ctx, task.ID, "complete", taskError, finished))

	readResult, err := repo.GetResult(ctx, result.ID)
	require.NoError(t, err)
	require.Equal(t, result.RawAnswer, readResult.RawAnswer)
	require.Equal(t, result.RawResponse, readResult.RawResponse)
	require.Equal(t, result.RawHTML, readResult.RawHTML)
	require.Equal(t, result.HTML, readResult.HTML)
	require.Equal(t, result.Error, readResult.Error)
	require.Equal(t, "gpt-6-astra", readResult.ModelID)
	require.Equal(t, result.UpstreamModel, readResult.UpstreamModel)
	require.Equal(t, result.DurationMS, readResult.DurationMS)
	require.Equal(t, result.Platform, readResult.Platform)
	require.Equal(t, result.ActualEndpoint, readResult.ActualEndpoint)
	require.Equal(t, result.ActualProtocol, readResult.ActualProtocol)
	require.Equal(t, result.ActualTransport, readResult.ActualTransport)
	require.True(t, readResult.BorrowApplied)
	require.Equal(t, result.QueueDurationMS, readResult.QueueDurationMS)
	require.Equal(t, result.PreparationDurationMS, readResult.PreparationDurationMS)
	require.Equal(t, result.GenerationDurationMS, readResult.GenerationDurationMS)
	require.True(t, readResult.GenerationStartedAt.Equal(generationStarted))
	require.True(t, readResult.StartedAt.Equal(started))
	require.True(t, readResult.FinishedAt.Equal(finished))
	require.True(t, readResult.ExpiresAt.Equal(task.ExpiresAt))
	readTask, err := repo.Get(ctx, task.ClientTaskID)
	require.NoError(t, err)
	require.Equal(t, "complete", readTask.Status)
	require.Equal(t, 1, readTask.Completed)
	require.Equal(t, taskError, readTask.Error)
	require.Len(t, readTask.Results, 1)
	require.Equal(t, result.RawAnswer, readTask.Results[0].RawAnswer)

	// A different server-side ID cannot replace an existing client UUID. The
	// replay returns the old complete task and its exact BYTEA-backed results.
	replayInput := codexGatewayBorrowIntegrationTask(t, result.AccountID)
	replayInput.ClientTaskID = task.ClientTaskID
	old, replayed, err := repo.Create(ctx, replayInput)
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, task.ID, old.ID)
	require.NotEqual(t, replayInput.ID, old.ID)
	require.Equal(t, result.RawResponse, old.Results[0].RawResponse)
	replayInput.RequestHash = strings.Repeat("b", 64)
	_, replayed, err = repo.Create(ctx, replayInput)
	require.True(t, replayed)
	require.ErrorIs(t, err, service.ErrCodexGatewayBorrowTestReplayConflict)
	var clientRows int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM codex_gateway_borrow_test_tasks WHERE client_task_id=$1`, task.ClientTaskID).Scan(&clientRows))
	require.Equal(t, 1, clientRows)

	keeper := codexGatewayBorrowIntegrationTask(t, 987654322)
	_, replayed, err = repo.Create(ctx, keeper)
	require.NoError(t, err)
	require.False(t, replayed)
	// Only a minimal, private canary is needed for the cleanup boundary. This
	// table intentionally uses a diagnostic name and an old timestamp; the
	// genuine shared diagnostic tables remain outside this pool's search path.
	_, err = db.ExecContext(ctx, `CREATE TABLE ops_error_logs (id UUID PRIMARY KEY, created_at TIMESTAMPTZ NOT NULL, original_body BYTEA NOT NULL)`)
	require.NoError(t, err)
	canaryID := uuid.NewString()
	canaryBody := []byte("retained diagnostic\x00original body")
	_, err = db.ExecContext(ctx, `INSERT INTO ops_error_logs(id,created_at,original_body) VALUES ($1,$2,$3)`, canaryID, task.CreatedAt.Add(-72*time.Hour), canaryBody)
	require.NoError(t, err)

	expiredCreated := time.Now().UTC().Add(-25 * time.Hour).Truncate(time.Microsecond)
	_, err = db.ExecContext(ctx, `UPDATE codex_gateway_borrow_test_tasks SET created_at=$2,expires_at=$3 WHERE id=$1`, task.ID, expiredCreated, expiredCreated.Add(service.CodexGatewayBorrowTestTTL))
	require.NoError(t, err)
	_, err = repo.Get(ctx, task.ID)
	require.ErrorIs(t, err, service.ErrCodexGatewayBorrowTestNotFound)
	_, err = repo.GetResult(ctx, result.ID)
	require.ErrorIs(t, err, service.ErrCodexGatewayBorrowTestNotFound)
	_, replayed, err = repo.Create(ctx, replayInput)
	require.True(t, replayed)
	require.ErrorIs(t, err, service.ErrCodexGatewayBorrowTestExpired)
	list, err := repo.List(ctx, 1, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), list.Total)
	require.Len(t, list.Items, 1)
	require.Equal(t, keeper.ID, list.Items[0].ID)

	countTaskAndChildren := func(taskID string) (int, int) {
		t.Helper()
		var tasks, children int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT
            (SELECT COUNT(*) FROM codex_gateway_borrow_test_tasks WHERE id=$1),
            (SELECT COUNT(*) FROM codex_gateway_borrow_test_results WHERE task_id=$1)`, taskID).Scan(&tasks, &children))
		return tasks, children
	}
	tasks, children := countTaskAndChildren(task.ID)
	require.Equal(t, 1, tasks, "expired rows stay physically present until cleanup")
	require.Equal(t, 1, children)
	// Invoke exactly the SQL method used by the runner's minute timer, without
	// sleeping or starting any model/runner service.
	require.NoError(t, repo.CleanupExpired(ctx, time.Now().UTC()))
	tasks, children = countTaskAndChildren(task.ID)
	require.Zero(t, tasks)
	require.Zero(t, children, "the migration's real foreign key must cascade")
	tasks, children = countTaskAndChildren(keeper.ID)
	require.Equal(t, 1, tasks)
	require.Equal(t, 1, children)
	var retainedBody []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT original_body FROM ops_error_logs WHERE id=$1`, canaryID).Scan(&retainedBody))
	require.Equal(t, canaryBody, retainedBody)
	_, err = repo.GetResult(ctx, keeper.Results[0].ID)
	require.NoError(t, err, "cleanup must preserve the unexpired observation")
}
