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
}

func (r *quotaMeasurementRepo) GetByID(context.Context, int64) (*Account, error) {
	return &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "owner"}}, nil
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

type quotaMeasurementConcurrency struct {
	ConcurrencyCache
	active int
}

func (c *quotaMeasurementConcurrency) GetAccountConcurrencyBatch(context.Context, []int64) (map[int64]int, error) {
	return map[int64]int{1: c.active}, nil
}

func TestQuotaMeasurementRejectsInFlightAndLateBills(t *testing.T) {
	ctx := context.Background()
	repo := &quotaMeasurementRepo{point: QuotaCostPoint{Cursor: 4, Requests: 4, Cost: 10}}
	cache := &quotaMeasurementConcurrency{}
	pool := NewUsageRecordWorkerPoolWithOptions(UsageRecordWorkerPoolOptions{WorkerCount: 1, QueueSize: 4})
	t.Cleanup(pool.Stop)
	svc := &OpenAIQuotaService{accountRepo: repo, concurrency: NewConcurrencyService(cache), usagePool: pool}
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
	cache.active = 1
	svc.finishQuotaMeasurement(ctx, 1, usage, before)
	require.Empty(t, repo.observations)
	cache.active = 0
	before = svc.beginQuotaMeasurement(ctx, 1)
	svc.finishQuotaMeasurement(ctx, 1, usage, before)
	require.Len(t, repo.observations, 1)
	require.Equal(t, 70.0, repo.observations[0].Utilization)
	require.Equal(t, 11.0, repo.observations[0].Cost)
}
