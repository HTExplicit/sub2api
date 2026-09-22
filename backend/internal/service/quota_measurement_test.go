package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type quotaMeasurementRepo struct {
	AccountRepository
	point        QuotaCostPoint
	observations []QuotaEstimateObservation
	accounts     map[int64]*Account
}

func (r *quotaMeasurementRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	if r.accounts != nil {
		return r.accounts[id], nil
	}
	return &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "owner"}}, nil
}

func TestQuotaMeasurementUsesShadowWindowAndParentBillingIdentity(t *testing.T) {
	ctx := context.Background()
	parentID := int64(1)
	rate := 2.0
	parent := &Account{ID: parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "parent-owner"}, RateMultiplier: &rate}
	shadow := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parentID}
	repo := &quotaMeasurementRepo{accounts: map[int64]*Account{1: parent, 2: shadow}, point: QuotaCostPoint{Cost: 10}}
	svc := &OpenAIQuotaService{accountRepo: repo, activity: NewQuotaActivityService(&quotaActivityMemoryStore{})}
	now := time.Now().UTC()
	window := func(used float64) *OpenAIRateLimit {
		return &OpenAIRateLimit{PrimaryWindow: &OpenAIRateLimitWindow{UsedPercent: used, LimitWindowSeconds: 18000, ResetAt: now.Add(time.Hour).Unix()}}
	}
	usage := &OpenAIQuotaUsage{FetchedAt: now.Unix(), RateLimit: window(99), AdditionalRateLimits: []OpenAIAdditionalRateLimit{{MeteredFeature: "codex_bengalfox", RateLimit: window(25)}}}
	before := svc.beginQuotaMeasurement(ctx, shadow.ID)
	require.NotNil(t, before)
	svc.finishQuotaMeasurement(ctx, shadow.ID, usage, before)
	require.Len(t, repo.observations, 1)
	require.Equal(t, 25.0, repo.observations[0].Utilization)
	rate = 3
	svc.finishQuotaMeasurement(ctx, shadow.ID, usage, before)
	require.Len(t, repo.observations, 1, "a parent rate change invalidates the pairing")
	before = svc.beginQuotaMeasurement(ctx, shadow.ID)
	parent.Credentials["chatgpt_account_id"] = "new-owner"
	svc.finishQuotaMeasurement(ctx, shadow.ID, usage, before)
	require.Len(t, repo.observations, 1, "a parent identity change invalidates the pairing")
}
func (r *quotaMeasurementRepo) QuotaCostPoint(context.Context, int64) (QuotaCostPoint, error) {
	return r.point, nil
}
func (r *quotaMeasurementRepo) ObserveQuotaEstimate(_ context.Context, point QuotaEstimateObservation) error {
	r.observations = append(r.observations, point)
	return nil
}
func (r *quotaMeasurementRepo) ReadQuotaEstimate(context.Context, int64, string, string) (*QuotaEstimateObservation, *QuotaEstimateObservation, error) {
	return nil, nil, nil
}

func TestQuotaMeasurementRejectsInFlightAndLateBills(t *testing.T) {
	ctx := context.Background()
	repo := &quotaMeasurementRepo{point: QuotaCostPoint{Cursor: 4, Requests: 4, Cost: 10}}
	cache := &quotaActivityMemoryStore{}
	svc := &OpenAIQuotaService{accountRepo: repo, activity: NewQuotaActivityService(cache)}
	now := time.Now().UTC()
	usage := &OpenAIQuotaUsage{FetchedAt: now.Unix(), RateLimit: &OpenAIRateLimit{PrimaryWindow: &OpenAIRateLimitWindow{UsedPercent: 70, LimitWindowSeconds: 2592000, ResetAt: now.Add(time.Hour).Unix()}}}
	before := svc.beginQuotaMeasurement(ctx, 1)
	require.NotNil(t, before)
	repo.point.Cursor++
	repo.point.Requests++
	repo.point.Cost++
	svc.finishQuotaMeasurement(ctx, 1, usage, before)
	require.Empty(t, repo.observations, "a bill committed during the upstream read cannot be paired with that read")
	before = svc.beginQuotaMeasurement(ctx, 1)
	require.NoError(t, cache.Begin(ctx, 1, "request"))
	svc.finishQuotaMeasurement(ctx, 1, usage, before)
	require.Empty(t, repo.observations)
	require.NoError(t, cache.Finish(ctx, 1, "request", true))
	before = svc.beginQuotaMeasurement(ctx, 1)
	require.NoError(t, cache.Begin(ctx, 1, "short-request"))
	require.NoError(t, cache.Finish(ctx, 1, "short-request", true))
	svc.finishQuotaMeasurement(ctx, 1, usage, before)
	require.Empty(t, repo.observations, "a request entirely inside the observation interval must also invalidate the pair")
	before = svc.beginQuotaMeasurement(ctx, 1)
	svc.finishQuotaMeasurement(ctx, 1, usage, before)
	require.Len(t, repo.observations, 1)
	require.Equal(t, 70.0, repo.observations[0].Utilization)
	require.Equal(t, 11.0, repo.observations[0].Cost)
}
