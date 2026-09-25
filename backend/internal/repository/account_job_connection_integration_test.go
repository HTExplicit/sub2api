//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAccountJobConnectionSnapshotAndStoppedRetry(t *testing.T) {
	ctx := context.Background()
	user := mustCreateUser(t, testEntClient(t), &service.User{Email: "job-connection-" + uuid.NewString() + "@example.com", PasswordHash: "fixture"})
	repo := &accountJobRepository{db: integrationDB}
	var ids []int64
	t.Cleanup(func() {
		for _, id := range ids {
			_, _ = integrationDB.ExecContext(ctx, "DELETE FROM admin_account_jobs WHERE id=$1", id)
		}
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id=$1", user.ID)
	})
	create := func(count int) *service.AccountJob {
		seeds := make([]service.AccountJobItemSeed, count)
		for i := range seeds {
			seeds[i].Ordinal = i + 1
		}
		job, _, err := repo.Create(ctx, service.CreateAccountJobParams{CreatedBy: user.ID, Kind: service.AccountJobKindBatchTest, IdempotencyKey: uuid.NewString(), RequestHash: strings.Repeat("a", 64), PayloadCipher: "encrypted-fixture", PayloadExpires: time.Now().Add(time.Hour), Items: seeds, Attempt: 1})
		require.NoError(t, err)
		ids = append(ids, job.ID)
		_, err = integrationDB.ExecContext(ctx, "UPDATE admin_account_jobs SET status='running' WHERE id=$1", job.ID)
		require.NoError(t, err)
		return job
	}
	job := create(3)
	items, err := repo.ReservePendingItems(ctx, job.ID, 100)
	require.NoError(t, err)
	require.Len(t, items, 3)
	plan := json.RawMessage(`{"execution_plan":{"model_id":"frozen","mapped_model_id":"wire","reasoning_effort":"high","plan_stamp":"stamp"},"model_id":"wire"}`)
	require.NoError(t, repo.SaveExecutionSnapshot(ctx, job.ID, items[0].ID, plan))
	require.NoError(t, repo.SaveExecutionSnapshot(ctx, job.ID, items[0].ID, plan), "the same plan is idempotent")
	require.Error(t, repo.SaveExecutionSnapshot(ctx, job.ID, items[0].ID, json.RawMessage(`{"execution_plan":{"model_id":"changed"}}`)))
	require.NoError(t, repo.CompleteItems(ctx, job.ID, []service.AccountJobExecutionResult{
		{ItemID: items[0].ID, Status: service.AccountJobItemStatusFailed, ErrorCode: "test_timeout", Metadata: json.RawMessage(`{"latency_ms":90}`)},
		{ItemID: items[1].ID, Status: service.AccountJobItemStatusSucceeded, Metadata: json.RawMessage(`{"recovery_status":"warning"}`)},
	}))
	_, err = repo.Cancel(ctx, job.ID, user.ID)
	require.NoError(t, err)
	finished, err := repo.Finish(ctx, job.ID, "", "")
	require.NoError(t, err)
	require.Equal(t, service.AccountJobStatusCanceled, finished.Status)
	_, seeds, _, _, err := repo.FailedItemSeeds(ctx, job.ID, user.ID)
	require.NoError(t, err)
	require.Len(t, seeds, 1)
	require.Contains(t, string(seeds[0].Metadata), `"model_id": "frozen"`)
	jobs := service.NewAccountJobService(repo, nil)
	view, err := jobs.Get(ctx, job.ID)
	require.NoError(t, err)
	require.True(t, view.RetryEligible)
	_, err = integrationDB.ExecContext(ctx, "UPDATE admin_account_jobs SET raw_payload_expires_at=NOW()-INTERVAL '1 second' WHERE id=$1", job.ID)
	require.NoError(t, err)
	view, err = jobs.Get(ctx, job.ID)
	require.NoError(t, err)
	require.False(t, view.RetryEligible)
	require.Equal(t, "payload_expired", view.RetryUnavailableReason)
	// All model calls may have succeeded before the monitor failed. The task
	// remains failed, and retry has no model calls to replay.
	job = create(1)
	items, err = repo.ReservePendingItems(ctx, job.ID, 100)
	require.NoError(t, err)
	require.NoError(t, repo.CompleteItems(ctx, job.ID, []service.AccountJobExecutionResult{{ItemID: items[0].ID, Status: service.AccountJobItemStatusSucceeded}}))
	finished, err = repo.Finish(ctx, job.ID, "cancel_check_failed", "account job cancellation state is unavailable")
	require.NoError(t, err)
	require.Equal(t, service.AccountJobStatusFailed, finished.Status)
	view, err = jobs.Get(ctx, job.ID)
	require.NoError(t, err)
	require.False(t, view.RetryEligible)
}
