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

func TestAccountJobStoppedRetryAndMonitorFailure(t *testing.T) {
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
		job, _, err := repo.Create(ctx, service.CreateAccountJobParams{CreatedBy: user.ID, Kind: service.AccountJobKindBatchDelete, IdempotencyKey: uuid.NewString(), RequestHash: strings.Repeat("a", 64), PayloadCipher: "encrypted-fixture", PayloadExpires: time.Now().Add(time.Hour), Items: seeds, Attempt: 1})
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
	require.NoError(t, repo.CompleteItems(ctx, job.ID, []service.AccountJobExecutionResult{
		{ItemID: items[0].ID, Status: service.AccountJobItemStatusFailed, ErrorCode: "delete_failed", Metadata: json.RawMessage(`{"account_id":7}`)},
		{ItemID: items[1].ID, Status: service.AccountJobItemStatusSucceeded, Metadata: json.RawMessage(`{"account_id":8}`)},
	}))
	_, err = repo.Cancel(ctx, job.ID, user.ID)
	require.NoError(t, err)
	finished, err := repo.Finish(ctx, job.ID, "", "")
	require.NoError(t, err)
	require.Equal(t, service.AccountJobStatusCanceled, finished.Status)
	_, seeds, _, _, err := repo.FailedItemSeeds(ctx, job.ID, user.ID)
	require.NoError(t, err)
	require.Len(t, seeds, 1)
	require.Contains(t, string(seeds[0].Metadata), `"account_id": 7`)
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
	// All items may have succeeded before the monitor failed. The task remains
	// failed, and retry has nothing to replay.
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
