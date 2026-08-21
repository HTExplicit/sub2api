//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type cindyBalanceProbeCompleteFixture struct {
	account     *service.Account
	reservation *service.CindyBalanceProbeReservation
	leaseToken  string
}

func TestCindyBalanceProbeCompleteStageRevalidatesAccountSnapshot(t *testing.T) {
	type testCase struct {
		name               string
		marked             bool
		outcome            string
		finalState         string
		networkFailure     bool
		mutate             func(*testing.T, *cindyBalanceProbeCompleteFixture)
		wantState          string
		wantStageOutcome   string
		wantFinalOutcome   string
		wantFinalOutcomeOK bool
		wantFailures       int
	}
	tests := []testCase{
		{
			name:    "current unmarked Luna success remains healthy",
			outcome: "success", finalState: "healthy",
			wantState: "healthy", wantStageOutcome: "success",
			wantFinalOutcome: "success", wantFinalOutcomeOK: true,
		},
		{
			name:    "unmarked Luna success skips a full credential drift",
			outcome: "success", finalState: "healthy",
			mutate: func(t *testing.T, fixture *cindyBalanceProbeCompleteFixture) {
				t.Helper()
				credentials := map[string]any{
					"api_key":      fixture.account.Credentials["api_key"],
					"base_url":     "https://api.laxarouter.ai",
					"organization": "replacement-generation",
				}
				raw, err := json.Marshal(credentials)
				require.NoError(t, err)
				_, err = integrationDB.ExecContext(context.Background(), `
					UPDATE accounts
					SET credentials = $2::jsonb
					WHERE id = $1
				`, fixture.account.ID, string(raw))
				require.NoError(t, err)
			},
			wantState: "skipped_stale", wantStageOutcome: "stale",
			wantFinalOutcome: "skipped_stale", wantFinalOutcomeOK: true,
		},
		{
			name:    "current unmarked Luna exact remains pending Terra confirmation",
			outcome: "exact", finalState: "luna_exact",
			wantState: "luna_exact", wantStageOutcome: "exact",
		},
		{
			name:    "unmarked Luna exact skips a disabled account generation",
			outcome: "exact", finalState: "luna_exact",
			mutate: func(t *testing.T, fixture *cindyBalanceProbeCompleteFixture) {
				t.Helper()
				_, err := integrationDB.ExecContext(context.Background(), `
					UPDATE accounts
					SET status = $2, updated_at = NOW() + INTERVAL '1 second'
					WHERE id = $1
				`, fixture.account.ID, service.StatusDisabled)
				require.NoError(t, err)
			},
			wantState: "skipped_stale", wantStageOutcome: "stale",
			wantFinalOutcome: "skipped_stale", wantFinalOutcomeOK: true,
		},
		{
			name:   "current marked Luna exact remains exhausted",
			marked: true, outcome: "exact", finalState: "still_exhausted",
			wantState: "still_exhausted", wantStageOutcome: "exact",
			wantFinalOutcome: "exact", wantFinalOutcomeOK: true,
		},
		{
			name:   "marked Luna exact skips a cleared marker generation",
			marked: true, outcome: "exact", finalState: "still_exhausted",
			mutate: func(t *testing.T, fixture *cindyBalanceProbeCompleteFixture) {
				t.Helper()
				_, err := integrationDB.ExecContext(context.Background(), `
					UPDATE accounts
					SET cindy_balance_insufficient_at = NULL,
					    updated_at = NOW() + INTERVAL '1 second'
					WHERE id = $1
				`, fixture.account.ID)
				require.NoError(t, err)
			},
			wantState: "skipped_stale", wantStageOutcome: "stale",
			wantFinalOutcome: "skipped_stale", wantFinalOutcomeOK: true,
		},
		{
			name:   "marked Luna exact skips a replaced marker generation",
			marked: true, outcome: "exact", finalState: "still_exhausted",
			mutate: func(t *testing.T, fixture *cindyBalanceProbeCompleteFixture) {
				t.Helper()
				_, err := integrationDB.ExecContext(context.Background(), `
					UPDATE accounts
					SET cindy_balance_insufficient_at = cindy_balance_insufficient_at + INTERVAL '1 second'
					WHERE id = $1
				`, fixture.account.ID)
				require.NoError(t, err)
			},
			wantState: "skipped_stale", wantStageOutcome: "stale",
			wantFinalOutcome: "skipped_stale", wantFinalOutcomeOK: true,
		},
		{
			name:   "current marked other error remains inconclusive",
			marked: true, outcome: "other_error", finalState: "inconclusive",
			wantState: "inconclusive", wantStageOutcome: "other_error",
			wantFinalOutcome: "other_error", wantFinalOutcomeOK: true,
		},
		{
			name:   "marked server error skips a deleted account without pausing the job",
			marked: true, outcome: "server_error", finalState: "inconclusive", networkFailure: true,
			mutate: func(t *testing.T, fixture *cindyBalanceProbeCompleteFixture) {
				t.Helper()
				_, err := integrationDB.ExecContext(context.Background(), `
					UPDATE accounts
					SET deleted_at = NOW(), updated_at = NOW() + INTERVAL '1 second'
					WHERE id = $1
				`, fixture.account.ID)
				require.NoError(t, err)
			},
			wantState: "skipped_stale", wantStageOutcome: "stale",
			wantFinalOutcome: "skipped_stale", wantFinalOutcomeOK: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newCindyBalanceProbeCompleteFixture(t, tc.marked)
			if tc.mutate != nil {
				tc.mutate(t, fixture)
			}

			keepRunning, applied, err := (&cindyBalanceProbeRepository{db: integrationDB}).CompleteStage(
				context.Background(),
				fixture.reservation,
				fixture.account,
				fixture.leaseToken,
				tc.outcome,
				tc.finalState,
				tc.networkFailure,
			)
			require.NoError(t, err)
			require.True(t, applied)
			require.True(t, keepRunning)

			var state, stageOutcome string
			var finalOutcome *string
			require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
				SELECT state, luna_outcome, final_outcome
				FROM cindy_balance_probe_items WHERE id = $1
			`, fixture.reservation.ItemID).Scan(&state, &stageOutcome, &finalOutcome))
			require.Equal(t, tc.wantState, state)
			require.Equal(t, tc.wantStageOutcome, stageOutcome)
			if tc.wantFinalOutcomeOK {
				require.NotNil(t, finalOutcome)
				require.Equal(t, tc.wantFinalOutcome, *finalOutcome)
			} else {
				require.Nil(t, finalOutcome)
			}

			var failures int
			require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
				SELECT consecutive_upstream_failures
				FROM cindy_balance_probe_jobs WHERE id = $1
			`, fixture.reservation.JobID).Scan(&failures))
			require.Equal(t, tc.wantFailures, failures)
		})
	}
}

func TestCindyBalanceProbeLatestByAccountIDsReturnsConclusionStateAfterCompletion(t *testing.T) {
	ctx := context.Background()
	fixture := newCindyBalanceProbeCompleteFixture(t, false)
	repo := &cindyBalanceProbeRepository{db: integrationDB}

	keepRunning, applied, err := repo.CompleteStage(
		ctx,
		fixture.reservation,
		fixture.account,
		fixture.leaseToken,
		"success",
		"healthy",
		false,
	)
	require.NoError(t, err)
	require.True(t, applied)
	require.True(t, keepRunning)

	latest, err := repo.LatestByAccountIDs(ctx, []int64{fixture.account.ID})
	require.NoError(t, err)
	require.Contains(t, latest, fixture.account.ID)
	require.Equal(t, "healthy", latest[fixture.account.ID].Outcome,
		"the account summary must expose the translatable conclusion state, not the stage outcome")
}

func newCindyBalanceProbeCompleteFixture(t *testing.T, marked bool) *cindyBalanceProbeCompleteFixture {
	t.Helper()
	ctx := context.Background()
	client := testEntClient(t)
	credentials := map[string]any{
		"api_key":  fmt.Sprintf("sk-cindy-complete-%d", time.Now().UnixNano()),
		"base_url": "https://api.laxarouter.ai",
	}
	account := mustCreateAccount(t, client, &service.Account{
		Name:            fmt.Sprintf("cindy-balance-complete-%d", time.Now().UnixNano()),
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
	fingerprint, err := service.CindyAccountIdentityFingerprint(account.Platform, account.Type, account.Credentials)
	require.NoError(t, err)

	leaseToken := fmt.Sprintf("cindy-complete-lease-%d", time.Now().UnixNano())
	var jobID, itemID int64
	err = integrationDB.QueryRowContext(ctx, `
		INSERT INTO cindy_balance_probe_jobs (
			status, scope, rate_rps, candidate_count, candidate_fingerprint,
			request_count, last_request_started_at, lease_token, lease_until,
			heartbeat_at, started_at
		) VALUES (
			'running', '{}'::jsonb, 1.0, 1, $1,
			1, NOW(), $2, NOW() + INTERVAL '2 minutes', NOW(), NOW()
		) RETURNING id
	`, strings.Repeat("d", 64), leaseToken).Scan(&jobID)
	require.NoError(t, err)
	err = integrationDB.QueryRowContext(ctx, `
		INSERT INTO cindy_balance_probe_items (
			job_id, account_id, ordinal, identity_fingerprint,
			account_updated_at, was_marked, state, request_count, started_at
		) VALUES ($1, $2, 1, $3, $4, $5, 'luna_running', 1, NOW())
		RETURNING id
	`, jobID, account.ID, fingerprint, account.UpdatedAt, marked).Scan(&itemID)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM cindy_balance_probe_jobs WHERE id = $1", jobID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_groups WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", account.ID)
	})

	return &cindyBalanceProbeCompleteFixture{
		account: account,
		reservation: &service.CindyBalanceProbeReservation{
			JobID:               jobID,
			ItemID:              itemID,
			AccountID:           account.ID,
			Stage:               "luna",
			LeaseToken:          leaseToken,
			JobRequestCount:     1,
			RequestCount:        1,
			IdentityFingerprint: fingerprint,
			AccountUpdatedAt:    account.UpdatedAt,
			WasMarked:           marked,
		},
		leaseToken: leaseToken,
	}
}
