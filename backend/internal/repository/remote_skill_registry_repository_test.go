package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

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
