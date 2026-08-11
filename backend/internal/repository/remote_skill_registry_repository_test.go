package repository

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestEnsureRemoteSkillSeedRegistersCandidateWithoutReplacingActiveVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	seed := service.RemoteSkillBundleVersion{
		BundleID:       service.BusinessSystemPromptRemoteSkillBundleID,
		SourceID:       service.RemoteSkillSourceGitHubOfficial,
		RemoteRoot:     "https://raw.githubusercontent.com/example/reverse-skill/1111111111111111111111111111111111111111/skills",
		SourceCommit:   strings.Repeat("1", 40),
		OverlaySHA256:  strings.Repeat("2", 64),
		ManifestSHA256: strings.Repeat("3", 64),
		ArchiveSHA256:  strings.Repeat("4", 64),
		FileCount:      545,
		TotalBytes:     7925493,
	}

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT active_bundle_version_id FROM system_prompt_skill_runtime WHERE id = 1 FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows([]string{"active_bundle_version_id"}).AddRow(int64(6)))
	mock.ExpectQuery("INSERT INTO system_prompt_skill_bundle_versions.*ON CONFLICT \\(source_id, manifest_sha256\\) DO NOTHING").
		WithArgs(
			seed.BundleID, seed.SourceID, seed.RemoteRoot, seed.SourceCommit,
			seed.OverlaySHA256, seed.ManifestSHA256, seed.ArchiveSHA256,
			seed.FileCount, seed.TotalBytes, 0, 0, 0, 0, 0, nil,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))
	mock.ExpectCommit()

	store := NewRemoteSkillRegistryRepository(db)
	err = store.EnsureRemoteSkillSeed(context.Background(), seed)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRemoteSkillRegistryRepositoryCreatesSourceBoundSyncJob(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT revision FROM system_prompt_skill_runtime WHERE id = 1 FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(int64(7)))
	mock.ExpectQuery("INSERT INTO system_prompt_skill_sync_jobs \\(source_id, status, progress_stage, created_by\\)").
		WithArgs(service.RemoteSkillSourceMoxinggang, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "source_id", "status", "progress_stage", "created_by", "created_at"}).
			AddRow(int64(9), service.RemoteSkillSourceMoxinggang, service.RemoteSkillSyncStatusQueued, "queued", int64(42), time.Now()))
	mock.ExpectCommit()

	store := NewRemoteSkillRegistryRepository(db)
	job, err := store.CreateRemoteSkillSyncJob(context.Background(), service.RemoteSkillSourceMoxinggang, 42, 7)
	require.NoError(t, err)
	require.Equal(t, service.RemoteSkillSourceMoxinggang, job.SourceID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRemoteSkillRegistryRepositoryLoadsPublishedSourceMetadata(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	updatedAt := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT revision, active_bundle_version_id, updated_at")).
		WillReturnRows(sqlmock.NewRows([]string{"revision", "active_bundle_version_id", "updated_at"}).AddRow(int64(4), int64(2), updatedAt))
	mock.ExpectQuery("SELECT v.id, v.bundle_id, v.source_id, v.remote_root").
		WithArgs(int64(2)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "bundle_id", "source_id", "remote_root", "source_commit", "overlay_sha256", "manifest_sha256", "archive_sha256",
			"file_count", "total_bytes", "added_files", "modified_files", "deleted_files", "script_changes", "binary_changes",
			"created_by", "published_at", "published_by", "created_at",
		}).AddRow(
			int64(2), service.BusinessSystemPromptRemoteSkillBundleID, service.RemoteSkillSourceMoxinggang, service.RemoteSkillMoxinggangRoot,
			"1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222222222222222222222222222",
			"3333333333333333333333333333333333333333333333333333333333333333", "4444444444444444444444444444444444444444444444444444444444444444",
			6, int64(1200), 1, 2, 0, 1, 0, int64(42), updatedAt, int64(42), updatedAt,
		))

	store := NewRemoteSkillRegistryRepository(db)
	snapshot, err := store.LoadRemoteSkillSnapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, service.RemoteSkillSourceMoxinggang, snapshot.SourceID)
	require.Equal(t, service.RemoteSkillMoxinggangRoot, snapshot.RemoteRoot)
	require.NotNil(t, snapshot.Active)
	require.Equal(t, service.RemoteSkillSourceMoxinggang, snapshot.Active.SourceID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRemoteSkillRegistryRepositoryReusesManifestOnlyWithinSameSource(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	createdAt := time.Now().UTC()
	version := service.RemoteSkillBundleVersion{
		ID: 17, BundleID: service.BusinessSystemPromptRemoteSkillBundleID,
		SourceID: service.RemoteSkillSourceMoxinggang, RemoteRoot: service.RemoteSkillMoxinggangRoot,
		SourceCommit: strings.Repeat("1", 40), OverlaySHA256: strings.Repeat("2", 64),
		ManifestSHA256: strings.Repeat("3", 64), ArchiveSHA256: strings.Repeat("4", 64),
		FileCount: 6, TotalBytes: 1200, CreatedBy: 42, CreatedAt: createdAt,
	}

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO system_prompt_skill_bundle_versions.*ON CONFLICT \\(source_id, manifest_sha256\\) DO NOTHING").
		WithArgs(
			version.BundleID, version.SourceID, version.RemoteRoot, version.SourceCommit,
			version.OverlaySHA256, version.ManifestSHA256, version.ArchiveSHA256,
			version.FileCount, version.TotalBytes, 0, 0, 0, 0, 0, int64(42),
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("WHERE v.source_id = \\$1 AND v.manifest_sha256 = \\$2").
		WithArgs(version.SourceID, version.ManifestSHA256).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "bundle_id", "source_id", "remote_root", "source_commit", "overlay_sha256", "manifest_sha256", "archive_sha256",
			"file_count", "total_bytes", "added_files", "modified_files", "deleted_files", "script_changes", "binary_changes",
			"created_by", "published_at", "published_by", "created_at",
		}).AddRow(
			version.ID, version.BundleID, version.SourceID, version.RemoteRoot, version.SourceCommit,
			version.OverlaySHA256, version.ManifestSHA256, version.ArchiveSHA256,
			version.FileCount, version.TotalBytes, 0, 0, 0, 0, 0,
			version.CreatedBy, nil, nil, version.CreatedAt,
		))
	mock.ExpectRollback()

	tx, err := db.Begin()
	require.NoError(t, err)
	id, err := insertOrValidateRemoteSkillVersion(context.Background(), tx, version)
	require.NoError(t, err)
	require.Equal(t, version.ID, id)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRemoteSkillRegistryRepositoryInsertsSameManifestForAnotherSource(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	version := service.RemoteSkillBundleVersion{
		BundleID: service.BusinessSystemPromptRemoteSkillBundleID,
		SourceID: service.RemoteSkillSourceMoxinggang, RemoteRoot: service.RemoteSkillMoxinggangRoot,
		SourceCommit: strings.Repeat("1", 40), OverlaySHA256: strings.Repeat("2", 64),
		ManifestSHA256: strings.Repeat("3", 64), ArchiveSHA256: strings.Repeat("4", 64),
		FileCount: 6, TotalBytes: 1200, CreatedBy: 42,
	}

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO system_prompt_skill_bundle_versions.*ON CONFLICT \\(source_id, manifest_sha256\\) DO NOTHING").
		WithArgs(
			version.BundleID, version.SourceID, version.RemoteRoot, version.SourceCommit,
			version.OverlaySHA256, version.ManifestSHA256, version.ArchiveSHA256,
			version.FileCount, version.TotalBytes, 0, 0, 0, 0, 0, int64(42),
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(18)))
	mock.ExpectRollback()

	tx, err := db.Begin()
	require.NoError(t, err)
	id, err := insertOrValidateRemoteSkillVersion(context.Background(), tx, version)
	require.NoError(t, err)
	require.Equal(t, int64(18), id)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}
