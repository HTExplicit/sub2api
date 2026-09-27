package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
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

// openAIBudgetExceededAccountError is the account error text: the upstream's
// own message followed by the structured type/code/param that identify the
// relay's budget rule. Without a message the complete error object is kept.
func openAIBudgetExceededAccountError(payload []byte) string {
	errorObject := gjson.GetBytes(payload, "error")
	if !errorObject.IsObject() {
		errorObject = gjson.GetBytes(payload, "response.error")
	}
	message := strings.TrimSpace(errorObject.Get("message").String())
	if message == "" {
		raw := []byte(errorObject.Raw)
		if !errorObject.IsObject() {
			raw = bytes.TrimSpace(payload)
		}
		var compact bytes.Buffer
		if json.Compact(&compact, raw) == nil && compact.Len() > 0 {
			return compact.String()
		}
		return openAIBudgetExceededMessage(payload)
	}
	var fields []string
	for _, name := range [...]string{"type", "code", "param"} {
		if value := errorObject.Get(name); value.Exists() && value.Type != gjson.Null && strings.TrimSpace(value.String()) != "" {
			fields = append(fields, fmt.Sprintf("%s=%s", name, strings.TrimSpace(value.String())))
		}
	}
	if len(fields) == 0 {
		return message
	}
	return message + " (" + strings.Join(fields, ", ") + ")"
}

// handleOpenAIBudgetExceeded moves the account to the error state.
func (s *RateLimitService) handleOpenAIBudgetExceeded(ctx context.Context, account *Account, payload []byte) {
	if s == nil || account == nil {
		return
	}
	message := openAIBudgetExceededAccountError(payload)
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
// an account-scoped failover that never retries the same account. replayable
// tells whether the caller can still move the request to another account.
func (s *OpenAIGatewayService) openAIBudgetExceededTerminalFailover(ctx context.Context, c *gin.Context, account *Account, headers http.Header, payload []byte, webSocket bool, replayable bool) (*UpstreamFailoverError, bool) {
	if !s.handleOpenAIBudgetExceededTerminalEvent(ctx, account, payload) {
		return nil, false
	}
	s.recordOpenAIBudgetExceededAttempt(ctx, c, account, http.StatusTooManyRequests, headers, payload, webSocket, replayable)
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
func (s *OpenAIGatewayService) openAIBudgetExceededHTTPResponseTerminalFailover(ctx context.Context, c *gin.Context, account *Account, statusCode int, headers http.Header, payload []byte) (*UpstreamFailoverError, bool) {
	if statusCode != http.StatusOK {
		return nil, false
	}
	// Once client output is committed the request cannot move to another account.
	return s.openAIBudgetExceededTerminalFailover(ctx, c, account, headers, payload, false, !openAIStreamClientOutputStarted(c, false))
}

// handleOpenAIBudgetExceededHTTPFailover consumes a budget_exceeded HTTP 429
// before any generic retry, recovery or rewriting can reinterpret it.
func (s *OpenAIGatewayService) handleOpenAIBudgetExceededHTTPFailover(ctx context.Context, c *gin.Context, account *Account, statusCode int, headers http.Header, body []byte) (*UpstreamFailoverError, bool) {
	if account == nil || !account.IsOpenAICompatible() || !isOpenAIBudgetExceededResponse(statusCode, body) {
		return nil, false
	}
	s.handleOpenAIBudgetExceeded(ctx, account, body)
	s.recordOpenAIBudgetExceededAttempt(ctx, c, account, statusCode, headers, body, false, true)
	failoverErr := newOpenAIUpstreamFailoverError(statusCode, headers, body, openAIBudgetExceededMessage(body), false)
	failoverErr.Scope = GatewayFailureScopeAccount
	failoverErr.NextAccountAction = NextAccountRetry
	return failoverErr, true
}

// recordOpenAIBudgetExceededAttempt records the budget rejection like any other
// upstream 429 attempt: the upstream message, the bounded body and the body log
// line. It counts as a failover only while the request can still move to
// another account.
func (s *OpenAIGatewayService) recordOpenAIBudgetExceededAttempt(ctx context.Context, c *gin.Context, account *Account, statusCode int, headers http.Header, payload []byte, webSocket bool, replayable bool) {
	// Keep the upstream Agent Identity credential redaction for recorded bodies.
	payload = s.redactAgentIdentitySensitiveBody(ctx, account, payload)
	s.logOpenAIUpstreamErrorBody(account, statusCode, payload)
	if c == nil {
		return
	}
	message := sanitizeUpstreamErrorMessage(strings.TrimSpace(openAIBudgetExceededMessage(payload)))
	detail := s.openAIUpstreamErrorDetail(payload)
	setOpsUpstreamError(c, statusCode, message, detail)
	proxyID, proxyName := opsUpstreamProxyAttribution(account)
	if webSocket {
		proxyID, proxyName = opsUpstreamWSProxyAttribution(account)
	}
	kind := "failover"
	if !replayable {
		kind = "stream_error"
	}
	event := OpsUpstreamErrorEvent{
		ProxyID:            proxyID,
		ProxyName:          proxyName,
		UpstreamStatusCode: statusCode,
		UpstreamRequestID:  headers.Get("x-request-id"),
		Kind:               kind,
		Reason:             openAIBudgetExceededKind,
		Message:            message,
		Detail:             detail,
	}
	if account != nil {
		event.Platform, event.AccountID, event.AccountName = account.Platform, account.ID, account.Name
	}
	appendOpsUpstreamError(c, event)
}
