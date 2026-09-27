package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// These helpers keep the administrator-facing record of an upstream failure
// complete where a downstream client receives a fixed terminal message. They
// follow handleErrorResponse / handleErrorResponsePassthrough: the sanitized
// upstream message and the configured bounded body reach the attempt event and
// the top-level Ops fields, and the body is logged when body logging is on.

// openAIUpstreamErrorDetail bounds an upstream error body exactly like
// handleErrorResponse (gateway.log_upstream_error_body[_max_bytes]).
func (s *OpenAIGatewayService) openAIUpstreamErrorDetail(body []byte) string {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.LogUpstreamErrorBody || len(body) == 0 {
		return ""
	}
	maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
	if maxBytes <= 0 {
		maxBytes = 2048
	}
	return truncateString(string(body), maxBytes)
}

// logOpenAIUpstreamErrorBody writes handleErrorResponse's body log line.
func (s *OpenAIGatewayService) logOpenAIUpstreamErrorBody(account *Account, statusCode int, body []byte) {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.LogUpstreamErrorBody || account == nil {
		return
	}
	logger.LegacyPrintf("service.openai_gateway",
		"OpenAI upstream error %d (account=%d platform=%s type=%s): %s",
		statusCode,
		account.ID,
		account.Platform,
		account.Type,
		truncateForLog(body, s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes),
	)
}

// openAIOpsUpstreamErrorMessage extracts the upstream message the way
// handleErrorResponse does and also reads Responses envelopes
// (response.error.message) that that extractor does not cover.
func openAIOpsUpstreamErrorMessage(body []byte) string {
	if message := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body))); message != "" {
		return message
	}
	return extractOpenAISSEErrorMessage(body)
}

// recordOpenAIRequestTerminalUpstreamError records a request-scoped terminal
// (continuation state or request rejection) with the upstream's own message
// and bounded body. Only the client response is replaced by the fixed message.
func (s *OpenAIGatewayService) recordOpenAIRequestTerminalUpstreamError(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	statusCode int,
	headers http.Header,
	requestBody []byte,
	body []byte,
	kind string,
	passthrough bool,
	diagnostic *OpenAIContinuationDiagnostic,
) {
	upstreamMsg := openAIOpsUpstreamErrorMessage(body)
	upstreamDetail := s.openAIUpstreamErrorDetail(body)
	setOpsUpstreamError(c, statusCode, upstreamMsg, upstreamDetail)
	logOpenAIInstructionsRequiredDebug(ctx, c, account, statusCode, upstreamMsg, requestBody, body)
	s.logOpenAIUpstreamErrorBody(account, statusCode, body)
	event := OpsUpstreamErrorEvent{
		ProxyID:                opsUpstreamProxyID(account),
		ProxyName:              opsUpstreamProxyName(account),
		UpstreamStatusCode:     statusCode,
		UpstreamRequestID:      headers.Get("x-request-id"),
		Passthrough:            passthrough,
		Kind:                   kind,
		Message:                upstreamMsg,
		Detail:                 upstreamDetail,
		ContinuationDiagnostic: diagnostic,
	}
	if passthrough {
		event.UpstreamResponseBody = upstreamDetail
	}
	if account != nil {
		event.Platform, event.AccountID, event.AccountName = account.Platform, account.ID, account.Name
	}
	appendOpsUpstreamError(c, event)
}

// OpenAIFailoverUpstreamMessage returns the upstream's own error text carried by
// a failover terminal (an HTTP error body or a Responses/WebSocket event),
// sanitized like every other Ops upstream message. Handlers record it for
// administrators; it is never written to the client from here.
func OpenAIFailoverUpstreamMessage(failoverErr *UpstreamFailoverError) string {
	if failoverErr == nil || len(failoverErr.ResponseBody) == 0 {
		return ""
	}
	return extractOpenAISSEErrorMessage(failoverErr.ResponseBody)
}

// OpenAIModelNotSupportedNoAccountOpsMessage describes, for Ops only, why a
// request that reached no upstream still ends as model_not_supported.
const OpenAIModelNotSupportedNoAccountOpsMessage = "No upstream request was sent: every account that supports this model is in the model_not_supported cooldown set by an earlier upstream 400 response; see the account's model rate limits and the earlier Ops attempt for the upstream text."

// openAIWSUpstreamEventError carries the upstream WebSocket event that ended an
// attempt. Error() is the upstream message, which is what fallback handling
// already used; the payload is kept only for Ops.
type openAIWSUpstreamEventError struct {
	message string
	payload []byte
}

func newOpenAIWSUpstreamEventError(message string, payload []byte) *openAIWSUpstreamEventError {
	return &openAIWSUpstreamEventError{message: message, payload: append([]byte(nil), payload...)}
}

func (e *openAIWSUpstreamEventError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

// recordOpenAIWSContinuationStateError records the rejected WebSocket event:
// the upstream's own message from the event (or the handshake response), the
// bounded payload, and the status writeOpenAIWSFallbackErrorResponse uses.
func (s *OpenAIGatewayService) recordOpenAIWSContinuationStateError(c *gin.Context, account *Account, wsErr error) {
	statusCode, _, _, fallbackMessage, ok := resolveOpenAIWSFallbackErrorResponse(wsErr)
	if !ok || statusCode <= 0 {
		statusCode = http.StatusBadRequest
	}
	upstreamMessage := ""
	var payload []byte
	var eventErr *openAIWSUpstreamEventError
	var dialErr *openAIWSDialError
	if errors.As(wsErr, &eventErr) && eventErr != nil {
		payload = eventErr.payload
		upstreamMessage = extractOpenAISSEErrorMessage(payload)
		if upstreamMessage == "" {
			upstreamMessage = sanitizeUpstreamErrorMessage(strings.TrimSpace(eventErr.message))
		}
	} else if errors.As(wsErr, &dialErr) && dialErr != nil {
		payload = dialErr.ResponseBody
		upstreamMessage = extractOpenAISSEErrorMessage(payload)
	}
	// resolveOpenAIWSFallbackErrorResponse falls back to fixed defaults; use
	// them only when the upstream supplied no text of its own.
	if upstreamMessage == "" {
		upstreamMessage = strings.TrimSpace(fallbackMessage)
	}
	if upstreamMessage == "" && wsErr != nil {
		upstreamMessage = sanitizeUpstreamErrorMessage(strings.TrimSpace(wsErr.Error()))
	}
	detail := s.openAIUpstreamErrorDetail(payload)
	setOpsUpstreamError(c, statusCode, upstreamMessage, detail)
	proxyID, proxyName := opsUpstreamWSProxyAttribution(account)
	event := OpsUpstreamErrorEvent{
		ProxyID:            proxyID,
		ProxyName:          proxyName,
		UpstreamStatusCode: statusCode,
		Kind:               "continuation_state",
		Message:            upstreamMessage,
		Detail:             detail,
	}
	if account != nil {
		event.Platform, event.AccountID, event.AccountName = account.Platform, account.ID, account.Name
	}
	appendOpsUpstreamError(c, event)
}

// recordOpenAIModelNotSupportedAttempt keeps the upstream's own model
// capability rejection on the attempt; the client still receives the fixed
// model_not_supported terminal once every account has refused the model.
func (s *OpenAIGatewayService) recordOpenAIModelNotSupportedAttempt(c *gin.Context, account *Account, headers http.Header, payload []byte, passthrough bool) {
	upstreamMsg := extractOpenAISSEErrorMessage(payload)
	upstreamDetail := s.openAIUpstreamErrorDetail(payload)
	setOpsUpstreamError(c, http.StatusBadRequest, upstreamMsg, upstreamDetail)
	event := OpsUpstreamErrorEvent{
		ProxyID:            opsUpstreamProxyID(account),
		ProxyName:          opsUpstreamProxyName(account),
		UpstreamStatusCode: http.StatusBadRequest,
		UpstreamRequestID:  headers.Get("x-request-id"),
		Passthrough:        passthrough,
		Kind:               "failover",
		Reason:             string(openAIModelNotSupportedReason),
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	}
	if account != nil {
		event.Platform, event.AccountID, event.AccountName = account.Platform, account.ID, account.Name
	}
	appendOpsUpstreamError(c, event)
}

// recordOpenAIWSModelNotSupportedAttempt is the WebSocket variant; a missing
// managed proxy on a WS dial is unknown rather than direct.
func (s *OpenAIGatewayService) recordOpenAIWSModelNotSupportedAttempt(c *gin.Context, account *Account, headers http.Header, payload []byte) {
	upstreamMsg := extractOpenAISSEErrorMessage(payload)
	upstreamDetail := s.openAIUpstreamErrorDetail(payload)
	setOpsUpstreamError(c, http.StatusBadRequest, upstreamMsg, upstreamDetail)
	proxyID, proxyName := opsUpstreamWSProxyAttribution(account)
	event := OpsUpstreamErrorEvent{
		ProxyID:            proxyID,
		ProxyName:          proxyName,
		UpstreamStatusCode: http.StatusBadRequest,
		UpstreamRequestID:  headers.Get("x-request-id"),
		Kind:               "failover",
		Reason:             string(openAIModelNotSupportedReason),
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	}
	if account != nil {
		event.Platform, event.AccountID, event.AccountName = account.Platform, account.ID, account.Name
	}
	appendOpsUpstreamError(c, event)
}

// openAIWSDeliveryMarksFailuresKey is set while a WebSocket refusal output
// marks delivered failures itself, so writers above it do not mark frames the
// output may still hold back or replace.
const openAIWSDeliveryMarksFailuresKey = "openai_ws_delivery_marks_failures"

func openAIWSDeliveryMarksFailures(c *gin.Context) bool {
	return c != nil && c.GetBool(openAIWSDeliveryMarksFailuresKey)
}

// markOpenAIWSDeliveredFailure marks an error or response.failed event the
// client received, as upstream v0.2.8 does, except a cyber-policy block: that
// already gets its own dedicated Ops row.
func markOpenAIWSDeliveredFailure(c *gin.Context, payload []byte) {
	eventType, _, _ := parseOpenAIWSEventEnvelope(payload)
	if eventType != "error" && eventType != "response.failed" {
		return
	}
	if hit, _, _ := detectOpenAICyberPolicy(payload); hit {
		return
	}
	markOpenAIWSClientVisibleFailure(c, eventType, payload)
}

// recordOpenAIWSBridgeTransportError keeps the transport error behind the
// fixed "Upstream request failed" event a later bridge turn sends the client.
func recordOpenAIWSBridgeTransportError(c *gin.Context, account *Account, safeErr string) {
	setOpsUpstreamError(c, 0, safeErr, "")
	event := OpsUpstreamErrorEvent{
		ProxyID:     opsUpstreamProxyID(account),
		ProxyName:   opsUpstreamProxyName(account),
		Passthrough: true,
		Kind:        "request_error",
		Message:     safeErr,
	}
	if account != nil {
		event.Platform, event.AccountID, event.AccountName = account.Platform, account.ID, account.Name
	}
	appendOpsUpstreamError(c, event)
}

// OpenAILastUpstreamErrorExtraKey keeps the most recent upstream 401/403/429 that
// the request failover state handled without persisting account error state.
// Administrators read it; scheduling never consults it.
const OpenAILastUpstreamErrorExtraKey = "last_upstream_error"

const (
	openAILastUpstreamErrorMaxBytes = 8 << 10
	// One write per account and status inside this window, counted from the
	// last successful write, so a burst of rejections costs one account write.
	openAILastUpstreamErrorRepeatWindow = 30 * time.Second
	// Background writes in flight at once; beyond this a record is dropped.
	openAILastUpstreamErrorMaxInflight = 16
)

type openAILastUpstreamErrorKey struct {
	accountID int64
	status    int
}

var (
	openAILastUpstreamErrorWritten  sync.Map // openAILastUpstreamErrorKey -> time.Time of the last successful write
	openAILastUpstreamErrorInflight sync.Map // openAILastUpstreamErrorKey -> struct{}
	openAILastUpstreamErrorSlots    = make(chan struct{}, openAILastUpstreamErrorMaxInflight)
)

// recordOpenAILastUpstreamError stores the upstream's verbatim error body in
// accounts.extra.last_upstream_error = {status, message, at, source}. The
// write runs in the background and is best effort: failover never waits for
// it, and a record is dropped while the same account and status is being
// written, was written inside the repeat window, or too many writes are in
// flight. It does not change status, schedulability or any cooldown.
func (s *OpenAIGatewayService) recordOpenAILastUpstreamError(ctx context.Context, account *Account, statusCode int, body []byte) {
	if s == nil || s.accountRepo == nil || account == nil || account.ID <= 0 {
		return
	}
	key := openAILastUpstreamErrorKey{accountID: account.ID, status: statusCode}
	now := time.Now()
	if last, ok := openAILastUpstreamErrorWritten.Load(key); ok {
		if at, ok := last.(time.Time); ok && now.Sub(at) < openAILastUpstreamErrorRepeatWindow {
			return
		}
	}
	if _, busy := openAILastUpstreamErrorInflight.LoadOrStore(key, struct{}{}); busy {
		return
	}
	select {
	case openAILastUpstreamErrorSlots <- struct{}{}:
	default:
		openAILastUpstreamErrorInflight.Delete(key)
		return
	}
	// Detach everything the write needs from the request. A shadow account's
	// credential owner is resolved in the background, never on this path.
	body = append([]byte(nil), body...)
	var shadow *Account
	if account.IsShadow() {
		parentID := *account.ParentAccountID
		shadow = &Account{ID: account.ID, Platform: account.Platform, Type: account.Type, ParentAccountID: &parentID}
	} else {
		body = redactAgentIdentitySensitiveBodyForAccount(ctx, nil, account, body)
	}
	repo := s.accountRepo
	stateCtx, cancel := openAIAccountStateContext(ctx)
	go func() {
		defer func() {
			cancel()
			openAILastUpstreamErrorInflight.Delete(key)
			<-openAILastUpstreamErrorSlots
		}()
		// A best-effort record must never take the process down.
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.FromContext(stateCtx).Error("openai.last_upstream_error_panic",
					zap.Int64("account_id", key.accountID),
					zap.Any("recover", recovered),
				)
			}
		}()
		if shadow != nil {
			body = redactAgentIdentitySensitiveBodyForAccount(stateCtx, repo, shadow, body)
		}
		message := strings.TrimSpace(strings.ToValidUTF8(string(body), "\uFFFD"))
		if message == "" {
			message = http.StatusText(statusCode)
		}
		value := map[string]any{
			"status":  statusCode,
			"message": truncateString(message, openAILastUpstreamErrorMaxBytes),
			"at":      now.UTC().Format(time.RFC3339),
			"source":  "gateway",
		}
		if err := repo.UpdateExtra(stateCtx, key.accountID, map[string]any{OpenAILastUpstreamErrorExtraKey: value}); err != nil {
			logger.FromContext(stateCtx).Warn("openai.last_upstream_error_persist_failed",
				zap.Int64("account_id", key.accountID),
				zap.Int("upstream_status", statusCode),
				zap.Error(err),
			)
			return
		}
		openAILastUpstreamErrorWritten.Store(key, time.Now())
	}()
}
