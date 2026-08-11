package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type remoteSkillRegistryRepository struct {
	db *sql.DB
}

func NewRemoteSkillRegistryRepository(db *sql.DB) service.RemoteSkillRegistryStore {
	return &remoteSkillRegistryRepository{db: db}
}

func (r *remoteSkillRegistryRepository) EnsureRemoteSkillSeed(ctx context.Context, version service.RemoteSkillBundleVersion) error {
	if r == nil || r.db == nil {
		return errors.New("remote skill registry database unavailable")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var activeID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT active_bundle_version_id FROM system_prompt_skill_runtime WHERE id = 1 FOR UPDATE`).Scan(&activeID); err != nil {
		return err
	}
	versionID, err := insertOrValidateRemoteSkillVersion(ctx, tx, version)
	if err != nil {
		return err
	}
	if activeID.Valid {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE system_prompt_skill_bundle_versions
		SET published_at = COALESCE(published_at, NOW())
		WHERE id = $1`, versionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE system_prompt_skill_runtime
		SET active_bundle_version_id = $1, updated_at = NOW()
		WHERE id = 1 AND active_bundle_version_id IS NULL`, versionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *remoteSkillRegistryRepository) LoadRemoteSkillSnapshot(ctx context.Context) (service.RemoteSkillRegistrySnapshot, error) {
	return loadRemoteSkillRegistrySnapshot(ctx, r.db)
}

func (r *remoteSkillRegistryRepository) ListRemoteSkillVersions(ctx context.Context) ([]service.RemoteSkillBundleVersion, error) {
	rows, err := r.db.QueryContext(ctx, remoteSkillVersionSelect+` ORDER BY v.created_at DESC, v.id DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	versions := make([]service.RemoteSkillBundleVersion, 0)
	for rows.Next() {
		version, err := scanRemoteSkillVersion(rows)
		if err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

func (r *remoteSkillRegistryRepository) GetRemoteSkillVersion(ctx context.Context, id int64) (service.RemoteSkillBundleVersion, error) {
	version, err := scanRemoteSkillVersion(r.db.QueryRowContext(ctx, remoteSkillVersionSelect+` WHERE v.id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return service.RemoteSkillBundleVersion{}, service.ErrRemoteSkillVersionNotFound
	}
	return version, err
}

func (r *remoteSkillRegistryRepository) CreateRemoteSkillSyncJob(ctx context.Context, sourceID string, actorID, expectedRevision int64) (service.RemoteSkillSyncJob, error) {
	sourceID, err := service.NormalizeRemoteSkillSourceID(sourceID)
	if err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockRemoteSkillRevision(ctx, tx, expectedRevision); err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	var job service.RemoteSkillSyncJob
	var createdBy sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO system_prompt_skill_sync_jobs (source_id, status, progress_stage, created_by)
		VALUES ($1, 'queued', 'queued', $2)
		RETURNING id, source_id, status, progress_stage, created_by, created_at`, sourceID, nullableActor(actorID)).Scan(
		&job.ID, &job.SourceID, &job.Status, &job.ProgressStage, &createdBy, &job.CreatedAt); err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	job.CreatedBy = nullableRemoteSkillInt64(createdBy)
	if err := tx.Commit(); err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	return job, nil
}

func (r *remoteSkillRegistryRepository) UpdateRemoteSkillSyncJobStage(ctx context.Context, id int64, stage string) error {
	stage = strings.TrimSpace(stage)
	if stage == "" || len(stage) > 64 {
		return service.ErrBusinessSystemPromptInvalid
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE system_prompt_skill_sync_jobs
		SET status = 'running', progress_stage = $2, started_at = COALESCE(started_at, NOW())
		WHERE id = $1 AND status IN ('queued', 'running')`, id, stage)
	if err != nil {
		return err
	}
	return requireRemoteSkillJobAffected(result)
}

func (r *remoteSkillRegistryRepository) CompleteRemoteSkillSyncJob(ctx context.Context, id int64, version service.RemoteSkillBundleVersion) (service.RemoteSkillSyncJob, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var status, sourceID string
	if err := tx.QueryRowContext(ctx, `SELECT status, source_id FROM system_prompt_skill_sync_jobs WHERE id = $1 FOR UPDATE`, id).Scan(&status, &sourceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.RemoteSkillSyncJob{}, service.ErrRemoteSkillSyncNotFound
		}
		return service.RemoteSkillSyncJob{}, err
	}
	if status != service.RemoteSkillSyncStatusQueued && status != service.RemoteSkillSyncStatusRunning {
		return service.RemoteSkillSyncJob{}, service.ErrBusinessSystemPromptRevisionConflict
	}
	if version.SourceID != sourceID || version.RemoteRoot == "" {
		return service.RemoteSkillSyncJob{}, service.ErrBusinessSystemPromptInvalid
	}
	versionID, err := insertOrValidateRemoteSkillVersion(ctx, tx, version)
	if err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE system_prompt_skill_sync_jobs
		SET status = 'succeeded', progress_stage = 'candidate_ready', source_commit = $2,
		    candidate_bundle_version_id = $3, error_code = NULL, completed_at = NOW()
		WHERE id = $1`, id, version.SourceCommit, versionID); err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	if err := tx.Commit(); err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	return r.GetRemoteSkillSyncJob(ctx, id)
}

func (r *remoteSkillRegistryRepository) FailRemoteSkillSyncJob(ctx context.Context, id int64, code string) error {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 100 {
		code = "sync_failed"
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE system_prompt_skill_sync_jobs
		SET status = 'failed', progress_stage = 'failed', error_code = $2, completed_at = NOW()
		WHERE id = $1 AND status IN ('queued', 'running')`, id, code)
	if err != nil {
		return err
	}
	return requireRemoteSkillJobAffected(result)
}

func (r *remoteSkillRegistryRepository) GetRemoteSkillSyncJob(ctx context.Context, id int64) (service.RemoteSkillSyncJob, error) {
	var job service.RemoteSkillSyncJob
	var sourceCommit, errorCode sql.NullString
	var candidateID, createdBy sql.NullInt64
	var startedAt, completedAt sql.NullTime
	err := r.db.QueryRowContext(ctx, `
		SELECT id, source_id, status, progress_stage, source_commit, candidate_bundle_version_id,
		       error_code, created_by, created_at, started_at, completed_at
		FROM system_prompt_skill_sync_jobs WHERE id = $1`, id).Scan(
		&job.ID, &job.SourceID, &job.Status, &job.ProgressStage, &sourceCommit, &candidateID,
		&errorCode, &createdBy, &job.CreatedAt, &startedAt, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return service.RemoteSkillSyncJob{}, service.ErrRemoteSkillSyncNotFound
	}
	if err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	job.SourceCommit = nullableStringValue(sourceCommit)
	job.CandidateBundleVersionID = nullableRemoteSkillInt64(candidateID)
	job.ErrorCode = nullableStringValue(errorCode)
	job.CreatedBy = nullableRemoteSkillInt64(createdBy)
	job.StartedAt = nullableTimePointer(startedAt)
	job.CompletedAt = nullableTimePointer(completedAt)
	return job, nil
}

func (r *remoteSkillRegistryRepository) PublishRemoteSkillVersion(ctx context.Context, versionID, expectedRevision, actorID int64) (service.RemoteSkillRegistrySnapshot, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockRemoteSkillRevision(ctx, tx, expectedRevision); err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM system_prompt_skill_bundle_versions WHERE id = $1)`, versionID).Scan(&exists); err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	if !exists {
		return service.RemoteSkillRegistrySnapshot{}, service.ErrRemoteSkillVersionNotFound
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE system_prompt_skill_bundle_versions
		SET published_at = COALESCE(published_at, NOW()), published_by = $2
		WHERE id = $1`, versionID, nullableActor(actorID)); err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE system_prompt_skill_runtime
		SET active_bundle_version_id = $1, revision = revision + 1,
		    updated_by = $2, updated_at = NOW()
		WHERE id = 1`, versionID, nullableActor(actorID)); err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	snapshot, err := loadRemoteSkillRegistrySnapshot(ctx, tx)
	if err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	return snapshot, nil
}

const remoteSkillVersionSelect = `
	SELECT v.id, v.bundle_id, v.source_id, v.remote_root, v.source_commit, v.overlay_sha256,
	       v.manifest_sha256, v.archive_sha256, v.file_count, v.total_bytes,
	       v.added_files, v.modified_files, v.deleted_files,
	       v.script_changes, v.binary_changes, v.created_by,
	       v.published_at, v.published_by, v.created_at
	FROM system_prompt_skill_bundle_versions v`

type remoteSkillRowScanner interface {
	Scan(...any) error
}

func scanRemoteSkillVersion(row remoteSkillRowScanner) (service.RemoteSkillBundleVersion, error) {
	var version service.RemoteSkillBundleVersion
	var createdBy, publishedBy sql.NullInt64
	var publishedAt sql.NullTime
	err := row.Scan(
		&version.ID, &version.BundleID, &version.SourceID, &version.RemoteRoot, &version.SourceCommit, &version.OverlaySHA256,
		&version.ManifestSHA256, &version.ArchiveSHA256, &version.FileCount, &version.TotalBytes,
		&version.AddedFiles, &version.ModifiedFiles, &version.DeletedFiles,
		&version.ScriptChanges, &version.BinaryChanges, &createdBy,
		&publishedAt, &publishedBy, &version.CreatedAt,
	)
	if err != nil {
		return service.RemoteSkillBundleVersion{}, err
	}
	version.CreatedBy = nullableRemoteSkillInt64(createdBy)
	version.PublishedBy = nullableRemoteSkillInt64(publishedBy)
	version.PublishedAt = nullableTimePointer(publishedAt)
	return version, nil
}

func loadRemoteSkillRegistrySnapshot(ctx context.Context, q businessSystemPromptQueryer) (service.RemoteSkillRegistrySnapshot, error) {
	var snapshot service.RemoteSkillRegistrySnapshot
	var activeID sql.NullInt64
	err := q.QueryRowContext(ctx, `
		SELECT revision, active_bundle_version_id, updated_at
		FROM system_prompt_skill_runtime WHERE id = 1`).Scan(&snapshot.Revision, &activeID, &snapshot.UpdatedAt)
	if err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	if !activeID.Valid {
		return snapshot, nil
	}
	version, err := scanRemoteSkillVersion(q.QueryRowContext(ctx, remoteSkillVersionSelect+` WHERE v.id = $1`, activeID.Int64))
	if err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	snapshot.Active = &version
	snapshot.SourceID = version.SourceID
	snapshot.RemoteRoot = version.RemoteRoot
	return snapshot, nil
}

func insertOrValidateRemoteSkillVersion(ctx context.Context, tx *sql.Tx, version service.RemoteSkillBundleVersion) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO system_prompt_skill_bundle_versions
		(bundle_id, source_id, remote_root, source_commit, overlay_sha256, manifest_sha256, archive_sha256,
		 file_count, total_bytes, added_files, modified_files, deleted_files,
		 script_changes, binary_changes, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (source_id, manifest_sha256) DO NOTHING
		RETURNING id`,
		version.BundleID, version.SourceID, version.RemoteRoot, version.SourceCommit, version.OverlaySHA256, version.ManifestSHA256,
		version.ArchiveSHA256, version.FileCount, version.TotalBytes, version.AddedFiles,
		version.ModifiedFiles, version.DeletedFiles, version.ScriptChanges, version.BinaryChanges,
		nullableActor(version.CreatedBy)).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	existing, err := scanRemoteSkillVersion(tx.QueryRowContext(
		ctx,
		remoteSkillVersionSelect+` WHERE v.source_id = $1 AND v.manifest_sha256 = $2`,
		version.SourceID,
		version.ManifestSHA256,
	))
	if err != nil {
		return 0, err
	}
	if existing.BundleID != version.BundleID || existing.SourceID != version.SourceID || existing.RemoteRoot != version.RemoteRoot || existing.SourceCommit != version.SourceCommit ||
		existing.OverlaySHA256 != version.OverlaySHA256 || existing.ArchiveSHA256 != version.ArchiveSHA256 ||
		existing.FileCount != version.FileCount || existing.TotalBytes != version.TotalBytes {
		return 0, fmt.Errorf("%w: existing manifest metadata mismatch", service.ErrBusinessSystemPromptUnavailable)
	}
	return existing.ID, nil
}

func lockRemoteSkillRevision(ctx context.Context, tx *sql.Tx, expected int64) error {
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM system_prompt_skill_runtime WHERE id = 1 FOR UPDATE`).Scan(&revision); err != nil {
		return err
	}
	if expected < 1 || revision != expected {
		return service.ErrBusinessSystemPromptRevisionConflict
	}
	return nil
}

func requireRemoteSkillJobAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return service.ErrRemoteSkillSyncNotFound
	}
	return nil
}

func nullableTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}

func nullableRemoteSkillInt64(value sql.NullInt64) int64 {
	if !value.Valid {
		return 0
	}
	return value.Int64
}
