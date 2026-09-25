package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type remoteSkillRegistryRepository struct {
	db *sql.DB
}

func NewRemoteSkillRegistryRepository(db *sql.DB) service.RemoteSkillRegistryStore {
	return &remoteSkillRegistryRepository{db: db}
}

func (r *remoteSkillRegistryRepository) EnsureRemoteSkillSeed(ctx context.Context, candidate service.RemoteSkillCandidate) (service.RemoteSkillRegistrySnapshot, error) {
	if err := r.requireDatabase(); err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	if err := validateRemoteSkillCandidateMetadata(candidate); err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var revision int64
	var activeVersionID, activePromptID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT revision, active_bundle_version_id, active_prompt_version_id
		FROM system_prompt_skill_runtime WHERE id = 1 FOR UPDATE`).Scan(&revision, &activeVersionID, &activePromptID); err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	detail, err := insertOrValidateRemoteSkillCandidate(ctx, tx, candidate, true)
	if err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}

	activeIsPaired := false
	if activeVersionID.Valid && activePromptID.Valid {
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM system_prompt_skill_bundle_versions AS v
				JOIN system_prompt_skill_prompt_versions AS p ON p.id = v.prompt_version_id
				WHERE v.id = $1 AND p.id = $2
				  AND v.upstream_source_id = $3 AND v.upstream_root = $4 AND v.public_root = $5
				  AND v.raw_tree_sha256 IS NOT NULL AND v.effective_tree_sha256 IS NOT NULL
			)
		`, activeVersionID.Int64, activePromptID.Int64, service.RemoteSkillUpstreamSourceID, service.RemoteSkillUpstreamRoot, service.RemoteSkillPublicRoot).Scan(&activeIsPaired); err != nil {
			return service.RemoteSkillRegistrySnapshot{}, err
		}
	}
	if !activeIsPaired {
		if _, err := tx.ExecContext(ctx, `
			UPDATE system_prompt_skill_bundle_versions
			SET published_at = COALESCE(published_at, NOW())
			WHERE id = $1`, detail.ID); err != nil {
			return service.RemoteSkillRegistrySnapshot{}, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE system_prompt_skill_runtime
			SET active_bundle_version_id = $1, active_prompt_version_id = $2,
			    revision = revision + 1, updated_at = NOW()
			WHERE id = 1`, detail.ID, detail.Prompt.ID); err != nil {
			return service.RemoteSkillRegistrySnapshot{}, err
		}
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

func (r *remoteSkillRegistryRepository) LoadRemoteSkillSnapshot(ctx context.Context) (service.RemoteSkillRegistrySnapshot, error) {
	if err := r.requireDatabase(); err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	return loadRemoteSkillRegistrySnapshot(ctx, r.db)
}

func (r *remoteSkillRegistryRepository) ListRemoteSkillVersions(ctx context.Context) ([]service.RemoteSkillBundleVersion, error) {
	if err := r.requireDatabase(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, remoteSkillVersionSelect+`
		WHERE v.upstream_source_id = $1 AND v.upstream_root = $2 AND v.public_root = $3
		  AND v.prompt_version_id IS NOT NULL
		ORDER BY v.created_at DESC, v.id DESC`, service.RemoteSkillUpstreamSourceID, service.RemoteSkillUpstreamRoot, service.RemoteSkillPublicRoot)
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

func (r *remoteSkillRegistryRepository) GetRemoteSkillVersion(ctx context.Context, id int64) (service.RemoteSkillBundleVersionDetail, error) {
	if err := r.requireDatabase(); err != nil {
		return service.RemoteSkillBundleVersionDetail{}, err
	}
	return getRemoteSkillVersionDetail(ctx, r.db, id, 0)
}

func (r *remoteSkillRegistryRepository) CreateRemoteSkillSyncJob(ctx context.Context, actorID, expectedRevision int64, promptProvided bool) (service.RemoteSkillSyncJob, error) {
	if err := r.requireDatabase(); err != nil {
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
		INSERT INTO system_prompt_skill_sync_jobs
			(status, progress_stage, prompt_capture_provided, created_by)
		VALUES ('queued', 'queued', $2, $1)
		RETURNING id, status, progress_stage, prompt_capture_provided, created_by, created_at`,
		nullableActor(actorID), promptProvided).Scan(
		&job.ID, &job.Status, &job.ProgressStage, &job.PromptCaptureProvided, &createdBy, &job.CreatedAt); err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	job.CreatedBy = nullableRemoteSkillInt64(createdBy)
	if err := tx.Commit(); err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	return job, nil
}

func (r *remoteSkillRegistryRepository) UpdateRemoteSkillSyncJobStage(ctx context.Context, id int64, stage string) error {
	if err := r.requireDatabase(); err != nil {
		return err
	}
	stage = strings.TrimSpace(stage)
	if stage == "" || len(stage) > 64 {
		return service.ErrBusinessSystemPromptInvalid
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE system_prompt_skill_sync_jobs
		SET status = 'running', progress_stage = $2, started_at = COALESCE(started_at, NOW())
		WHERE id = $1 AND status IN ('queued', 'running')
		  AND created_at > clock_timestamp() - $3 * INTERVAL '1 second'`, id, stage, int64(service.RemoteSkillSyncJobTimeout/time.Second))
	if err != nil {
		return err
	}
	return requireRemoteSkillJobAffected(result)
}

func (r *remoteSkillRegistryRepository) CompleteRemoteSkillSyncJob(ctx context.Context, id int64, candidate service.RemoteSkillCandidate) (service.RemoteSkillSyncJob, error) {
	if err := r.requireDatabase(); err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	if err := validateRemoteSkillCandidateMetadata(candidate); err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	var createdBy sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT status, created_by FROM system_prompt_skill_sync_jobs
		WHERE id = $1 AND created_at > clock_timestamp() - $2 * INTERVAL '1 second' FOR UPDATE`,
		id, int64(service.RemoteSkillSyncJobTimeout/time.Second)).Scan(&status, &createdBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.RemoteSkillSyncJob{}, service.ErrRemoteSkillSyncNotFound
		}
		return service.RemoteSkillSyncJob{}, err
	}
	if status != service.RemoteSkillSyncStatusQueued && status != service.RemoteSkillSyncStatusRunning {
		return service.RemoteSkillSyncJob{}, service.ErrBusinessSystemPromptRevisionConflict
	}
	jobActor := nullableRemoteSkillInt64(createdBy)
	if candidate.Version.CreatedBy != jobActor || candidate.Prompt.CreatedBy != jobActor {
		return service.RemoteSkillSyncJob{}, service.ErrBusinessSystemPromptInvalid
	}
	detail, err := insertOrValidateRemoteSkillCandidate(ctx, tx, candidate, false)
	if err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE system_prompt_skill_sync_jobs
		SET status = 'succeeded', progress_stage = 'candidate_ready',
		    candidate_bundle_version_id = $2, error_code = NULL, completed_at = NOW()
		WHERE id = $1 AND created_at > clock_timestamp() - $3 * INTERVAL '1 second'`,
		id, detail.ID, int64(service.RemoteSkillSyncJobTimeout/time.Second))
	if err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	if err := requireRemoteSkillJobAffected(result); err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	if err := tx.Commit(); err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	return r.GetRemoteSkillSyncJob(ctx, id)
}

func (r *remoteSkillRegistryRepository) FailRemoteSkillSyncJob(ctx context.Context, id int64, code string) error {
	if err := r.requireDatabase(); err != nil {
		return err
	}
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
	if err := r.requireDatabase(); err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	if err := r.ExpireRemoteSkillSyncJobs(ctx); err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	var job service.RemoteSkillSyncJob
	var candidateID, createdBy sql.NullInt64
	var errorCode sql.NullString
	var startedAt, completedAt sql.NullTime
	err := r.db.QueryRowContext(ctx, `
		SELECT id, status, progress_stage, candidate_bundle_version_id,
		       prompt_capture_provided, error_code, created_by, created_at, started_at, completed_at
		FROM system_prompt_skill_sync_jobs WHERE id = $1`, id).Scan(
		&job.ID, &job.Status, &job.ProgressStage, &candidateID,
		&job.PromptCaptureProvided, &errorCode, &createdBy, &job.CreatedAt, &startedAt, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return service.RemoteSkillSyncJob{}, service.ErrRemoteSkillSyncNotFound
	}
	if err != nil {
		return service.RemoteSkillSyncJob{}, err
	}
	job.CandidateBundleVersionID = nullableRemoteSkillInt64(candidateID)
	job.ErrorCode = nullableStringValue(errorCode)
	job.CreatedBy = nullableRemoteSkillInt64(createdBy)
	job.StartedAt = nullableTimePointer(startedAt)
	job.CompletedAt = nullableTimePointer(completedAt)
	return job, nil
}

// Expiry is measured by the database clock and touches only unfinished jobs.
// Another instance's active job remains owned by that instance until its fixed
// deadline. Abandoned jobs are failed, never replayed or published on startup.
func (r *remoteSkillRegistryRepository) ExpireRemoteSkillSyncJobs(ctx context.Context) error {
	if err := r.requireDatabase(); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE system_prompt_skill_sync_jobs
		SET status = 'failed', progress_stage = 'failed', error_code = 'sync_expired', completed_at = NOW()
		WHERE status IN ('queued', 'running')
		  AND created_at <= clock_timestamp() - $1 * INTERVAL '1 second'`, int64(service.RemoteSkillSyncJobTimeout/time.Second))
	return err
}

func (r *remoteSkillRegistryRepository) PublishRemoteSkillVersion(ctx context.Context, versionID, expectedRevision, actorID int64) (service.RemoteSkillRegistrySnapshot, error) {
	return r.publishRemoteSkillVersion(ctx, versionID, expectedRevision, actorID, nil)
}

func (r *remoteSkillRegistryRepository) PublishRemoteSkillVersionWithPromptTargets(ctx context.Context, versionID, expectedRevision, actorID int64, targets service.PromptSourceTargets) (service.RemoteSkillRegistrySnapshot, error) {
	return r.publishRemoteSkillVersion(ctx, versionID, expectedRevision, actorID, &targets)
}

func (r *remoteSkillRegistryRepository) publishRemoteSkillVersion(ctx context.Context, versionID, expectedRevision, actorID int64, targets *service.PromptSourceTargets) (service.RemoteSkillRegistrySnapshot, error) {
	if err := r.requireDatabase(); err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if targets != nil {
		if err := lockBusinessSystemPromptRuntimeRevision(ctx, tx, targets.ExpectedRevision); err != nil {
			return service.RemoteSkillRegistrySnapshot{}, err
		}
		ids := make(map[string]bool, len(targets.RuleIDs))
		for _, id := range targets.RuleIDs {
			if id == "" || ids[id] || len(targets.RuleIDs) > nativeapi.PromptRulesMaxCount {
				return service.RemoteSkillRegistrySnapshot{}, service.ErrBusinessSystemPromptInvalid
			}
			ids[id] = true
		}
		rows, err := tx.QueryContext(ctx, `SELECT rule->>'id'
			FROM system_prompt_rule_policies p, jsonb_array_elements(p.policy->'rules') rule
			JOIN system_prompt_template_versions v ON v.id = (rule->>'version_id')::bigint
			WHERE p.id = 1 AND v.composition_mode = 'codex_skill_hybrid'`)
		if err != nil {
			return service.RemoteSkillRegistrySnapshot{}, err
		}
		matches := 0
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return service.RemoteSkillRegistrySnapshot{}, err
			}
			if !ids[id] {
				_ = rows.Close()
				return service.RemoteSkillRegistrySnapshot{}, fmt.Errorf("%w: paired source affects all subscribed rules", service.ErrBusinessSystemPromptInvalid)
			}
			matches++
		}
		rowErr := rows.Err()
		closeErr := rows.Close()
		if rowErr != nil || closeErr != nil {
			return service.RemoteSkillRegistrySnapshot{}, errors.Join(rowErr, closeErr)
		}
		if matches != len(ids) {
			return service.RemoteSkillRegistrySnapshot{}, service.ErrBusinessSystemPromptInvalid
		}
	}
	if err := lockRemoteSkillRevision(ctx, tx, expectedRevision); err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	var promptID int64
	if err := tx.QueryRowContext(ctx, `
		SELECT prompt_version_id FROM system_prompt_skill_bundle_versions WHERE id = $1
		  AND upstream_source_id = $2 AND upstream_root = $3 AND public_root = $4`,
		versionID, service.RemoteSkillUpstreamSourceID, service.RemoteSkillUpstreamRoot, service.RemoteSkillPublicRoot).Scan(&promptID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.RemoteSkillRegistrySnapshot{}, service.ErrRemoteSkillVersionNotFound
		}
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE system_prompt_skill_bundle_versions
		SET published_at = COALESCE(published_at, NOW()), published_by = $2
		WHERE id = $1`, versionID, nullableActor(actorID)); err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE system_prompt_skill_runtime
		SET active_bundle_version_id = $1, active_prompt_version_id = $2,
		    revision = revision + 1, updated_by = $3, updated_at = NOW()
		WHERE id = 1`, versionID, promptID, nullableActor(actorID)); err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	if targets != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE system_prompt_runtime SET revision = revision + 1, updated_by = $1, updated_at = NOW() WHERE id = 1`, nullableActor(actorID)); err != nil {
			return service.RemoteSkillRegistrySnapshot{}, err
		}
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
	SELECT v.id, v.upstream_source_id, v.upstream_root, v.public_root,
	       v.raw_tree_sha256, v.effective_tree_sha256, v.prompt_version_id,
	       v.file_count, v.raw_total_bytes, v.effective_total_bytes,
	       v.added_files, v.modified_files, v.deleted_files,
	       v.script_changes, v.binary_changes, v.fetched_at, v.created_by,
	       v.published_at, v.published_by, v.created_at
	FROM system_prompt_skill_bundle_versions AS v`

const remoteSkillDetailSelect = `
	SELECT v.id, v.upstream_source_id, v.upstream_root, v.public_root,
	       v.raw_tree_sha256, v.effective_tree_sha256, v.prompt_version_id,
	       v.file_count, v.raw_total_bytes, v.effective_total_bytes,
	       v.added_files, v.modified_files, v.deleted_files,
	       v.script_changes, v.binary_changes, v.fetched_at, v.created_by,
	       v.published_at, v.published_by, v.created_at, v.file_changes,
	       p.id, p.raw_sha256, p.effective_sha256, p.diff, p.raw_body, p.effective_body,
	       p.created_by, p.created_at
	FROM system_prompt_skill_bundle_versions AS v
	JOIN system_prompt_skill_prompt_versions AS p ON p.id = v.prompt_version_id`

type remoteSkillRowScanner interface {
	Scan(...any) error
}

func scanRemoteSkillVersion(row remoteSkillRowScanner) (service.RemoteSkillBundleVersion, error) {
	var version service.RemoteSkillBundleVersion
	var createdBy, publishedBy sql.NullInt64
	var publishedAt sql.NullTime
	err := row.Scan(
		&version.ID, &version.UpstreamSourceID, &version.UpstreamRoot, &version.PublicRoot,
		&version.RawTreeSHA256, &version.EffectiveTreeSHA256, &version.PromptVersionID,
		&version.FileCount, &version.RawTotalBytes, &version.EffectiveTotalBytes,
		&version.AddedFiles, &version.ModifiedFiles, &version.DeletedFiles,
		&version.ScriptChanges, &version.BinaryChanges, &version.FetchedAt, &createdBy,
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

func scanRemoteSkillVersionDetail(row remoteSkillRowScanner) (service.RemoteSkillBundleVersionDetail, error) {
	var detail service.RemoteSkillBundleVersionDetail
	var versionCreatedBy, publishedBy, promptCreatedBy sql.NullInt64
	var publishedAt sql.NullTime
	var changes []byte
	err := row.Scan(
		&detail.ID, &detail.UpstreamSourceID, &detail.UpstreamRoot, &detail.PublicRoot,
		&detail.RawTreeSHA256, &detail.EffectiveTreeSHA256, &detail.PromptVersionID,
		&detail.FileCount, &detail.RawTotalBytes, &detail.EffectiveTotalBytes,
		&detail.AddedFiles, &detail.ModifiedFiles, &detail.DeletedFiles,
		&detail.ScriptChanges, &detail.BinaryChanges, &detail.FetchedAt, &versionCreatedBy,
		&publishedAt, &publishedBy, &detail.CreatedAt, &changes,
		&detail.Prompt.ID, &detail.Prompt.RawSHA256, &detail.Prompt.EffectiveSHA256,
		&detail.Prompt.Diff, &detail.Prompt.RawBody, &detail.Prompt.EffectiveBody,
		&promptCreatedBy, &detail.Prompt.CreatedAt,
	)
	if err != nil {
		return service.RemoteSkillBundleVersionDetail{}, err
	}
	if len(changes) == 0 || string(changes) == "null" {
		changes = []byte("[]")
	}
	if err := json.Unmarshal(changes, &detail.FileChanges); err != nil {
		return service.RemoteSkillBundleVersionDetail{}, fmt.Errorf("decode remote skill file changes: %w", err)
	}
	detail.CreatedBy = nullableRemoteSkillInt64(versionCreatedBy)
	detail.PublishedBy = nullableRemoteSkillInt64(publishedBy)
	detail.PublishedAt = nullableTimePointer(publishedAt)
	detail.Prompt.CreatedBy = nullableRemoteSkillInt64(promptCreatedBy)
	return detail, nil
}

func getRemoteSkillVersionDetail(ctx context.Context, q businessSystemPromptQueryer, id, promptID int64) (service.RemoteSkillBundleVersionDetail, error) {
	query := remoteSkillDetailSelect + ` WHERE v.id = $1`
	args := []any{id}
	if promptID > 0 {
		query += ` AND p.id = $2`
		args = append(args, promptID)
	}
	detail, err := scanRemoteSkillVersionDetail(q.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return service.RemoteSkillBundleVersionDetail{}, service.ErrRemoteSkillVersionNotFound
	}
	return detail, err
}

func loadRemoteSkillRegistrySnapshot(ctx context.Context, q businessSystemPromptQueryer) (service.RemoteSkillRegistrySnapshot, error) {
	var snapshot service.RemoteSkillRegistrySnapshot
	var activeVersionID, activePromptID sql.NullInt64
	err := q.QueryRowContext(ctx, `
		SELECT revision, active_bundle_version_id, active_prompt_version_id, updated_at
		FROM system_prompt_skill_runtime WHERE id = 1`).Scan(
		&snapshot.Revision, &activeVersionID, &activePromptID, &snapshot.UpdatedAt)
	if err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	if !activeVersionID.Valid && !activePromptID.Valid {
		return snapshot, nil
	}
	if !activeVersionID.Valid || !activePromptID.Valid {
		return service.RemoteSkillRegistrySnapshot{}, fmt.Errorf("active remote skill pair is incomplete")
	}
	detail, err := getRemoteSkillVersionDetail(ctx, q, activeVersionID.Int64, activePromptID.Int64)
	if err != nil {
		return service.RemoteSkillRegistrySnapshot{}, err
	}
	snapshot.Active = &detail.RemoteSkillBundleVersion
	snapshot.ActivePrompt = &detail.Prompt
	return snapshot, nil
}

func insertOrValidateRemoteSkillCandidate(ctx context.Context, tx *sql.Tx, candidate service.RemoteSkillCandidate, reuseExact bool) (service.RemoteSkillBundleVersionDetail, error) {
	prompt, err := insertOrValidateRemoteSkillPrompt(ctx, tx, candidate.Prompt)
	if err != nil {
		return service.RemoteSkillBundleVersionDetail{}, err
	}
	changes := candidate.FileChanges
	if changes == nil {
		changes = []service.RemoteSkillFileChange{}
	}
	changesJSON, err := json.Marshal(changes)
	if err != nil {
		return service.RemoteSkillBundleVersionDetail{}, err
	}
	var id int64
	if reuseExact {
		err = tx.QueryRowContext(ctx, `
			SELECT id FROM system_prompt_skill_bundle_versions
			WHERE upstream_source_id = $1 AND upstream_root = $2 AND public_root = $3
			  AND raw_tree_sha256 = $4 AND effective_tree_sha256 = $5 AND prompt_version_id = $6
			  AND file_count = $7 AND raw_total_bytes = $8 AND effective_total_bytes = $9
			  AND added_files = $10 AND modified_files = $11 AND deleted_files = $12
			  AND script_changes = $13 AND binary_changes = $14 AND file_changes = $15::jsonb
			  AND fetched_at = $16 AND created_by IS NOT DISTINCT FROM $17
			ORDER BY id ASC LIMIT 1`,
			candidate.Version.UpstreamSourceID, candidate.Version.UpstreamRoot, candidate.Version.PublicRoot,
			candidate.Version.RawTreeSHA256, candidate.Version.EffectiveTreeSHA256, prompt.ID,
			candidate.Version.FileCount, candidate.Version.RawTotalBytes, candidate.Version.EffectiveTotalBytes,
			candidate.Version.AddedFiles, candidate.Version.ModifiedFiles, candidate.Version.DeletedFiles,
			candidate.Version.ScriptChanges, candidate.Version.BinaryChanges, changesJSON,
			candidate.Version.FetchedAt, nullableActor(candidate.Version.CreatedBy)).Scan(&id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return service.RemoteSkillBundleVersionDetail{}, err
		}
	}
	if id == 0 {
		err = tx.QueryRowContext(ctx, `
		INSERT INTO system_prompt_skill_bundle_versions
			(upstream_source_id, upstream_root, public_root, raw_tree_sha256, effective_tree_sha256,
			 prompt_version_id, file_count, raw_total_bytes, effective_total_bytes,
			 added_files, modified_files, deleted_files, script_changes, binary_changes,
			 file_changes, fetched_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		RETURNING id`,
			candidate.Version.UpstreamSourceID, candidate.Version.UpstreamRoot, candidate.Version.PublicRoot,
			candidate.Version.RawTreeSHA256, candidate.Version.EffectiveTreeSHA256, prompt.ID,
			candidate.Version.FileCount, candidate.Version.RawTotalBytes, candidate.Version.EffectiveTotalBytes,
			candidate.Version.AddedFiles, candidate.Version.ModifiedFiles, candidate.Version.DeletedFiles,
			candidate.Version.ScriptChanges, candidate.Version.BinaryChanges, changesJSON,
			candidate.Version.FetchedAt, nullableActor(candidate.Version.CreatedBy)).Scan(&id)
	}
	if err != nil {
		return service.RemoteSkillBundleVersionDetail{}, err
	}
	detail, err := getRemoteSkillVersionDetail(ctx, tx, id, prompt.ID)
	if err != nil {
		return service.RemoteSkillBundleVersionDetail{}, err
	}
	if err := validateStoredRemoteSkillCandidate(detail, candidate, changes); err != nil {
		return service.RemoteSkillBundleVersionDetail{}, err
	}
	return detail, nil
}

func insertOrValidateRemoteSkillPrompt(ctx context.Context, tx *sql.Tx, prompt service.RemoteSkillPromptVersion) (service.RemoteSkillPromptVersion, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO system_prompt_skill_prompt_versions
			(raw_sha256, effective_sha256, raw_body, effective_body, diff, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (raw_sha256, effective_sha256) DO NOTHING
		RETURNING id`, prompt.RawSHA256, prompt.EffectiveSHA256, prompt.RawBody, prompt.EffectiveBody,
		prompt.Diff, nullableActor(prompt.CreatedBy)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `
			SELECT id FROM system_prompt_skill_prompt_versions
			WHERE raw_sha256 = $1 AND effective_sha256 = $2`, prompt.RawSHA256, prompt.EffectiveSHA256).Scan(&id)
	}
	if err != nil {
		return service.RemoteSkillPromptVersion{}, err
	}
	var stored service.RemoteSkillPromptVersion
	var createdBy sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT id, raw_sha256, effective_sha256, diff, raw_body, effective_body, created_by, created_at
		FROM system_prompt_skill_prompt_versions WHERE id = $1`, id).Scan(
		&stored.ID, &stored.RawSHA256, &stored.EffectiveSHA256, &stored.Diff,
		&stored.RawBody, &stored.EffectiveBody, &createdBy, &stored.CreatedAt); err != nil {
		return service.RemoteSkillPromptVersion{}, err
	}
	stored.CreatedBy = nullableRemoteSkillInt64(createdBy)
	if stored.RawSHA256 != prompt.RawSHA256 || stored.EffectiveSHA256 != prompt.EffectiveSHA256 ||
		stored.RawBody != prompt.RawBody || stored.EffectiveBody != prompt.EffectiveBody || stored.Diff != prompt.Diff {
		return service.RemoteSkillPromptVersion{}, fmt.Errorf("%w: stored prompt metadata mismatch", service.ErrBusinessSystemPromptUnavailable)
	}
	return stored, nil
}

func validateRemoteSkillCandidateMetadata(candidate service.RemoteSkillCandidate) error {
	v := candidate.Version
	p := candidate.Prompt
	if v.UpstreamSourceID != service.RemoteSkillUpstreamSourceID || v.UpstreamRoot != service.RemoteSkillUpstreamRoot || v.PublicRoot != service.RemoteSkillPublicRoot ||
		!validRemoteSkillRepositorySHA(v.RawTreeSHA256) || !validRemoteSkillRepositorySHA(v.EffectiveTreeSHA256) ||
		!validRemoteSkillRepositorySHA(p.RawSHA256) || !validRemoteSkillRepositorySHA(p.EffectiveSHA256) ||
		v.FileCount < 1 || v.RawTotalBytes < 1 || v.EffectiveTotalBytes < 1 || v.FetchedAt.IsZero() ||
		sha256String(p.RawBody) != p.RawSHA256 || sha256String(p.EffectiveBody) != p.EffectiveSHA256 {
		return service.ErrBusinessSystemPromptInvalid
	}
	return nil
}

func validateStoredRemoteSkillCandidate(stored service.RemoteSkillBundleVersionDetail, candidate service.RemoteSkillCandidate, changes []service.RemoteSkillFileChange) error {
	v := candidate.Version
	if stored.UpstreamSourceID != v.UpstreamSourceID || stored.UpstreamRoot != v.UpstreamRoot || stored.PublicRoot != v.PublicRoot ||
		stored.RawTreeSHA256 != v.RawTreeSHA256 || stored.EffectiveTreeSHA256 != v.EffectiveTreeSHA256 ||
		stored.FileCount != v.FileCount || stored.RawTotalBytes != v.RawTotalBytes || stored.EffectiveTotalBytes != v.EffectiveTotalBytes ||
		stored.AddedFiles != v.AddedFiles || stored.ModifiedFiles != v.ModifiedFiles || stored.DeletedFiles != v.DeletedFiles ||
		stored.ScriptChanges != v.ScriptChanges || stored.BinaryChanges != v.BinaryChanges ||
		!stored.FetchedAt.Equal(v.FetchedAt) || stored.CreatedBy != v.CreatedBy {
		return fmt.Errorf("%w: stored candidate metadata mismatch", service.ErrBusinessSystemPromptUnavailable)
	}
	wantChanges, _ := json.Marshal(changes)
	gotChanges, _ := json.Marshal(stored.FileChanges)
	if string(wantChanges) != string(gotChanges) {
		return fmt.Errorf("%w: stored candidate diff mismatch", service.ErrBusinessSystemPromptUnavailable)
	}
	return nil
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

func (r *remoteSkillRegistryRepository) requireDatabase() error {
	if r == nil || r.db == nil {
		return errors.New("remote skill registry database unavailable")
	}
	return nil
}

func validRemoteSkillRepositorySHA(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func sha256String(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
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
