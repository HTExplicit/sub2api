package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBusinessSystemPromptRepositoryLoadsActiveSnapshot(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT r.enabled")).
		WillReturnRows(sqlmock.NewRows([]string{
			"enabled", "expose_server_prompt", "compact_enabled", "active_template_id", "active_version_id", "revision", "template_version", "body", "sha256", "byte_length", "composition_mode", "bundle_id", "bundle_manifest_sha256", "updated_at",
		}).AddRow(true, false, false, int64(3), int64(8), int64(4), int64(2), "body", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", 4, service.BusinessSystemPromptCompositionOfflineBundle, service.BusinessSystemPromptSeedBundleID, service.BusinessSystemPromptSeedBundleManifestSHA256, time.Date(2026, 8, 6, 1, 2, 3, 0, time.UTC)))

	store := NewBusinessSystemPromptRepository(db)
	snapshot, err := store.LoadBusinessSystemPrompt(context.Background())
	require.NoError(t, err)
	require.True(t, snapshot.Enabled)
	require.Equal(t, int64(4), snapshot.Revision)
	require.Equal(t, int64(2), snapshot.TemplateVersion)
	require.Equal(t, "body", snapshot.Body)
	require.Equal(t, service.BusinessSystemPromptCompositionOfflineBundle, snapshot.CompositionMode)
	require.Equal(t, service.BusinessSystemPromptSeedBundleID, snapshot.BundleID)
	require.Equal(t, service.BusinessSystemPromptSeedBundleManifestSHA256, snapshot.BundleManifestSHA256)
	require.Equal(t, time.Date(2026, 8, 6, 1, 2, 3, 0, time.UTC), snapshot.UpdatedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPublishBusinessSystemPromptVersionRejectsCorruptStoredBodyBeforeCommit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT revision FROM system_prompt_runtime WHERE id = 1 FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(int64(7)))
	mock.ExpectQuery("SELECT v\\.body, v\\.sha256, v\\.byte_length").
		WithArgs(int64(3), int64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"body", "sha256", "byte_length", "composition_mode", "bundle_id", "bundle_manifest_sha256"}).AddRow("body", strings.Repeat("0", 64), 4, "inline", nil, nil))
	mock.ExpectRollback()

	store := NewBusinessSystemPromptRepository(db)
	_, err = store.PublishBusinessSystemPromptVersion(context.Background(), 2, 3, 7, 9)
	require.ErrorIs(t, err, service.ErrBusinessSystemPromptUnavailable)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPublishBusinessSystemPromptVersionRejectsInvalidOfflineBundleReference(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	body := "body"
	digest := sha256.Sum256([]byte(body))
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT revision FROM system_prompt_runtime WHERE id = 1 FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(int64(7)))
	mock.ExpectQuery("SELECT v\\.body, v\\.sha256, v\\.byte_length").
		WithArgs(int64(3), int64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"body", "sha256", "byte_length", "composition_mode", "bundle_id", "bundle_manifest_sha256"}).AddRow(body, hex.EncodeToString(digest[:]), len(body), "offline_bundle", "bundle", "bad"))
	mock.ExpectRollback()

	store := NewBusinessSystemPromptRepository(db)
	_, err = store.PublishBusinessSystemPromptVersion(context.Background(), 2, 3, 7, 9)
	require.ErrorIs(t, err, service.ErrBusinessSystemPromptUnavailable)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestValidateStoredBusinessSystemPromptAcceptsMatchingDigest(t *testing.T) {
	body := "stored body"
	digest := sha256.Sum256([]byte(body))
	require.NoError(t, validateStoredBusinessSystemPrompt(body, hex.EncodeToString(digest[:]), len(body)))
	require.ErrorIs(t, validateStoredBusinessSystemPrompt(body, strings.Repeat("0", 64), len(body)), service.ErrBusinessSystemPromptUnavailable)
}

func TestEnsureBusinessSystemPromptSeedDoesNotTouchExistingSlug(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO system_prompt_runtime")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO system_prompt_templates")).
		WithArgs("moxinggang_reverse_skill", "seed", "description", nil).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectCommit()

	store := NewBusinessSystemPromptRepository(db)
	err = store.EnsureBusinessSystemPromptSeed(context.Background(), service.BusinessSystemPromptSeed{
		Slug: "moxinggang_reverse_skill", Name: "seed", Description: "description", Body: "captured body",
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureBusinessSystemPromptSeedUpgradesKnownActiveSeedOnly(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	body := "new pinned recovery contract"
	digest := sha256.Sum256([]byte(body))
	bodySHA := hex.EncodeToString(digest[:])
	templateID, oldVersionID, newVersionID := int64(3), int64(8), int64(9)
	oldSHA := strings.Repeat("a", 64)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO system_prompt_runtime")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO system_prompt_templates")).
		WithArgs("codexrip_reverse_skill", "CodexRip", "", nil).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, is_seed, managed_source")).
		WithArgs("codexrip_reverse_skill").
		WillReturnRows(sqlmock.NewRows([]string{"id", "is_seed", "managed_source"}).AddRow(templateID, true, nil))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM system_prompt_template_versions")).
		WithArgs(templateID, bodySHA, len(body)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COALESCE(MAX(version), 0)")).
		WithArgs(templateID).
		WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(int64(1)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT r.active_template_id, r.active_version_id, v.sha256")).
		WillReturnRows(sqlmock.NewRows([]string{"active_template_id", "active_version_id", "sha256"}).AddRow(templateID, oldVersionID, oldSHA))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO system_prompt_template_versions")).
		WithArgs(templateID, int64(2), body, bodySHA, len(body), service.BusinessSystemPromptCompositionCodexSkillHybrid,
			service.BusinessSystemPromptRemoteSkillBundleID, nil, "pinned git recovery", nil, nil, nil, nil, nil, nil).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(newVersionID))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE system_prompt_runtime")).
		WithArgs(newVersionID, templateID, oldVersionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	store := NewBusinessSystemPromptRepository(db)
	err = store.EnsureBusinessSystemPromptSeed(context.Background(), service.BusinessSystemPromptSeed{
		Slug: "codexrip_reverse_skill", Name: "CodexRip", Body: body,
		Note: "pinned git recovery", CompositionMode: service.BusinessSystemPromptCompositionCodexSkillHybrid,
		BundleID:            service.BusinessSystemPromptRemoteSkillBundleID,
		UpgradeExistingSeed: true, AutoActivateFromSHA: []string{oldSHA},
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureBusinessSystemPromptSeedActivatesExistingCandidateFromKnownActiveSeed(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	body := "already installed recovery contract"
	digest := sha256.Sum256([]byte(body))
	bodySHA := hex.EncodeToString(digest[:])
	templateID, oldVersionID, candidateVersionID := int64(3), int64(8), int64(9)
	oldSHA := strings.Repeat("a", 64)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO system_prompt_runtime")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO system_prompt_templates")).
		WithArgs("codexrip_reverse_skill", "CodexRip", "", nil).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, is_seed, managed_source")).
		WithArgs("codexrip_reverse_skill").
		WillReturnRows(sqlmock.NewRows([]string{"id", "is_seed", "managed_source"}).AddRow(templateID, true, nil))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM system_prompt_template_versions")).
		WithArgs(templateID, bodySHA, len(body)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(candidateVersionID))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT r.active_template_id, r.active_version_id, v.sha256")).
		WillReturnRows(sqlmock.NewRows([]string{"active_template_id", "active_version_id", "sha256"}).AddRow(templateID, oldVersionID, oldSHA))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE system_prompt_runtime")).
		WithArgs(candidateVersionID, templateID, oldVersionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	store := NewBusinessSystemPromptRepository(db)
	err = store.EnsureBusinessSystemPromptSeed(context.Background(), service.BusinessSystemPromptSeed{
		Slug: "codexrip_reverse_skill", Name: "CodexRip", Body: body,
		CompositionMode:     service.BusinessSystemPromptCompositionCodexSkillHybrid,
		BundleID:            service.BusinessSystemPromptRemoteSkillBundleID,
		UpgradeExistingSeed: true, AutoActivateFromSHA: []string{oldSHA},
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureBusinessSystemPromptSeedActivatesExistingCandidateFromCodexripRelease5(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	body := "complete codexrip prompt candidate"
	digest := sha256.Sum256([]byte(body))
	bodySHA := hex.EncodeToString(digest[:])
	templateID, oldVersionID, candidateVersionID := int64(3), int64(16), int64(17)
	oldSHA := "5813c55c0763e1472becec874232f3daafb28a69107b94ca8284daf44fceb2a0"

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO system_prompt_runtime")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO system_prompt_templates")).
		WithArgs("codexrip_reverse_skill", "CodexRip", "", nil).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, is_seed, managed_source")).
		WithArgs("codexrip_reverse_skill").
		WillReturnRows(sqlmock.NewRows([]string{"id", "is_seed", "managed_source"}).AddRow(templateID, true, nil))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM system_prompt_template_versions")).
		WithArgs(templateID, bodySHA, len(body)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(candidateVersionID))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT r.active_template_id, r.active_version_id, v.sha256")).
		WillReturnRows(sqlmock.NewRows([]string{"active_template_id", "active_version_id", "sha256"}).AddRow(templateID, oldVersionID, oldSHA))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE system_prompt_runtime")).
		WithArgs(candidateVersionID, templateID, oldVersionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	store := NewBusinessSystemPromptRepository(db)
	err = store.EnsureBusinessSystemPromptSeed(context.Background(), service.BusinessSystemPromptSeed{
		Slug: "codexrip_reverse_skill", Name: "CodexRip", Body: body,
		CompositionMode:     service.BusinessSystemPromptCompositionCodexSkillHybrid,
		BundleID:            service.BusinessSystemPromptRemoteSkillBundleID,
		UpgradeExistingSeed: true, AutoActivateFromSHA: []string{oldSHA},
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureBusinessSystemPromptSeedUpgradeIsIdempotent(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	body := "already installed recovery contract"
	digest := sha256.Sum256([]byte(body))
	bodySHA := hex.EncodeToString(digest[:])
	templateID, versionID := int64(3), int64(9)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO system_prompt_runtime")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO system_prompt_templates")).
		WithArgs("codexrip_reverse_skill", "CodexRip", "", nil).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, is_seed, managed_source")).
		WithArgs("codexrip_reverse_skill").
		WillReturnRows(sqlmock.NewRows([]string{"id", "is_seed", "managed_source"}).AddRow(templateID, true, nil))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM system_prompt_template_versions")).
		WithArgs(templateID, bodySHA, len(body)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(versionID))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT r.active_template_id, r.active_version_id, v.sha256")).
		WillReturnRows(sqlmock.NewRows([]string{"active_template_id", "active_version_id", "sha256"}).AddRow(templateID, versionID, bodySHA))
	mock.ExpectCommit()

	store := NewBusinessSystemPromptRepository(db)
	err = store.EnsureBusinessSystemPromptSeed(context.Background(), service.BusinessSystemPromptSeed{
		Slug: "codexrip_reverse_skill", Name: "CodexRip", Body: body,
		CompositionMode:     service.BusinessSystemPromptCompositionCodexSkillHybrid,
		BundleID:            service.BusinessSystemPromptRemoteSkillBundleID,
		UpgradeExistingSeed: true, AutoActivateFromSHA: []string{strings.Repeat("a", 64)},
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureBusinessSystemPromptSeedDoesNotReplaceCustomActiveVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	body := "new pinned recovery contract"
	digest := sha256.Sum256([]byte(body))
	bodySHA := hex.EncodeToString(digest[:])
	templateID, customVersionID, candidateVersionID := int64(3), int64(11), int64(12)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO system_prompt_runtime")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO system_prompt_templates")).
		WithArgs("codexrip_reverse_skill", "CodexRip", "", nil).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, is_seed, managed_source")).
		WithArgs("codexrip_reverse_skill").
		WillReturnRows(sqlmock.NewRows([]string{"id", "is_seed", "managed_source"}).AddRow(templateID, true, nil))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM system_prompt_template_versions")).
		WithArgs(templateID, bodySHA, len(body)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COALESCE(MAX(version), 0)")).
		WithArgs(templateID).WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(int64(3)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT r.active_template_id, r.active_version_id, v.sha256")).
		WillReturnRows(sqlmock.NewRows([]string{"active_template_id", "active_version_id", "sha256"}).AddRow(templateID, customVersionID, strings.Repeat("f", 64)))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO system_prompt_template_versions")).
		WithArgs(templateID, int64(4), body, bodySHA, len(body), service.BusinessSystemPromptCompositionCodexSkillHybrid,
			service.BusinessSystemPromptRemoteSkillBundleID, nil, "pinned git recovery", nil, nil, nil, nil, nil, nil).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(candidateVersionID))
	mock.ExpectCommit()

	store := NewBusinessSystemPromptRepository(db)
	err = store.EnsureBusinessSystemPromptSeed(context.Background(), service.BusinessSystemPromptSeed{
		Slug: "codexrip_reverse_skill", Name: "CodexRip", Body: body,
		Note: "pinned git recovery", CompositionMode: service.BusinessSystemPromptCompositionCodexSkillHybrid,
		BundleID:            service.BusinessSystemPromptRemoteSkillBundleID,
		UpgradeExistingSeed: true, AutoActivateFromSHA: []string{strings.Repeat("a", 64)},
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureBusinessSystemPromptSeedPersistsManagedSourceProvenanceWithoutReplacingRuntime(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	body := "managed prompt"
	digest := sha256.Sum256([]byte(body))
	bodySHA := hex.EncodeToString(digest[:])
	templateID := int64(12)
	versionID := int64(34)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO system_prompt_runtime")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO system_prompt_templates")).
		WithArgs("gpt_5_6_instruct", "GPT-5.6 Instruct v45", "optional", service.BusinessSystemPromptManagedSourceGPT56).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(templateID))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO system_prompt_template_versions")).
		WithArgs(
			templateID, body, bodySHA, len(body), service.BusinessSystemPromptCompositionInline,
			nil, nil, "Imported from MDX-Tom/gpt-5.6-instruct v45",
			"MDX-Tom/gpt-5.6-instruct", "77e7a649903f9556f2d7bfa0223fa99e123aad52", "v45",
			"gpt-5.6-sol-unrestricted-v45.zip", "c86c2c6d20a4d1155d87422f485eb37b77539132270918c002b5d8237a5adf54",
			service.GPT56PromptLicenseSHA256,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(versionID))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE system_prompt_runtime")).
		WithArgs(templateID, versionID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	store := NewBusinessSystemPromptRepository(db)
	err = store.EnsureBusinessSystemPromptSeed(context.Background(), service.BusinessSystemPromptSeed{
		Slug: "gpt_5_6_instruct", Name: "GPT-5.6 Instruct v45", Description: "optional",
		ManagedSource: service.BusinessSystemPromptManagedSourceGPT56,
		Body:          body, SHA256: bodySHA, ByteLength: len(body), CompositionMode: service.BusinessSystemPromptCompositionInline,
		Note:             "Imported from MDX-Tom/gpt-5.6-instruct v45",
		SourceRepository: "MDX-Tom/gpt-5.6-instruct", SourceCommit: "77e7a649903f9556f2d7bfa0223fa99e123aad52",
		SourceVersion: "v45", SourceArtifact: "gpt-5.6-sol-unrestricted-v45.zip",
		SourceArtifactSHA256: "c86c2c6d20a4d1155d87422f485eb37b77539132270918c002b5d8237a5adf54",
		SourceLicenseSHA256:  service.GPT56PromptLicenseSHA256,
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureBusinessSystemPromptSeedRejectsMismatchedByteLength(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := NewBusinessSystemPromptRepository(db)
	err = store.EnsureBusinessSystemPromptSeed(context.Background(), service.BusinessSystemPromptSeed{
		Slug: "moxinggang_reverse_skill", Name: "seed", Body: "captured body", ByteLength: 99,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "seed byte length mismatch")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureBusinessSystemPromptSeedRejectsPartialManagedSourceProvenance(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	store := NewBusinessSystemPromptRepository(db)
	err = store.EnsureBusinessSystemPromptSeed(context.Background(), service.BusinessSystemPromptSeed{
		Slug: "gpt_5_6_instruct", Name: "GPT-5.6", Body: "prompt",
		ManagedSource: service.BusinessSystemPromptManagedSourceGPT56,
	})
	require.ErrorIs(t, err, service.ErrBusinessSystemPromptSourceInvalid)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBusinessSystemPromptRepositoryReturnsManagedSourceProvenance(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Date(2026, 8, 8, 1, 2, 3, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, slug, name, description, is_seed, managed_source, deleted_at")).
		WithArgs(int64(12)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "slug", "name", "description", "is_seed", "managed_source", "deleted_at",
			"created_by", "updated_by", "created_at", "updated_at",
		}).AddRow(int64(12), "gpt_5_6_instruct", "GPT-5.6", "optional", true,
			service.BusinessSystemPromptManagedSourceGPT56, nil, nil, nil, now, now))

	template, err := queryBusinessSystemPromptTemplate(context.Background(), db, 12)
	require.NoError(t, err)
	require.Equal(t, service.BusinessSystemPromptManagedSourceGPT56, template.ManagedSource)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, template_id, version, body, sha256, byte_length")).
		WithArgs(int64(12)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "template_id", "version", "body", "sha256", "byte_length",
			"composition_mode", "bundle_id", "bundle_manifest_sha256", "note",
			"source_repository", "source_commit", "source_version", "source_artifact",
			"source_artifact_sha256", "source_license_sha256",
			"created_by", "published_at", "published_by", "created_at",
		}).AddRow(int64(34), int64(12), int64(1), "body", strings.Repeat("a", 64), 4,
			service.BusinessSystemPromptCompositionInline, nil, nil, "imported",
			"MDX-Tom/gpt-5.6-instruct", strings.Repeat("b", 40), "v45", "gpt-5.6-sol-unrestricted-v45.zip",
			strings.Repeat("c", 64), service.GPT56PromptLicenseSHA256,
			nil, nil, nil, now))

	versions, err := queryBusinessSystemPromptVersions(context.Background(), db, 12)
	require.NoError(t, err)
	require.Len(t, versions, 1)
	require.Equal(t, "MDX-Tom/gpt-5.6-instruct", versions[0].SourceRepository)
	require.Equal(t, strings.Repeat("b", 40), versions[0].SourceCommit)
	require.Equal(t, "v45", versions[0].SourceVersion)
	require.Equal(t, strings.Repeat("c", 64), versions[0].SourceArtifactSHA256)
	require.Equal(t, service.GPT56PromptLicenseSHA256, versions[0].SourceLicenseSHA256)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSyncBusinessSystemPromptSourceVersionCreatesInactiveCandidate(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	body := "new upstream prompt"
	digest := sha256.Sum256([]byte(body))
	bodySHA := hex.EncodeToString(digest[:])
	candidate := service.BusinessSystemPromptSourceCandidate{
		ManagedSource:    service.BusinessSystemPromptManagedSourceGPT56,
		SourceRepository: "MDX-Tom/gpt-5.6-instruct", SourceCommit: strings.Repeat("d", 40),
		SourceVersion: "v46", SourceArtifact: "gpt-5.6-sol-unrestricted-v46.zip",
		SourceArtifactSHA256: strings.Repeat("e", 64), SourceLicenseSHA256: service.GPT56PromptLicenseSHA256,
		Body: body, SHA256: bodySHA, ByteLength: len(body),
	}
	now := time.Date(2026, 8, 8, 2, 3, 4, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT revision FROM system_prompt_runtime WHERE id = 1 FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(int64(4)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT managed_source FROM system_prompt_templates")).
		WithArgs(int64(12)).
		WillReturnRows(sqlmock.NewRows([]string{"managed_source"}).AddRow(service.BusinessSystemPromptManagedSourceGPT56))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT version, sha256, source_repository, source_commit, source_artifact_sha256")).
		WithArgs(int64(12)).
		WillReturnRows(sqlmock.NewRows([]string{
			"version", "sha256", "source_repository", "source_commit", "source_artifact_sha256",
		}).AddRow(int64(1), strings.Repeat("a", 64), "MDX-Tom/gpt-5.6-instruct", strings.Repeat("b", 40), strings.Repeat("c", 64)))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO system_prompt_template_versions")).
		WithArgs(
			int64(12), int64(2), body, bodySHA, len(body),
			"Synced from MDX-Tom/gpt-5.6-instruct v46", int64(9),
			candidate.SourceRepository, candidate.SourceCommit, candidate.SourceVersion,
			candidate.SourceArtifact, candidate.SourceArtifactSHA256, candidate.SourceLicenseSHA256,
		).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "template_id", "version", "body", "sha256", "byte_length",
			"composition_mode", "bundle_id", "bundle_manifest_sha256", "note",
			"source_repository", "source_commit", "source_version", "source_artifact",
			"source_artifact_sha256", "source_license_sha256",
			"created_by", "published_at", "published_by", "created_at",
		}).AddRow(int64(35), int64(12), int64(2), body, bodySHA, len(body),
			service.BusinessSystemPromptCompositionInline, nil, nil, "Synced from MDX-Tom/gpt-5.6-instruct v46",
			candidate.SourceRepository, candidate.SourceCommit, candidate.SourceVersion, candidate.SourceArtifact,
			candidate.SourceArtifactSHA256, candidate.SourceLicenseSHA256,
			int64(9), nil, nil, now))
	mock.ExpectCommit()

	store, ok := NewBusinessSystemPromptRepository(db).(service.BusinessSystemPromptSourceStore)
	require.True(t, ok)
	result, err := store.SyncBusinessSystemPromptSourceVersion(context.Background(), 12, candidate, 9, 1, 4)
	require.NoError(t, err)
	require.Equal(t, service.BusinessSystemPromptSourceSyncCandidateCreated, result.Status)
	require.NotNil(t, result.Version)
	require.Equal(t, int64(2), result.Version.Version)
	require.False(t, result.Version.IsActive)
	require.Equal(t, candidate.SourceCommit, result.Version.SourceCommit)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSyncBusinessSystemPromptSourceVersionReturnsUpToDateWithoutInsert(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	body := "same prompt"
	digest := sha256.Sum256([]byte(body))
	candidate := service.BusinessSystemPromptSourceCandidate{
		ManagedSource:    service.BusinessSystemPromptManagedSourceGPT56,
		SourceRepository: "MDX-Tom/gpt-5.6-instruct", SourceCommit: strings.Repeat("f", 40),
		SourceVersion: "v45", SourceArtifact: "gpt-5.6-sol-unrestricted-v45.zip",
		SourceArtifactSHA256: strings.Repeat("e", 64), SourceLicenseSHA256: service.GPT56PromptLicenseSHA256,
		Body: body, SHA256: hex.EncodeToString(digest[:]), ByteLength: len(body),
	}
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT revision FROM system_prompt_runtime WHERE id = 1 FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(int64(4)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT managed_source FROM system_prompt_templates")).
		WithArgs(int64(12)).
		WillReturnRows(sqlmock.NewRows([]string{"managed_source"}).AddRow(service.BusinessSystemPromptManagedSourceGPT56))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT version, sha256, source_repository, source_commit, source_artifact_sha256")).
		WithArgs(int64(12)).
		WillReturnRows(sqlmock.NewRows([]string{"version", "sha256", "source_repository", "source_commit", "source_artifact_sha256"}).
			AddRow(int64(1), candidate.SHA256, candidate.SourceRepository, candidate.SourceCommit, candidate.SourceArtifactSHA256))
	mock.ExpectCommit()

	store, ok := NewBusinessSystemPromptRepository(db).(service.BusinessSystemPromptSourceStore)
	require.True(t, ok)
	result, err := store.SyncBusinessSystemPromptSourceVersion(context.Background(), 12, candidate, 9, 1, 4)
	require.NoError(t, err)
	require.Equal(t, service.BusinessSystemPromptSourceSyncUpToDate, result.Status)
	require.Nil(t, result.Version)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSyncBusinessSystemPromptSourceVersionReturnsNoPromptChangeWithoutInsert(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	body := "unchanged prompt body"
	digest := sha256.Sum256([]byte(body))
	candidate := service.BusinessSystemPromptSourceCandidate{
		ManagedSource:    service.BusinessSystemPromptManagedSourceGPT56,
		SourceRepository: "MDX-Tom/gpt-5.6-instruct", SourceCommit: strings.Repeat("f", 40),
		SourceVersion: "v46", SourceArtifact: "gpt-5.6-sol-unrestricted-v46.zip",
		SourceArtifactSHA256: strings.Repeat("e", 64), SourceLicenseSHA256: service.GPT56PromptLicenseSHA256,
		Body: body, SHA256: hex.EncodeToString(digest[:]), ByteLength: len(body),
	}
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT revision FROM system_prompt_runtime WHERE id = 1 FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(int64(4)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT managed_source FROM system_prompt_templates")).
		WithArgs(int64(12)).
		WillReturnRows(sqlmock.NewRows([]string{"managed_source"}).AddRow(service.BusinessSystemPromptManagedSourceGPT56))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT version, sha256, source_repository, source_commit, source_artifact_sha256")).
		WithArgs(int64(12)).
		WillReturnRows(sqlmock.NewRows([]string{"version", "sha256", "source_repository", "source_commit", "source_artifact_sha256"}).
			AddRow(int64(1), candidate.SHA256, candidate.SourceRepository, strings.Repeat("a", 40), strings.Repeat("b", 64)))
	mock.ExpectCommit()

	store, ok := NewBusinessSystemPromptRepository(db).(service.BusinessSystemPromptSourceStore)
	require.True(t, ok)
	result, err := store.SyncBusinessSystemPromptSourceVersion(context.Background(), 12, candidate, 9, 1, 4)
	require.NoError(t, err)
	require.Equal(t, service.BusinessSystemPromptSourceSyncNoPromptChange, result.Status)
	require.Nil(t, result.Version)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSyncBusinessSystemPromptSourceVersionRejectsStaleExpectedLatestVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	body := "new prompt"
	digest := sha256.Sum256([]byte(body))
	candidate := service.BusinessSystemPromptSourceCandidate{
		ManagedSource:    service.BusinessSystemPromptManagedSourceGPT56,
		SourceRepository: "MDX-Tom/gpt-5.6-instruct", SourceCommit: strings.Repeat("d", 40),
		SourceVersion: "v46", SourceArtifact: "gpt-5.6-sol-unrestricted-v46.zip",
		SourceArtifactSHA256: strings.Repeat("e", 64), SourceLicenseSHA256: service.GPT56PromptLicenseSHA256,
		Body: body, SHA256: hex.EncodeToString(digest[:]), ByteLength: len(body),
	}
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT revision FROM system_prompt_runtime WHERE id = 1 FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(int64(4)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT managed_source FROM system_prompt_templates")).
		WithArgs(int64(12)).
		WillReturnRows(sqlmock.NewRows([]string{"managed_source"}).AddRow(service.BusinessSystemPromptManagedSourceGPT56))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT version, sha256, source_repository, source_commit, source_artifact_sha256")).
		WithArgs(int64(12)).
		WillReturnRows(sqlmock.NewRows([]string{"version", "sha256", "source_repository", "source_commit", "source_artifact_sha256"}).
			AddRow(int64(2), strings.Repeat("a", 64), "MDX-Tom/gpt-5.6-instruct", strings.Repeat("b", 40), strings.Repeat("c", 64)))
	mock.ExpectRollback()

	store, ok := NewBusinessSystemPromptRepository(db).(service.BusinessSystemPromptSourceStore)
	require.True(t, ok)
	_, err = store.SyncBusinessSystemPromptSourceVersion(context.Background(), 12, candidate, 9, 1, 4)
	require.ErrorIs(t, err, service.ErrBusinessSystemPromptRevisionConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLockBusinessSystemPromptRuntimeRevisionRejectsStaleExpectedRevision(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT revision FROM system_prompt_runtime WHERE id = 1 FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(int64(7)))
	mock.ExpectRollback()

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = lockBusinessSystemPromptRuntimeRevision(context.Background(), tx, 6)
	require.ErrorIs(t, err, service.ErrBusinessSystemPromptRevisionConflict)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}
