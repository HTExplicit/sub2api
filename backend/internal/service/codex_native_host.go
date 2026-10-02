package service

import (
	"context"
	"sync"
)

// nativeCodexHost fences runtime invocations to one configuration epoch: bind
// holds the persistent runtime lease for a call, drain waits for the epoch's
// calls before a configuration change.
type nativeCodexHost struct {
	leaseMu   sync.Mutex
	leaseWork sync.WaitGroup
	closing   bool
	metadata  *NativeCodexMetadata
	repo      NativeCodexRepository
	epoch     context.Context
}
type nativeCodexLeaseBindingKey struct{}
type nativeCodexPolicySignalsKey struct{}

func nativeCodexPolicySignals(ctx context.Context) []context.Context {
	signals, _ := ctx.Value(nativeCodexPolicySignalsKey{}).([]context.Context)
	return signals
}

type nativeCodexLeaseBinding struct {
	metadata NativeCodexMetadata
	ctx      context.Context
}

func (h *nativeCodexHost) bind(parent context.Context) (context.Context, context.CancelFunc, error) {
	if h == nil || h.metadata == nil || h.repo == nil || h.epoch == nil || h.epoch.Err() != nil {
		return nil, nil, ErrNativeCodexRuntimeUnavailable
	}
	if wanted, ok := NativeCodexExecutionFromContext(parent); ok && wanted != *h.metadata {
		return nil, nil, ErrNativeCodexRuntimeChanged
	}
	if existing, ok := parent.Value(nativeCodexLeaseBindingKey{}).(nativeCodexLeaseBinding); ok && existing.metadata == *h.metadata && existing.ctx.Err() == nil {
		ctx, cancel := context.WithCancel(parent)
		return ctx, cancel, nil
	}
	h.leaseMu.Lock()
	if h.closing {
		h.leaseMu.Unlock()
		return nil, nil, ErrNativeCodexRuntimeUnavailable
	}
	h.leaseWork.Add(1)
	h.leaseMu.Unlock()
	bound, cancel := context.WithCancel(WithNativeCodexExecution(parent, h.metadata))
	signal, stopSignal := context.WithCancel(context.Background())
	stopPropagation := context.AfterFunc(signal, cancel)
	stopEpoch := context.AfterFunc(h.epoch, stopSignal)
	signals := append(append([]context.Context(nil), nativeCodexPolicySignals(parent)...), signal)
	bound = context.WithValue(bound, nativeCodexPolicySignalsKey{}, signals)
	if h.epoch.Err() != nil {
		stopSignal()
		cancel()
	}
	lease, err := h.repo.HoldNativeCodexRuntime(WithNativeCodexBusinessIO(bound), h.metadata)
	if err != nil {
		stopEpoch()
		stopPropagation()
		stopSignal()
		cancel()
		h.leaseWork.Done()
		return nil, nil, err
	}
	if lease == nil || lease.Done() == nil {
		stopEpoch()
		stopPropagation()
		stopSignal()
		cancel()
		if lease != nil {
			lease.Release()
		}
		h.leaseWork.Done()
		return nil, nil, ErrNativeCodexRuntimeUnavailable
	}
	go func() {
		<-lease.Done()
		if lease.Err() != nil {
			stopSignal()
		}
	}()
	binding := nativeCodexLeaseBinding{metadata: *h.metadata, ctx: bound}
	bound = context.WithValue(bound, nativeCodexLeaseBindingKey{}, binding)
	var once sync.Once
	return bound, func() {
		once.Do(func() {
			stopEpoch()
			stopPropagation()
			stopSignal()
			cancel()
			lease.Release()
			h.leaseWork.Done()
		})
	}, nil
}

func (h *nativeCodexHost) drain(ctx context.Context) error {
	h.leaseMu.Lock()
	h.closing = true
	h.leaseMu.Unlock()
	done := make(chan struct{})
	go func() { h.leaseWork.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
