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

type cindyBalanceProbeFinalizeFixture struct {
	account     *service.Account
	reservation *service.CindyBalanceProbeReservation
	leaseToken  string
}

func TestCindyBalanceProbeFinalizeExhaustedCommitsMarkerItemAndOutbox(t *testing.T) {
	ctx := context.Background()
	fixture := newCindyBalanceProbeFinalizeFixture(t, false, "terra_running")
	repo := &cindyBalanceProbeRepository{db: integrationDB}
	observedAt := time.Now().UTC()

	state, err := repo.FinalizeExhausted(
		ctx,
		fixture.reservation,
		fixture.leaseToken,
		observedAt,
		5*time.Minute,
	)
	require.NoError(t, err)
	require.Equal(t, "exhausted", state)

	var markedAt *time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT cindy_balance_insufficient_at FROM accounts WHERE id = $1`,
		fixture.account.ID,
	).Scan(&markedAt))
	require.NotNil(t, markedAt)

	var itemState, terraOutcome, finalOutcome string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT state, terra_outcome, final_outcome
		FROM cindy_balance_probe_items WHERE id = $1
	`, fixture.reservation.ItemID).Scan(&itemState, &terraOutcome, &finalOutcome))
	require.Equal(t, "exhausted", itemState)
	require.Equal(t, "exact", terraOutcome)
	require.Equal(t, "exhausted", finalOutcome)

	assertCindyBalanceProbeOutboxPayload(t, fixture)
}

func TestCindyBalanceProbeFinalizeRecoveryCommitsMarkerItemAndOutbox(t *testing.T) {
	ctx := context.Background()
	fixture := newCindyBalanceProbeFinalizeFixture(t, true, "luna_running")
	repo := &cindyBalanceProbeRepository{db: integrationDB}

	recovered, err := repo.FinalizeRecovery(
		ctx,
		fixture.reservation,
		fixture.account,
		fixture.leaseToken,
		time.Now().UTC(),
	)
	require.NoError(t, err)
	require.True(t, recovered)

	var markerMissing bool
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT cindy_balance_insufficient_at IS NULL FROM accounts WHERE id = $1`,
		fixture.account.ID,
	).Scan(&markerMissing))
	require.True(t, markerMissing)

	var itemState, lunaOutcome, finalOutcome string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT state, luna_outcome, final_outcome
		FROM cindy_balance_probe_items WHERE id = $1
	`, fixture.reservation.ItemID).Scan(&itemState, &lunaOutcome, &finalOutcome))
	require.Equal(t, "recovered", itemState)
	require.Equal(t, "success", lunaOutcome)
	require.Equal(t, "recovered", finalOutcome)

	assertCindyBalanceProbeOutboxPayload(t, fixture)
}

func TestCindyBalanceProbeFinalizeRecoveryRejectsReplacedMarkerGeneration(t *testing.T) {
	ctx := context.Background()
	fixture := newCindyBalanceProbeFinalizeFixture(t, true, "luna_running")
	repo := &cindyBalanceProbeRepository{db: integrationDB}
	require.NotNil(t, fixture.account.CindyBalanceInsufficientAt)
	replacementMarker := fixture.account.CindyBalanceInsufficientAt.UTC().Add(time.Second)

	// Simulate an out-of-band marker replacement that deliberately bypasses the
	// normal account update path and therefore does not change updated_at.
	_, err := integrationDB.ExecContext(ctx, `
		UPDATE accounts
		SET cindy_balance_insufficient_at = $2
		WHERE id = $1
	`, fixture.account.ID, replacementMarker)
	require.NoError(t, err)

	recovered, err := repo.FinalizeRecovery(
		ctx,
		fixture.reservation,
		fixture.account,
		fixture.leaseToken,
		time.Now().UTC(),
	)
	require.NoError(t, err)
	require.False(t, recovered)

	var currentMarker time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT cindy_balance_insufficient_at FROM accounts WHERE id = $1`,
		fixture.account.ID,
	).Scan(&currentMarker))
	require.True(t, currentMarker.UTC().Equal(replacementMarker), "the stale probe must not clear the replacement marker")

	var itemState, lunaOutcome, finalOutcome string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT state, luna_outcome, final_outcome
		FROM cindy_balance_probe_items WHERE id = $1
	`, fixture.reservation.ItemID).Scan(&itemState, &lunaOutcome, &finalOutcome))
	require.Equal(t, "skipped_stale", itemState)
	require.Equal(t, "stale", lunaOutcome)
	require.Equal(t, "skipped_stale", finalOutcome)
}

func TestCindyBalanceProbeFinalizeRecoveryStaleSuccessResetsFailureStreak(t *testing.T) {
	ctx := context.Background()
	fixture := newCindyBalanceProbeFinalizeFixture(t, true, "luna_running")
	repo := &cindyBalanceProbeRepository{db: integrationDB}
	require.NotNil(t, fixture.account.CindyBalanceInsufficientAt)

	_, err := integrationDB.ExecContext(ctx, `
		UPDATE cindy_balance_probe_jobs
		SET consecutive_upstream_failures = 2
		WHERE id = $1
	`, fixture.reservation.JobID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `
		UPDATE accounts
		SET cindy_balance_insufficient_at = $2
		WHERE id = $1
	`, fixture.account.ID, fixture.account.CindyBalanceInsufficientAt.UTC().Add(time.Second))
	require.NoError(t, err)

	recovered, err := repo.FinalizeRecovery(
		ctx,
		fixture.reservation,
		fixture.account,
		fixture.leaseToken,
		time.Now().UTC(),
	)
	require.NoError(t, err)
	require.False(t, recovered)

	var failures int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT consecutive_upstream_failures
		FROM cindy_balance_probe_jobs WHERE id = $1
	`, fixture.reservation.JobID).Scan(&failures))
	require.Zero(t, failures, "a valid upstream completion must break the failure streak even when its recovery CAS is stale")
}

func TestCindyBalanceProbeSuccessfulFinalizersResetFailureStreak(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		marked    bool
		itemState string
	}{
		{name: "recovery", marked: true, itemState: "luna_running"},
		{name: "double exact exhaustion", marked: false, itemState: "terra_running"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			nextAccount := newCindyBalanceProbeLifecycleAccount(t, "post-finalize-"+testCase.name)
			fixture := newCindyBalanceProbeFinalizeFixture(t, testCase.marked, testCase.itemState)
			repo := &cindyBalanceProbeRepository{db: integrationDB}

			_, err := integrationDB.ExecContext(ctx, `
				UPDATE cindy_balance_probe_jobs
				SET consecutive_upstream_failures = 2
				WHERE id = $1
			`, fixture.reservation.JobID)
			require.NoError(t, err)

			if testCase.marked {
				recovered, finalizeErr := repo.FinalizeRecovery(
					ctx,
					fixture.reservation,
					fixture.account,
					fixture.leaseToken,
					time.Now().UTC(),
				)
				require.NoError(t, finalizeErr)
				require.True(t, recovered)
			} else {
				state, finalizeErr := repo.FinalizeExhausted(
					ctx,
					fixture.reservation,
					fixture.leaseToken,
					time.Now().UTC(),
					5*time.Minute,
				)
				require.NoError(t, finalizeErr)
				require.Equal(t, "exhausted", state)
			}

			var resetFailures int
			require.NoError(t, integrationDB.QueryRowContext(ctx, `
				SELECT consecutive_upstream_failures
				FROM cindy_balance_probe_jobs WHERE id = $1
			`, fixture.reservation.JobID).Scan(&resetFailures))
			require.Zero(t, resetFailures, "a successful terminal result must break the upstream failure streak")

			insertCindyBalanceProbeLifecycleItem(t, fixture.reservation.JobID, nextAccount, 2)
			reservation, delay, reserveErr := repo.ReserveNext(
				ctx,
				fixture.reservation.JobID,
				fixture.leaseToken,
				time.Now().UTC().Add(3*time.Second),
				time.Now().UTC().Add(-5*time.Minute),
			)
			require.NoError(t, reserveErr)
			require.Zero(t, delay)
			require.NotNil(t, reservation)

			keepRunning, applied, completeErr := repo.CompleteStage(
				ctx,
				reservation,
				nextAccount,
				fixture.leaseToken,
				"server_error",
				"inconclusive",
				true,
			)
			require.NoError(t, completeErr)
			require.True(t, applied)
			require.True(t, keepRunning, "the next isolated upstream failure must not pause the task")

			job, getErr := repo.GetJob(ctx, fixture.reservation.JobID)
			require.NoError(t, getErr)
			require.Equal(t, "running", job.Status)
			require.Equal(t, 1, job.ConsecutiveFailures)
		})
	}
}

func TestCindyBalanceProbeFinalizeRollsBackWhenOutboxInsertFails(t *testing.T) {
	ctx := context.Background()
	fixture := newCindyBalanceProbeFinalizeFixture(t, false, "terra_running")
	repo := &cindyBalanceProbeRepository{db: integrationDB}
	suffix := time.Now().UnixNano()
	functionName := fmt.Sprintf("fail_cindy_balance_probe_outbox_%d", suffix)
	triggerName := fmt.Sprintf("fail_cindy_balance_probe_outbox_trigger_%d", suffix)

	_, err := integrationDB.ExecContext(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.account_id = %d THEN
				RAISE EXCEPTION 'forced Cindy balance probe outbox failure';
			END IF;
			RETURN NEW;
		END;
		$$`, functionName, fixture.account.ID))
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(
		"CREATE TRIGGER %s BEFORE INSERT ON scheduler_outbox FOR EACH ROW EXECUTE FUNCTION %s()",
		triggerName,
		functionName,
	))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON scheduler_outbox",
			triggerName,
		))
		_, _ = integrationDB.ExecContext(context.Background(), fmt.Sprintf(
			"DROP FUNCTION IF EXISTS %s()",
			functionName,
		))
	})

	state, err := repo.FinalizeExhausted(
		ctx,
		fixture.reservation,
		fixture.leaseToken,
		time.Now().UTC(),
		5*time.Minute,
	)
	require.ErrorContains(t, err, "forced Cindy balance probe outbox failure")
	require.Empty(t, state)

	var markerMissing bool
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT cindy_balance_insufficient_at IS NULL FROM accounts WHERE id = $1`,
		fixture.account.ID,
	).Scan(&markerMissing))
	require.True(t, markerMissing, "the marker update must roll back with the outbox insert")

	var itemState string
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT state FROM cindy_balance_probe_items WHERE id = $1`,
		fixture.reservation.ItemID,
	).Scan(&itemState))
	require.Equal(t, "terra_running", itemState, "the item update must roll back with the outbox insert")
}

func newCindyBalanceProbeFinalizeFixture(
	t *testing.T,
	marked bool,
	itemState string,
) *cindyBalanceProbeFinalizeFixture {
	t.Helper()
	ctx := context.Background()
	client := testEntClient(t)
	credentials := map[string]any{
		"api_key":  fmt.Sprintf("sk-cindy-finalize-%d", time.Now().UnixNano()),
		"base_url": "https://api.laxarouter.ai",
	}
	account := mustCreateAccount(t, client, &service.Account{
		Name:            fmt.Sprintf("cindy-balance-finalize-%d", time.Now().UnixNano()),
		Platform:        service.PlatformCindy,
		WirePlatform:    service.WirePlatformOpenAI,
		ProviderProfile: service.ProviderProfileCindyLaxaV1,
		Type:            service.AccountTypeAPIKey,
		Status:          service.StatusActive,
		Schedulable:     true,
		Credentials:     credentials,
	})
	if marked {
		_, err := integrationDB.ExecContext(ctx, `
			UPDATE accounts
			SET cindy_balance_insufficient_at = NOW(), updated_at = NOW()
			WHERE id = $1
		`, account.ID)
		require.NoError(t, err)
	}
	account, err := NewAccountRepository(client, integrationDB, nil).GetByID(ctx, account.ID)
	require.NoError(t, err)
	fingerprint, err := service.CindyAccountIdentityFingerprint(
		account.Platform,
		account.Type,
		account.Credentials,
	)
	require.NoError(t, err)

	leaseToken := fmt.Sprintf("cindy-finalize-lease-%d", time.Now().UnixNano())
	var lunaOutcome any
	if !marked {
		lunaOutcome = "exact"
	}
	var jobID, itemID int64
	err = integrationDB.QueryRowContext(ctx, `
		INSERT INTO cindy_balance_probe_jobs (
			status, scope, rate_rps, candidate_count, candidate_fingerprint,
			request_count, last_request_started_at, lease_token, lease_until,
			heartbeat_at, started_at
		) VALUES (
			'running', '{}'::jsonb, 1.0, 1, $1,
			$2, NOW(), $3, NOW() + INTERVAL '2 minutes', NOW(), NOW()
		) RETURNING id
	`, strings.Repeat("c", 64), map[bool]int{false: 2, true: 1}[marked], leaseToken).Scan(&jobID)
	require.NoError(t, err)
	err = integrationDB.QueryRowContext(ctx, `
		INSERT INTO cindy_balance_probe_items (
			job_id, account_id, ordinal, identity_fingerprint,
			account_updated_at, was_marked, state, luna_outcome, luna_at,
			request_count, started_at
		) VALUES ($1, $2, 1, $3, $4, $5, $6, $7, NOW(), $8, NOW())
		RETURNING id
	`,
		jobID,
		account.ID,
		fingerprint,
		account.UpdatedAt,
		marked,
		itemState,
		lunaOutcome,
		map[bool]int{false: 2, true: 1}[marked],
	).Scan(&itemID)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM cindy_balance_probe_jobs WHERE id = $1", jobID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_groups WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", account.ID)
	})

	return &cindyBalanceProbeFinalizeFixture{
		account: account,
		reservation: &service.CindyBalanceProbeReservation{
			JobID:               jobID,
			ItemID:              itemID,
			AccountID:           account.ID,
			Stage:               map[bool]string{false: "terra", true: "luna"}[marked],
			LeaseToken:          leaseToken,
			RequestCount:        map[bool]int{false: 2, true: 1}[marked],
			IdentityFingerprint: fingerprint,
			AccountUpdatedAt:    account.UpdatedAt,
			WasMarked:           marked,
		},
		leaseToken: leaseToken,
	}
}

func assertCindyBalanceProbeOutboxPayload(t *testing.T, fixture *cindyBalanceProbeFinalizeFixture) {
	t.Helper()
	var source string
	var jobID int64
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
		SELECT payload->>'source', (payload->>'job_id')::bigint
		FROM scheduler_outbox
		WHERE event_type = $1 AND account_id = $2
		ORDER BY id DESC LIMIT 1
	`, service.SchedulerOutboxEventAccountChanged, fixture.account.ID).Scan(&source, &jobID))
	require.Equal(t, "cindy_balance_probe", source)
	require.Equal(t, fixture.reservation.JobID, jobID)
}
