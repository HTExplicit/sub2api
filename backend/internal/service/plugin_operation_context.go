package service

import (
	"context"
	"sync"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// Host IO executed under a plugin policy must stop when that policy is
// disabled, replaced, or reconfigured. Persistent results stay host-owned.
type extensionOperationContextBinder interface {
	BindOperationContext(context.Context, string, string, extensionv1.Invocation) (context.Context, context.CancelFunc, error)
}

type pluginPolicySignalsKey struct{}

// Policy cancellation is independent from a client disconnect. Upstream IO
// may detach from that disconnect, but must still stop on plugin replacement.
func detachPluginPolicyContext(parent context.Context) (context.Context, context.CancelFunc) {
	signals, _ := parent.Value(pluginPolicySignalsKey{}).([]context.Context)
	if len(signals) == 0 {
		return context.WithoutCancel(parent), func() {}
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	stops := make([]func() bool, 0, len(signals))
	for _, signal := range signals {
		if signal.Err() != nil {
			cancel()
		}
		stops = append(stops, context.AfterFunc(signal, cancel))
	}
	return ctx, func() {
		for _, stop := range stops {
			stop()
		}
		cancel()
	}
}

func bindProcessExtensionContext(ctx context.Context, platform, accountType string, in extensionv1.Invocation) (context.Context, context.CancelFunc, error) {
	provider := processExtensionOperations.Load()
	if provider == nil {
		return nil, nil, ErrExtensionOperationDisabled
	}
	if binder, ok := provider.invoker.(extensionOperationContextBinder); ok {
		return binder.BindOperationContext(ctx, platform, accountType, in)
	}
	// In-process contract fixtures use only the caller's cancellation lifetime.
	bound, cancel := context.WithCancel(ctx)
	return bound, cancel, nil
}

func (m *PluginManager) BindOperationContext(ctx context.Context, platform, accountType string, in extensionv1.Invocation) (context.Context, context.CancelFunc, error) {
	id, registry, err := m.operationOwner(platform, accountType, in)
	if err != nil {
		return nil, nil, err
	}
	runtime := registry.runtimes[id]
	if runtime == nil || runtime.client.Exited() || !pluginDependenciesHealthy(registry.installations[id], registry, map[int64]bool{}) {
		return nil, nil, ErrExtensionOperationUnavailable
	}
	return runtime.bindPolicyContext(ctx)
}

func (r *pluginRuntime) bindPolicyContext(parent context.Context) (context.Context, context.CancelFunc, error) {
	r.policyMu.Lock()
	defer r.policyMu.Unlock()
	if r.draining.Load() || r.configuring.Load() {
		return nil, nil, ErrExtensionOperationUnavailable
	}
	ctx, cancel := context.WithCancel(parent)
	signal, stopPolicy := context.WithCancel(context.Background())
	signals, _ := parent.Value(pluginPolicySignalsKey{}).([]context.Context)
	ctx = context.WithValue(ctx, pluginPolicySignalsKey{}, append(append([]context.Context{}, signals...), signal))
	stopPropagation := context.AfterFunc(signal, cancel)
	if r.policyLeases == nil {
		r.policyLeases = map[uint64]context.CancelFunc{}
	}
	r.policyLeaseID++
	id := r.policyLeaseID
	r.policyLeases[id] = func() { stopPolicy(); cancel() }
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			stopPropagation()
			stopPolicy()
			cancel()
			r.policyMu.Lock()
			delete(r.policyLeases, id)
			r.policyMu.Unlock()
		})
	}, nil
}

func (r *pluginRuntime) cancelPolicyContexts() {
	r.policyMu.Lock()
	defer r.policyMu.Unlock()
	for _, cancel := range r.policyLeases {
		cancel()
	}
	clear(r.policyLeases)
}

func (r *pluginRuntime) beginDrain() {
	r.draining.Store(true)
	r.cancelPolicyContexts()
}
