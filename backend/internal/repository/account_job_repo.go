package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type accountJobRepository struct{ db *sql.DB }

func NewAccountJobRepository(db *sql.DB) service.AccountJobRepository {
	return &accountJobRepository{db: db}
}

const accountJobSelectColumns = `id, created_by, kind, idempotency_key, request_hash, status, metadata,
	target_count, processed_count, succeeded_count, failed_count, canceled_count,
	cancel_requested_at, error_code, error_message, retry_of_job_id, attempt,
	started_at, finished_at, created_at, updated_at`

const accountJobItemSelectColumns = `id, job_id, ordinal, action, target_account_id, status, metadata,
	error_code, error_message, started_at, finished_at, created_at, updated_at`

type accountJobScanner interface{ Scan(...any) error }

func scanAccountJob(row accountJobScanner) (*service.AccountJob, error) {
	job := &service.AccountJob{}
	var metadata []byte
	var cancelRequested, started, finished sql.NullTime
	var errorCode, errorMessage sql.NullString
	var retryOf sql.NullInt64
	if err := row.Scan(
		&job.ID, &job.CreatedBy, &job.Kind, &job.IdempotencyKey, &job.RequestHash, &job.Status, &metadata,
		&job.TargetCount, &job.ProcessedCount, &job.SucceededCount, &job.FailedCount, &job.CanceledCount,
		&cancelRequested, &errorCode, &errorMessage, &retryOf, &job.Attempt,
		&started, &finished, &job.CreatedAt, &job.UpdatedAt,
	); err != nil {
		return nil, err
	}
	job.Metadata = append(json.RawMessage(nil), metadata...)
	if cancelRequested.Valid {
		job.CancelRequestedAt = &cancelRequested.Time
	}
	if errorCode.Valid {
		job.ErrorCode = errorCode.String
	}
	if errorMessage.Valid {
		job.ErrorMessage = errorMessage.String
	}
	if retryOf.Valid {
		job.RetryOfJobID = &retryOf.Int64
	}
	if started.Valid {
		job.StartedAt = &started.Time
	}
	if finished.Valid {
		job.FinishedAt = &finished.Time
	}
	return job, nil
}

func scanAccountJobItem(row accountJobScanner) (*service.AccountJobItem, error) {
	item := &service.AccountJobItem{}
	var metadata []byte
	var target sql.NullInt64
	var errorCode, errorMessage sql.NullString
	var started, finished sql.NullTime
	if err := row.Scan(
		&item.ID, &item.JobID, &item.Ordinal, &item.Action, &target, &item.Status, &metadata,
		&errorCode, &errorMessage, &started, &finished, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return nil, err
	}
	item.Metadata = append(json.RawMessage(nil), metadata...)
	if target.Valid {
		item.TargetAccountID = &target.Int64
	}
	if errorCode.Valid {
		item.ErrorCode = errorCode.String
	}
	if errorMessage.Valid {
		item.ErrorMessage = errorMessage.String
	}
	if started.Valid {
		item.StartedAt = &started.Time
	}
	if finished.Valid {
		item.FinishedAt = &finished.Time
	}
	return item, nil
}

func (r *accountJobRepository) Create(ctx context.Context, params service.CreateAccountJobParams) (*service.AccountJob, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()

	job, err := scanAccountJob(tx.QueryRowContext(ctx, `
		INSERT INTO admin_account_jobs
			(created_by, kind, idempotency_key, request_hash, raw_payload_ciphertext,
			 raw_payload_expires_at, metadata, target_count, retry_of_job_id, attempt)
		VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,$10)
		ON CONFLICT (created_by, kind, idempotency_key) DO NOTHING
		RETURNING `+accountJobSelectColumns,
		params.CreatedBy, params.Kind, params.IdempotencyKey, params.RequestHash, params.PayloadCipher,
		params.PayloadExpires, string(normalizeRepositoryJobMetadata(params.Metadata)), len(params.Items),
		params.RetryOfJobID, params.Attempt,
	))
	if errors.Is(err, sql.ErrNoRows) {
		job, err = scanAccountJob(tx.QueryRowContext(ctx, `SELECT `+accountJobSelectColumns+`
			FROM admin_account_jobs WHERE created_by=$1 AND kind=$2 AND idempotency_key=$3 FOR UPDATE`,
			params.CreatedBy, params.Kind, params.IdempotencyKey))
		if err != nil {
			return nil, false, err
		}
		if job.RequestHash != params.RequestHash {
			return nil, false, service.ErrAccountJobIdempotencyConflict
		}
		if err = tx.Commit(); err != nil {
			return nil, false, err
		}
		return job, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	for index, seed := range params.Items {
		ordinal := seed.Ordinal
		if ordinal <= 0 {
			ordinal = index + 1
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO admin_account_job_items
			(job_id, ordinal, action, target_account_id, metadata) VALUES ($1,$2,$3,$4,$5::jsonb)`,
			job.ID, ordinal, seed.Action, seed.TargetAccountID, string(normalizeRepositoryJobMetadata(seed.Metadata))); err != nil {
			return nil, false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	return job, false, nil
}

func normalizeRepositoryJobMetadata(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`{}`)
	}
	return raw
}

func (r *accountJobRepository) FindIdempotent(ctx context.Context, createdBy int64, kind, key string) (*service.AccountJob, error) {
	job, err := scanAccountJob(r.db.QueryRowContext(ctx, `SELECT `+accountJobSelectColumns+`
		FROM admin_account_jobs WHERE created_by=$1 AND kind=$2 AND idempotency_key=$3`, createdBy, kind, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrAccountJobNotFound
	}
	return job, err
}

func (r *accountJobRepository) Get(ctx context.Context, jobID int64) (*service.AccountJob, error) {
	job, err := scanAccountJob(r.db.QueryRowContext(ctx, `SELECT `+accountJobSelectColumns+`
		FROM admin_account_jobs WHERE id=$1`, jobID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrAccountJobNotFound
	}
	return job, err
}

func normalizeAccountJobPage(page, size int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	return page, size
}

func (r *accountJobRepository) List(ctx context.Context, createdBy int64, kind, status string, page, pageSize int) (*service.AccountJobList, error) {
	page, pageSize = normalizeAccountJobPage(page, pageSize)
	where := []string{"1=1"}
	args := make([]any, 0, 5)
	if createdBy > 0 {
		args = append(args, createdBy)
		where = append(where, "created_by=$"+strconv.Itoa(len(args)))
	}
	if kind = strings.TrimSpace(kind); kind != "" {
		args = append(args, kind)
		where = append(where, "kind=$"+strconv.Itoa(len(args)))
	}
	if status = strings.TrimSpace(status); status != "" {
		args = append(args, status)
		where = append(where, "status=$"+strconv.Itoa(len(args)))
	}
	whereSQL := strings.Join(where, " AND ")
	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM admin_account_jobs WHERE "+whereSQL, args...).Scan(&total); err != nil {
		return nil, err
	}
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := r.db.QueryContext(ctx, `SELECT `+accountJobSelectColumns+` FROM admin_account_jobs WHERE `+whereSQL+
		` ORDER BY created_at DESC, id DESC LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]service.AccountJob, 0, pageSize)
	for rows.Next() {
		job, scanErr := scanAccountJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		jobs = append(jobs, *job)
	}
	return &service.AccountJobList{Items: jobs, Total: total, Page: page, Size: pageSize}, rows.Err()
}

func (r *accountJobRepository) ListItems(ctx context.Context, jobID int64, status string, page, pageSize int) (*service.AccountJobItemList, error) {
	page, pageSize = normalizeAccountJobPage(page, pageSize)
	args := []any{jobID}
	where := "job_id=$1"
	if status = strings.TrimSpace(status); status != "" {
		args = append(args, status)
		where += " AND status=$2"
	}
	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM admin_account_job_items WHERE "+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := r.db.QueryContext(ctx, `SELECT `+accountJobItemSelectColumns+` FROM admin_account_job_items WHERE `+where+
		` ORDER BY ordinal LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.AccountJobItem, 0, pageSize)
	for rows.Next() {
		item, scanErr := scanAccountJobItem(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, *item)
	}
	return &service.AccountJobItemList{Items: items, Total: total, Page: page, Size: pageSize}, rows.Err()
}

func (r *accountJobRepository) MarkInterrupted(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `UPDATE admin_account_job_items
		SET status='failed', error_code='interrupted', error_message='account job was interrupted',
		    finished_at=NOW(), updated_at=NOW()
		WHERE job_id IN (SELECT id FROM admin_account_jobs WHERE status='running')
		  AND status IN ('pending','running')`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE admin_account_jobs AS job
		SET status='failed', processed_count=counts.processed, succeeded_count=counts.succeeded,
		    failed_count=counts.failed, canceled_count=counts.canceled,
		    error_code='interrupted', error_message='account job was interrupted',
		    finished_at=NOW(), updated_at=NOW()
		FROM (SELECT job_id, COUNT(*) FILTER (WHERE status IN ('succeeded','failed','canceled'))::integer processed,
		             COUNT(*) FILTER (WHERE status='succeeded')::integer succeeded,
		             COUNT(*) FILTER (WHERE status='failed')::integer failed,
		             COUNT(*) FILTER (WHERE status='canceled')::integer canceled
		      FROM admin_account_job_items GROUP BY job_id) counts
		WHERE job.id=counts.job_id AND job.status='running'`); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *accountJobRepository) Claim(ctx context.Context) (*service.AccountJob, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('admin_account_jobs_claim'))`); err != nil {
		return nil, err
	}
	job, err := scanAccountJob(tx.QueryRowContext(ctx, `
		WITH candidate AS (
			SELECT pending.id
			FROM admin_account_jobs pending
			WHERE pending.status = 'pending'
			  AND (SELECT COUNT(*) FROM admin_account_jobs WHERE status = 'running') < 2
			  AND NOT EXISTS (
				SELECT 1 FROM admin_account_jobs active
				WHERE active.status = 'running'
				  AND active.created_by = pending.created_by
				  AND active.kind = pending.kind
			  )
			ORDER BY pending.created_at, pending.id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		), claimed AS (
			UPDATE admin_account_jobs job
			SET status='running', started_at=COALESCE(started_at,NOW()), updated_at=NOW()
			FROM candidate WHERE job.id=candidate.id
			RETURNING job.*
		)
		SELECT `+accountJobSelectColumns+` FROM claimed`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *accountJobRepository) Payload(ctx context.Context, jobID int64) (string, time.Time, error) {
	var payload sql.NullString
	var expires time.Time
	if err := r.db.QueryRowContext(ctx, `SELECT raw_payload_ciphertext, raw_payload_expires_at
		FROM admin_account_jobs WHERE id=$1`, jobID).Scan(&payload, &expires); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", time.Time{}, service.ErrAccountJobNotFound
		}
		return "", time.Time{}, err
	}
	return payload.String, expires, nil
}

func (r *accountJobRepository) ReservePendingItems(ctx context.Context, jobID int64, limit int) ([]service.AccountJobItem, error) {
	if limit <= 0 || limit > service.AccountJobBatchSize {
		limit = service.AccountJobBatchSize
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `WITH picked AS (
		SELECT item.id FROM admin_account_job_items item
		JOIN admin_account_jobs job ON job.id=item.job_id
		WHERE item.job_id=$1 AND item.status='pending' AND job.status='running'
		  AND job.cancel_requested_at IS NULL
		ORDER BY item.ordinal FOR UPDATE OF item SKIP LOCKED LIMIT $2
	), reserved AS (
		UPDATE admin_account_job_items item
		SET status='running', started_at=COALESCE(started_at,NOW()), updated_at=NOW()
		FROM picked WHERE item.id=picked.id RETURNING item.*
	)
	SELECT `+accountJobItemSelectColumns+` FROM reserved ORDER BY ordinal`, jobID, limit)
	if err != nil {
		return nil, err
	}
	items := make([]service.AccountJobItem, 0, limit)
	for rows.Next() {
		item, scanErr := scanAccountJobItem(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, scanErr
		}
		items = append(items, *item)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *accountJobRepository) CancelRequested(ctx context.Context, jobID int64) (bool, error) {
	var canceled bool
	err := r.db.QueryRowContext(ctx, `SELECT cancel_requested_at IS NOT NULL
		FROM admin_account_jobs WHERE id=$1 AND status='running'`, jobID).Scan(&canceled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, service.ErrAccountJobNotFound
	}
	return canceled, err
}

func (r *accountJobRepository) CompleteItems(ctx context.Context, jobID int64, results []service.AccountJobExecutionResult) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, result := range results {
		status := result.Status
		if status != service.AccountJobItemStatusSucceeded && status != service.AccountJobItemStatusFailed && status != service.AccountJobItemStatusCanceled {
			status = service.AccountJobItemStatusFailed
		}
		metadata := normalizeRepositoryJobMetadata(result.Metadata)
		if _, err = tx.ExecContext(ctx, `UPDATE admin_account_job_items
			SET status=$3, metadata=$4::jsonb, error_code=NULLIF($5,''), error_message=NULLIF($6,''),
			    finished_at=NOW(), updated_at=NOW()
			WHERE job_id=$1 AND id=$2 AND status='running'`,
			jobID, result.ItemID, status, string(metadata), result.ErrorCode, result.ErrorMessage); err != nil {
			return err
		}
	}
	if err = refreshAccountJobCounts(ctx, tx, jobID); err != nil {
		return err
	}
	return tx.Commit()
}

func refreshAccountJobCounts(ctx context.Context, tx *sql.Tx, jobID int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE admin_account_jobs job
		SET processed_count=counts.processed, succeeded_count=counts.succeeded,
		    failed_count=counts.failed, canceled_count=counts.canceled, updated_at=NOW()
		FROM (SELECT COUNT(*) FILTER (WHERE status IN ('succeeded','failed','canceled'))::integer processed,
		             COUNT(*) FILTER (WHERE status='succeeded')::integer succeeded,
		             COUNT(*) FILTER (WHERE status='failed')::integer failed,
		             COUNT(*) FILTER (WHERE status='canceled')::integer canceled
		      FROM admin_account_job_items WHERE job_id=$1) counts
		WHERE job.id=$1`, jobID)
	return err
}

func (r *accountJobRepository) Finish(ctx context.Context, jobID int64, errorCode, errorMessage string) (*service.AccountJob, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var cancelRequested bool
	if err = tx.QueryRowContext(ctx, `SELECT cancel_requested_at IS NOT NULL FROM admin_account_jobs
		WHERE id=$1 AND status='running' FOR UPDATE`, jobID).Scan(&cancelRequested); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrAccountJobNotFound
		}
		return nil, err
	}
	remainingStatus, remainingCode, remainingMessage := service.AccountJobItemStatusFailed, errorCode, errorMessage
	if cancelRequested {
		remainingStatus, remainingCode, remainingMessage = service.AccountJobItemStatusCanceled, "", ""
	} else if errorCode == "" {
		remainingCode, remainingMessage = "execution_failed", "account job item failed"
	}
	if _, err = tx.ExecContext(ctx, `UPDATE admin_account_job_items
		SET status=$2, error_code=NULLIF($3,''), error_message=NULLIF($4,''), finished_at=NOW(), updated_at=NOW()
		WHERE job_id=$1 AND status IN ('pending','running')`, jobID, remainingStatus, remainingCode, remainingMessage); err != nil {
		return nil, err
	}
	if err = refreshAccountJobCounts(ctx, tx, jobID); err != nil {
		return nil, err
	}
	status := service.AccountJobStatusFailed
	if cancelRequested {
		status = service.AccountJobStatusCanceled
	} else {
		var succeeded, failed int
		if err = tx.QueryRowContext(ctx, `SELECT succeeded_count, failed_count FROM admin_account_jobs WHERE id=$1`, jobID).Scan(&succeeded, &failed); err != nil {
			return nil, err
		}
		switch {
		case failed == 0:
			status = service.AccountJobStatusSucceeded
		case succeeded > 0:
			status = service.AccountJobStatusPartiallySucceeded
		}
	}
	job, err := scanAccountJob(tx.QueryRowContext(ctx, `UPDATE admin_account_jobs
		SET status=$2, error_code=NULLIF($3,''), error_message=NULLIF($4,''), finished_at=NOW(), updated_at=NOW()
		WHERE id=$1 RETURNING `+accountJobSelectColumns, jobID, status, errorCode, errorMessage))
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *accountJobRepository) Cancel(ctx context.Context, jobID, createdBy int64) (*service.AccountJob, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	job, err := scanAccountJob(tx.QueryRowContext(ctx, `SELECT `+accountJobSelectColumns+`
		FROM admin_account_jobs WHERE id=$1 AND created_by=$2 FOR UPDATE`, jobID, createdBy))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrAccountJobNotFound
	}
	if err != nil {
		return nil, err
	}
	switch job.Status {
	case service.AccountJobStatusPending:
		if _, err = tx.ExecContext(ctx, `UPDATE admin_account_job_items SET status='canceled', finished_at=NOW(), updated_at=NOW()
			WHERE job_id=$1 AND status='pending'`, jobID); err != nil {
			return nil, err
		}
		job, err = scanAccountJob(tx.QueryRowContext(ctx, `UPDATE admin_account_jobs SET status='canceled',
			cancel_requested_at=NOW(), processed_count=target_count, canceled_count=target_count,
			finished_at=NOW(), updated_at=NOW() WHERE id=$1 RETURNING `+accountJobSelectColumns, jobID))
	case service.AccountJobStatusRunning:
		job, err = scanAccountJob(tx.QueryRowContext(ctx, `UPDATE admin_account_jobs SET cancel_requested_at=NOW(), updated_at=NOW()
			WHERE id=$1 RETURNING `+accountJobSelectColumns, jobID))
	default:
		// Terminal jobs are idempotent cancellation responses.
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *accountJobRepository) FailedItemSeeds(ctx context.Context, jobID, createdBy int64) (*service.AccountJob, []service.AccountJobItemSeed, string, time.Time, error) {
	job, err := r.Get(ctx, jobID)
	if err != nil || job.CreatedBy != createdBy {
		return nil, nil, "", time.Time{}, service.ErrAccountJobNotFound
	}
	if job.Status != service.AccountJobStatusFailed && job.Status != service.AccountJobStatusPartiallySucceeded {
		return nil, nil, "", time.Time{}, service.ErrAccountJobNotRetryable
	}
	payload, expires, err := r.Payload(ctx, jobID)
	if err != nil {
		return nil, nil, "", time.Time{}, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT ordinal, action, target_account_id, metadata
		FROM admin_account_job_items WHERE job_id=$1 AND status='failed' ORDER BY ordinal`, jobID)
	if err != nil {
		return nil, nil, "", time.Time{}, err
	}
	defer func() { _ = rows.Close() }()
	seeds := make([]service.AccountJobItemSeed, 0)
	for rows.Next() {
		var seed service.AccountJobItemSeed
		var target sql.NullInt64
		var metadata []byte
		if err = rows.Scan(&seed.Ordinal, &seed.Action, &target, &metadata); err != nil {
			return nil, nil, "", time.Time{}, err
		}
		if target.Valid {
			seed.TargetAccountID = &target.Int64
		}
		seed.Metadata = append(json.RawMessage(nil), metadata...)
		seeds = append(seeds, seed)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, "", time.Time{}, err
	}
	if len(seeds) == 0 {
		return nil, nil, "", time.Time{}, service.ErrAccountJobNotRetryable
	}
	return job, seeds, payload, expires, nil
}

func (r *accountJobRepository) ExpirePayloads(ctx context.Context, before time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE admin_account_jobs SET raw_payload_ciphertext=NULL, updated_at=NOW()
		WHERE raw_payload_ciphertext IS NOT NULL AND raw_payload_expires_at <= $1`, before)
	return err
}

func (r *accountJobRepository) Prune(ctx context.Context, before time.Time) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM admin_account_jobs WHERE finished_at IS NOT NULL AND finished_at < $1`, before)
	return err
}

var _ service.AccountJobRepository = (*accountJobRepository)(nil)
