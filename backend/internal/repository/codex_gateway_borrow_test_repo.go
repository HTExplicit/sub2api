package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type codexGatewayBorrowTestRepository struct{ db *sql.DB }

func NewCodexGatewayBorrowTestRepository(db *sql.DB) service.CodexGatewayBorrowTestRepository {
	return &codexGatewayBorrowTestRepository{db: db}
}

const codexGatewayBorrowTestTaskColumns = `id, client_task_id, created_by, request_hash, status, prompt,
    created_at, expires_at, started_at, finished_at, total, completed, error, generation_timeout_seconds, execution_mode`

const codexGatewayBorrowTestResultColumns = `r.id, r.task_id, r.ordinal, r.account_id, r.account_name, r.model_id,
    r.upstream_model, r.effort, r.status, r.raw_answer, r.raw_response, r.raw_html, r.html, r.error,
    r.started_at, r.finished_at, r.duration_ms, t.expires_at, r.platform, r.actual_endpoint, r.actual_protocol,
    r.actual_transport, r.borrow_applied, r.queue_duration_ms, r.preparation_duration_ms, r.generation_duration_ms, r.generation_started_at`

type codexGatewayBorrowTestScanner interface{ Scan(...any) error }

func scanCodexGatewayBorrowTestTask(row codexGatewayBorrowTestScanner) (*service.CodexGatewayBorrowTestTask, error) {
	task := &service.CodexGatewayBorrowTestTask{}
	var started, finished sql.NullTime
	var rawError []byte
	if err := row.Scan(&task.ID, &task.ClientTaskID, &task.CreatedBy, &task.RequestHash, &task.Status, &task.Prompt,
		&task.CreatedAt, &task.ExpiresAt, &started, &finished, &task.Total, &task.Completed, &rawError, &task.GenerationTimeoutSeconds, &task.ExecutionMode); err != nil {
		return nil, err
	}
	if started.Valid {
		task.StartedAt = &started.Time
	}
	if finished.Valid {
		task.FinishedAt = &finished.Time
	}
	task.Error = string(rawError)
	return task, nil
}

func scanCodexGatewayBorrowTestResult(row codexGatewayBorrowTestScanner) (*service.CodexGatewayBorrowTestResult, error) {
	result := &service.CodexGatewayBorrowTestResult{}
	var answer, rawResponse, rawHTML, html, rawError []byte
	var started, finished, generationStarted sql.NullTime
	if err := row.Scan(&result.ID, &result.TaskID, &result.Ordinal, &result.AccountID, &result.AccountName, &result.ModelID,
		&result.UpstreamModel, &result.Effort, &result.Status, &answer, &rawResponse, &rawHTML, &html, &rawError,
		&started, &finished, &result.DurationMS, &result.ExpiresAt, &result.Platform, &result.ActualEndpoint, &result.ActualProtocol,
		&result.ActualTransport, &result.BorrowApplied, &result.QueueDurationMS, &result.PreparationDurationMS, &result.GenerationDurationMS, &generationStarted); err != nil {
		return nil, err
	}
	result.RawAnswer, result.RawResponse, result.RawHTML, result.HTML, result.Error = string(answer), string(rawResponse), string(rawHTML), string(html), string(rawError)
	if started.Valid {
		result.StartedAt = &started.Time
	}
	if finished.Valid {
		result.FinishedAt = &finished.Time
	}
	if generationStarted.Valid {
		result.GenerationStartedAt = &generationStarted.Time
	}
	if result.Status != "complete" {
		result.PreviewUnavailable = "test " + result.Status
	} else if result.HTML == "" {
		result.PreviewUnavailable = "answer contains no extractable HTML or SVG"
	}
	return result, nil
}

// The conflict read intentionally includes expired UUIDs. Reposting a UUID can
// never re-run a task while its row exists, even before expired-row cleanup.
func (r *codexGatewayBorrowTestRepository) Create(ctx context.Context, task *service.CodexGatewayBorrowTestTask) (*service.CodexGatewayBorrowTestTask, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	stored, err := scanCodexGatewayBorrowTestTask(tx.QueryRowContext(ctx, `INSERT INTO codex_gateway_borrow_test_tasks
        (id,client_task_id,created_by,request_hash,status,prompt,total,created_at,expires_at,generation_timeout_seconds,execution_mode)
        VALUES ($1,$2,$3,$4,'pending',$5,$6,$7,$8,$9,$10)
        ON CONFLICT (client_task_id) DO NOTHING RETURNING `+codexGatewayBorrowTestTaskColumns,
		task.ID, task.ClientTaskID, task.CreatedBy, task.RequestHash, task.Prompt, task.Total, task.CreatedAt, task.ExpiresAt, task.GenerationTimeoutSeconds, task.ExecutionMode))
	if errors.Is(err, sql.ErrNoRows) {
		stored, err = scanCodexGatewayBorrowTestTask(tx.QueryRowContext(ctx, `SELECT `+codexGatewayBorrowTestTaskColumns+`
            FROM codex_gateway_borrow_test_tasks WHERE scope='manual_pelican' AND client_task_id=$1 FOR UPDATE`, task.ClientTaskID))
		if err != nil {
			return nil, false, err
		}
		if !stored.ExpiresAt.After(time.Now().UTC()) {
			return nil, true, service.ErrCodexGatewayBorrowTestExpired
		}
		if stored.RequestHash != task.RequestHash {
			return nil, true, service.ErrCodexGatewayBorrowTestReplayConflict
		}
		if err = tx.Commit(); err != nil {
			return nil, true, err
		}
		stored, err = r.Get(ctx, stored.ID)
		return stored, true, err
	}
	if err != nil {
		return nil, false, err
	}
	for _, result := range task.Results {
		if _, err = tx.ExecContext(ctx, `INSERT INTO codex_gateway_borrow_test_results
            (id,task_id,ordinal,account_id,account_name,model_id,effort,status,platform)
            VALUES ($1,$2,$3,$4,$5,$6,$7,'pending',$8)`, result.ID, stored.ID, result.Ordinal, result.AccountID, result.AccountName, result.ModelID, result.Effort, result.Platform); err != nil {
			return nil, false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	stored.Results = task.Results
	return stored, false, nil
}

func (r *codexGatewayBorrowTestRepository) Get(ctx context.Context, id string) (*service.CodexGatewayBorrowTestTask, error) {
	task, err := scanCodexGatewayBorrowTestTask(r.db.QueryRowContext(ctx, `SELECT `+codexGatewayBorrowTestTaskColumns+`
        FROM codex_gateway_borrow_test_tasks WHERE scope='manual_pelican' AND (id=$1 OR client_task_id=$1) AND expires_at>CURRENT_TIMESTAMP`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrCodexGatewayBorrowTestNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+codexGatewayBorrowTestResultColumns+`
        FROM codex_gateway_borrow_test_results r JOIN codex_gateway_borrow_test_tasks t ON t.id=r.task_id
        WHERE t.scope='manual_pelican' AND t.id=$1 AND t.expires_at>CURRENT_TIMESTAMP ORDER BY r.ordinal`, task.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	task.Results = make([]*service.CodexGatewayBorrowTestResult, 0, task.Total)
	for rows.Next() {
		result, scanErr := scanCodexGatewayBorrowTestResult(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		task.Results = append(task.Results, result)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if !task.ExpiresAt.After(time.Now().UTC()) {
		return nil, service.ErrCodexGatewayBorrowTestNotFound
	}
	return task, nil
}

func (r *codexGatewayBorrowTestRepository) GetResult(ctx context.Context, id string) (*service.CodexGatewayBorrowTestResult, error) {
	result, err := scanCodexGatewayBorrowTestResult(r.db.QueryRowContext(ctx, `SELECT `+codexGatewayBorrowTestResultColumns+`
        FROM codex_gateway_borrow_test_results r JOIN codex_gateway_borrow_test_tasks t ON t.id=r.task_id
        WHERE t.scope='manual_pelican' AND r.id=$1 AND t.expires_at>CURRENT_TIMESTAMP`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrCodexGatewayBorrowTestNotFound
	}
	if err == nil && !result.ExpiresAt.After(time.Now().UTC()) {
		return nil, service.ErrCodexGatewayBorrowTestNotFound
	}
	return result, err
}

func (r *codexGatewayBorrowTestRepository) List(ctx context.Context, page, size int) (*service.CodexGatewayBorrowTestList, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	list := &service.CodexGatewayBorrowTestList{Items: make([]*service.CodexGatewayBorrowTestTask, 0, size), Page: page, Size: size, PageSize: size}
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM codex_gateway_borrow_test_tasks
        WHERE scope='manual_pelican' AND expires_at>CURRENT_TIMESTAMP`).Scan(&list.Total); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+codexGatewayBorrowTestTaskColumns+` FROM codex_gateway_borrow_test_tasks
        WHERE scope='manual_pelican' AND expires_at>CURRENT_TIMESTAMP ORDER BY created_at DESC,id DESC LIMIT $1 OFFSET $2`, size, (page-1)*size)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		task, scanErr := scanCodexGatewayBorrowTestTask(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if task.ExpiresAt.After(time.Now().UTC()) {
			list.Items = append(list.Items, task)
		}
	}
	return list, rows.Err()
}

func codexGatewayBorrowRequireAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return service.ErrCodexGatewayBorrowTestNotFound
	}
	return nil
}

func (r *codexGatewayBorrowTestRepository) StartTask(ctx context.Context, id string, started time.Time) error {
	return codexGatewayBorrowRequireAffected(r.db.ExecContext(ctx, `UPDATE codex_gateway_borrow_test_tasks
        SET status='running',started_at=$2,updated_at=CURRENT_TIMESTAMP
        WHERE scope='manual_pelican' AND id=$1 AND status='pending' AND expires_at>CURRENT_TIMESTAMP`, id, started))
}

func (r *codexGatewayBorrowTestRepository) SaveResult(ctx context.Context, result *service.CodexGatewayBorrowTestResult) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = codexGatewayBorrowRequireAffected(tx.ExecContext(ctx, `UPDATE codex_gateway_borrow_test_results r
        SET account_name=$3,upstream_model=$4,effort=$5,status=$6,raw_answer=$7,raw_response=$8,raw_html=$9,html=$10,error=$11,
            started_at=$12,finished_at=$13,duration_ms=$14,platform=$15,actual_endpoint=$16,actual_protocol=$17,
            actual_transport=$18,borrow_applied=$19,queue_duration_ms=$20,preparation_duration_ms=$21,
            generation_duration_ms=$22,generation_started_at=$23,updated_at=CURRENT_TIMESTAMP
        FROM codex_gateway_borrow_test_tasks t
        WHERE r.id=$1 AND r.task_id=$2 AND t.id=r.task_id AND t.scope='manual_pelican' AND t.expires_at>CURRENT_TIMESTAMP`,
		result.ID, result.TaskID, result.AccountName, result.UpstreamModel, result.Effort, result.Status,
		[]byte(result.RawAnswer), []byte(result.RawResponse), []byte(result.RawHTML), []byte(result.HTML), []byte(result.Error),
		result.StartedAt, result.FinishedAt, result.DurationMS, result.Platform, result.ActualEndpoint, result.ActualProtocol,
		result.ActualTransport, result.BorrowApplied, result.QueueDurationMS, result.PreparationDurationMS, result.GenerationDurationMS, result.GenerationStartedAt)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE codex_gateway_borrow_test_tasks t
        SET completed=(SELECT COUNT(*) FROM codex_gateway_borrow_test_results r WHERE r.task_id=t.id
            AND r.status NOT IN ('pending','running')),updated_at=CURRENT_TIMESTAMP
        WHERE t.scope='manual_pelican' AND t.id=$1 AND t.expires_at>CURRENT_TIMESTAMP`, result.TaskID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *codexGatewayBorrowTestRepository) FinishTask(ctx context.Context, id, status, errorText string, finished time.Time) error {
	return codexGatewayBorrowRequireAffected(r.db.ExecContext(ctx, `UPDATE codex_gateway_borrow_test_tasks
        SET status=$2,error=$3,finished_at=$4,updated_at=CURRENT_TIMESTAMP
        WHERE scope='manual_pelican' AND id=$1 AND expires_at>CURRENT_TIMESTAMP`, id, status, []byte(errorText), finished))
}

func (r *codexGatewayBorrowTestRepository) MarkInterrupted(ctx context.Context, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	message := []byte("process stopped before the test finished; this task will not be replayed")
	if _, err = tx.ExecContext(ctx, `UPDATE codex_gateway_borrow_test_results r
        SET status='incomplete',error=$2,finished_at=$1,updated_at=$1
        FROM codex_gateway_borrow_test_tasks t WHERE t.id=r.task_id AND t.scope='manual_pelican'
        AND t.expires_at>$1 AND t.status IN ('pending','running') AND r.status IN ('pending','running')`, now, message); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE codex_gateway_borrow_test_tasks t
        SET status='incomplete',error=$2,finished_at=$1,completed=t.total,updated_at=$1
        WHERE t.scope='manual_pelican' AND t.expires_at>$1 AND t.status IN ('pending','running')`, now, message); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *codexGatewayBorrowTestRepository) CleanupExpired(ctx context.Context, now time.Time) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM codex_gateway_borrow_test_tasks
        WHERE scope='manual_pelican' AND expires_at<=$1`, now)
	return err
}
