package service

import "context"

type accountObservationContextKey struct{}

// WithAccountObservation marks an administrator's account invocation as
// observation-only. Outbound requests, compatibility handling and credential
// refresh still run normally; account health, quota and billing observations
// must not be published by this invocation.
func WithAccountObservation(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, accountObservationContextKey{}, true)
}

func IsAccountObservation(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	marked, _ := ctx.Value(accountObservationContextKey{}).(bool)
	return marked
}

// CopyAccountObservationContext preserves the mode when a callback needs a
// separate lifecycle. Copy before starting a goroutine or queueing a task:
// replacing a request context with Background alone loses the write boundary.
func CopyAccountObservationContext(parent, base context.Context) context.Context {
	if IsAccountObservation(parent) {
		return WithAccountObservation(base)
	}
	return base
}
