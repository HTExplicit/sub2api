//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestInternalRateConversionBatchSnapshotAndLegacyJobs(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, _, _ := newTestBatchImagePublicService(true)
	old, err := svc.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "before-conversion")
	require.NoError(t, err)
	require.Equal(t, 0.25, old.EstimatedCost)
	require.Equal(t, 1, repo.jobs[old.ID].PricingSnapshotVersion)

	// Missing setting is the production default: enabled.
	svc.Settings = NewSettingService(&internalRateSettingRepo{values: map[string]string{}}, &config.Config{})
	current, err := svc.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "after-conversion")
	require.NoError(t, err)
	job := repo.jobs[current.ID]
	require.Equal(t, 2, job.PricingSnapshotVersion)
	require.Equal(t, 1.6820625, current.EstimatedCost)
	require.Equal(t, 2.018475, *job.HoldAmount)
	require.Equal(t, 0.25, job.BaseUnitPrice)
	require.Equal(t, 1.0, job.GroupRateMultiplier)
	require.Equal(t, 1.0, job.AccountRateMultiplier)

	// Turn off after creation: an idempotent resubmit and settlement use the stored snapshot.
	svc.Settings.refreshInternalRateConversionCache(false)
	replayed, err := svc.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "after-conversion")
	require.NoError(t, err)
	require.Equal(t, current.ID, replayed.ID)
	require.Equal(t, current.EstimatedCost, replayed.EstimatedCost)
	billing := svc.BillingRepo.(*fakeBatchImageBillingRepo)
	require.Len(t, billing.reserves, 2)
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	settlement := &BatchImageSettlementService{Repo: repo, BillingRepo: billing, UsageLogRepo: logs,
		Pricing: &fakeBatchImagePricingResolver{unitPrice: 99}}
	job.Status, job.SuccessCount, job.FailCount = BatchImageJobStatusSettling, 1, 1
	result, err := settlement.Settle(ctx, current.ID)
	require.NoError(t, err)
	require.Equal(t, 0.84103125, result.ActualCost)
	require.Equal(t, result.ActualCost, billing.captures[0].ActualAmount)
	require.Equal(t, result.ActualCost, logs.lastLog.ActualCost)
	require.Equal(t, 0.125, logs.lastLog.TotalCost, "raw account/statistical cost remains unconverted")
	require.Equal(t, 0.5, logs.lastLog.RateMultiplier)
	result, err = settlement.Settle(ctx, current.ID)
	require.NoError(t, err)
	require.True(t, result.AlreadySettled)
	require.Len(t, billing.captures, 1)

	svc.Settings.refreshInternalRateConversionCache(true)
	oldJob := repo.jobs[old.ID]
	oldJob.Status, oldJob.SuccessCount, oldJob.FailCount = BatchImageJobStatusSettling, 1, 1
	result, err = settlement.Settle(ctx, old.ID)
	require.NoError(t, err)
	require.Equal(t, 0.125, result.ActualCost, "existing jobs never receive retrospective conversion")
}

func TestInternalRateConversionBatchMonetaryPrecision(t *testing.T) {
	svc, repo, _, _, _ := newTestBatchImagePublicService(true)
	svc.Settings = nil // enabled by default
	svc.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.1234567891}
	batch, err := svc.Submit(context.Background(), testBatchImageOwner(), validBatchImageSubmitRequest(), "precision")
	require.NoError(t, err)
	job := repo.jobs[batch.ID]
	job.Status, job.SuccessCount = BatchImageJobStatusSettling, 2
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	settlement := &BatchImageSettlementService{Repo: repo, BillingRepo: svc.BillingRepo, UsageLogRepo: logs, Pricing: svc.Pricing}
	result, err := settlement.Settle(context.Background(), batch.ID)
	require.NoError(t, err)
	capture := svc.BillingRepo.(*fakeBatchImageBillingRepo).captures[0]
	require.Equal(t, batch.EstimatedCost, result.ActualCost)
	require.Equal(t, result.ActualCost, capture.ActualAmount)
	require.Equal(t, result.ActualCost, logs.lastLog.ActualCost)
	require.Equal(t, result.ActualCost, QuantizeUsageBillingAmount(result.ActualCost))
}
