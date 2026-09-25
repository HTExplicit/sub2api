package service

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// OpenAI-compatible relays (for example LiteLLM-based gateways) report an
// exhausted key budget as error type or code "budget_exceeded":
// either as an HTTP 429, or as a terminal response.failed / error event inside
// an HTTP-200 stream. The key cannot serve any request until its budget is
// replenished, so the account enters the ordinary error state with the
// upstream message. There is no timed recovery: an administrator clears the
// error, or a successful account test recovers it.
const openAIBudgetExceededKind = "budget_exceeded"

func isOpenAIBudgetExceededErrorObject(errorObject gjson.Result) bool {
	if !errorObject.IsObject() {
		return false
	}
	for _, field := range [...]string{"type", "code"} {
		value := errorObject.Get(field)
		if value.Type == gjson.String && strings.EqualFold(strings.TrimSpace(value.String()), openAIBudgetExceededKind) {
			return true
		}
	}
	return false
}

// isOpenAIBudgetExceededResponse matches an HTTP 429 budget error response.
func isOpenAIBudgetExceededResponse(statusCode int, body []byte) bool {
	return statusCode == http.StatusTooManyRequests && len(body) > 0 && gjson.ValidBytes(body) &&
		isOpenAIBudgetExceededErrorObject(gjson.GetBytes(body, "error"))
}

// isOpenAIBudgetExceededTerminalEvent matches a terminal Responses
// response.failed event or an error event carrying the budget signal.
func isOpenAIBudgetExceededTerminalEvent(payload []byte) bool {
	if len(payload) == 0 || !gjson.ValidBytes(payload) {
		return false
	}
	switch gjson.GetBytes(payload, "type").String() {
	case "response.failed":
		return isOpenAIBudgetExceededErrorObject(gjson.GetBytes(payload, "response.error"))
	case "error":
		return isOpenAIBudgetExceededErrorObject(gjson.GetBytes(payload, "error"))
	}
	return false
}

// openAIBudgetExceededMessage returns the upstream's own error message, or the
// compact upstream payload when it carries no message.
func openAIBudgetExceededMessage(payload []byte) string {
	for _, path := range [...]string{"error.message", "response.error.message"} {
		if message := strings.TrimSpace(gjson.GetBytes(payload, path).String()); message != "" {
			return message
		}
	}
	return buildForbiddenErrorMessage("", "", payload, openAIBudgetExceededKind)
}

// handleOpenAIBudgetExceeded moves the account to the error state.
func (s *RateLimitService) handleOpenAIBudgetExceeded(ctx context.Context, account *Account, payload []byte) {
	if s == nil || account == nil {
		return
	}
	message := openAIBudgetExceededMessage(payload)
	s.notifyAccountSchedulingBlocked(account, time.Time{}, openAIBudgetExceededKind)
	if s.accountRepo == nil {
		return
	}
	if err := s.accountRepo.SetError(ctx, account.ID, message); err != nil {
		slog.Warn("account_set_error_failed", "account_id", account.ID, "error", err)
		return
	}
	slog.Warn("account_disabled_budget_exceeded", "account_id", account.ID)
}

func (s *OpenAIGatewayService) handleOpenAIBudgetExceeded(ctx context.Context, account *Account, payload []byte) {
	if s == nil || account == nil {
		return
	}
	if s.rateLimitService == nil {
		s.BlockAccountScheduling(account, time.Time{}, openAIBudgetExceededKind)
		return
	}
	stateCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()
	s.rateLimitService.handleOpenAIBudgetExceeded(stateCtx, account, payload)
}

// handleOpenAIBudgetExceededTerminalEvent applies the budget rule to an in-band
// terminal event before any generic error rewriting discards its structure.
func (s *OpenAIGatewayService) handleOpenAIBudgetExceededTerminalEvent(ctx context.Context, account *Account, payload []byte) bool {
	if account == nil || !account.IsOpenAICompatible() || !isOpenAIBudgetExceededTerminalEvent(payload) {
		return false
	}
	s.handleOpenAIBudgetExceeded(ctx, account, payload)
	return true
}

// openAIBudgetExceededTerminalFailover converts a budget terminal event into
// an account-scoped failover that never retries the same account.
func (s *OpenAIGatewayService) openAIBudgetExceededTerminalFailover(ctx context.Context, account *Account, headers http.Header, payload []byte) (*UpstreamFailoverError, bool) {
	if !s.handleOpenAIBudgetExceededTerminalEvent(ctx, account, payload) {
		return nil, false
	}
	return &UpstreamFailoverError{
		StatusCode:        http.StatusTooManyRequests,
		ResponseBody:      append([]byte(nil), payload...),
		ResponseHeaders:   headers.Clone(),
		Scope:             GatewayFailureScopeAccount,
		NextAccountAction: NextAccountRetry,
	}, true
}

// openAIBudgetExceededHTTPResponseTerminalFailover accepts a terminal event
// only on an exact HTTP-200 response, so a 201/202 body cannot masquerade as
// an in-band budget event.
func (s *OpenAIGatewayService) openAIBudgetExceededHTTPResponseTerminalFailover(ctx context.Context, account *Account, statusCode int, headers http.Header, payload []byte) (*UpstreamFailoverError, bool) {
	if statusCode != http.StatusOK {
		return nil, false
	}
	return s.openAIBudgetExceededTerminalFailover(ctx, account, headers, payload)
}

// handleOpenAIBudgetExceededHTTPFailover consumes a budget_exceeded HTTP 429
// before any generic retry, recovery or rewriting can reinterpret it.
func (s *OpenAIGatewayService) handleOpenAIBudgetExceededHTTPFailover(ctx context.Context, account *Account, statusCode int, headers http.Header, body []byte) (*UpstreamFailoverError, bool) {
	if account == nil || !account.IsOpenAICompatible() || !isOpenAIBudgetExceededResponse(statusCode, body) {
		return nil, false
	}
	s.handleOpenAIBudgetExceeded(ctx, account, body)
	failoverErr := newOpenAIUpstreamFailoverError(statusCode, headers, body, openAIBudgetExceededMessage(body), false)
	failoverErr.Scope = GatewayFailureScopeAccount
	failoverErr.NextAccountAction = NextAccountRetry
	return failoverErr, true
}
