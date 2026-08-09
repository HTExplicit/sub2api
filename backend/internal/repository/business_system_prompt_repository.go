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

type businessSystemPromptQueryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type businessSystemPromptRepository struct {
	db *sql.DB
}

func NewBusinessSystemPromptRepository(db *sql.DB) service.BusinessSystemPromptStore {
	return &businessSystemPromptRepository{db: db}
}

func (r *businessSystemPromptRepository) EnsureBusinessSystemPromptSeed(ctx context.Context, seed service.BusinessSystemPromptSeed) error {
	if r == nil || r.db == nil {
		return errors.New("business system prompt database unavailable")
	}
	hash, byteLength, err := service.ValidateBusinessSystemPromptBody(seed.Body)
	if err != nil {
		return err
	}
	if seed.SHA256 != "" && !strings.EqualFold(seed.SHA256, hash) {
		return fmt.Errorf("seed hash mismatch")
	}
	if seed.ByteLength > 0 && seed.ByteLength != byteLength {
		return fmt.Errorf("seed byte length mismatch")
	}
	if seed.ManagedSource != "" || seed.SourceRepository != "" || seed.SourceCommit != "" ||
		seed.SourceVersion != "" || seed.SourceArtifact != "" || seed.SourceArtifactSHA256 != "" ||
		seed.SourceLicenseSHA256 != "" {
		if err := service.ValidateBusinessSystemPromptSourceCandidate(service.BusinessSystemPromptSourceCandidate{
			ManagedSource: seed.ManagedSource, SourceRepository: seed.SourceRepository,
			SourceCommit: seed.SourceCommit, SourceVersion: seed.SourceVersion,
			SourceArtifact: seed.SourceArtifact, SourceArtifactSHA256: seed.SourceArtifactSHA256,
			SourceLicenseSHA256: seed.SourceLicenseSHA256,
			Body:                seed.Body, SHA256: hash, ByteLength: byteLength,
		}); err != nil {
			return err
		}
	}
	composition, err := service.NormalizeBusinessSystemPromptComposition(seed.CompositionMode, seed.BundleID, seed.BundleManifestSHA256)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin seed transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO system_prompt_runtime (id, enabled, expose_server_prompt, compact_enabled, revision)
		VALUES (1, FALSE, FALSE, FALSE, 1)
		ON CONFLICT (id) DO NOTHING`); err != nil {
		return fmt.Errorf("ensure system prompt runtime: %w", err)
	}

	var templateID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO system_prompt_templates (slug, name, description, is_seed, managed_source)
		VALUES ($1, $2, $3, TRUE, $4)
		ON CONFLICT (slug) DO NOTHING
		RETURNING id`, seed.Slug, seed.Name, seed.Description, nullableString(seed.ManagedSource)).Scan(&templateID)
	if errors.Is(err, sql.ErrNoRows) {
		if !seed.UpgradeExistingSeed {
			// A pre-existing slug belongs to administrators unless the caller
			// explicitly identifies a protected built-in seed upgrade.
			return tx.Commit()
		}
		return ensureExistingBusinessSystemPromptSeed(ctx, tx, seed, hash, byteLength, composition)
	}
	if err != nil {
		return fmt.Errorf("insert system prompt seed template: %w", err)
	}

	var versionID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO system_prompt_template_versions
		(template_id, version, body, sha256, byte_length, composition_mode, bundle_id, bundle_manifest_sha256, note,
		 source_repository, source_commit, source_version, source_artifact, source_artifact_sha256, source_license_sha256)
		VALUES ($1, 1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id`, templateID, seed.Body, hash, byteLength, composition.Mode,
		nullableString(composition.BundleID), nullableString(composition.BundleManifestSHA256), seed.Note,
		nullableString(seed.SourceRepository), nullableString(seed.SourceCommit), nullableString(seed.SourceVersion),
		nullableString(seed.SourceArtifact), nullableString(seed.SourceArtifactSHA256), nullableString(seed.SourceLicenseSHA256)).Scan(&versionID); err != nil {
		return fmt.Errorf("insert system prompt seed version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE system_prompt_runtime
		SET active_template_id = $1, active_version_id = $2, updated_at = NOW()
		WHERE id = 1 AND active_template_id IS NULL`, templateID, versionID); err != nil {
		return fmt.Errorf("activate initial system prompt seed: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit system prompt seed: %w", err)
	}
	return nil
}

func ensureExistingBusinessSystemPromptSeed(
	ctx context.Context,
	tx *sql.Tx,
	seed service.BusinessSystemPromptSeed,
	hash string,
	byteLength int,
	composition service.BusinessSystemPromptComposition,
) error {
	var templateID int64
	var isSeed bool
	var managedSource sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT id, is_seed, managed_source
		FROM system_prompt_templates
		WHERE slug = $1 AND deleted_at IS NULL
		FOR UPDATE`, seed.Slug).Scan(&templateID, &isSeed, &managedSource); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrBusinessSystemPromptRevisionConflict
		}
		return err
	}
	if !isSeed || nullableStringValue(managedSource) != seed.ManagedSource {
		return tx.Commit()
	}

	var existingVersionID int64
	err := tx.QueryRowContext(ctx, `
		SELECT id FROM system_prompt_template_versions
		WHERE template_id = $1 AND sha256 = $2 AND byte_length = $3
		ORDER BY version DESC LIMIT 1`, templateID, hash, byteLength).Scan(&existingVersionID)
	if err == nil {
		if err := activateExistingBusinessSystemPromptSeedCandidate(ctx, tx, seed, templateID, existingVersionID); err != nil {
			return err
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	var latestVersion int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0)
		FROM system_prompt_template_versions
		WHERE template_id = $1`, templateID).Scan(&latestVersion); err != nil {
		return err
	}
	var activeTemplateID, activeVersionID sql.NullInt64
	var activeSHA sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT r.active_template_id, r.active_version_id, v.sha256
		FROM system_prompt_runtime r
		LEFT JOIN system_prompt_template_versions v ON v.id = r.active_version_id
		WHERE r.id = 1
		FOR UPDATE OF r`).Scan(&activeTemplateID, &activeVersionID, &activeSHA); err != nil {
		return err
	}

	var versionID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO system_prompt_template_versions
		(template_id, version, body, sha256, byte_length, composition_mode, bundle_id, bundle_manifest_sha256, note,
		 source_repository, source_commit, source_version, source_artifact, source_artifact_sha256, source_license_sha256)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING id`, templateID, latestVersion+1, seed.Body, hash, byteLength, composition.Mode,
		nullableString(composition.BundleID), nullableString(composition.BundleManifestSHA256), seed.Note,
		nullableString(seed.SourceRepository), nullableString(seed.SourceCommit), nullableString(seed.SourceVersion),
		nullableString(seed.SourceArtifact), nullableString(seed.SourceArtifactSHA256), nullableString(seed.SourceLicenseSHA256)).Scan(&versionID); err != nil {
		return translateBusinessSystemPromptWriteError(err)
	}

	if activeTemplateID.Valid && activeVersionID.Valid && activeTemplateID.Int64 == templateID && activeSHA.Valid &&
		containsBusinessSystemPromptSHA(seed.AutoActivateFromSHA, activeSHA.String) {
		if _, err := tx.ExecContext(ctx, `
			UPDATE system_prompt_runtime
			SET active_version_id = $1, revision = revision + 1, updated_at = NOW()
			WHERE id = 1 AND active_template_id = $2 AND active_version_id = $3`,
			versionID, templateID, activeVersionID.Int64); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func activateExistingBusinessSystemPromptSeedCandidate(
	ctx context.Context,
	tx *sql.Tx,
	seed service.BusinessSystemPromptSeed,
	templateID int64,
	candidateVersionID int64,
) error {
	if len(seed.AutoActivateFromSHA) == 0 {
		return nil
	}
	var activeTemplateID, activeVersionID sql.NullInt64
	var activeSHA sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT r.active_template_id, r.active_version_id, v.sha256
		FROM system_prompt_runtime r
		LEFT JOIN system_prompt_template_versions v ON v.id = r.active_version_id
		WHERE r.id = 1
		FOR UPDATE OF r`).Scan(&activeTemplateID, &activeVersionID, &activeSHA); err != nil {
		return err
	}
	if !activeTemplateID.Valid || !activeVersionID.Valid || activeTemplateID.Int64 != templateID ||
		activeVersionID.Int64 == candidateVersionID || !activeSHA.Valid ||
		!containsBusinessSystemPromptSHA(seed.AutoActivateFromSHA, activeSHA.String) {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE system_prompt_runtime
		SET active_version_id = $1, revision = revision + 1, updated_at = NOW()
		WHERE id = 1 AND active_template_id = $2 AND active_version_id = $3`,
		candidateVersionID, templateID, activeVersionID.Int64)
	return err
}

func containsBusinessSystemPromptSHA(allowed []string, value string) bool {
	for _, candidate := range allowed {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(value)) {
			return true
		}
	}
	return false
}

func (r *businessSystemPromptRepository) LoadBusinessSystemPrompt(ctx context.Context) (service.BusinessSystemPromptSnapshot, error) {
	if r == nil || r.db == nil {
		return service.BusinessSystemPromptSnapshot{}, errors.New("business system prompt database unavailable")
	}
	return loadBusinessSystemPromptSnapshot(ctx, r.db)
}

func loadBusinessSystemPromptSnapshot(ctx context.Context, q businessSystemPromptQueryer) (service.BusinessSystemPromptSnapshot, error) {
	var snapshot service.BusinessSystemPromptSnapshot
	var templateID, versionID, templateVersion sql.NullInt64
	var body, hash, compositionMode, bundleID, bundleManifestSHA256 sql.NullString
	var byteLength sql.NullInt64
	err := q.QueryRowContext(ctx, `
		SELECT r.enabled, r.expose_server_prompt, r.compact_enabled,
		       r.active_template_id, r.active_version_id, r.revision,
		       v.version, v.body, v.sha256, v.byte_length,
		       v.composition_mode, v.bundle_id, v.bundle_manifest_sha256,
		       r.updated_at
		FROM system_prompt_runtime r
		LEFT JOIN system_prompt_template_versions v
		  ON v.id = r.active_version_id AND v.template_id = r.active_template_id
		WHERE r.id = 1`).Scan(
		&snapshot.Enabled, &snapshot.ExposeServerPrompt, &snapshot.CompactEnabled,
		&templateID, &versionID, &snapshot.Revision, &templateVersion, &body, &hash, &byteLength,
		&compositionMode, &bundleID, &bundleManifestSHA256, &snapshot.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return service.BusinessSystemPromptSnapshot{}, service.ErrBusinessSystemPromptUnavailable
	}
	if err != nil {
		return service.BusinessSystemPromptSnapshot{}, fmt.Errorf("load system prompt runtime: %w", err)
	}
	if templateID.Valid {
		snapshot.TemplateID = templateID.Int64
	}
	if versionID.Valid {
		snapshot.VersionID = versionID.Int64
	}
	if templateVersion.Valid {
		snapshot.TemplateVersion = templateVersion.Int64
	}
	if body.Valid {
		snapshot.Body = body.String
	}
	if hash.Valid {
		snapshot.SHA256 = hash.String
	}
	if byteLength.Valid {
		snapshot.ByteLength = int(byteLength.Int64)
	}
	if compositionMode.Valid {
		snapshot.CompositionMode = compositionMode.String
	}
	if bundleID.Valid {
		snapshot.BundleID = bundleID.String
	}
	if bundleManifestSHA256.Valid {
		snapshot.BundleManifestSHA256 = bundleManifestSHA256.String
	}
	return snapshot, nil
}

func (r *businessSystemPromptRepository) ListBusinessSystemPromptTemplates(ctx context.Context) ([]service.BusinessSystemPromptTemplate, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, slug, name, description, is_seed, managed_source, deleted_at,
		       created_by, updated_by, created_at, updated_at
		FROM system_prompt_templates
		WHERE deleted_at IS NULL
		ORDER BY name ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list system prompt templates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make([]service.BusinessSystemPromptTemplate, 0)
	for rows.Next() {
		template, err := scanBusinessSystemPromptTemplate(rows)
		if err != nil {
			return nil, fmt.Errorf("scan system prompt template: %w", err)
		}
		result = append(result, template)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate system prompt templates: %w", err)
	}
	return result, nil
}

func (r *businessSystemPromptRepository) GetBusinessSystemPromptTemplate(ctx context.Context, id int64) (service.BusinessSystemPromptTemplateDetail, error) {
	template, err := queryBusinessSystemPromptTemplate(ctx, r.db, id)
	if err != nil {
		return service.BusinessSystemPromptTemplateDetail{}, err
	}
	versions, err := queryBusinessSystemPromptVersions(ctx, r.db, id)
	if err != nil {
		return service.BusinessSystemPromptTemplateDetail{}, err
	}
	runtime, err := r.LoadBusinessSystemPrompt(ctx)
	if err == nil {
		for i := range versions {
			versions[i].IsActive = versions[i].ID == runtime.VersionID
		}
	}
	return service.BusinessSystemPromptTemplateDetail{Template: template, Versions: versions}, nil
}

func (r *businessSystemPromptRepository) CreateBusinessSystemPromptTemplate(ctx context.Context, req service.BusinessSystemPromptTemplateCreate, actorID, expectedRevision int64) (service.BusinessSystemPromptTemplateDetail, error) {
	hash, byteLength, err := service.ValidateBusinessSystemPromptBody(req.Body)
	if err != nil {
		return service.BusinessSystemPromptTemplateDetail{}, err
	}
	composition, err := service.NormalizeBusinessSystemPromptComposition(req.CompositionMode, req.BundleID, req.BundleManifestSHA256)
	if err != nil {
		return service.BusinessSystemPromptTemplateDetail{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return service.BusinessSystemPromptTemplateDetail{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockBusinessSystemPromptRuntimeRevision(ctx, tx, expectedRevision); err != nil {
		return service.BusinessSystemPromptTemplateDetail{}, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO system_prompt_templates (slug, name, description, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $4) RETURNING id`, req.Slug, req.Name, req.Description, nullableActor(actorID)).Scan(&id); err != nil {
		return service.BusinessSystemPromptTemplateDetail{}, translateBusinessSystemPromptWriteError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO system_prompt_template_versions
		(template_id, version, body, sha256, byte_length, composition_mode, bundle_id, bundle_manifest_sha256, note, created_by)
		VALUES ($1, 1, $2, $3, $4, $5, $6, $7, $8, $9)`, id, req.Body, hash, byteLength,
		composition.Mode, nullableString(composition.BundleID), nullableString(composition.BundleManifestSHA256), req.Note, nullableActor(actorID)); err != nil {
		return service.BusinessSystemPromptTemplateDetail{}, fmt.Errorf("insert system prompt template version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return service.BusinessSystemPromptTemplateDetail{}, err
	}
	return r.GetBusinessSystemPromptTemplate(ctx, id)
}

func (r *businessSystemPromptRepository) UpdateBusinessSystemPromptTemplate(ctx context.Context, id int64, req service.BusinessSystemPromptTemplateUpdate, actorID, expectedRevision int64) (service.BusinessSystemPromptTemplate, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return service.BusinessSystemPromptTemplate{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockBusinessSystemPromptRuntimeRevision(ctx, tx, expectedRevision); err != nil {
		return service.BusinessSystemPromptTemplate{}, err
	}
	var name, description any
	if req.Name != nil {
		name = *req.Name
	}
	if req.Description != nil {
		description = *req.Description
	}
	row := tx.QueryRowContext(ctx, `
		UPDATE system_prompt_templates
		SET name = COALESCE($2, name), description = COALESCE($3, description),
		    updated_by = $4, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING id, slug, name, description, is_seed, managed_source, deleted_at,
		          created_by, updated_by, created_at, updated_at`, id, name, description, nullableActor(actorID))
	template, err := scanBusinessSystemPromptTemplate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return service.BusinessSystemPromptTemplate{}, service.ErrBusinessSystemPromptTemplateNotFound
	}
	if err != nil {
		return service.BusinessSystemPromptTemplate{}, fmt.Errorf("update system prompt template: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return service.BusinessSystemPromptTemplate{}, err
	}
	return template, nil
}

func (r *businessSystemPromptRepository) CreateBusinessSystemPromptVersion(ctx context.Context, templateID int64, body, note string, actorID, expectedLatestVersion, expectedRevision int64) (service.BusinessSystemPromptVersion, error) {
	return r.CreateBusinessSystemPromptVersionWithComposition(ctx, templateID, service.BusinessSystemPromptVersionCreate{
		Body:            body,
		Note:            note,
		CompositionMode: service.BusinessSystemPromptCompositionInline,
	}, actorID, expectedLatestVersion, expectedRevision)
}

func (r *businessSystemPromptRepository) CreateBusinessSystemPromptVersionWithComposition(ctx context.Context, templateID int64, req service.BusinessSystemPromptVersionCreate, actorID, expectedLatestVersion, expectedRevision int64) (service.BusinessSystemPromptVersion, error) {
	hash, byteLength, err := service.ValidateBusinessSystemPromptBody(req.Body)
	if err != nil {
		return service.BusinessSystemPromptVersion{}, err
	}
	composition, err := service.NormalizeBusinessSystemPromptComposition(req.CompositionMode, req.BundleID, req.BundleManifestSHA256)
	if err != nil {
		return service.BusinessSystemPromptVersion{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return service.BusinessSystemPromptVersion{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockBusinessSystemPromptRuntimeRevision(ctx, tx, expectedRevision); err != nil {
		return service.BusinessSystemPromptVersion{}, err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM system_prompt_templates WHERE id = $1 AND deleted_at IS NULL)
	`, templateID).Scan(&exists); err != nil {
		return service.BusinessSystemPromptVersion{}, err
	}
	if !exists {
		return service.BusinessSystemPromptVersion{}, service.ErrBusinessSystemPromptTemplateNotFound
	}
	// Lock the parent row before reading the aggregate. PostgreSQL does not
	// allow FOR UPDATE on an aggregate query, and the parent lock serializes
	// version allocation for this template.
	var lockedID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM system_prompt_templates WHERE id = $1 FOR UPDATE`, templateID).Scan(&lockedID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.BusinessSystemPromptVersion{}, service.ErrBusinessSystemPromptTemplateNotFound
		}
		return service.BusinessSystemPromptVersion{}, err
	}
	var latest int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM system_prompt_template_versions WHERE template_id = $1`, templateID).Scan(&latest); err != nil {
		return service.BusinessSystemPromptVersion{}, err
	}
	if expectedLatestVersion > 0 && latest != expectedLatestVersion {
		return service.BusinessSystemPromptVersion{}, service.ErrBusinessSystemPromptRevisionConflict
	}
	var version service.BusinessSystemPromptVersion
	var createdBy sql.NullInt64
	var bundleID, bundleManifestSHA256 sql.NullString
	var sourceRepository, sourceCommit, sourceVersion, sourceArtifact sql.NullString
	var sourceArtifactSHA256, sourceLicenseSHA256 sql.NullString
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO system_prompt_template_versions
		(template_id, version, body, sha256, byte_length, composition_mode, bundle_id, bundle_manifest_sha256, note, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, template_id, version, body, sha256, byte_length,
		          composition_mode, bundle_id, bundle_manifest_sha256, note,
		          source_repository, source_commit, source_version, source_artifact,
		          source_artifact_sha256, source_license_sha256,
		          created_by, published_at, published_by, created_at`,
		templateID, latest+1, req.Body, hash, byteLength, composition.Mode,
		nullableString(composition.BundleID), nullableString(composition.BundleManifestSHA256), req.Note, nullableActor(actorID)).Scan(
		&version.ID, &version.TemplateID, &version.Version, &version.Body, &version.SHA256,
		&version.ByteLength, &version.CompositionMode, &bundleID, &bundleManifestSHA256,
		&version.Note, &sourceRepository, &sourceCommit, &sourceVersion, &sourceArtifact,
		&sourceArtifactSHA256, &sourceLicenseSHA256,
		&createdBy, &version.PublishedAt, &version.PublishedBy, &version.CreatedAt); err != nil {
		return service.BusinessSystemPromptVersion{}, translateBusinessSystemPromptWriteError(err)
	}
	version.CreatedBy = nullableInt64Value(createdBy)
	version.BundleID = nullableStringValue(bundleID)
	version.BundleManifestSHA256 = nullableStringValue(bundleManifestSHA256)
	version.SourceRepository = nullableStringValue(sourceRepository)
	version.SourceCommit = nullableStringValue(sourceCommit)
	version.SourceVersion = nullableStringValue(sourceVersion)
	version.SourceArtifact = nullableStringValue(sourceArtifact)
	version.SourceArtifactSHA256 = nullableStringValue(sourceArtifactSHA256)
	version.SourceLicenseSHA256 = nullableStringValue(sourceLicenseSHA256)
	if err := tx.Commit(); err != nil {
		return service.BusinessSystemPromptVersion{}, err
	}
	return version, nil
}

func (r *businessSystemPromptRepository) SyncBusinessSystemPromptSourceVersion(
	ctx context.Context,
	templateID int64,
	candidate service.BusinessSystemPromptSourceCandidate,
	actorID, expectedLatestVersion, expectedRevision int64,
) (service.BusinessSystemPromptSourceSyncResult, error) {
	if err := service.ValidateBusinessSystemPromptSourceCandidate(candidate); err != nil {
		return service.BusinessSystemPromptSourceSyncResult{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return service.BusinessSystemPromptSourceSyncResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockBusinessSystemPromptRuntimeRevision(ctx, tx, expectedRevision); err != nil {
		return service.BusinessSystemPromptSourceSyncResult{}, err
	}
	var managedSource sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT managed_source FROM system_prompt_templates
		WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, templateID).Scan(&managedSource); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.BusinessSystemPromptSourceSyncResult{}, service.ErrBusinessSystemPromptTemplateNotFound
		}
		return service.BusinessSystemPromptSourceSyncResult{}, err
	}
	if !managedSource.Valid || managedSource.String != candidate.ManagedSource {
		return service.BusinessSystemPromptSourceSyncResult{}, service.ErrBusinessSystemPromptSourceNotManaged
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT version, sha256, source_repository, source_commit, source_artifact_sha256
		FROM system_prompt_template_versions
		WHERE template_id = $1 ORDER BY version DESC`, templateID)
	if err != nil {
		return service.BusinessSystemPromptSourceSyncResult{}, err
	}
	var latest int64
	upToDate := false
	noPromptChange := false
	for rows.Next() {
		var version int64
		var sha string
		var repository, commit, artifactSHA sql.NullString
		if err := rows.Scan(&version, &sha, &repository, &commit, &artifactSHA); err != nil {
			_ = rows.Close()
			return service.BusinessSystemPromptSourceSyncResult{}, err
		}
		if version > latest {
			latest = version
		}
		if repository.Valid && commit.Valid && artifactSHA.Valid &&
			repository.String == candidate.SourceRepository && commit.String == candidate.SourceCommit &&
			artifactSHA.String == candidate.SourceArtifactSHA256 {
			upToDate = true
		}
		if strings.EqualFold(sha, candidate.SHA256) {
			noPromptChange = true
		}
	}
	if err := rows.Close(); err != nil {
		return service.BusinessSystemPromptSourceSyncResult{}, err
	}
	if err := rows.Err(); err != nil {
		return service.BusinessSystemPromptSourceSyncResult{}, err
	}
	if expectedLatestVersion < 1 || latest != expectedLatestVersion {
		return service.BusinessSystemPromptSourceSyncResult{}, service.ErrBusinessSystemPromptRevisionConflict
	}
	if upToDate || noPromptChange {
		status := service.BusinessSystemPromptSourceSyncNoPromptChange
		if upToDate {
			status = service.BusinessSystemPromptSourceSyncUpToDate
		}
		if err := tx.Commit(); err != nil {
			return service.BusinessSystemPromptSourceSyncResult{}, err
		}
		return service.BusinessSystemPromptSourceSyncResult{Status: status}, nil
	}
	note := fmt.Sprintf("Synced from %s %s", candidate.SourceRepository, candidate.SourceVersion)
	version, err := scanBusinessSystemPromptVersion(tx.QueryRowContext(ctx, `
		INSERT INTO system_prompt_template_versions
		(template_id, version, body, sha256, byte_length, composition_mode, note, created_by,
		 source_repository, source_commit, source_version, source_artifact, source_artifact_sha256, source_license_sha256)
		VALUES ($1, $2, $3, $4, $5, 'inline', $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, template_id, version, body, sha256, byte_length,
		          composition_mode, bundle_id, bundle_manifest_sha256, note,
		          source_repository, source_commit, source_version, source_artifact,
		          source_artifact_sha256, source_license_sha256,
		          created_by, published_at, published_by, created_at`,
		templateID, latest+1, candidate.Body, candidate.SHA256, candidate.ByteLength,
		note, nullableActor(actorID), candidate.SourceRepository, candidate.SourceCommit,
		candidate.SourceVersion, candidate.SourceArtifact, candidate.SourceArtifactSHA256,
		candidate.SourceLicenseSHA256))
	if err != nil {
		return service.BusinessSystemPromptSourceSyncResult{}, translateBusinessSystemPromptWriteError(err)
	}
	if err := tx.Commit(); err != nil {
		return service.BusinessSystemPromptSourceSyncResult{}, err
	}
	return service.BusinessSystemPromptSourceSyncResult{
		Status: service.BusinessSystemPromptSourceSyncCandidateCreated, Version: &version,
	}, nil
}

func (r *businessSystemPromptRepository) DuplicateBusinessSystemPromptTemplate(ctx context.Context, sourceID int64, slug, name string, actorID, expectedRevision int64) (service.BusinessSystemPromptTemplateDetail, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return service.BusinessSystemPromptTemplateDetail{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockBusinessSystemPromptRuntimeRevision(ctx, tx, expectedRevision); err != nil {
		return service.BusinessSystemPromptTemplateDetail{}, err
	}
	var body, hash, note, compositionMode string
	var bundleID, bundleManifestSHA256 sql.NullString
	var byteLength int
	if err := tx.QueryRowContext(ctx, `
		SELECT v.body, v.sha256, v.byte_length, v.composition_mode,
		       v.bundle_id, v.bundle_manifest_sha256, v.note
		FROM system_prompt_templates t
		JOIN LATERAL (
			SELECT body, sha256, byte_length, composition_mode,
			       bundle_id, bundle_manifest_sha256, note
			FROM system_prompt_template_versions
			WHERE template_id = t.id ORDER BY version DESC LIMIT 1
		) v ON TRUE
		WHERE t.id = $1 AND t.deleted_at IS NULL`, sourceID).Scan(
		&body, &hash, &byteLength, &compositionMode, &bundleID, &bundleManifestSHA256, &note); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.BusinessSystemPromptTemplateDetail{}, service.ErrBusinessSystemPromptTemplateNotFound
		}
		return service.BusinessSystemPromptTemplateDetail{}, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO system_prompt_templates (slug, name, description, created_by, updated_by)
		SELECT $1, $2, 'Duplicated business system prompt', $3, $3
		RETURNING id`, slug, name, nullableActor(actorID)).Scan(&id); err != nil {
		return service.BusinessSystemPromptTemplateDetail{}, translateBusinessSystemPromptWriteError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO system_prompt_template_versions
		(template_id, version, body, sha256, byte_length, composition_mode, bundle_id, bundle_manifest_sha256, note, created_by)
		VALUES ($1, 1, $2, $3, $4, $5, $6, $7, $8, $9)`, id, body, hash, byteLength,
		compositionMode, nullableString(nullableStringValue(bundleID)), nullableString(nullableStringValue(bundleManifestSHA256)), note, nullableActor(actorID)); err != nil {
		return service.BusinessSystemPromptTemplateDetail{}, err
	}
	if err := tx.Commit(); err != nil {
		return service.BusinessSystemPromptTemplateDetail{}, err
	}
	return r.GetBusinessSystemPromptTemplate(ctx, id)
}

func (r *businessSystemPromptRepository) SoftDeleteBusinessSystemPromptTemplate(ctx context.Context, id, actorID, expectedRevision int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockBusinessSystemPromptRuntimeRevision(ctx, tx, expectedRevision); err != nil {
		return err
	}
	var isSeed bool
	if err := tx.QueryRowContext(ctx, `SELECT is_seed FROM system_prompt_templates WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&isSeed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrBusinessSystemPromptTemplateNotFound
		}
		return err
	}
	if isSeed {
		return service.ErrBusinessSystemPromptSeedProtected
	}
	var activeID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT active_template_id FROM system_prompt_runtime WHERE id = 1`).Scan(&activeID); err != nil {
		return err
	}
	if activeID.Valid && activeID.Int64 == id {
		return service.ErrBusinessSystemPromptActive
	}
	if _, err := tx.ExecContext(ctx, `UPDATE system_prompt_templates SET deleted_at = NOW(), updated_by = $2, updated_at = NOW() WHERE id = $1`, id, nullableActor(actorID)); err != nil {
		return err
	}
	return tx.Commit()
}

func lockBusinessSystemPromptRuntimeRevision(ctx context.Context, tx *sql.Tx, expectedRevision int64) error {
	if tx == nil {
		return errors.New("business system prompt transaction unavailable")
	}
	var currentRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM system_prompt_runtime WHERE id = 1 FOR UPDATE`).Scan(&currentRevision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrBusinessSystemPromptUnavailable
		}
		return err
	}
	if expectedRevision < 1 || currentRevision != expectedRevision {
		return service.ErrBusinessSystemPromptRevisionConflict
	}
	return nil
}

func (r *businessSystemPromptRepository) PublishBusinessSystemPromptVersion(ctx context.Context, templateID, versionID, expectedRevision, actorID int64) (service.BusinessSystemPromptSnapshot, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return service.BusinessSystemPromptSnapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var currentRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM system_prompt_runtime WHERE id = 1 FOR UPDATE`).Scan(&currentRevision); err != nil {
		return service.BusinessSystemPromptSnapshot{}, err
	}
	if expectedRevision < 1 || currentRevision != expectedRevision {
		return service.BusinessSystemPromptSnapshot{}, service.ErrBusinessSystemPromptRevisionConflict
	}
	var body, hash, compositionMode string
	var bundleID, bundleManifestSHA256 sql.NullString
	var byteLength int
	if err := tx.QueryRowContext(ctx, `
		SELECT v.body, v.sha256, v.byte_length, v.composition_mode,
		       v.bundle_id, v.bundle_manifest_sha256
		FROM system_prompt_template_versions v
		JOIN system_prompt_templates t ON t.id = v.template_id
		WHERE v.id = $1 AND v.template_id = $2 AND t.deleted_at IS NULL`, versionID, templateID).Scan(
		&body, &hash, &byteLength, &compositionMode, &bundleID, &bundleManifestSHA256); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.BusinessSystemPromptSnapshot{}, service.ErrBusinessSystemPromptVersionNotFound
		}
		return service.BusinessSystemPromptSnapshot{}, err
	}
	if err := validateStoredBusinessSystemPromptVersion(body, hash, byteLength, compositionMode, nullableStringValue(bundleID), nullableStringValue(bundleManifestSHA256)); err != nil {
		return service.BusinessSystemPromptSnapshot{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE system_prompt_template_versions SET published_at = COALESCE(published_at, NOW()), published_by = $3 WHERE id = $1 AND template_id = $2`, versionID, templateID, nullableActor(actorID)); err != nil {
		return service.BusinessSystemPromptSnapshot{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE system_prompt_runtime
		SET active_template_id = $1, active_version_id = $2, revision = revision + 1,
		    updated_by = $3, updated_at = NOW()
		WHERE id = 1`, templateID, versionID, nullableActor(actorID)); err != nil {
		return service.BusinessSystemPromptSnapshot{}, err
	}
	snapshot, err := loadBusinessSystemPromptSnapshot(ctx, tx)
	if err != nil {
		return service.BusinessSystemPromptSnapshot{}, err
	}
	if snapshot.Body == "" {
		snapshot.Body, snapshot.SHA256, snapshot.ByteLength = body, hash, byteLength
		snapshot.CompositionMode = compositionMode
		snapshot.BundleID = nullableStringValue(bundleID)
		snapshot.BundleManifestSHA256 = nullableStringValue(bundleManifestSHA256)
	}
	if err := tx.Commit(); err != nil {
		return service.BusinessSystemPromptSnapshot{}, err
	}
	return snapshot, nil
}

func (r *businessSystemPromptRepository) UpdateBusinessSystemPromptRuntime(ctx context.Context, update service.BusinessSystemPromptRuntimeUpdate) (service.BusinessSystemPromptSnapshot, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return service.BusinessSystemPromptSnapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var currentRevision int64
	var templateID, versionID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT revision, active_template_id, active_version_id FROM system_prompt_runtime WHERE id = 1 FOR UPDATE`).Scan(&currentRevision, &templateID, &versionID); err != nil {
		return service.BusinessSystemPromptSnapshot{}, err
	}
	if update.ExpectedRevision < 1 || currentRevision != update.ExpectedRevision {
		return service.BusinessSystemPromptSnapshot{}, service.ErrBusinessSystemPromptRevisionConflict
	}
	if update.Enabled && (!templateID.Valid || !versionID.Valid) {
		return service.BusinessSystemPromptSnapshot{}, service.ErrBusinessSystemPromptUnavailable
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE system_prompt_runtime
		SET enabled = $1, expose_server_prompt = $2, compact_enabled = $3,
		    revision = revision + 1, updated_by = $4, updated_at = NOW()
		WHERE id = 1`, update.Enabled, update.ExposeServerPrompt, update.CompactEnabled, nullableActor(update.ActorID)); err != nil {
		return service.BusinessSystemPromptSnapshot{}, err
	}
	snapshot, err := loadBusinessSystemPromptSnapshot(ctx, tx)
	if err != nil {
		return service.BusinessSystemPromptSnapshot{}, err
	}
	if update.Enabled {
		if err := validateStoredBusinessSystemPromptVersion(snapshot.Body, snapshot.SHA256, snapshot.ByteLength, snapshot.CompositionMode, snapshot.BundleID, snapshot.BundleManifestSHA256); err != nil {
			return service.BusinessSystemPromptSnapshot{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return service.BusinessSystemPromptSnapshot{}, err
	}
	return snapshot, nil
}

func validateStoredBusinessSystemPrompt(body, expectedHash string, expectedLength int) error {
	hash, byteLength, err := service.ValidateBusinessSystemPromptBody(body)
	if err != nil {
		return fmt.Errorf("%w: %v", service.ErrBusinessSystemPromptUnavailable, err)
	}
	if !strings.EqualFold(strings.TrimSpace(expectedHash), hash) || expectedLength != byteLength {
		return fmt.Errorf("%w: stored prompt digest mismatch", service.ErrBusinessSystemPromptUnavailable)
	}
	return nil
}

func validateStoredBusinessSystemPromptVersion(body, expectedHash string, expectedLength int, compositionMode, bundleID, bundleManifestSHA256 string) error {
	if err := validateStoredBusinessSystemPrompt(body, expectedHash, expectedLength); err != nil {
		return err
	}
	if _, err := service.NormalizeBusinessSystemPromptComposition(compositionMode, bundleID, bundleManifestSHA256); err != nil {
		return fmt.Errorf("%w: %v", service.ErrBusinessSystemPromptUnavailable, err)
	}
	return nil
}

func queryBusinessSystemPromptTemplate(ctx context.Context, q businessSystemPromptQueryer, id int64) (service.BusinessSystemPromptTemplate, error) {
	row := q.QueryRowContext(ctx, `
		SELECT id, slug, name, description, is_seed, managed_source, deleted_at,
		       created_by, updated_by, created_at, updated_at
		FROM system_prompt_templates WHERE id = $1 AND deleted_at IS NULL`, id)
	template, err := scanBusinessSystemPromptTemplate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return service.BusinessSystemPromptTemplate{}, service.ErrBusinessSystemPromptTemplateNotFound
	}
	return template, err
}

func queryBusinessSystemPromptVersions(ctx context.Context, q businessSystemPromptQueryer, templateID int64) ([]service.BusinessSystemPromptVersion, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id, template_id, version, body, sha256, byte_length,
		       composition_mode, bundle_id, bundle_manifest_sha256, note,
		       source_repository, source_commit, source_version, source_artifact,
		       source_artifact_sha256, source_license_sha256,
		       created_by, published_at, published_by, created_at
		FROM system_prompt_template_versions
		WHERE template_id = $1 ORDER BY version DESC`, templateID)
	if err != nil {
		return nil, fmt.Errorf("list system prompt versions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	versions := make([]service.BusinessSystemPromptVersion, 0)
	for rows.Next() {
		version, err := scanBusinessSystemPromptVersion(rows)
		if err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

type businessSystemPromptScanner interface {
	Scan(...any) error
}

func scanBusinessSystemPromptTemplate(scanner businessSystemPromptScanner) (service.BusinessSystemPromptTemplate, error) {
	var template service.BusinessSystemPromptTemplate
	var deletedAt sql.NullTime
	var managedSource sql.NullString
	var createdByID, updatedByID sql.NullInt64
	err := scanner.Scan(&template.ID, &template.Slug, &template.Name, &template.Description, &template.IsSeed,
		&managedSource, &deletedAt, &createdByID, &updatedByID, &template.CreatedAt, &template.UpdatedAt)
	if err != nil {
		return service.BusinessSystemPromptTemplate{}, err
	}
	if deletedAt.Valid {
		template.DeletedAt = &deletedAt.Time
	}
	template.CreatedBy = nullableInt64Value(createdByID)
	template.UpdatedBy = nullableInt64Value(updatedByID)
	template.ManagedSource = nullableStringValue(managedSource)
	return template, nil
}

func scanBusinessSystemPromptVersion(scanner businessSystemPromptScanner) (service.BusinessSystemPromptVersion, error) {
	var version service.BusinessSystemPromptVersion
	var createdBy, publishedBy sql.NullInt64
	var bundleID, bundleManifestSHA256 sql.NullString
	var sourceRepository, sourceCommit, sourceVersion, sourceArtifact sql.NullString
	var sourceArtifactSHA256, sourceLicenseSHA256 sql.NullString
	err := scanner.Scan(&version.ID, &version.TemplateID, &version.Version, &version.Body, &version.SHA256,
		&version.ByteLength, &version.CompositionMode, &bundleID, &bundleManifestSHA256,
		&version.Note, &sourceRepository, &sourceCommit, &sourceVersion, &sourceArtifact,
		&sourceArtifactSHA256, &sourceLicenseSHA256,
		&createdBy, &version.PublishedAt, &publishedBy, &version.CreatedAt)
	if err != nil {
		return service.BusinessSystemPromptVersion{}, err
	}
	version.CreatedBy = nullableInt64Value(createdBy)
	version.PublishedBy = nullableInt64Value(publishedBy)
	version.BundleID = nullableStringValue(bundleID)
	version.BundleManifestSHA256 = nullableStringValue(bundleManifestSHA256)
	version.SourceRepository = nullableStringValue(sourceRepository)
	version.SourceCommit = nullableStringValue(sourceCommit)
	version.SourceVersion = nullableStringValue(sourceVersion)
	version.SourceArtifact = nullableStringValue(sourceArtifact)
	version.SourceArtifactSHA256 = nullableStringValue(sourceArtifactSHA256)
	version.SourceLicenseSHA256 = nullableStringValue(sourceLicenseSHA256)
	return version, nil
}

func nullableActor(actorID int64) any {
	if actorID <= 0 {
		return nil
	}
	return actorID
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableStringValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func nullableInt64Value(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func translateBusinessSystemPromptWriteError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "duplicate key") {
		return fmt.Errorf("%w: %v", service.ErrBusinessSystemPromptRevisionConflict, err)
	}
	return err
}

var _ = time.Time{}
