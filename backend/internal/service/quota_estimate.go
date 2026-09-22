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
	activity QuotaActivityStamp
	identity string
	shadow   bool
}

// Only existing /wham/usage queries can create calibration observations. A
// response-header snapshot while inference is running cannot be paired with a
// settled local bill. No extra upstream query is made for estimates.
func (s *OpenAIQuotaService) beginQuotaMeasurement(ctx context.Context, id int64) *quotaMeasurementFence {
	store, ok := s.accountRepo.(QuotaEstimateRepository)
	if !ok || s.activity == nil {
		return nil
	}
	activity, known := s.activity.Read(ctx, id)
	if !known || activity.Active != 0 {
		return nil
	}
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil || account == nil {
		return nil
	}
	account, err = s.quotaEstimateAccount(ctx, account)
	if err != nil {
		return nil
	}
	point, err := store.QuotaCostPoint(ctx, id)
	if err != nil {
		return nil
	}
	return &quotaMeasurementFence{point: point, activity: activity, identity: quotaEstimateBasis(account, activity), shadow: account.IsShadow()}
}

func (s *OpenAIQuotaService) finishQuotaMeasurement(ctx context.Context, id int64, usage *OpenAIQuotaUsage, before *quotaMeasurementFence) {
	if before == nil || usage == nil {
		return
	}
	after := s.beginQuotaMeasurement(ctx, id)
	if after == nil || before.identity != after.identity || before.point != after.point || before.activity != after.activity {
		return
	}
	store, ok := s.accountRepo.(QuotaEstimateRepository)
	if !ok {
		return
	}
	limits := usage.RateLimit
	if after.shadow {
		limits = nil
		for _, additional := range usage.AdditionalRateLimits {
			if additional.MeteredFeature == "codex_bengalfox" {
				limits = additional.RateLimit
				break
			}
		}
	}
	if limits == nil {
		return
	}
	observed := time.Unix(usage.FetchedAt, 0).UTC()
	for _, entry := range []struct {
		id     string
		window *OpenAIRateLimitWindow
	}{{"primary", limits.PrimaryWindow}, {"secondary", limits.SecondaryWindow}} {
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

func quotaEstimateBasis(account *Account, stamp QuotaActivityStamp) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s/epoch=%s/gaps=%d", quotaEstimateIdentity(account), stamp.Epoch, stamp.Gaps)))
	return hex.EncodeToString(sum[:])
}

func (s *OpenAIQuotaService) estimateBasis(ctx context.Context, account *Account) (string, bool) {
	if s == nil || account == nil {
		return "", false
	}
	stamp, ok := s.activity.Read(ctx, account.ID)
	if !ok {
		return "", false
	}
	account, err := s.quotaEstimateAccount(ctx, account)
	if err != nil {
		return "", false
	}
	return quotaEstimateBasis(account, stamp), true
}

// Spark consumes a separate upstream window but bills the parent credential's
// current account rate. Keep the child usage-log identity while binding its
// calibration to the same principal and rate used at billing time.
func (s *OpenAIQuotaService) quotaEstimateAccount(ctx context.Context, account *Account) (*Account, error) {
	if !account.IsShadow() {
		return account, nil
	}
	parent, err := resolveCredentialAccount(ctx, s.accountRepo, account)
	if err != nil {
		return nil, err
	}
	copy := *account
	copy.Credentials, copy.RateMultiplier = parent.Credentials, parent.RateMultiplier
	return &copy, nil
}
