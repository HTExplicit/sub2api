//go:build integration

package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCindyBalanceProbeActiveJobConstraintIsEnforcedByPostgres(t *testing.T) {
	ctx := context.Background()
	firstID := insertCindyBalanceProbeLifecycleJob(t, "queued", "", time.Time{})

	var ignored int64
	err := integrationDB.QueryRowContext(ctx, `
		INSERT INTO cindy_balance_probe_jobs (
			status, scope, rate_rps, candidate_count, candidate_fingerprint
		) VALUES ('queued', '{}'::jsonb, 0.5, 0, $1)
		RETURNING id
	`, strings.Repeat("e", 64)).Scan(&ignored)
	require.Error(t, err)
	require.Contains(t, err.Error(), "idx_cindy_balance_probe_jobs_one_active")

	_, err = integrationDB.ExecContext(ctx, `
		UPDATE cindy_balance_probe_jobs
		SET status = 'completed', finished_at = NOW()
		WHERE id = $1
	`, firstID)
	require.NoError(t, err)
	secondID := insertCindyBalanceProbeLifecycleJob(t, "queued", "", time.Time{})
	require.NotZero(t, secondID)
}

func TestCindyBalanceProbeLeaseTakeoverMarksCrashUnknownWithoutResend(t *testing.T) {
	ctx := context.Background()
	account := newCindyBalanceProbeLifecycleAccount(t, "takeover")
	jobID := insertCindyBalanceProbeLifecycleJob(t, "queued", "", time.Time{})
	insertCindyBalanceProbeLifecycleItem(t, jobID, account, 1)
	repo := &cindyBalanceProbeRepository{db: integrationDB}

	firstLease := fmt.Sprintf("cindy-lifecycle-first-%d", time.Now().UnixNano())
	job, err := repo.ClaimJob(ctx, firstLease, time.Now().UTC().Add(time.Minute))
	require.NoError(t, err)
	require.NotNil(t, job)
	require.Equal(t, jobID, job.ID)

	reservation, delay, err := repo.ReserveNext(
		ctx,
		jobID,
		firstLease,
		time.Now().UTC(),
		time.Now().UTC().Add(-5*time.Minute),
	)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.NotNil(t, reservation)
	require.Equal(t, "luna", reservation.Stage)
	require.Equal(t, 1, reservation.RequestCount)

	_, err = integrationDB.ExecContext(ctx, `
		UPDATE cindy_balance_probe_jobs
		SET lease_until = NOW() - INTERVAL '1 second'
		WHERE id = $1
	`, jobID)
	require.NoError(t, err)
	heartbeat, err := repo.Heartbeat(ctx, jobID, firstLease, time.Now().UTC().Add(time.Minute))
	require.NoError(t, err)
	require.False(t, heartbeat)

	secondLease := fmt.Sprintf("cindy-lifecycle-second-%d", time.Now().UnixNano())
	job, err = repo.ClaimJob(ctx, secondLease, time.Now().UTC().Add(time.Minute))
	require.NoError(t, err)
	require.NotNil(t, job)
	require.Equal(t, jobID, job.ID)
	require.Equal(t, secondLease, job.LeaseToken)
	keepRunning, applied, err := repo.CompleteStage(
		ctx,
		reservation,
		account,
		firstLease,
		"success",
		"healthy",
		false,
	)
	require.NoError(t, err)
	require.False(t, keepRunning)
	require.False(t, applied, "the reclaimed lease must reject the previous worker result")
	require.NoError(t, repo.RecoverInterruptedItems(ctx, jobID, secondLease))

	retry, retryDelay, err := repo.ReserveNext(
		ctx,
		jobID,
		secondLease,
		time.Now().UTC().Add(2*time.Second),
		time.Now().UTC().Add(-5*time.Minute),
	)
	require.NoError(t, err)
	require.Nil(t, retry)
	require.Zero(t, retryDelay)

	var itemState, finalOutcome string
	var itemRequests, jobRequests int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT state, final_outcome, request_count
		FROM cindy_balance_probe_items WHERE id = $1
	`, reservation.ItemID).Scan(&itemState, &finalOutcome, &itemRequests))
	require.Equal(t, "unknown_after_crash", itemState)
	require.Equal(t, "unknown_after_crash", finalOutcome)
	require.Equal(t, 1, itemRequests)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT request_count FROM cindy_balance_probe_jobs WHERE id = $1
	`, jobID).Scan(&jobRequests))
	require.Equal(t, 1, jobRequests, "a post-send crash must never reserve a replacement request")

	done, err := repo.FinishIfDone(ctx, jobID, secondLease)
	require.NoError(t, err)
	require.True(t, done)
	finished, err := repo.GetJob(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, "completed_with_issues", finished.Status)
}

func TestCindyBalanceProbePauseResumeCancelLifecycle(t *testing.T) {
	ctx := context.Background()
	account := newCindyBalanceProbeLifecycleAccount(t, "cancel")
	jobID := insertCindyBalanceProbeLifecycleJob(t, "queued", "", time.Time{})
	itemID := insertCindyBalanceProbeLifecycleItem(t, jobID, account, 1)
	repo := &cindyBalanceProbeRepository{db: integrationDB}

	job, err := repo.Pause(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, "paused", job.Status)
	claim, err := repo.ClaimJob(ctx, "paused-job-must-not-run", time.Now().UTC().Add(time.Minute))
	require.NoError(t, err)
	require.Nil(t, claim)

	job, err = repo.Resume(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, "queued", job.Status)
	lease := fmt.Sprintf("cindy-lifecycle-cancel-%d", time.Now().UnixNano())
	job, err = repo.ClaimJob(ctx, lease, time.Now().UTC().Add(time.Minute))
	require.NoError(t, err)
	require.NotNil(t, job)
	heartbeat, err := repo.Heartbeat(ctx, jobID, lease, time.Now().UTC().Add(2*time.Minute))
	require.NoError(t, err)
	require.True(t, heartbeat)
	reservation, delay, err := repo.ReserveNext(
		ctx,
		jobID,
		lease,
		time.Now().UTC(),
		time.Now().UTC().Add(-5*time.Minute),
	)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.NotNil(t, reservation)

	job, err = repo.Cancel(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, "cancel_requested", job.Status)
	require.NotNil(t, job.CancelRequestedAt)
	heartbeat, err = repo.Heartbeat(ctx, jobID, lease, time.Now().UTC().Add(2*time.Minute))
	require.NoError(t, err)
	require.False(t, heartbeat, "cancel revokes the previous lease epoch")
	keepRunning, applied, err := repo.CompleteStage(
		ctx,
		reservation,
		account,
		lease,
		"success",
		"healthy",
		false,
	)
	require.NoError(t, err)
	require.False(t, keepRunning)
	require.False(t, applied, "a cancellation that wins first must reject the in-flight result")

	cancelLease := fmt.Sprintf("cindy-lifecycle-cancel-finalize-%d", time.Now().UnixNano())
	job, err = repo.ClaimJob(ctx, cancelLease, time.Now().UTC().Add(time.Minute))
	require.NoError(t, err)
	require.NotNil(t, job)
	require.Equal(t, "cancel_requested", job.Status)
	require.NoError(t, repo.RecoverInterruptedItems(ctx, jobID, cancelLease))
	done, err := repo.FinishIfDone(ctx, jobID, cancelLease)
	require.NoError(t, err)
	require.True(t, done)

	job, err = repo.GetJob(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, "canceled", job.Status)
	var itemState string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT state FROM cindy_balance_probe_items WHERE id = $1
	`, itemID).Scan(&itemState))
	require.Equal(t, "unknown_after_crash", itemState)
}

func TestCindyBalanceProbeThirdUpstreamFailurePausesAndResumeClearsStreak(t *testing.T) {
	ctx := context.Background()
	accounts := []*service.Account{
		newCindyBalanceProbeLifecycleAccount(t, "failure-1"),
		newCindyBalanceProbeLifecycleAccount(t, "failure-2"),
		newCindyBalanceProbeLifecycleAccount(t, "failure-3"),
	}
	lease := fmt.Sprintf("cindy-lifecycle-failure-%d", time.Now().UnixNano())
	jobID := insertCindyBalanceProbeLifecycleJob(t, "running", lease, time.Now().UTC().Add(5*time.Minute))
	for index, account := range accounts {
		insertCindyBalanceProbeLifecycleItem(t, jobID, account, index+1)
	}
	repo := &cindyBalanceProbeRepository{db: integrationDB}
	baseTime := time.Now().UTC()

	for index, account := range accounts {
		reservation, delay, err := repo.ReserveNext(
			ctx,
			jobID,
			lease,
			baseTime.Add(time.Duration(index)*2*time.Second),
			baseTime.Add(-5*time.Minute),
		)
		require.NoError(t, err)
		require.Zero(t, delay)
		require.NotNil(t, reservation)
		keepRunning, applied, err := repo.CompleteStage(
			ctx,
			reservation,
			account,
			lease,
			"server_error",
			"inconclusive",
			true,
		)
		require.NoError(t, err)
		require.True(t, applied)
		require.Equal(t, index < 2, keepRunning)
	}

	job, err := repo.GetJob(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, "paused_upstream", job.Status)
	require.Equal(t, 3, job.ConsecutiveFailures)
	require.Empty(t, job.LeaseToken)
	require.Equal(t, "consecutive_upstream_failures", job.FailureReason)

	job, err = repo.Resume(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, "queued", job.Status)
	require.Zero(t, job.ConsecutiveFailures)
	require.Empty(t, job.FailureReason)
}

func TestCindyBalanceProbePruneFinishedKeepsThirtyDayBoundary(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	oldJobID := insertCindyBalanceProbeLifecycleJob(t, "completed", "", time.Time{})
	recentJobID := insertCindyBalanceProbeLifecycleJob(t, "completed_with_issues", "", time.Time{})
	_, err := integrationDB.ExecContext(ctx, `
		UPDATE cindy_balance_probe_jobs
		SET finished_at = CASE WHEN id = $1 THEN $3::timestamptz ELSE $4::timestamptz END
		WHERE id IN ($1, $2)
	`, oldJobID, recentJobID, now.Add(-31*24*time.Hour), now.Add(-29*24*time.Hour))
	require.NoError(t, err)

	repo := &cindyBalanceProbeRepository{db: integrationDB}
	require.NoError(t, repo.PruneFinished(ctx, now.Add(-30*24*time.Hour)))
	var oldCount, recentCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM cindy_balance_probe_jobs WHERE id = $1`, oldJobID,
	).Scan(&oldCount))
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM cindy_balance_probe_jobs WHERE id = $1`, recentJobID,
	).Scan(&recentCount))
	require.Zero(t, oldCount)
	require.Equal(t, 1, recentCount)
}

func newCindyBalanceProbeLifecycleAccount(t *testing.T, suffix string) *service.Account {
	t.Helper()
	ctx := context.Background()
	client := testEntClient(t)
	account := mustCreateAccount(t, client, &service.Account{
		Name:        fmt.Sprintf("cindy-balance-lifecycle-%s-%d", suffix, time.Now().UnixNano()),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"api_key":  fmt.Sprintf("sk-cindy-lifecycle-%s-%d", suffix, time.Now().UnixNano()),
			"base_url": "https://api.laxarouter.ai",
		},
	})
	account, err := NewAccountRepository(client, integrationDB, nil).GetByID(ctx, account.ID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_groups WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", account.ID)
	})
	return account
}

func insertCindyBalanceProbeLifecycleJob(t *testing.T, status, leaseToken string, leaseUntil time.Time) int64 {
	t.Helper()
	var leaseTokenValue any
	var leaseUntilValue any
	if leaseToken != "" {
		leaseTokenValue = leaseToken
	}
	if !leaseUntil.IsZero() {
		leaseUntilValue = leaseUntil
	}
	var jobID int64
	err := integrationDB.QueryRowContext(context.Background(), `
		INSERT INTO cindy_balance_probe_jobs (
			status, scope, rate_rps, candidate_count, candidate_fingerprint,
			lease_token, lease_until, heartbeat_at, started_at
		) VALUES ($1, '{}'::jsonb, 1.0, 0, $2, $3, $4,
		          CASE WHEN $3::text IS NULL THEN NULL ELSE NOW() END,
		          CASE WHEN $3::text IS NULL THEN NULL ELSE NOW() END)
		RETURNING id
	`, status, strings.Repeat("f", 64), leaseTokenValue, leaseUntilValue).Scan(&jobID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM cindy_balance_probe_jobs WHERE id = $1", jobID)
	})
	return jobID
}

func insertCindyBalanceProbeLifecycleItem(t *testing.T, jobID int64, account *service.Account, ordinal int) int64 {
	t.Helper()
	fingerprint, err := service.CindyAccountIdentityFingerprint(account.Platform, account.Type, account.Credentials)
	require.NoError(t, err)
	var itemID int64
	err = integrationDB.QueryRowContext(context.Background(), `
		INSERT INTO cindy_balance_probe_items (
			job_id, account_id, ordinal, identity_fingerprint,
			account_updated_at, was_marked, state
		) VALUES ($1, $2, $3, $4, $5, FALSE, 'pending')
		RETURNING id
	`, jobID, account.ID, ordinal, fingerprint, account.UpdatedAt).Scan(&itemID)
	require.NoError(t, err)
	return itemID
}
