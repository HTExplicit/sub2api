package service

import (
	"context"
	"errors"
)

type openAIOfficialHTTPFailoverContextKey struct{}

// WithOpenAIOfficialHTTPFailover opts the ordinary HTTP handler into the
// upstream retry/state policy. Identity and managed-route checks remain at
// every use site so a shared handler cannot change Cindy or OAuth behavior.
func WithOpenAIOfficialHTTPFailover(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, openAIOfficialHTTPFailoverContextKey{}, true)
}

func IsOpenAIOfficialHTTPFailover(ctx context.Context, account *Account) bool {
	if ctx == nil || account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeAPIKey {
		return false
	}
	marked, _ := ctx.Value(openAIOfficialHTTPFailoverContextKey{}).(bool)
	if !marked || account.EffectiveProviderProfile() == ProviderProfileCindyLaxaV1 ||
		IsCindyRuntimeCompatibleAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
		return false
	}
	_, managed := ManagedModelRequestFromContext(ctx)
	return !managed
}

// ResolveOpenAIAccountUpstreamModelForRequest exposes the scheduler's exact
// mapping chain for handler-side outcome reporting, without changing routing.
func ResolveOpenAIAccountUpstreamModelForRequest(account *Account, requestedModel string, requireCompact bool) string {
	return resolveOpenAIAccountUpstreamModelForRequest(account, requestedModel, requireCompact)
}

// ReportOpenAIOfficialHTTPAccountScheduleResult preserves the upstream health
// observation and model-streak reset. Downstream probe leases are deliberately
// not touched: eligible API keys never own those OAuth/Cindy mechanisms.
func (s *OpenAIGatewayService) ReportOpenAIOfficialHTTPAccountScheduleResult(
	ctx context.Context,
	account *Account,
	model string,
	success bool,
	firstTokenMs *int,
	observedErr error,
) bool {
	if s == nil || !IsOpenAIOfficialHTTPFailover(ctx, account) {
		return false
	}
	if !success && (ctx.Err() != nil || errors.Is(observedErr, context.Canceled)) {
		return false
	}
	healthTripped := false
	if success {
		if s.rateLimitService != nil {
			s.rateLimitService.ObserveOpenAIAPIKeyHealthSuccess(context.Background(), account)
		}
		s.openaiOAuth429RetryStartedAt.Delete(account.ID)
		mu := s.openAIAccountRuntimeBlockLock(account.ID)
		mu.Lock()
		s.getOpenAIAccountModelTransientState().recordOfficialSuccess(account.ID, model)
		mu.Unlock()
	} else {
		healthTripped = s.ObserveOpenAIOfficialHTTPAccountHealthFailure(ctx, account, observedErr)
	}
	if scheduler := s.getOpenAIAccountScheduler(context.Background()); scheduler != nil {
		scheduler.ReportResult(account.ID, success, firstTokenMs)
	}
	return healthTripped
}

// ObserveOpenAIOfficialHTTPAccountHealthFailure is also used when semantic
// output has already committed, where observing health must not replay output.
func (s *OpenAIGatewayService) ObserveOpenAIOfficialHTTPAccountHealthFailure(ctx context.Context, account *Account, observedErr error) bool {
	if s == nil || s.rateLimitService == nil || observedErr == nil || !IsOpenAIOfficialHTTPFailover(ctx, account) {
		return false
	}
	if ctx.Err() != nil || errors.Is(observedErr, context.Canceled) {
		return false
	}
	var failoverErr *UpstreamFailoverError
	if errors.As(observedErr, &failoverErr) && failoverErr.SuppressAccountHealthPenalty {
		return false
	}
	return s.rateLimitService.ObserveOpenAIAPIKeyHealthFailure(context.WithoutCancel(ctx), account, observedErr)
}

// openAIHTTPPoolRetryable retains the existing pool policy outside the marked
// surface, and restores upstream's transient-processing exception inside it.
func openAIHTTPPoolRetryable(ctx context.Context, account *Account, statusCode int, upstreamMsg string, body []byte, shouldDisable bool) bool {
	return !shouldDisable && account != nil && account.IsPoolMode() &&
		(account.IsPoolModeRetryableStatus(statusCode) ||
			(IsOpenAIOfficialHTTPFailover(ctx, account) && isOpenAITransientProcessingError(statusCode, upstreamMsg, body)))
}
