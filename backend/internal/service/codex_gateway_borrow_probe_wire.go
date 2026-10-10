package service

import "context"

// The pinned source and two-shot STATE protocols send plaintext JSON. They are
// independent of the user's business-request compression setting. Only an
// explicit administrator encoding comparison may opt into the configured codec.
type codexBorrowProbeConfiguredEncodingKey struct{}

func withCodexBorrowPlainProbe(ctx context.Context) context.Context {
	if _, set := ctx.Value(codexBorrowProbeConfiguredEncodingKey{}).(bool); set {
		return ctx
	}
	return context.WithValue(ctx, codexBorrowProbeConfiguredEncodingKey{}, false)
}
