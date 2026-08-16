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

func TestCindyBalanceProbeTerraDispatchUsesAuthoritativeEpoch(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	credentials := map[string]any{
		"api_key":  "sk-cindy-dispatch-fixture",
		"base_url": "https://api.laxarouter.ai",
	}
	account := mustCreateAccount(t, client, &service.Account{
		Name:        fmt.Sprintf("cindy-balance-dispatch-%d", time.Now().UnixNano()),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
		Credentials: credentials,
	})
	fingerprint, err := service.CindyAccountIdentityFingerprint(account.Platform, account.Type, account.Credentials)
	require.NoError(t, err)

	const firstLease = "cindy-dispatch-lease-1"
	var jobID, itemID int64
	err = integrationDB.QueryRowContext(ctx, `
		INSERT INTO cindy_balance_probe_jobs (
			status, scope, rate_rps, candidate_count, candidate_fingerprint,
			request_count, last_request_started_at, lease_token, lease_until,
			heartbeat_at, started_at
		) VALUES (
			'running', '{}'::jsonb, 1.0, 1, $1,
			1, NOW() - INTERVAL '2 seconds', $2, NOW() + INTERVAL '2 minutes',
			NOW(), NOW()
		) RETURNING id
	`, strings.Repeat("a", 64), firstLease).Scan(&jobID)
	require.NoError(t, err)
	err = integrationDB.QueryRowContext(ctx, `
		INSERT INTO cindy_balance_probe_items (
			job_id, account_id, ordinal, identity_fingerprint,
			account_updated_at, was_marked, state, request_count, started_at
		) VALUES ($1, $2, 1, $3, $4, FALSE, 'luna_running', 1, NOW())
		RETURNING id
	`, jobID, account.ID, fingerprint, account.UpdatedAt).Scan(&itemID)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM cindy_balance_probe_jobs WHERE id = $1", jobID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_groups WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", account.ID)
	})

	repo := &cindyBalanceProbeRepository{db: integrationDB}
	lunaReservation := &service.CindyBalanceProbeReservation{
		JobID:               jobID,
		ItemID:              itemID,
		AccountID:           account.ID,
		Stage:               "luna",
		LeaseToken:          firstLease,
		JobRequestCount:     1,
		RequestCount:        1,
		IdentityFingerprint: fingerprint,
		AccountUpdatedAt:    account.UpdatedAt,
		WasMarked:           false,
	}
	changed, applied, err := repo.CompleteStage(ctx, lunaReservation, firstLease, "exact", "luna_exact", false)
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, applied)

	terraReservation, delay, err := repo.ReserveNext(
		ctx,
		jobID,
		firstLease,
		time.Now().UTC(),
		time.Now().UTC().Add(-5*time.Minute),
	)
	require.NoError(t, err)
	require.Zero(t, delay)
	require.NotNil(t, terraReservation)
	require.Equal(t, "terra", terraReservation.Stage)
	require.Equal(t, 2, terraReservation.RequestCount)
	require.NotNil(t, terraReservation.LunaAt)

	credentialsJSON, err := json.Marshal(account.Credentials)
	require.NoError(t, err)
	legacyStrictPredicateMatches := func(reservation *service.CindyBalanceProbeReservation) bool {
		t.Helper()
		var observedLunaAt any
		if reservation.LunaAt != nil {
			observedLunaAt = reservation.LunaAt.UTC()
		}
		var matches bool
		require.NoError(t, integrationDB.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM cindy_balance_probe_items AS i
				JOIN cindy_balance_probe_jobs AS j ON j.id = i.job_id
				JOIN accounts AS a ON a.id = i.account_id
				WHERE i.id = $1 AND j.id = $2 AND i.account_id = $6
				  AND j.lease_token = $3 AND j.lease_until >= NOW()
				  AND j.status = 'running' AND j.cancel_requested_at IS NULL
				  AND j.request_count = $4
				  AND i.state = $5 AND i.identity_fingerprint = $7
				  AND i.account_updated_at = $8 AND i.was_marked = $9
				  AND i.request_count = $10 AND i.luna_at = $11
				  AND a.platform = $12 AND a.type = $13 AND a.status = $14
				  AND a.schedulable = TRUE AND a.deleted_at IS NULL
				  AND a.updated_at = $8
				  AND ((a.cindy_balance_insufficient_at IS NOT NULL) = $9)
				  AND a.credentials = $15::jsonb
				  AND LOWER(BTRIM(a.credentials->>'base_url')) IN
				      ('https://api.laxarouter.ai', 'https://api.laxarouter.ai/')
			)
		`, reservation.ItemID, reservation.JobID, firstLease, reservation.JobRequestCount,
			reservation.Stage+"_running", reservation.AccountID, reservation.IdentityFingerprint,
			reservation.AccountUpdatedAt, reservation.WasMarked, reservation.RequestCount,
			observedLunaAt, service.PlatformOpenAI, service.AccountTypeAPIKey,
			service.StatusActive, string(credentialsJSON)).Scan(&matches))
		return matches
	}
	require.True(t, legacyStrictPredicateMatches(terraReservation),
		"the original strict predicate must accept an unmodified ReserveNext snapshot")

	ready, err := repo.ValidateReservationForSend(ctx, terraReservation, account, firstLease)
	require.NoError(t, err)
	require.True(t, ready)

	driftCases := []struct {
		name   string
		mutate func(*service.CindyBalanceProbeReservation)
	}{
		{
			name: "job request count observation",
			mutate: func(reservation *service.CindyBalanceProbeReservation) {
				reservation.JobRequestCount++
			},
		},
		{
			name: "Luna timestamp observation",
			mutate: func(reservation *service.CindyBalanceProbeReservation) {
				drifted := reservation.LunaAt.Add(-time.Second)
				reservation.LunaAt = &drifted
			},
		},
		{
			name: "missing Luna timestamp observation",
			mutate: func(reservation *service.CindyBalanceProbeReservation) {
				reservation.LunaAt = nil
			},
		},
	}
	for _, tc := range driftCases {
		t.Run(tc.name, func(t *testing.T) {
			staleObservation := *terraReservation
			tc.mutate(&staleObservation)
			require.False(t, legacyStrictPredicateMatches(&staleObservation),
				"the original strict predicate treated an observation echo as lost authority")
			ready, validateErr := repo.ValidateReservationForSend(ctx, &staleObservation, account, firstLease)
			require.NoError(t, validateErr)
			require.True(t, ready, "an observation echo must not revoke a current item dispatch epoch")
		})
	}

	const replacementLease = "cindy-dispatch-lease-2"
	_, err = integrationDB.ExecContext(ctx, `
		UPDATE cindy_balance_probe_jobs
		SET lease_token = $2, lease_until = NOW() + INTERVAL '2 minutes'
		WHERE id = $1
	`, jobID, replacementLease)
	require.NoError(t, err)
	ready, err = repo.ValidateReservationForSend(ctx, terraReservation, account, firstLease)
	require.NoError(t, err)
	require.False(t, ready, "a reclaimed job must reject the previous worker lease")
}
