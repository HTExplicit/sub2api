package service

import (
	"context"
	"sync"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestCindyProbeAPIReportsDisabledAndFailedProviderDistinctly(t *testing.T) {
	previous := captureNativeCindyTestInvoker()
	t.Cleanup(func() { restoreNativeCindyTestInvoker(previous) })
	setNativeCindyTestInvoker(nil)
	err := requireCindyBalanceProbePolicy(context.Background())
	var apiError *infraerrors.ApplicationError
	require.ErrorAs(t, err, &apiError)
	require.EqualValues(t, 404, apiError.Code)
	setNativeCindyTestInvoker(failedPromptProcess{})
	err = requireCindyBalanceProbePolicy(context.Background())
	require.ErrorAs(t, err, &apiError)
	require.EqualValues(t, 503, apiError.Code)
}

type disabledCindyProbeFixture struct {
	seen chan struct{}
	once sync.Once
}

func (f *disabledCindyProbeFixture) InvokeOperation(_ context.Context, _, _ string, in extensionv1.Invocation) (extensionv1.Result, error) {
	if in.Operation == "cindy.probe.plan" {
		f.once.Do(func() { close(f.seen) })
	}
	return extensionv1.Result{}, ErrExtensionOperationDisabled
}

type cindyProbeLifecycleRepository struct {
	CindyBalanceProbeRepository
	claimCalls       int
	recoveredContext context.Context
	stop             func()
}

func (r *cindyProbeLifecycleRepository) ClaimJob(context.Context, string, time.Time) (*CindyBalanceProbeJob, error) {
	r.claimCalls++
	return nil, nil
}

func (r *cindyProbeLifecycleRepository) RecoverInterruptedItems(ctx context.Context, _ int64, _ string) error {
	r.recoveredContext = ctx
	r.stop()
	return nil
}

func TestCindyProbeDisabledPolicyDoesNotClaimJobsOrDeleteHistory(t *testing.T) {
	previous := captureNativeCindyTestInvoker()
	t.Cleanup(func() { restoreNativeCindyTestInvoker(previous) })
	fixture := &disabledCindyProbeFixture{seen: make(chan struct{})}
	setNativeCindyTestInvoker(fixture)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo := &cindyProbeLifecycleRepository{}
	svc := &CindyBalanceProbeService{ctx: ctx, repo: repo, now: time.Now}
	svc.wg.Add(1)
	go svc.run()
	select {
	case <-fixture.seen:
	case <-time.After(time.Second):
		t.Fatal("worker did not check the active provider")
	}
	cancel()
	svc.wg.Wait()
	require.Zero(t, repo.claimCalls)
	_, err := svc.Resume(context.Background(), 17)
	require.ErrorIs(t, err, ErrExtensionOperationDisabled)
}

func TestCindyProbeActiveJobContextEndsWhenNativeParentStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo := &cindyProbeLifecycleRepository{stop: cancel}
	svc := &CindyBalanceProbeService{ctx: ctx, repo: repo, now: time.Now}
	svc.processJob(&CindyBalanceProbeJob{ID: 17}, "fixture-lease")
	require.NotNil(t, repo.recoveredContext)
	require.ErrorIs(t, repo.recoveredContext.Err(), context.Canceled)
}
