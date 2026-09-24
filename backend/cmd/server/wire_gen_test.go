package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestProvideServiceBuildInfo(t *testing.T) {
	in := handler.BuildInfo{
		Version:   "v-test",
		BuildType: "release",
	}
	out := provideServiceBuildInfo(in)
	require.Equal(t, in.Version, out.Version)
	require.Equal(t, in.BuildType, out.BuildType)
}

func TestProvideCleanup_WithMinimalDependencies_NoPanic(t *testing.T) {
	cleanup := minimalDependencyCleanup(nil)
	require.NotPanics(t, cleanup)
}

func minimalDependencyCleanup(autoReset *service.OpenAIQuotaAutoResetService) func() {
	cfg := &config.Config{}

	oauthSvc := service.NewOAuthService(nil, nil)
	openAIOAuthSvc := service.NewOpenAIOAuthService(nil, nil)
	geminiOAuthSvc := service.NewGeminiOAuthService(nil, nil, nil, nil, cfg)
	antigravityOAuthSvc := service.NewAntigravityOAuthService(nil)

	tokenRefreshSvc := service.NewTokenRefreshService(
		nil,
		oauthSvc,
		openAIOAuthSvc,
		geminiOAuthSvc,
		antigravityOAuthSvc,
		nil,
		nil,
		cfg,
		nil,
	)
	accountExpirySvc := service.NewAccountExpiryService(nil, time.Second)
	codexVersionSyncSvc := service.NewOpenAICodexVersionSyncService(nil, nil, nil, time.Second)
	claudeCodeVersionSyncSvc := service.NewClaudeCodeVersionSyncService(nil, nil, nil, time.Second)
	proxyExpirySvc := service.NewProxyExpiryService(nil, time.Second)
	subscriptionExpirySvc := service.NewSubscriptionExpiryService(nil, time.Second)
	pricingSvc := service.NewPricingService(cfg, nil)
	emailQueueSvc := service.NewEmailQueueService(nil, 1)
	billingCacheSvc := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	idempotencyCleanupSvc := service.NewIdempotencyCleanupService(nil, cfg)
	schedulerSnapshotSvc := service.NewSchedulerSnapshotService(nil, nil, nil, nil, cfg)
	opsSystemLogSinkSvc := service.NewOpsSystemLogSink(nil)

	return provideCleanup(
		nil, // entClient
		nil, // redis
		&service.OpsMetricsCollector{},
		&service.OpsAggregationService{},
		&service.OpsAlertEvaluatorService{},
		&service.OpsCleanupService{},
		&service.OpsScheduledReportService{},
		opsSystemLogSinkSvc,
		nil, // opsService
		nil, // opsIngressRejectAggregator
		nil, // apiKeyService
		nil, // authCacheInvalidationWorker
		schedulerSnapshotSvc,
		tokenRefreshSvc,
		accountExpirySvc,
		nil, // cnProviderBalanceCheck
		codexVersionSyncSvc,
		nil, // codexClientIdentityBackfill
		claudeCodeVersionSyncSvc,
		proxyExpirySvc,
		subscriptionExpirySvc,
		&service.UsageCleanupService{},
		idempotencyCleanupSvc,
		&service.BatchImageCleanupService{},
		nil, // batchImageWorker
		pricingSvc,
		emailQueueSvc,
		billingCacheSvc,
		&service.UsageRecordWorkerPool{},
		&service.SubscriptionService{},
		oauthSvc,
		openAIOAuthSvc,
		geminiOAuthSvc,
		antigravityOAuthSvc,
		nil, // grokOAuth
		nil, // openAIGateway
		nil, // scheduledTestRunner
		nil, // backupSvc
		nil, // paymentOrderExpiry
		nil, // channelMonitorRunner
		nil, // channelMonitorV2Aggregator
		nil, // quotaFlusher
		nil, // upstreamBillingProbe
		nil, // ollamaCloudUsage
		nil, // opencodeGoUsage
		nil, // auditLog
		autoReset,
		nil, // promptAudit
		nil, // promptDomain
		nil, // businessPrompt
		nil, // remoteSkillRegistry
		nil, // accountJobRuntime
		nil, // cindyHealth
		nil, // cindyBalanceProbe
		nil, // imageStudioRuntime
		nil, // pluginManager
		nil, // nativeCodexRuntime
	)
}

type cleanupAutoResetAccountRepository struct {
	service.AccountRepository
	started chan context.Context
}

func (r *cleanupAutoResetAccountRepository) GetByID(ctx context.Context, _ int64) (*service.Account, error) {
	r.started <- ctx
	<-ctx.Done()
	return nil, ctx.Err()
}

func (*cleanupAutoResetAccountRepository) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string, string, int64, string) ([]service.Account, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

type cleanupAutoResetQuota struct{ t *testing.T }

func (q *cleanupAutoResetQuota) QueryUsage(context.Context, int64) (*service.OpenAIQuotaUsage, error) {
	q.t.Error("cleanup fixture must not query an upstream quota")
	return nil, errors.New("unexpected quota query")
}

func (q *cleanupAutoResetQuota) CacheResetCreditsSnapshot(context.Context, int64, *service.OpenAIRateLimitResetCredits) error {
	q.t.Error("cleanup fixture must not cache quota data")
	return errors.New("unexpected quota cache")
}

func (q *cleanupAutoResetQuota) CachePostResetSnapshot(context.Context, int64, *service.OpenAIQuotaUsage) error {
	q.t.Error("cleanup fixture must not cache quota data")
	return errors.New("unexpected quota cache")
}

func (q *cleanupAutoResetQuota) ResetCreditTargeted(context.Context, int64, string, string) (*service.OpenAIQuotaResetResult, error) {
	q.t.Error("cleanup fixture must not consume a reset credit")
	return nil, errors.New("unexpected quota reset")
}

func TestProvideCleanupStopsExistingQuotaAutoResetWorker(t *testing.T) {
	repo := &cleanupAutoResetAccountRepository{started: make(chan context.Context, 1)}
	autoReset := service.NewOpenAIQuotaAutoResetService(repo, &cleanupAutoResetQuota{t: t}, nil, &service.IdempotencyCoordinator{}, nil, nil, nil)
	autoReset.Start()
	t.Cleanup(autoReset.Stop)
	autoReset.Notify(17)
	var work context.Context
	select {
	case work = <-repo.started:
	case <-time.After(2 * time.Second):
		t.Fatal("the actual auto-reset worker did not reach the synthetic repository")
	}
	require.NoError(t, work.Err())
	minimalDependencyCleanup(autoReset)()
	require.ErrorIs(t, work.Err(), context.Canceled, "application cleanup must stop the existing upstream worker before infrastructure closes")
}
