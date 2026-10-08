package service

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/gin-gonic/gin"
)

const CodexGatewayBorrowPreparationFailureReason GatewayFailureReason = "codex_gateway_borrow_preparation_failed"

// The existing failover owner may select another account. A local preparation
// failure is not an inference attempt or evidence against the selected account.
func NewCodexGatewayBorrowRequestFailure(_ error) *UpstreamFailoverError {
	return &UpstreamFailoverError{
		StatusCode:                   http.StatusServiceUnavailable,
		ResponseBody:                 []byte(`{"error":{"type":"upstream_error","code":"CODEX_GATEWAY_BORROW_UNAVAILABLE","message":"no qualified Codex gateway borrow route is available"}}`),
		Scope:                        GatewayFailureScopeRequest,
		Reason:                       CodexGatewayBorrowPreparationFailureReason,
		NextAccountAction:            NextAccountRetry,
		RequestScopedTransient:       true,
		SuppressAccountHealthPenalty: true,
		SafeToFailoverAfterWrite:     true,
		ClientStatusCode:             http.StatusServiceUnavailable,
		ClientErrorCode:              "CODEX_GATEWAY_BORROW_UNAVAILABLE",
		ClientErrorType:              "upstream_error",
		ClientMessage:                "no qualified Codex gateway borrow route is available",
	}
}

func IsCodexGatewayBorrowRequestFailure(err error) bool {
	if IsCodexGatewayBorrowFailure(err) {
		return true
	}
	var failure *UpstreamFailoverError
	return errors.As(err, &failure) && failure.Reason == CodexGatewayBorrowPreparationFailureReason
}

// Record before converting to failover metadata: the administrator keeps the
// complete local/source cause, while exhausted client responses use their own
// existing error contract. No account or scheduler state is touched here.
func RecordCodexGatewayBorrowPreparationFailure(c *gin.Context, account *Account, err error) {
	if err == nil {
		return
	}
	detail := err.Error()
	setOpsUpstreamError(c, http.StatusServiceUnavailable, detail, detail)
	event := OpsUpstreamErrorEvent{
		UpstreamStatusCode: http.StatusServiceUnavailable,
		Kind:               string(CodexGatewayBorrowPreparationFailureReason),
		Scope:              string(GatewayFailureScopeRequest),
		Reason:             string(CodexGatewayBorrowPreparationFailureReason),
		Message:            detail,
		Detail:             detail,
	}
	if account != nil {
		event.ProxyID, event.ProxyName = opsUpstreamProxyID(account), opsUpstreamProxyName(account)
		event.Platform, event.AccountID, event.AccountName = account.Platform, account.ID, account.Name
	}
	appendOpsUpstreamError(c, event)
}

type codexGatewayBorrowHTTPPreparationContextKey struct{}
type codexGatewayBorrowHTTPFirstOutputContextKey struct{}

type codexGatewayBorrowHTTPPreparation struct {
	service     *CodexGatewayBorrowService
	revision    uint64
	accountID   int64
	model       string
	proxy       string
	application *CodexGatewayBorrowApplication
}

func (s *OpenAIGatewayService) codexGatewayBorrowHTTPConfigured(account *Account, model string) bool {
	if s == nil || s.gatewayBorrow == nil || !codexGatewayBorrowAccountSupported(account) {
		return false
	}
	borrow := s.gatewayBorrow
	borrow.mu.Lock()
	defer borrow.mu.Unlock()
	return borrow.config.Enabled && slices.Contains(borrow.config.TargetAccountIDs, account.ID) && slices.Contains(borrow.config.Models, model)
}

// Qualify the final request using the original client's cancellation/deadline,
// before the business first-output guard. WithContext shares Body/GetBody: this
// phase neither reads nor copies the business body, including a 100 MiB body.
func (s *OpenAIGatewayService) prepareCodexGatewayBorrowHTTP(ctx context.Context, req *http.Request, account *Account, model, proxy string) (*http.Request, time.Duration, error) {
	// Ordinary requests retain their exact identity: continuation diagnostics
	// bind an immutable body snapshot to this pointer after GetBody is disabled.
	if !CodexGatewayBorrowRequestEligible(req) || !s.codexGatewayBorrowHTTPConfigured(account, model) {
		return req, 0, nil
	}
	started := time.Now()
	prepared := codexGatewayBorrowHTTPPreparation{service: s.gatewayBorrow, accountID: account.ID, model: model, proxy: proxy}
	prepared.service.mu.Lock()
	prepared.revision = prepared.service.revision
	prepared.service.mu.Unlock()
	borrowed, application, err := prepared.service.Apply(req.WithContext(ctx), account, model, proxy, nil, false)
	if err != nil {
		return nil, 0, err
	}
	if application == nil || !application.Applied {
		return nil, 0, &CodexGatewayBorrowFailure{Cause: ErrCodexGatewayBorrowChanged}
	}
	prepared.application = application
	req = borrowed.WithContext(context.WithValue(req.Context(), codexGatewayBorrowHTTPPreparationContextKey{}, prepared))
	return req, time.Since(started), nil
}

// Forward already performed its one preparation before starting the business
// clock. At dispatch only matching cached evidence may be used. Expiry, a save
// or an identity change must not start another probe underneath that clock.
func (s *OpenAIGatewayService) applyCodexGatewayBorrowHTTP(req *http.Request, account *Account, model, proxy string) (*http.Request, error) {
	prepared, ok := req.Context().Value(codexGatewayBorrowHTTPPreparationContextKey{}).(codexGatewayBorrowHTTPPreparation)
	if !ok {
		if s.gatewayBorrow == nil {
			return req, nil
		}
		borrowed, _, err := s.gatewayBorrow.Apply(req, account, model, proxy, nil, false)
		return borrowed, err
	}
	changed := func() (*http.Request, error) {
		return nil, &CodexGatewayBorrowFailure{Cause: ErrCodexGatewayBorrowChanged}
	}
	if prepared.application == nil || !prepared.application.Applied {
		if s.codexGatewayBorrowHTTPConfigured(account, model) && CodexGatewayBorrowRequestEligible(req) {
			return changed()
		}
		return req, nil
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	if s.gatewayBorrow != prepared.service || account.ID != prepared.accountID || model != prepared.model || proxy != prepared.proxy {
		return changed()
	}
	prepared.service.mu.Lock()
	current := prepared.service.revision == prepared.revision && prepared.service.revisionCtx.Err() == nil
	prepared.service.mu.Unlock()
	if !current || !time.Now().Before(prepared.application.ExpiresAt) {
		return changed()
	}
	borrowed, application, err := prepared.service.Apply(req, account, model, proxy, nil, true)
	if err != nil {
		return nil, err
	}
	if application == nil || !application.Applied || application.Fingerprint != prepared.application.Fingerprint ||
		application.CookieFingerprint != prepared.application.CookieFingerprint || application.SourceAccountID != prepared.application.SourceAccountID ||
		!application.ExpiresAt.Equal(prepared.application.ExpiresAt) {
		return changed()
	}
	// The preparation already canonicalized this Cookie header. Validation
	// must keep the bound plaintext request identity when it changes no bytes.
	if slices.Equal(req.Header.Values("Cookie"), borrowed.Header.Values("Cookie")) {
		return req, nil
	}
	return borrowed, nil
}

type codexGatewayBorrowRecoveryContext struct {
	context.Context
	values context.Context
}

func (c codexGatewayBorrowRecoveryContext) Value(key any) any { return c.values.Value(key) }

// A prepared recovery request already carries the caller's cancellation and
// deadline. Preserve that exact context unless an old header guard was replaced.
// In that race retain request values, but bind lifecycle to the original caller
// without adding cancellation callbacks or letting the old guard cancel it.
func codexGatewayBorrowHTTPPreparedContext(caller, prepared context.Context, replacedGuard, recovery bool) context.Context {
	if !replacedGuard {
		return prepared
	}
	values := context.WithoutCancel(prepared)
	if recovery {
		return codexGatewayBorrowRecoveryContext{Context: caller, values: values}
	}
	return values
}

func withCodexGatewayBorrowHTTPFirstOutputStart(ctx context.Context, start time.Time) context.Context {
	return context.WithValue(ctx, codexGatewayBorrowHTTPFirstOutputContextKey{}, start)
}

func codexGatewayBorrowHTTPFirstOutputStart(ctx context.Context, fallback time.Time) time.Time {
	if IsPelicanGeneration(ctx) {
		return PelicanFirstOutputStart(ctx, fallback)
	}
	if ctx != nil {
		if start, ok := ctx.Value(codexGatewayBorrowHTTPFirstOutputContextKey{}).(time.Time); ok {
			return start
		}
	}
	return fallback
}
