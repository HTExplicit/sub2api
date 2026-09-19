package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"time"
)

type QuotaCostPoint struct {
	Cursor   int64
	Requests int64
	Cost     float64
}

type QuotaEstimateObservation struct {
	AccountID   int64
	Identity    string
	Period      string
	ObservedAt  time.Time
	ResetsAt    time.Time
	Utilization float64
	Cost        float64
}

type QuotaEstimateRepository interface {
	QuotaCostPoint(context.Context, int64) (QuotaCostPoint, error)
	ObserveQuotaEstimate(context.Context, QuotaEstimateObservation) error
	ReadQuotaEstimate(context.Context, int64, string, string) (*QuotaEstimateObservation, *QuotaEstimateObservation, error)
}

func quotaEstimateIdentity(account *Account) string {
	if account == nil {
		return ""
	}
	// Account-rate changes start a new calibration. The owner identity excludes
	// access-token refreshes, which do not represent a different subscription.
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s/%.17g", CodexTicketAccountIdentity(account), account.BillingRateMultiplier())))
	return hex.EncodeToString(sum[:])
}

func EstimateQuotaValue(base, latest *QuotaEstimateObservation, current AccountQuotaWindow) *QuotaEstimate {
	pending := &QuotaEstimate{Status: "insufficient_data"}
	if base == nil || latest == nil || current.Expired || current.PeriodKey() == "" ||
		base.Identity != latest.Identity || base.Period != latest.Period || latest.Period != current.PeriodKey() ||
		!latest.ObservedAt.After(base.ObservedAt) || current.Utilization < latest.Utilization {
		return pending
	}
	deltaUsed := latest.Utilization - base.Utilization
	deltaCost := latest.Cost - base.Cost
	if deltaUsed <= 0 || deltaCost <= 0 || math.IsNaN(deltaCost) || math.IsInf(deltaCost, 0) || math.IsNaN(deltaUsed) || math.IsInf(deltaUsed, 0) {
		return pending
	}
	total := 100 * deltaCost / deltaUsed
	remaining := total * math.Max(0, 1-current.Utilization/100)
	if math.IsNaN(total) || math.IsInf(total, 0) || math.IsNaN(remaining) || math.IsInf(remaining, 0) {
		return pending
	}
	return &QuotaEstimate{Status: "ready", Total: &total, Remaining: &remaining}
}

type quotaMeasurementFence struct {
	point    QuotaCostPoint
	pool     UsageRecordWorkerPoolStats
	identity string
}

// Only existing /wham/usage queries can create calibration observations. A
// response-header snapshot while inference is running cannot be paired with a
// settled local bill. No extra upstream query is made for estimates.
func (s *OpenAIQuotaService) beginQuotaMeasurement(ctx context.Context, id int64) *quotaMeasurementFence {
	store, ok := s.accountRepo.(QuotaEstimateRepository)
	if !ok || s.concurrency == nil || s.usagePool == nil {
		return nil
	}
	pool := s.usagePool.Stats()
	if pool.WaitingTasks != 0 || pool.SubmittedTasks != pool.CompletedTasks+pool.DroppedTasks {
		return nil
	}
	counts, err := s.concurrency.GetAccountConcurrencyBatch(ctx, []int64{id})
	if err != nil || counts[id] != 0 {
		return nil
	}
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil || account == nil {
		return nil
	}
	point, err := store.QuotaCostPoint(ctx, id)
	if err != nil {
		return nil
	}
	return &quotaMeasurementFence{point: point, pool: pool, identity: quotaEstimateIdentity(account)}
}

func (s *OpenAIQuotaService) finishQuotaMeasurement(ctx context.Context, id int64, usage *OpenAIQuotaUsage, before *quotaMeasurementFence) {
	if before == nil || usage == nil || usage.RateLimit == nil {
		return
	}
	after := s.beginQuotaMeasurement(ctx, id)
	if after == nil || before.identity != after.identity || before.point != after.point ||
		before.pool.SubmittedTasks != after.pool.SubmittedTasks || before.pool.FailedTasks != after.pool.FailedTasks || before.pool.DroppedTasks != after.pool.DroppedTasks {
		return
	}
	store := s.accountRepo.(QuotaEstimateRepository)
	observed := time.Unix(usage.FetchedAt, 0).UTC()
	for _, entry := range []struct {
		id     string
		window *OpenAIRateLimitWindow
	}{{"primary", usage.RateLimit.PrimaryWindow}, {"secondary", usage.RateLimit.SecondaryWindow}} {
		w := entry.window
		if w == nil || w.LimitWindowSeconds <= 0 || w.ResetAt <= 0 {
			continue
		}
		reset := time.Unix(w.ResetAt, 0).UTC()
		window := newQuotaWindow(entry.id, int(w.LimitWindowSeconds/60), w.UsedPercent, &reset, &observed, observed)
		if window.Expired || window.PeriodKey() == "" {
			continue
		}
		_ = store.ObserveQuotaEstimate(ctx, QuotaEstimateObservation{AccountID: id, Identity: after.identity, Period: window.PeriodKey(), ObservedAt: observed, ResetsAt: reset, Utilization: w.UsedPercent, Cost: after.point.Cost})
	}
}
