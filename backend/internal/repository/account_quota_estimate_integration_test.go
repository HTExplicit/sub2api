//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestQuotaEstimateRepositoryPersistsBaselineAndRejectsLateObservations(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	account := mustCreateAccount(t, client, &service.Account{Name: "quota-estimate-" + uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM scheduler_outbox WHERE account_id=$1`, account.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, account.ID)
	})
	repo := newAccountRepositoryWithSQL(client, integrationDB, nil)
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(time.Hour)
	window := service.AccountQuotaWindow{ID: "primary", WindowMinutes: 43200, Utilization: 90, ResetsAt: &reset}
	point := service.QuotaEstimateObservation{AccountID: account.ID, Identity: "test-owner", Period: window.PeriodKey(), ObservedAt: now, ResetsAt: reset, Utilization: 80, Cost: 120}
	require.NoError(t, repo.ObserveQuotaEstimate(ctx, point))
	point.ObservedAt = now.Add(time.Minute)
	point.Utilization = 90
	point.Cost = 130
	require.NoError(t, repo.ObserveQuotaEstimate(ctx, point))
	late := point
	late.ObservedAt = now.Add(30 * time.Second)
	late.Utilization = 99
	late.Cost = 999
	require.NoError(t, repo.ObserveQuotaEstimate(ctx, late))
	base, last, err := repo.ReadQuotaEstimate(ctx, account.ID, point.Identity, point.Period)
	require.NoError(t, err)
	require.Equal(t, 80.0, base.Utilization)
	require.Equal(t, 120.0, base.Cost)
	require.Equal(t, 90.0, last.Utilization)
	require.Equal(t, 130.0, last.Cost)
	estimate := service.EstimateQuotaValue(base, last, window)
	require.Equal(t, "ready", estimate.Status)
	require.InDelta(t, 100, *estimate.Total, 1e-9)
	require.InDelta(t, 10, *estimate.Remaining, 1e-9)
	// An upstream increase without any local cost cannot calibrate this account.
	point.ObservedAt = now.Add(2 * time.Minute)
	point.Utilization = 95
	require.NoError(t, repo.ObserveQuotaEstimate(ctx, point))
	base, last, err = repo.ReadQuotaEstimate(ctx, account.ID, point.Identity, point.Period)
	require.NoError(t, err)
	window.Utilization = 95
	require.Equal(t, "insufficient_data", service.EstimateQuotaValue(base, last, window).Status)
	missingBase, missingLatest, err := repo.ReadQuotaEstimate(ctx, account.ID, "different-owner", point.Period)
	require.NoError(t, err)
	require.Nil(t, missingBase)
	require.Nil(t, missingLatest)
}

func TestQuotaEstimateRepositoryRecalibratesAfterCostHistoryShrinks(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	account := mustCreateAccount(t, client, &service.Account{Name: "quota-cost-" + uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth})
	t.Cleanup(func() { _, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, account.ID) })
	repo := newAccountRepositoryWithSQL(client, integrationDB, nil)
	now := time.Now().UTC().Truncate(time.Second)
	point := service.QuotaEstimateObservation{AccountID: account.ID, Identity: "owner", Period: "same-window", ResetsAt: now.Add(time.Hour), ObservedAt: now, Utilization: 70, Cost: 100}
	require.NoError(t, repo.ObserveQuotaEstimate(ctx, point))
	point.ObservedAt, point.Utilization, point.Cost = now.Add(time.Minute), 80, 120
	require.NoError(t, repo.ObserveQuotaEstimate(ctx, point))
	point.ObservedAt, point.Cost = now.Add(2*time.Minute), 20
	require.NoError(t, repo.ObserveQuotaEstimate(ctx, point))
	base, latest, err := repo.ReadQuotaEstimate(ctx, account.ID, point.Identity, point.Period)
	require.NoError(t, err)
	require.Equal(t, 80.0, base.Utilization)
	require.Equal(t, 20.0, base.Cost)
	require.Equal(t, latest.ObservedAt, base.ObservedAt)
}
