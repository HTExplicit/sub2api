package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

func TestCindyProbeAPIReportsDisabledAndFailedProviderDistinctly(t *testing.T) {
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	processExtensionOperations.Store(nil)
	err := requireCindyBalanceProbePolicy(context.Background())
	var apiError *infraerrors.ApplicationError
	require.ErrorAs(t, err, &apiError)
	require.EqualValues(t, 404, apiError.Code)
	processExtensionOperations.Store(&extensionOperationProvider{invoker: failedPromptProcess{}})
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
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	fixture := &disabledCindyProbeFixture{seen: make(chan struct{})}
	processExtensionOperations.Store(&extensionOperationProvider{invoker: fixture})
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

func TestCindyProbeActiveJobContextEndsWhenProviderStops(t *testing.T) {
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	manager := ticketTestManager(t, config.OpenAICodexTicketConfig{}, func(extensionv1.Invocation) (extensionv1.Result, error) {
		raw, _ := json.Marshal(extensionv1.CindyProbePlan{Models: [2]string{"fixture-a", "fixture-b"}, Input: "Reply OK.", MaxOutputTokens: 1})
		return extensionv1.Result{Payload: raw}, nil
	})
	registry := manager.extensions.Load()
	registry.installations[1].Bindings = []PluginBinding{{Capability: extensionv1.CapabilityProvider, Platform: PlatformCindy, AccountType: AccountTypeAPIKey, Enabled: true}}
	registry.installations[1].Manifest.Operations = map[string][]string{extensionv1.CapabilityProvider: {"cindy.probe.plan"}}
	processExtensionOperations.Store(&extensionOperationProvider{invoker: manager})
	repo := &cindyProbeLifecycleRepository{stop: registry.runtimes[1].beginDrain}
	svc := &CindyBalanceProbeService{ctx: context.Background(), repo: repo, now: time.Now}
	svc.processJob(&CindyBalanceProbeJob{ID: 17}, "fixture-lease")
	require.NotNil(t, repo.recoveredContext)
	require.ErrorIs(t, repo.recoveredContext.Err(), context.Canceled)
}
