package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type imageStudioRepository struct {
	db *sql.DB
}

func NewImageStudioRepository(db *sql.DB) service.ImageStudioRepository {
	return &imageStudioRepository{db: db}
}

const imageStudioJobSelectColumns = `id, user_id, api_key_id, mode, model, prompt, size, quality, count, status,
	processed_count, succeeded_count, failed_count, canceled_count, cancel_requested_at,
	error_code, error_message, request_expires_at, retain_until, started_at, finished_at, created_at, updated_at`

const imageStudioItemSelectColumns = `id, job_id, ordinal, status, error_code, error_message,
	started_at, finished_at, created_at, updated_at`

const imageStudioArtifactSelectColumns = `id, job_id, item_id, kind, storage_key, content_type,
	byte_size, revised_prompt, expires_at, created_at`

type imageStudioScanner interface {
	Scan(...any) error
}

func scanImageStudioJob(row imageStudioScanner) (*service.ImageStudioJob, error) {
	job := &service.ImageStudioJob{}
	var mode, status string
	var cancelRequested, started, finished sql.NullTime
	var errorCode, errorMessage sql.NullString
	err := row.Scan(
		&job.ID, &job.UserID, &job.APIKeyID, &mode, &job.Model, &job.Prompt, &job.Size, &job.Quality,
		&job.Count, &status, &job.Counts.Processed, &job.Counts.Succeeded, &job.Counts.Failed,
		&job.Counts.Canceled, &cancelRequested, &errorCode, &errorMessage, &job.RequestExpiresAt,
		&job.RetainUntil, &started, &finished, &job.CreatedAt, &job.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	job.Mode = service.ImageStudioMode(mode)
	job.Status = service.ImageStudioJobStatus(status)
	if cancelRequested.Valid {
		job.CancelRequestedAt = &cancelRequested.Time
	}
	if errorCode.Valid {
		job.ErrorCode = errorCode.String
	}
	if errorMessage.Valid {
		job.ErrorMessage = errorMessage.String
	}
	if started.Valid {
		job.StartedAt = &started.Time
	}
	if finished.Valid {
		job.FinishedAt = &finished.Time
	}
	return job, nil
}

func scanImageStudioItem(row imageStudioScanner) (*service.ImageStudioItem, error) {
	item := &service.ImageStudioItem{}
	var status string
	var errorCode, errorMessage sql.NullString
	var started, finished sql.NullTime
	err := row.Scan(
		&item.ID, &item.JobID, &item.Ordinal, &status, &errorCode, &errorMessage,
		&started, &finished, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	item.Status = service.ImageStudioItemStatus(status)
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

func scanImageStudioArtifact(row imageStudioScanner) (*service.ImageStudioArtifact, error) {
	artifact := &service.ImageStudioArtifact{}
	var itemID sql.NullInt64
	var kind string
	var revisedPrompt sql.NullString
	err := row.Scan(
		&artifact.ID, &artifact.JobID, &itemID, &kind, &artifact.StorageKey, &artifact.ContentType,
		&artifact.ByteSize, &revisedPrompt, &artifact.ExpiresAt, &artifact.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	artifact.Kind = service.ImageStudioArtifactKind(kind)
	if itemID.Valid {
		artifact.ItemID = &itemID.Int64
	}
	if revisedPrompt.Valid {
		artifact.RevisedPrompt = revisedPrompt.String
	}
	return artifact, nil
}

func (r *imageStudioRepository) Create(ctx context.Context, params service.ImageStudioCreateParams) (*service.ImageStudioJob, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	job, err := scanImageStudioJob(tx.QueryRowContext(ctx, `INSERT INTO image_studio_jobs
		(user_id, api_key_id, mode, model, prompt, size, quality, count, request_expires_at, retain_until)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING `+imageStudioJobSelectColumns,
		params.UserID, params.Input.APIKeyID, params.Input.Mode, params.Input.Model, params.Input.Prompt,
		params.Input.Size, params.Input.Quality, params.Input.Count, params.RequestExpiresAt, params.RetainUntil,
	))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, service.ErrImageStudioActiveJob
		}
		return nil, err
	}
	for ordinal := 1; ordinal <= params.Input.Count; ordinal++ {
		if _, err = tx.ExecContext(ctx,
			`INSERT INTO image_studio_items (job_id, ordinal) VALUES ($1,$2)`, job.ID, ordinal); err != nil {
			return nil, err
		}
	}
	for _, artifact := range params.InputArtifacts {
		if _, err = tx.ExecContext(ctx, `INSERT INTO image_studio_artifacts
			(job_id, kind, storage_key, content_type, byte_size, expires_at)
			VALUES ($1,$2,$3,$4,$5,$6)`, job.ID, artifact.Kind, artifact.StorageKey,
			artifact.ContentType, artifact.ByteSize, params.RequestExpiresAt); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *imageStudioRepository) Get(ctx context.Context, userID, jobID int64) (*service.ImageStudioJob, error) {
	job, err := scanImageStudioJob(r.db.QueryRowContext(ctx, `SELECT `+imageStudioJobSelectColumns+`
		FROM image_studio_jobs WHERE id=$1 AND user_id=$2`, jobID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrImageStudioNotFound
	}
	return job, err
}

func (r *imageStudioRepository) List(ctx context.Context, userID int64, limit, offset int) ([]service.ImageStudioJob, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+imageStudioJobSelectColumns+`
		FROM image_studio_jobs WHERE user_id=$1 ORDER BY created_at DESC, id DESC LIMIT $2 OFFSET $3`,
		userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]service.ImageStudioJob, 0, limit)
	for rows.Next() {
		job, scanErr := scanImageStudioJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		jobs = append(jobs, *job)
	}
	return jobs, rows.Err()
}

func (r *imageStudioRepository) ListItems(ctx context.Context, userID, jobID int64) ([]service.ImageStudioItem, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+prefixedImageStudioItemColumns("item")+`
		FROM image_studio_items item JOIN image_studio_jobs job ON job.id=item.job_id
		WHERE item.job_id=$1 AND job.user_id=$2 ORDER BY item.ordinal`, jobID, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.ImageStudioItem, 0, service.ImageStudioMaxOutputCount)
	for rows.Next() {
		item, scanErr := scanImageStudioItem(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, *item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		if _, getErr := r.Get(ctx, userID, jobID); getErr != nil {
			return nil, getErr
		}
	}
	return items, nil
}

func (r *imageStudioRepository) ListOutputArtifacts(ctx context.Context, userID, jobID int64) ([]service.ImageStudioArtifact, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+prefixedImageStudioArtifactColumns("artifact")+`
		FROM image_studio_artifacts artifact JOIN image_studio_jobs job ON job.id=artifact.job_id
		WHERE artifact.job_id=$1 AND job.user_id=$2 AND artifact.kind='output'
		ORDER BY artifact.item_id, artifact.id`, jobID, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	artifacts := make([]service.ImageStudioArtifact, 0)
	for rows.Next() {
		artifact, scanErr := scanImageStudioArtifact(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		artifacts = append(artifacts, *artifact)
	}
	return artifacts, rows.Err()
}

func (r *imageStudioRepository) GetArtifact(ctx context.Context, userID, jobID, artifactID int64) (*service.ImageStudioArtifact, error) {
	artifact, err := scanImageStudioArtifact(r.db.QueryRowContext(ctx, `SELECT `+prefixedImageStudioArtifactColumns("artifact")+`
		FROM image_studio_artifacts artifact JOIN image_studio_jobs job ON job.id=artifact.job_id
		WHERE artifact.id=$1 AND artifact.job_id=$2 AND job.user_id=$3 AND artifact.kind='output'`,
		artifactID, jobID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrImageStudioNotFound
	}
	return artifact, err
}

func prefixedImageStudioItemColumns(prefix string) string {
	return prefix + `.id, ` + prefix + `.job_id, ` + prefix + `.ordinal, ` + prefix + `.status, ` +
		prefix + `.error_code, ` + prefix + `.error_message, ` + prefix + `.started_at, ` +
		prefix + `.finished_at, ` + prefix + `.created_at, ` + prefix + `.updated_at`
}

func prefixedImageStudioArtifactColumns(prefix string) string {
	return prefix + `.id, ` + prefix + `.job_id, ` + prefix + `.item_id, ` + prefix + `.kind, ` +
		prefix + `.storage_key, ` + prefix + `.content_type, ` + prefix + `.byte_size, ` +
		prefix + `.revised_prompt, ` + prefix + `.expires_at, ` + prefix + `.created_at`
}

func (r *imageStudioRepository) RequestCancel(ctx context.Context, userID, jobID int64) (*service.ImageStudioJob, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	job, err := getImageStudioJobForUpdate(ctx, tx, userID, jobID)
	if err != nil {
		return nil, err
	}
	if job.Terminal() {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return job, nil
	}
	if _, err = tx.ExecContext(ctx, `UPDATE image_studio_jobs
		SET cancel_requested_at=COALESCE(cancel_requested_at,NOW()), updated_at=NOW()
		WHERE id=$1`, jobID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE image_studio_items
		SET status='canceled', error_code='canceled', error_message='canceled by user',
			finished_at=NOW(), updated_at=NOW()
		WHERE job_id=$1 AND status='pending'`, jobID); err != nil {
		return nil, err
	}
	if err = refreshImageStudioCounts(ctx, tx, jobID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return r.Finalize(ctx, jobID)
}

func (r *imageStudioRepository) Retry(ctx context.Context, userID, jobID int64, now time.Time) (*service.ImageStudioJob, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	job, err := getImageStudioJobForUpdate(ctx, tx, userID, jobID)
	if err != nil {
		return nil, err
	}
	if job.Status != service.ImageStudioJobFailed {
		return nil, service.ErrImageStudioNotRetryable
	}
	if !job.RequestExpiresAt.After(now) || job.Prompt == "" {
		return nil, service.ErrImageStudioRequestExpired
	}
	result, err := tx.ExecContext(ctx, `UPDATE image_studio_items
		SET status='pending', error_code=NULL, error_message=NULL, started_at=NULL, finished_at=NULL, updated_at=NOW()
		WHERE job_id=$1 AND status IN ('running','failed','canceled')`, jobID)
	if err != nil {
		return nil, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if changed == 0 {
		return nil, service.ErrImageStudioNotRetryable
	}
	job, err = scanImageStudioJob(tx.QueryRowContext(ctx, `UPDATE image_studio_jobs
		SET status='pending', cancel_requested_at=NULL, error_code=NULL, error_message=NULL,
			processed_count=(SELECT COUNT(*)::smallint FROM image_studio_items WHERE job_id=$1 AND status='succeeded'),
			succeeded_count=(SELECT COUNT(*)::smallint FROM image_studio_items WHERE job_id=$1 AND status='succeeded'),
			failed_count=0, canceled_count=0, started_at=NULL, finished_at=NULL, updated_at=NOW()
		WHERE id=$1 RETURNING `+imageStudioJobSelectColumns, jobID))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, service.ErrImageStudioActiveJob
		}
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return job, nil
}

func getImageStudioJobForUpdate(ctx context.Context, tx *sql.Tx, userID, jobID int64) (*service.ImageStudioJob, error) {
	job, err := scanImageStudioJob(tx.QueryRowContext(ctx, `SELECT `+imageStudioJobSelectColumns+`
		FROM image_studio_jobs WHERE id=$1 AND user_id=$2 FOR UPDATE`, jobID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrImageStudioNotFound
	}
	return job, err
}

func (r *imageStudioRepository) RecoverInterrupted(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `UPDATE image_studio_items item
		SET status='failed', error_code='interrupted', error_message='image generation was interrupted',
			finished_at=NOW(), updated_at=NOW()
		FROM image_studio_jobs job
		WHERE item.job_id=job.id AND job.status IN ('preparing','running') AND item.status IN ('pending','running')`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE image_studio_jobs
		SET status='failed', error_code='interrupted', error_message='image generation was interrupted',
			processed_count=(SELECT COUNT(*)::smallint FROM image_studio_items WHERE job_id=image_studio_jobs.id AND status IN ('succeeded','failed','canceled')),
			succeeded_count=(SELECT COUNT(*)::smallint FROM image_studio_items WHERE job_id=image_studio_jobs.id AND status='succeeded'),
			failed_count=(SELECT COUNT(*)::smallint FROM image_studio_items WHERE job_id=image_studio_jobs.id AND status='failed'),
			canceled_count=(SELECT COUNT(*)::smallint FROM image_studio_items WHERE job_id=image_studio_jobs.id AND status='canceled'),
			finished_at=NOW(), updated_at=NOW()
		WHERE status IN ('preparing','running')`); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *imageStudioRepository) ClaimNext(ctx context.Context) (*service.ImageStudioClaim, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	item, err := scanImageStudioItem(tx.QueryRowContext(ctx, `WITH picked AS (
		SELECT item.id FROM image_studio_items item
		JOIN image_studio_jobs job ON job.id=item.job_id
		WHERE item.status='pending' AND job.status IN ('pending','running')
			AND job.cancel_requested_at IS NULL AND job.request_expires_at > NOW()
		ORDER BY job.created_at, item.ordinal
		FOR UPDATE OF item SKIP LOCKED LIMIT 1
	)
	UPDATE image_studio_items item SET status='running', started_at=NOW(), updated_at=NOW()
	FROM picked WHERE item.id=picked.id RETURNING `+prefixedImageStudioItemColumns("item")))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrImageStudioNoWork
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE image_studio_jobs
		SET status='running', started_at=COALESCE(started_at,NOW()), updated_at=NOW()
		WHERE id=$1 AND status IN ('pending','preparing','running')`, item.JobID); err != nil {
		return nil, err
	}
	job, err := scanImageStudioJob(tx.QueryRowContext(ctx, `SELECT `+imageStudioJobSelectColumns+`
		FROM image_studio_jobs WHERE id=$1 FOR UPDATE`, item.JobID))
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+imageStudioArtifactSelectColumns+`
		FROM image_studio_artifacts WHERE job_id=$1 AND item_id IS NULL
			AND kind IN ('reference','mask') AND expires_at > NOW() ORDER BY id`, item.JobID)
	if err != nil {
		return nil, err
	}
	inputs := make([]service.ImageStudioArtifact, 0, 2)
	for rows.Next() {
		artifact, scanErr := scanImageStudioArtifact(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, scanErr
		}
		inputs = append(inputs, *artifact)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &service.ImageStudioClaim{Job: *job, Item: *item, Inputs: inputs}, nil
}

func (r *imageStudioRepository) CompleteSuccess(ctx context.Context, itemID int64, artifact service.ImageStudioInputArtifact, revisedPrompt string, expiresAt time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var jobID int64
	if err = tx.QueryRowContext(ctx, `SELECT job_id FROM image_studio_items WHERE id=$1 AND status='running' FOR UPDATE`, itemID).Scan(&jobID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrImageStudioNotFound
		}
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO image_studio_artifacts
		(job_id, item_id, kind, storage_key, content_type, byte_size, revised_prompt, expires_at)
		VALUES ($1,$2,'output',$3,$4,$5,NULLIF($6,''),$7)`, jobID, itemID, artifact.StorageKey,
		artifact.ContentType, artifact.ByteSize, revisedPrompt, expiresAt); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE image_studio_items
		SET status='succeeded', error_code=NULL, error_message=NULL, finished_at=NOW(), updated_at=NOW()
		WHERE id=$1 AND status='running'`, itemID); err != nil {
		return err
	}
	if err = refreshImageStudioCounts(ctx, tx, jobID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *imageStudioRepository) CompleteFailure(ctx context.Context, itemID int64, code, message string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var jobID int64
	if err = tx.QueryRowContext(ctx, `UPDATE image_studio_items
		SET status='failed', error_code=$2, error_message=$3, finished_at=NOW(), updated_at=NOW()
		WHERE id=$1 AND status='running' RETURNING job_id`, itemID, code, message).Scan(&jobID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrImageStudioNotFound
		}
		return err
	}
	if err = refreshImageStudioCounts(ctx, tx, jobID); err != nil {
		return err
	}
	return tx.Commit()
}

func refreshImageStudioCounts(ctx context.Context, tx *sql.Tx, jobID int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE image_studio_jobs job SET
		processed_count=counts.processed,
		succeeded_count=counts.succeeded,
		failed_count=counts.failed,
		canceled_count=counts.canceled,
		updated_at=NOW()
	FROM (
		SELECT COUNT(*) FILTER (WHERE status IN ('succeeded','failed','canceled'))::smallint AS processed,
			COUNT(*) FILTER (WHERE status='succeeded')::smallint AS succeeded,
			COUNT(*) FILTER (WHERE status='failed')::smallint AS failed,
			COUNT(*) FILTER (WHERE status='canceled')::smallint AS canceled
		FROM image_studio_items WHERE job_id=$1
	) counts WHERE job.id=$1`, jobID)
	return err
}

func (r *imageStudioRepository) Finalize(ctx context.Context, jobID int64) (*service.ImageStudioJob, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	job, err := scanImageStudioJob(tx.QueryRowContext(ctx, `SELECT `+imageStudioJobSelectColumns+`
		FROM image_studio_jobs WHERE id=$1 FOR UPDATE`, jobID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrImageStudioNotFound
	}
	if err != nil {
		return nil, err
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_studio_items
		WHERE job_id=$1 AND status IN ('pending','running')`, jobID).Scan(&active); err != nil {
		return nil, err
	}
	if active == 0 && !job.Terminal() {
		if err = refreshImageStudioCounts(ctx, tx, jobID); err != nil {
			return nil, err
		}
		var counts service.ImageStudioCounts
		if err = tx.QueryRowContext(ctx, `SELECT processed_count, succeeded_count, failed_count, canceled_count
			FROM image_studio_jobs WHERE id=$1`, jobID).Scan(&counts.Processed, &counts.Succeeded, &counts.Failed, &counts.Canceled); err != nil {
			return nil, err
		}
		status := service.ResolveImageStudioTerminalStatus(job.CancelRequestedAt != nil, counts)
		errorCode, errorMessage := "", ""
		if status == service.ImageStudioJobFailed {
			errorCode, errorMessage = "generation_failed", "Image generation failed"
		}
		job, err = scanImageStudioJob(tx.QueryRowContext(ctx, `UPDATE image_studio_jobs
			SET status=$2, error_code=NULLIF($3,''), error_message=NULLIF($4,''), finished_at=NOW(), updated_at=NOW()
			WHERE id=$1 RETURNING `+imageStudioJobSelectColumns, jobID, status, errorCode, errorMessage))
		if err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *imageStudioRepository) StorageUsage(ctx context.Context, userID int64, now time.Time) (service.ImageStudioStorageUsage, error) {
	var usage service.ImageStudioStorageUsage
	err := r.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(artifact.byte_size),0),
		COALESCE(SUM(artifact.byte_size) FILTER (WHERE job.user_id=$1),0)
	FROM image_studio_artifacts artifact
	JOIN image_studio_jobs job ON job.id=artifact.job_id
	WHERE artifact.expires_at > $2`, userID, now).Scan(&usage.Global, &usage.User)
	return usage, err
}

func (r *imageStudioRepository) ListExpiredArtifacts(ctx context.Context, now time.Time, limit int) ([]service.ImageStudioArtifact, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+imageStudioArtifactSelectColumns+`
		FROM image_studio_artifacts WHERE expires_at <= $1 ORDER BY expires_at, id LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	artifacts := make([]service.ImageStudioArtifact, 0, limit)
	for rows.Next() {
		artifact, scanErr := scanImageStudioArtifact(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		artifacts = append(artifacts, *artifact)
	}
	return artifacts, rows.Err()
}

func (r *imageStudioRepository) DeleteArtifact(ctx context.Context, artifactID int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM image_studio_artifacts WHERE id=$1`, artifactID)
	if err != nil {
		return err
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil {
		return rowsErr
	} else if affected == 0 {
		return service.ErrImageStudioNotFound
	}
	return nil
}

func (r *imageStudioRepository) ExpireRequests(ctx context.Context, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `UPDATE image_studio_items item
		SET status='failed', error_code='request_expired', error_message='Image Studio request expired',
			finished_at=NOW(), updated_at=NOW()
		FROM image_studio_jobs job
		WHERE item.job_id=job.id AND job.status='pending' AND job.request_expires_at <= $1
			AND item.status='pending'`, now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE image_studio_jobs job
		SET status='failed', prompt='', error_code='request_expired', error_message='Image Studio request expired',
			processed_count=counts.processed, succeeded_count=counts.succeeded,
			failed_count=counts.failed, canceled_count=counts.canceled,
			finished_at=NOW(), updated_at=NOW()
		FROM (
			SELECT job_id,
				COUNT(*) FILTER (WHERE status IN ('succeeded','failed','canceled'))::smallint AS processed,
				COUNT(*) FILTER (WHERE status='succeeded')::smallint AS succeeded,
				COUNT(*) FILTER (WHERE status='failed')::smallint AS failed,
				COUNT(*) FILTER (WHERE status='canceled')::smallint AS canceled
			FROM image_studio_items GROUP BY job_id
		) counts
		WHERE job.id=counts.job_id AND job.status='pending' AND job.request_expires_at <= $1`, now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE image_studio_jobs SET prompt='', updated_at=NOW()
		WHERE request_expires_at <= $1 AND prompt <> ''`, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *imageStudioRepository) DeleteExpiredJobs(ctx context.Context, now time.Time) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM image_studio_jobs job
		WHERE job.retain_until <= $1
		AND NOT EXISTS (SELECT 1 FROM image_studio_artifacts artifact WHERE artifact.job_id=job.id)`, now)
	return err
}
