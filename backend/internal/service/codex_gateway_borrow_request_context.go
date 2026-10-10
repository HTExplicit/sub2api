package service

import (
	"context"
	"net/http"
)

type codexBorrowServiceTierContextKey struct{}

// The finalized body owns service_tier. The routing hint intentionally omits
// default/auto/scale, so it cannot reconstruct every actual request condition.
// Keep the exact effective tier locally; never add an internal transport header.
func withCodexBorrowServiceTier(ctx context.Context, tier string) context.Context {
	return context.WithValue(ctx, codexBorrowServiceTierContextKey{}, normalizedOpenAIServiceTierValue(tier))
}

func codexBorrowRequestServiceTier(req *http.Request) string {
	if tier, ok := req.Context().Value(codexBorrowServiceTierContextKey{}).(string); ok {
		return tier
	}
	return borrowProbeServiceTier(req.Header) // Compatibility for existing manual templates.
}
