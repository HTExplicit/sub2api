package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

type observedRuntimeLeaseFixture struct {
	done        chan struct{}
	finishOnce  sync.Once
	releaseOnce sync.Once
	mu          sync.RWMutex
	err         error
	onRelease   func()
	releases    atomic.Int32
}

func newObservedRuntimeLeaseFixture() *observedRuntimeLeaseFixture {
	return &observedRuntimeLeaseFixture{done: make(chan struct{})}
}
func (l *observedRuntimeLeaseFixture) Done() <-chan struct{} { return l.done }
func (l *observedRuntimeLeaseFixture) Err() error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.err
}
func (l *observedRuntimeLeaseFixture) finish(err error) {
	l.finishOnce.Do(func() {
		l.mu.Lock()
		l.err = err
		close(l.done)
		l.mu.Unlock()
	})
}
func (l *observedRuntimeLeaseFixture) Release() {
	l.releaseOnce.Do(func() {
		l.releases.Add(1)
		if l.onRelease != nil {
			l.onRelease()
		}
		l.finish(nil)
	})
}

type observedRuntimeLeaseRepository struct {
	*hostIOAdmissionRepository
	hold          func(context.Context, *PluginInstallation) (PluginRuntimeLease, error)
	observedCalls int
	legacyCalls   int
}

func (r *observedRuntimeLeaseRepository) HoldObservedPluginRuntime(ctx context.Context, installation *PluginInstallation) (PluginRuntimeLease, error) {
	r.observedCalls++
	return r.hold(ctx, installation)
}
func (r *observedRuntimeLeaseRepository) HoldPluginRuntime(ctx context.Context, installation *PluginInstallation) (func(), error) {
	r.legacyCalls++
	return r.hostIOAdmissionRepository.HoldPluginRuntime(ctx, installation)
}

var _ PluginRepository = (*observedRuntimeLeaseRepository)(nil)
var _ ObservedPluginRuntimeLocker = (*observedRuntimeLeaseRepository)(nil)

func waitRuntimeLeaseSignal(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("lease notification listener did not finish")
	}
}

func TestObservedHostIOLeaseLossCancelsDetachedPolicyNotBilling(t *testing.T) {
	manager, legacy, runtime := hostIOAdmissionManager(t)
	lease := newObservedRuntimeLeaseFixture()
	repo := &observedRuntimeLeaseRepository{hostIOAdmissionRepository: legacy}
	repo.hold = func(ctx context.Context, installation *PluginInstallation) (PluginRuntimeLease, error) {
		release, err := legacy.HoldPluginRuntime(ctx, installation)
		lease.onRelease = release
		return lease, err
	}
	manager.repo = repo
	bound, release, err := manager.BindOperationContext(context.Background(), PlatformOpenAI, AccountTypeAPIKey, extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "host.io", AccountID: 42})
	require.NoError(t, err)
	t.Cleanup(release)
	detached, stopDetached := detachUpstreamContext(bound)
	defer stopDetached()
	billing, stopBilling := detachedBillingContext(bound)
	defer stopBilling()
	require.Equal(t, 1, legacy.businessHolds)
	require.False(t, PluginBusinessIOLeaseRequired(bound))
	lease.finish(ErrPluginRuntimeLeaseLost)
	waitRuntimeLeaseSignal(t, bound.Done())
	waitRuntimeLeaseSignal(t, detached.Done())
	require.ErrorIs(t, bound.Err(), context.Canceled)
	require.ErrorIs(t, detached.Err(), context.Canceled)
	require.NoError(t, billing.Err(), "already incurred usage still gets its detached billing cleanup")
	require.Equal(t, 1, legacy.active, "loss notification cancels work but does not run its cleanup prematurely")
	require.Equal(t, int64(1), runtime.inFlight.Load())
	release()
	release()
	require.Zero(t, legacy.active)
	require.Zero(t, runtime.inFlight.Load())
	require.Equal(t, int32(1), lease.releases.Load())
	require.Equal(t, 1, repo.observedCalls)
	require.Zero(t, repo.legacyCalls, "production observed capability is preferred over the old port")
}

func TestObservedProcessLeaseLossDrainsBeforeRuntimeCleanup(t *testing.T) {
	runtime := &pluginRuntime{done: make(chan struct{})}
	policy, releasePolicy, err := runtime.bindPolicyContext(context.Background())
	require.NoError(t, err)
	defer releasePolicy()
	detached, stopDetached := detachUpstreamContext(policy)
	defer stopDetached()
	lease := newObservedRuntimeLeaseFixture()
	var drainedBeforeRelease atomic.Bool
	lease.onRelease = func() {
		drainedBeforeRelease.Store(runtime.draining.Load() && policy.Err() != nil)
	}
	require.NoError(t, runtime.observeLease(lease))
	lease.finish(ErrPluginRuntimeLeaseLost)
	waitRuntimeLeaseSignal(t, runtime.leaseWatchDone)
	waitRuntimeLeaseSignal(t, detached.Done())
	require.True(t, drainedBeforeRelease.Load())
	require.True(t, runtime.draining.Load())
	require.False(t, runtime.beginRequest())
	runtime.kill()
	require.Equal(t, int32(1), lease.releases.Load())
}

func TestObservedLeaseNormalReleaseEndsListenerWithoutFalseLoss(t *testing.T) {
	lease := newObservedRuntimeLeaseFixture()
	var lost atomic.Bool
	finished := watchPluginRuntimeLease(lease, func() { lost.Store(true) })
	lease.Release()
	waitRuntimeLeaseSignal(t, finished)
	require.False(t, lost.Load())
	require.NoError(t, lease.Err())
}

func TestObservedProcessLeaseLostBeforeAttachmentRejectsRuntime(t *testing.T) {
	lease := newObservedRuntimeLeaseFixture()
	lease.finish(ErrPluginRuntimeLeaseLost)
	runtime := &pluginRuntime{done: make(chan struct{})}
	err := runtime.observeLease(lease)
	require.ErrorIs(t, err, ErrPluginRuntimeLeaseLost)
	require.True(t, runtime.draining.Load(), "a lease lost during process startup cannot certify publication")
	require.False(t, runtime.beginRequest())
	require.Equal(t, int32(1), lease.releases.Load())
	require.Nil(t, runtime.leaseWatchDone, "no extra listener is retained after startup rejection")
}

func TestObservedLeaseFailureNeverFallsBackToReleaseOnlyPort(t *testing.T) {
	for _, mode := range []string{"error", "nil", "unobservable", "already_lost"} {
		t.Run(mode, func(t *testing.T) {
			_, legacy, _ := hostIOAdmissionManager(t)
			fixture := newObservedRuntimeLeaseFixture()
			repo := &observedRuntimeLeaseRepository{hostIOAdmissionRepository: legacy}
			repo.hold = func(context.Context, *PluginInstallation) (PluginRuntimeLease, error) {
				switch mode {
				case "error":
					return nil, errors.New("observer acquisition failed")
				case "nil":
					return nil, nil
				case "unobservable":
					return &pluginReleaseOnlyLease{release: fixture.Release}, nil
				default:
					fixture.finish(ErrPluginRuntimeLeaseLost)
					return fixture, nil
				}
			}
			lease, err := acquirePluginRuntimeLease(context.Background(), repo, legacy.current)
			require.Error(t, err)
			require.Nil(t, lease)
			require.Equal(t, 1, repo.observedCalls)
			require.Zero(t, repo.legacyCalls)
			if mode == "unobservable" || mode == "already_lost" {
				require.Equal(t, int32(1), fixture.releases.Load())
			}
		})
	}
}
