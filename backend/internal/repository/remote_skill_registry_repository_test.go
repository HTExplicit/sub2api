package repository

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRemoteSkillRegistryRepositoryCreatesFixedSourceSyncJob(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	createdAt := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT revision FROM system_prompt_skill_runtime WHERE id = 1 FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(int64(7)))
	mock.ExpectQuery("INSERT INTO system_prompt_skill_sync_jobs").
		WithArgs(int64(42), true).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "status", "progress_stage", "prompt_capture_provided", "created_by", "created_at",
		}).AddRow(int64(9), service.RemoteSkillSyncStatusQueued, "queued", true, int64(42), createdAt))
	mock.ExpectCommit()

	store := NewRemoteSkillRegistryRepository(db)
	job, err := store.CreateRemoteSkillSyncJob(context.Background(), 42, 7, true)
	require.NoError(t, err)
	require.Equal(t, int64(9), job.ID)
	require.True(t, job.PromptCaptureProvided)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRemoteSkillRegistryRepositoryLoadsPairedSnapshot(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	updatedAt := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT revision, active_bundle_version_id, active_prompt_version_id, updated_at")).
		WillReturnRows(sqlmock.NewRows([]string{
			"revision", "active_bundle_version_id", "active_prompt_version_id", "updated_at",
		}).AddRow(int64(4), int64(2), int64(3), updatedAt))
	mock.ExpectQuery("SELECT v.id, v.upstream_source_id, v.upstream_root, v.public_root").
		WithArgs(int64(2), int64(3)).
		WillReturnRows(remoteSkillDetailRows(updatedAt, []byte(`[{"path":"SKILL.md","change":"modified","kind":"text"}]`)))

	store := NewRemoteSkillRegistryRepository(db)
	snapshot, err := store.LoadRemoteSkillSnapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(4), snapshot.Revision)
	require.NotNil(t, snapshot.Active)
	require.NotNil(t, snapshot.ActivePrompt)
	require.Equal(t, int64(3), snapshot.ActivePrompt.ID)
	require.Equal(t, service.RemoteSkillUpstreamSourceID, snapshot.Active.UpstreamSourceID)
	require.Equal(t, service.RemoteSkillPublicRoot, snapshot.Active.PublicRoot)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRemoteSkillRegistryRepositoryPublishesCandidateAndPromptTogether(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	updatedAt := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT revision FROM system_prompt_skill_runtime WHERE id = 1 FOR UPDATE")).
		WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(int64(7)))
	mock.ExpectQuery("SELECT prompt_version_id FROM system_prompt_skill_bundle_versions WHERE id = \\$1").
		WithArgs(int64(2), service.RemoteSkillUpstreamSourceID, service.RemoteSkillUpstreamRoot, service.RemoteSkillPublicRoot).
		WillReturnRows(sqlmock.NewRows([]string{"prompt_version_id"}).AddRow(int64(3)))
	mock.ExpectExec("UPDATE system_prompt_skill_bundle_versions").
		WithArgs(int64(2), int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE system_prompt_skill_runtime").
		WithArgs(int64(2), int64(3), int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT revision, active_bundle_version_id, active_prompt_version_id, updated_at")).
		WillReturnRows(sqlmock.NewRows([]string{
			"revision", "active_bundle_version_id", "active_prompt_version_id", "updated_at",
		}).AddRow(int64(8), int64(2), int64(3), updatedAt))
	mock.ExpectQuery("SELECT v.id, v.upstream_source_id, v.upstream_root, v.public_root").
		WithArgs(int64(2), int64(3)).
		WillReturnRows(remoteSkillDetailRows(updatedAt, []byte(`[]`)))
	mock.ExpectCommit()

	store := NewRemoteSkillRegistryRepository(db)
	snapshot, err := store.PublishRemoteSkillVersion(context.Background(), 2, 7, 42)
	require.NoError(t, err)
	require.Equal(t, int64(8), snapshot.Revision)
	require.Equal(t, int64(2), snapshot.Active.ID)
	require.Equal(t, int64(3), snapshot.ActivePrompt.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func remoteSkillDetailRows(createdAt time.Time, fileChanges []byte) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "upstream_source_id", "upstream_root", "public_root", "raw_tree_sha256", "effective_tree_sha256",
		"prompt_version_id", "file_count", "raw_total_bytes", "effective_total_bytes", "added_files", "modified_files",
		"deleted_files", "script_changes", "binary_changes", "fetched_at", "created_by", "published_at", "published_by", "created_at",
		"file_changes", "prompt_id", "raw_sha256", "effective_sha256", "diff", "raw_body", "effective_body", "prompt_created_by", "prompt_created_at",
	}).AddRow(
		int64(2), service.RemoteSkillUpstreamSourceID, service.RemoteSkillUpstreamRoot, service.RemoteSkillPublicRoot,
		strings.Repeat("1", 64), strings.Repeat("2", 64), int64(3), 73, int64(100), int64(100), 0, 0, 0, 0, 0,
		createdAt, int64(42), createdAt, int64(42), createdAt, fileChanges, int64(3), strings.Repeat("3", 64), strings.Repeat("4", 64), "diff", "raw", "effective", int64(42), createdAt,
	)
}

func TestRemoteSkillDetailFileChangesAreJSON(t *testing.T) {
	var changes []service.RemoteSkillFileChange
	require.NoError(t, json.Unmarshal([]byte(`[{"path":"SKILL.md","change":"modified","kind":"text"}]`), &changes))
	require.Len(t, changes, 1)
}
