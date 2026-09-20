package repository

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Uses only a connection-local temporary table in the explicitly supplied test
// database. The shared fixture and application tables are not reset or modified.
func TestRemoteSkillJobExpiryPreservesActiveOwnersAndRejectsLateCompletion(t *testing.T) {
	dsn := os.Getenv("SUB2API_TEST_POSTGRES_ONLY_DSN")
	if dsn == "" {
		t.Skip("requires an explicitly configured local PostgreSQL fixture")
	}
	db, err := OpenPostgresDB(dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	_, err = db.ExecContext(ctx, `CREATE TEMP TABLE system_prompt_skill_sync_jobs (
		id BIGINT PRIMARY KEY, status TEXT NOT NULL, progress_stage TEXT NOT NULL,
		candidate_bundle_version_id BIGINT, prompt_capture_provided BOOLEAN NOT NULL DEFAULT FALSE,
		error_code TEXT, created_by BIGINT, created_at TIMESTAMPTZ NOT NULL,
		started_at TIMESTAMPTZ, completed_at TIMESTAMPTZ)`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO system_prompt_skill_sync_jobs(id,status,progress_stage,created_at) VALUES
		(1,'running','fetching_source',clock_timestamp() - INTERVAL '11 minutes'),
		(2,'queued','queued',clock_timestamp() - INTERVAL '11 minutes'),
		(3,'running','fetching_source',clock_timestamp() - INTERVAL '1 minute'),
		(4,'succeeded','candidate_ready',clock_timestamp() - INTERVAL '11 minutes')`)
	require.NoError(t, err)
	repo := NewRemoteSkillRegistryRepository(db)
	require.NoError(t, repo.ExpireRemoteSkillSyncJobs(ctx))
	for _, id := range []int64{1, 2} {
		job, err := repo.GetRemoteSkillSyncJob(ctx, id)
		require.NoError(t, err)
		require.Equal(t, "failed", job.Status)
		require.Equal(t, "sync_expired", job.ErrorCode)
		require.NotNil(t, job.CompletedAt)
	}
	active, err := repo.GetRemoteSkillSyncJob(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, "running", active.Status)
	require.Nil(t, active.CompletedAt)
	finished, err := repo.GetRemoteSkillSyncJob(ctx, 4)
	require.NoError(t, err)
	require.Equal(t, "succeeded", finished.Status)
	require.NoError(t, repo.UpdateRemoteSkillSyncJobStage(ctx, 3, "verifying_candidate"))
	_, err = db.ExecContext(ctx, `UPDATE system_prompt_skill_sync_jobs
		SET created_at = clock_timestamp() - INTERVAL '11 minutes' WHERE id = 3`)
	require.NoError(t, err)
	require.Error(t, repo.UpdateRemoteSkillSyncJobStage(ctx, 3, "verifying_candidate"))
	textHash := sha256String("fixture")
	candidate := service.RemoteSkillCandidate{
		Version: service.RemoteSkillBundleVersion{UpstreamSourceID: service.RemoteSkillUpstreamSourceID,
			UpstreamRoot: service.RemoteSkillUpstreamRoot, PublicRoot: service.RemoteSkillPublicRoot,
			RawTreeSHA256: strings.Repeat("a", 64), EffectiveTreeSHA256: strings.Repeat("b", 64),
			FileCount: 1, RawTotalBytes: 7, EffectiveTotalBytes: 7, FetchedAt: time.Now()},
		Prompt: service.RemoteSkillPromptVersion{RawBody: "fixture", EffectiveBody: "fixture",
			RawSHA256: textHash, EffectiveSHA256: textHash},
	}
	_, err = repo.CompleteRemoteSkillSyncJob(ctx, 3, candidate)
	require.ErrorIs(t, err, service.ErrRemoteSkillSyncNotFound,
		"an expired worker must be rejected before inserting a candidate")
	active, err = repo.GetRemoteSkillSyncJob(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, "failed", active.Status)
	require.Zero(t, active.CandidateBundleVersionID)
}
