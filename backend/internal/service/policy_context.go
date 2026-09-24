package service

import "context"

type policyCancellationSignalsKey struct{}

// Policy cancellation is independent from a client disconnect. Upstream IO
// may detach from that disconnect, but must still stop on native policy replacement.
func detachPolicyContext(parent context.Context) (context.Context, context.CancelFunc) {
	signals, _ := parent.Value(policyCancellationSignalsKey{}).([]context.Context)
	signals = append(append([]context.Context(nil), signals...), nativeCodexPolicySignals(parent)...)
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
