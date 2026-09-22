package service

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQuotaEstimateIgnoresImportedConsumption(t *testing.T) {
	now := time.Date(2026, 9, 19, 8, 0, 0, 0, time.UTC)
	reset := now.Add(24 * time.Hour)
	window := newQuotaWindow("primary", 10080, 90, &reset, &now, now)
	base := &QuotaEstimateObservation{Identity: "owner-a", Period: window.PeriodKey(), ObservedAt: now.Add(-time.Hour), Utilization: 80, Cost: 120}
	latest := &QuotaEstimateObservation{Identity: "owner-a", Period: window.PeriodKey(), ObservedAt: now, Utilization: 90, Cost: 130}
	estimate := EstimateQuotaValue(base, latest, window)
	require.Equal(t, "ready", estimate.Status)
	require.InDelta(t, 100, *estimate.Total, 1e-9)
	require.InDelta(t, 10, *estimate.Remaining, 1e-9)
	// Costs from before the baseline cancel out instead of changing the result.
	base.Cost = 0
	latest.Cost = 10
	require.Equal(t, estimate, EstimateQuotaValue(base, latest, window))
}

func TestQuotaEstimateRejectsUnpairedAndResetObservations(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(time.Hour)
	window := newQuotaWindow("primary", 43200, 70, &reset, &now, now)
	baseline := QuotaEstimateObservation{Identity: "a", Period: window.PeriodKey(), ObservedAt: now.Add(-time.Hour), Utilization: 60, Cost: 10}
	current := QuotaEstimateObservation{Identity: "a", Period: window.PeriodKey(), ObservedAt: now, Utilization: 70, Cost: 15}
	for _, name := range []string{"no_delta", "cost_missing", "other_owner", "other_period", "late_observation", "reset", "invalid_cost", "expired"} {
		t.Run(name, func(t *testing.T) {
			base, latest, w := baseline, current, window
			switch name {
			case "no_delta":
				latest.Utilization = base.Utilization
			case "cost_missing":
				latest.Cost = base.Cost
			case "other_owner":
				latest.Identity = "other"
			case "other_period":
				latest.Period = "another period"
			case "late_observation":
				latest.ObservedAt = base.ObservedAt.Add(-time.Second)
			case "reset":
				w.Utilization = 0
			case "invalid_cost":
				latest.Cost = math.Inf(1)
			case "expired":
				w.Expired = true
			}
			estimate := EstimateQuotaValue(&base, &latest, w)
			require.Nil(t, estimate.Total)
			require.Nil(t, estimate.Remaining)
		})
	}
}

func TestQuotaEstimateIdentityUsesValueNotPointerAddress(t *testing.T) {
	rateA, rateB := 1.0, 1.0
	a := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "owner"}, RateMultiplier: &rateA}
	b := *a
	b.RateMultiplier = &rateB
	require.Equal(t, quotaEstimateIdentity(a), quotaEstimateIdentity(&b))
	rateB = 2
	require.NotEqual(t, quotaEstimateIdentity(a), quotaEstimateIdentity(&b))
}
