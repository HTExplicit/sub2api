package service

import (
	"context"
	"errors"
	"sync"
)

const (
	PluginStateUpdating = "updating"
	PluginUpdateBundled = "bundled"
	PluginUpdatePinned  = "pinned"
)

var ErrPluginUpdateWaiting = errors.New("previous plugin processes are still draining")
var ErrPluginRuntimeLeaseLost = errors.New("plugin runtime lease session lost")

// Separate from the original repository port so upstream implementations and
// contract fixtures do not accidentally claim support for independent updates.
type PluginUpdateRepository interface {
	StagePluginUpdate(context.Context, *PluginInstallation, *PluginInstallation, string) error
	PendingPluginArtifact(context.Context, int64) ([]byte, error)
	CommitPluginUpdate(context.Context, *PluginInstallation, *PluginInstallation) error
	SetPluginUpdatePolicy(context.Context, int64, int64, string) error
}

type PluginRuntimeLocker interface {
	HoldPluginRuntime(context.Context, *PluginInstallation) (func(), error)
}

// Done closes on either deliberate release (Err is nil) or observed session
// loss. Release is idempotent and waits only for the connection owner's cleanup,
// never for callers that may themselves react to Done by calling Release.
type PluginRuntimeLease interface {
	Done() <-chan struct{}
	Err() error
	Release()
}

// Keep the legacy port for existing repositories/fixtures. Production provides
// this stronger optional port; its failure must never select the weaker port.
type ObservedPluginRuntimeLocker interface {
	HoldObservedPluginRuntime(context.Context, *PluginInstallation) (PluginRuntimeLease, error)
}

type pluginReleaseOnlyLease struct {
	release func()
	once    sync.Once
}

func (*pluginReleaseOnlyLease) Done() <-chan struct{} { return nil }
func (*pluginReleaseOnlyLease) Err() error            { return nil }
func (l *pluginReleaseOnlyLease) Release()            { l.once.Do(l.release) }

func acquirePluginRuntimeLease(ctx context.Context, repo PluginRepository, installation *PluginInstallation) (PluginRuntimeLease, error) {
	if observer, ok := repo.(ObservedPluginRuntimeLocker); ok {
		lease, err := observer.HoldObservedPluginRuntime(ctx, installation)
		if err != nil {
			if lease != nil {
				lease.Release()
			}
			return nil, err
		}
		if lease == nil || lease.Done() == nil {
			if lease != nil {
				lease.Release()
			}
			return nil, ErrPluginRuntimeLeaseLost
		}
		select {
		case <-lease.Done():
			err = lease.Err()
			lease.Release()
			if err == nil {
				err = ErrPluginRuntimeLeaseLost
			}
			return nil, err
		default:
			return lease, nil
		}
	}
	if locker, ok := repo.(PluginRuntimeLocker); ok {
		release, err := locker.HoldPluginRuntime(ctx, installation)
		if err != nil {
			return nil, err
		}
		if release == nil {
			return nil, ErrPluginRuntimeLeaseLost
		}
		return &pluginReleaseOnlyLease{release: release}, nil
	}
	return nil, nil
}

// The listener does not own or release the lease. In particular, onLoss may
// call runtime.kill/Release without a listener/connection-owner wait cycle.
func watchPluginRuntimeLease(lease PluginRuntimeLease, onLoss func()) <-chan struct{} {
	finished := make(chan struct{})
	if lease == nil || lease.Done() == nil {
		close(finished)
		return finished
	}
	go func() {
		defer close(finished)
		<-lease.Done()
		if lease.Err() != nil {
			onLoss()
		}
	}()
	return finished
}

type pluginBusinessIOLeaseKey struct{}

// WithPluginBusinessIOLease marks a host policy admission, not the lifecycle
// lease of a process used for startup or disabled-plugin config diagnostics.
// This private context key is never decoded from a plugin or HTTP payload.
func WithPluginBusinessIOLease(ctx context.Context) context.Context {
	return context.WithValue(ctx, pluginBusinessIOLeaseKey{}, true)
}

func PluginBusinessIOLeaseRequired(ctx context.Context) bool {
	required, _ := ctx.Value(pluginBusinessIOLeaseKey{}).(bool)
	return required
}

type pluginExecutionKey struct{}
type PluginExecution struct{ ID, Generation int64 }

// Only the authenticated host broker adds this fence. It is never decoded from
// a plugin payload, including when plugins use public storage operations.
func WithPluginExecution(ctx context.Context, installation *PluginInstallation) context.Context {
	return context.WithValue(ctx, pluginExecutionKey{}, PluginExecution{installation.ID, installation.RuntimeGeneration})
}
func PluginExecutionFromContext(ctx context.Context) (PluginExecution, bool) {
	execution, ok := ctx.Value(pluginExecutionKey{}).(PluginExecution)
	return execution, ok
}

type pluginRevisionKey struct{}

func WithPluginExpectedRevision(ctx context.Context, revision int64) context.Context {
	return context.WithValue(ctx, pluginRevisionKey{}, revision)
}
func PluginExpectedRevision(ctx context.Context) int64 {
	revision, _ := ctx.Value(pluginRevisionKey{}).(int64)
	return revision
}

// Preserve exact existing bindings; newly declared capabilities start disabled.
func PluginReplacementBindings(previous []PluginBinding, manifest PluginManifest) []PluginBinding {
	bindings := make([]PluginBinding, 0, len(manifest.Capabilities))
	for _, capability := range manifest.SortedCapabilities() {
		binding := PluginBinding{Capability: capability.ID, Platform: capability.Platform, AccountType: capability.AccountType, RolloutPercent: 100}
		for _, old := range previous {
			if old.Capability == binding.Capability && old.Platform == binding.Platform && old.AccountType == binding.AccountType {
				binding.Enabled, binding.RolloutPercent = old.Enabled, old.RolloutPercent
				break
			}
		}
		bindings = append(bindings, binding)
	}
	return bindings
}
