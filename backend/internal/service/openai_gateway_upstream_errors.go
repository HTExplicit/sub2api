package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

func logOpenAIInstructionsRequiredDebug(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	upstreamStatusCode int,
	upstreamMsg string,
	requestBody []byte,
	upstreamBody []byte,
) {
	msg := strings.TrimSpace(upstreamMsg)
	if !isOpenAIInstructionsRequiredError(upstreamStatusCode, msg, upstreamBody) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	accountID := int64(0)
	accountName := ""
	if account != nil {
		accountID = account.ID
		accountName = strings.TrimSpace(account.Name)
	}

	userAgent := ""
	originator := ""
	if c != nil {
		userAgent = strings.TrimSpace(c.GetHeader("User-Agent"))
		originator = strings.TrimSpace(c.GetHeader("originator"))
	}

	fields := []zap.Field{
		zap.String("component", "service.openai_gateway"),
		zap.Int64("account_id", accountID),
		zap.String("account_name", accountName),
		zap.Int("upstream_status_code", upstreamStatusCode),
		zap.String("upstream_error_message", msg),
		zap.String("request_user_agent", userAgent),
		zap.Bool("codex_official_client_match", openai.IsCodexOfficialClientByHeaders(userAgent, originator)),
	}
	fields = appendCodexCLIOnlyRejectedRequestFields(fields, c, requestBody)

	logger.FromContext(ctx).With(fields...).Warn("OpenAI 上游返回 Instructions are required，已记录请求详情用于排查")
}

func isOpenAIInstructionsRequiredError(upstreamStatusCode int, upstreamMsg string, upstreamBody []byte) bool {
	if upstreamStatusCode != http.StatusBadRequest {
		return false
	}

	hasInstructionRequired := func(text string) bool {
		lower := strings.ToLower(strings.TrimSpace(text))
		if lower == "" {
			return false
		}
		if strings.Contains(lower, "instructions are required") {
			return true
		}
		if strings.Contains(lower, "required parameter: 'instructions'") {
			return true
		}
		if strings.Contains(lower, "required parameter: instructions") {
			return true
		}
		if strings.Contains(lower, "missing required parameter") && strings.Contains(lower, "instructions") {
			return true
		}
		return strings.Contains(lower, "instruction") && strings.Contains(lower, "required")
	}

	if hasInstructionRequired(upstreamMsg) {
		return true
	}
	if len(upstreamBody) == 0 {
		return false
	}

	errMsg := gjson.GetBytes(upstreamBody, "error.message").String()
	errMsgLower := strings.ToLower(strings.TrimSpace(errMsg))
	errCode := strings.ToLower(strings.TrimSpace(gjson.GetBytes(upstreamBody, "error.code").String()))
	errParam := strings.ToLower(strings.TrimSpace(gjson.GetBytes(upstreamBody, "error.param").String()))
	errType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(upstreamBody, "error.type").String()))

	if errParam == "instructions" {
		return true
	}
	if hasInstructionRequired(errMsg) {
		return true
	}
	if strings.Contains(errCode, "missing_required_parameter") && strings.Contains(errMsgLower, "instructions") {
		return true
	}
	if strings.Contains(errType, "invalid_request") && strings.Contains(errMsgLower, "instructions") && strings.Contains(errMsgLower, "required") {
		return true
	}

	return false
}

func isOpenAITransientProcessingError(upstreamStatusCode int, upstreamMsg string, upstreamBody []byte) bool {
	if upstreamStatusCode < http.StatusBadRequest {
		return false
	}

	hasOpenAIServerOverloadedCode := func(payload []byte) bool {
		code := strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "error.code").String()))
		if code == "" {
			code = strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "response.error.code").String()))
		}
		return code == "server_is_overloaded" || code == "slow_down"
	}

	if len(upstreamBody) > 0 && hasOpenAIServerOverloadedCode(upstreamBody) {
		return true
	}
	if isOpenAICapacityShedMessage(upstreamMsg) ||
		isOpenAICapacityShedMessage(gjson.GetBytes(upstreamBody, "error.message").String()) ||
		isOpenAICapacityShedMessage(gjson.GetBytes(upstreamBody, "response.error.message").String()) ||
		(!gjson.ValidBytes(upstreamBody) && isOpenAICapacityShedMessage(string(upstreamBody))) {
		return true
	}
	if upstreamStatusCode != http.StatusBadRequest && upstreamStatusCode != http.StatusServiceUnavailable {
		return false
	}
	if upstreamStatusCode != http.StatusBadRequest {
		return false
	}

	match := func(text string) bool {
		lower := strings.ToLower(strings.TrimSpace(text))
		if lower == "" {
			return false
		}
		if strings.Contains(lower, "an error occurred while processing your request") {
			return true
		}
		if strings.Contains(lower, "selected model is at capacity") {
			return true
		}
		return strings.Contains(lower, "you can retry your request") &&
			strings.Contains(lower, "help.openai.com") &&
			strings.Contains(lower, "request id")
	}

	if match(upstreamMsg) {
		return true
	}
	if len(upstreamBody) == 0 {
		return false
	}
	if match(gjson.GetBytes(upstreamBody, "error.message").String()) {
		return true
	}
	if match(gjson.GetBytes(upstreamBody, "response.error.message").String()) ||
		match(gjson.GetBytes(upstreamBody, "message").String()) {
		return true
	}
	return !gjson.ValidBytes(upstreamBody) && match(string(upstreamBody))
}

func isOpenAICapacityShedMessage(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(lower, "server is overloaded") ||
		strings.Contains(lower, "servers are overloaded") ||
		strings.Contains(lower, "servers are currently overloaded")
}

func isOpenAIRequestScopedCapacityShed(upstreamMsg string, upstreamBody []byte) bool {
	return isOpenAIUpstreamCapacityShedEvent(upstreamBody) ||
		isOpenAICapacityShedMessage(upstreamMsg) ||
		(!gjson.ValidBytes(upstreamBody) && isOpenAICapacityShedMessage(string(upstreamBody)))
}

func isOpenAIContextWindowError(upstreamMsg string, upstreamBody []byte) bool {
	match := func(text string) bool {
		lower := strings.ToLower(strings.TrimSpace(text))
		if lower == "" {
			return false
		}
		if strings.Contains(lower, "context_too_large") || strings.Contains(lower, "context_length_exceeded") {
			return true
		}
		if strings.Contains(lower, "maximum context length") || strings.Contains(lower, "max context length") {
			return true
		}
		hasExceeded := strings.Contains(lower, "exceed") || strings.Contains(lower, "too large") || strings.Contains(lower, "too long")
		if strings.Contains(lower, "context window") && hasExceeded {
			return true
		}
		if strings.Contains(lower, "context length") && hasExceeded {
			return true
		}
		return strings.Contains(lower, "token limit") &&
			strings.Contains(lower, "context") &&
			hasExceeded
	}

	if match(upstreamMsg) {
		return true
	}
	if len(upstreamBody) == 0 {
		return false
	}
	for _, path := range []string{
		"error.message",
		"response.error.message",
		"message",
		"error.code",
		"response.error.code",
		"code",
	} {
		if match(gjson.GetBytes(upstreamBody, path).String()) {
			return true
		}
	}
	return !gjson.ValidBytes(upstreamBody) && match(string(upstreamBody))
}

func (s *OpenAIGatewayService) shouldFailoverUpstreamError(statusCode int) bool {
	switch statusCode {
	case 401, 402, 403, http.StatusMethodNotAllowed, http.StatusRequestTimeout, 429, 529:
		return true
	default:
		return statusCode >= 500
	}
}

func (s *OpenAIGatewayService) shouldFailoverOpenAIUpstreamResponse(statusCode int, upstreamMsg string, upstreamBody []byte) bool {
	if hit, _, _ := detectOpenAICyberPolicy(upstreamBody); hit {
		return false
	}
	// A continuation-state rejection is request-scoped: choosing a different
	// account cannot validate a previous response or provider-specific encrypted
	// reasoning item. The caller returns a bounded recovery or a terminal client
	// error before account-health side effects run.
	if isOpenAIContinuationStateError(upstreamMsg, upstreamBody) {
		return false
	}
	if isOpenAIContextWindowError(upstreamMsg, upstreamBody) {
		return false
	}
	// Laxa may expose a model in the shared catalogue while an individual
	// API-key credential is temporarily unable to serve it.  This is an
	// account/model capability failure, not a deterministic client 400: switch
	// accounts and let RateLimitService cool only the affected pair.
	if isOpenAIModelNotSupportedError(statusCode, upstreamMsg, upstreamBody) {
		return true
	}
	if isOpenAIHTTPUpstreamAccessStateError(statusCode, upstreamMsg, upstreamBody) {
		return true
	}
	if isOpenAIRequestBodyTooLargeError(statusCode, upstreamMsg, upstreamBody) {
		return true
	}
	if s.shouldFailoverUpstreamError(statusCode) {
		return true
	}
	return isOpenAITransientProcessingError(statusCode, upstreamMsg, upstreamBody)
}

// OpenAIRequestBodyTooLargeClientMessage is the fixed downstream message used
// after all account-specific request body limit failovers are exhausted.
const OpenAIRequestBodyTooLargeClientMessage = "Request payload is too large"

const openAIRequestBodyTooLargeReason = GatewayFailureReason("openai_request_body_too_large")

// OpenAIContinuationStateUnavailableCode identifies a request whose previous
// response or encrypted reasoning state is no longer valid on its upstream.
// It is deliberately non-retryable: retrying on another account can only make
// the same account-bound state failure fan out across the scheduler pool.
const OpenAIContinuationStateUnavailableCode = "continuation_state_unavailable"

// OpenAIContinuationStateUnavailableClientMessage is safe to surface to a
// Responses client without exposing upstream account or session details.
const OpenAIContinuationStateUnavailableClientMessage = "This conversation can no longer be resumed because its upstream state is unavailable. Start a new conversation to continue."

const openAIContinuationStateUnavailableReason = GatewayFailureReason("openai_continuation_state_unavailable")

type openAIContinuationStateErrorKind string

const (
	openAIContinuationStateErrorNone                     openAIContinuationStateErrorKind = ""
	openAIContinuationStateErrorPreviousResponseNotFound openAIContinuationStateErrorKind = "previous_response_not_found"
	openAIContinuationStateErrorInvalidEncryptedContent  openAIContinuationStateErrorKind = "invalid_encrypted_content"
)

func classifyOpenAIContinuationStateError(upstreamMsg string, upstreamBody []byte) openAIContinuationStateErrorKind {
	const (
		previousResponseNotFound = "previous_response_not_found"
		invalidEncryptedContent  = "invalid_encrypted_content"
	)

	for _, path := range []string{
		"error.code",
		"response.error.code",
		"code",
		"response.code",
	} {
		switch strings.ToLower(strings.TrimSpace(gjson.GetBytes(upstreamBody, path).String())) {
		case previousResponseNotFound:
			return openAIContinuationStateErrorPreviousResponseNotFound
		case invalidEncryptedContent:
			return openAIContinuationStateErrorInvalidEncryptedContent
		}
	}

	// Compatibility upstreams sometimes put a stable error code in the message
	// while using a gateway status such as 502 or 503. Restrict fallback matching
	// to exact continuation-state phrases rather than scanning arbitrary bodies.
	for _, message := range []string{
		upstreamMsg,
		gjson.GetBytes(upstreamBody, "error.message").String(),
		gjson.GetBytes(upstreamBody, "response.error.message").String(),
		gjson.GetBytes(upstreamBody, "message").String(),
	} {
		lower := strings.ToLower(strings.TrimSpace(message))
		switch {
		case strings.Contains(lower, previousResponseNotFound),
			strings.Contains(lower, "previous response not found"):
			return openAIContinuationStateErrorPreviousResponseNotFound
		case strings.Contains(lower, invalidEncryptedContent),
			strings.Contains(lower, "encrypted content could not be verified"),
			strings.Contains(lower, "invalid encrypted content"):
			return openAIContinuationStateErrorInvalidEncryptedContent
		}
	}

	return openAIContinuationStateErrorNone
}

func isOpenAIContinuationStateError(upstreamMsg string, upstreamBody []byte) bool {
	return classifyOpenAIContinuationStateError(upstreamMsg, upstreamBody) != openAIContinuationStateErrorNone
}

// openAIContinuationStateErrorFromFailedEvent classifies a protocol-level
// response.failed payload before it can be forwarded, matched by a generic
// passthrough rule, or treated as an account failure. Upstreams may return this
// terminal inside an otherwise successful HTTP or WebSocket transport.
func openAIContinuationStateErrorFromFailedEvent(statusCode int, responseHeaders http.Header, payload []byte) *UpstreamFailoverError {
	message := extractOpenAISSEErrorMessage(payload)
	if classifyOpenAIContinuationStateError(message, payload) == openAIContinuationStateErrorNone {
		return nil
	}
	return NewOpenAIContinuationStateUnavailableError(statusCode, responseHeaders, append([]byte(nil), payload...))
}

func isOpenAIInvalidEncryptedContentError(upstreamMsg string, upstreamBody []byte) bool {
	return classifyOpenAIContinuationStateError(upstreamMsg, upstreamBody) == openAIContinuationStateErrorInvalidEncryptedContent
}

// openAIOpaqueStreamPreflightReason identifies a stream:true request that a
// compatibility upstream rejected with an otherwise unclassified 400 before it
// emitted any Responses event.  It is request-scoped: changing accounts cannot
// make an opaque client request valid, and treating it as an account failure
// would contaminate scheduler health with a request-local condition.
const openAIOpaqueStreamPreflightReason = GatewayFailureReason("openai_opaque_stream_preflight")

// isOpenAIOpaqueCompatibilityBadRequest detects the narrow compatibility
// wrapper shape where an upstream exposes only a generic 400.  Explicit error
// codes and the semantic cases handled elsewhere deliberately remain on their
// existing paths; this is only the no-code fallback needed to preserve the
// Responses streaming contract without retrying or penalizing an account.
func isOpenAIOpaqueCompatibilityBadRequest(statusCode int, upstreamMsg string, upstreamBody []byte) bool {
	if statusCode != http.StatusBadRequest {
		return false
	}
	if classifyOpenAIContinuationStateError(upstreamMsg, upstreamBody) != openAIContinuationStateErrorNone {
		return false
	}
	if hasOpenAIUpstreamStructuredErrorCode(upstreamBody) {
		return false
	}
	if isOpenAIContextWindowError(upstreamMsg, upstreamBody) ||
		isOpenAITransientProcessingError(statusCode, upstreamMsg, upstreamBody) {
		return false
	}
	return true
}

func hasOpenAIUpstreamStructuredErrorCode(upstreamBody []byte) bool {
	if strings.TrimSpace(extractUpstreamErrorCode(upstreamBody)) != "" {
		return true
	}
	for _, path := range []string{
		"error.code",
		"response.error.code",
		"code",
		"response.code",
	} {
		if strings.TrimSpace(gjson.GetBytes(upstreamBody, path).String()) != "" {
			return true
		}
	}
	return false
}

// isOpenAIOpaqueContinuationToolChainBadRequest handles an upstream that has
// removed the normal continuation-state code/message from a 400.  The request
// shape is intentionally strict: an encrypted reasoning carrier plus a
// function_call_output is an upstream-bound tool continuation and cannot be
// safely replayed after its state disappears.  The caller must terminate it;
// it must not strip the tool output, switch accounts, or downgrade health.
func isOpenAIOpaqueContinuationToolChainBadRequest(
	statusCode int,
	requestBody []byte,
	upstreamMsg string,
	upstreamBody []byte,
) bool {
	if !isOpenAIOpaqueCompatibilityBadRequest(statusCode, upstreamMsg, upstreamBody) {
		return false
	}
	if !ValidateFunctionCallOutputContextBytes(requestBody).HasFunctionCallOutput {
		return false
	}

	input := parseRawJSONView(requestBody).Get("input")
	if !input.IsArray() {
		return false
	}
	hasEncryptedContinuationItem := false
	input.ForEach(func(_, item gjson.Result) bool {
		if !item.IsObject() {
			return true
		}
		switch strings.TrimSpace(item.Get("type").String()) {
		case "reasoning", "compaction", "compaction_summary":
			if encrypted := strings.TrimSpace(item.Get("encrypted_content").String()); encrypted != "" {
				hasEncryptedContinuationItem = true
				return false
			}
		}
		return true
	})
	return hasEncryptedContinuationItem
}

func newOpenAIOpaqueStreamPreflightError(
	statusCode int,
	responseHeaders http.Header,
	responseBody []byte,
) *UpstreamFailoverError {
	return &UpstreamFailoverError{
		StatusCode:                   statusCode,
		ResponseBody:                 responseBody,
		ResponseHeaders:              responseHeaders.Clone(),
		Scope:                        GatewayFailureScopeRequest,
		Reason:                       openAIOpaqueStreamPreflightReason,
		NextAccountAction:            NextAccountStop,
		SuppressAccountHealthPenalty: true,
	}
}

// IsOpenAIOpaqueStreamPreflight reports the bounded no-code compatibility
// wrapper that must be framed as a Responses terminal before any upstream
// stream byte exists.  It is intentionally distinct from normal HTTP errors.
func (e *UpstreamFailoverError) IsOpenAIOpaqueStreamPreflight() bool {
	return e != nil && e.Reason == openAIOpaqueStreamPreflightReason
}

func isOpenAIRequestBodyTooLargeError(statusCode int, upstreamMsg string, upstreamBody []byte) bool {
	return statusCode == http.StatusRequestEntityTooLarge && !isOpenAIContextWindowError(upstreamMsg, upstreamBody)
}

func newOpenAIUpstreamFailoverError(
	statusCode int,
	responseHeaders http.Header,
	responseBody []byte,
	upstreamMsg string,
	retryableOnSameAccount bool,
) *UpstreamFailoverError {
	if isOpenAIContinuationStateError(upstreamMsg, responseBody) {
		return NewOpenAIContinuationStateUnavailableError(statusCode, responseHeaders, responseBody)
	}
	requestScopedCapacity := isOpenAIRequestScopedCapacityShed(upstreamMsg, responseBody)
	failoverErr := &UpstreamFailoverError{
		StatusCode:             statusCode,
		ResponseBody:           responseBody,
		ResponseHeaders:        responseHeaders.Clone(),
		RetryableOnSameAccount: retryableOnSameAccount || requestScopedCapacity,
		RequestScopedTransient: requestScopedCapacity,
	}
	if isOpenAIRequestBodyTooLargeError(statusCode, upstreamMsg, responseBody) {
		failoverErr.RetryableOnSameAccount = false
		failoverErr.RequestScopedTransient = false
		failoverErr.Scope = GatewayFailureScopeAccount
		failoverErr.Reason = openAIRequestBodyTooLargeReason
		failoverErr.NextAccountAction = NextAccountRetry
		failoverErr.ClientStatusCode = http.StatusRequestEntityTooLarge
		failoverErr.ClientMessage = OpenAIRequestBodyTooLargeClientMessage
	}
	if isOpenAIModelNotSupportedError(statusCode, upstreamMsg, responseBody) {
		// Do not retry the same credential: this response is specific to its
		// advertised model capability.  Keep the raw status/body for internal
		// attribution; exhausted failover follows the normal sanitized envelope.
		failoverErr.RetryableOnSameAccount = false
		failoverErr.RequestScopedTransient = false
		failoverErr.Scope = GatewayFailureScopeAccount
		failoverErr.Reason = openAIModelNotSupportedReason
		failoverErr.NextAccountAction = NextAccountRetry
	}
	if isOpenAIHTTPUpstreamAccessStateError(statusCode, upstreamMsg, responseBody) {
		failoverErr.RetryableOnSameAccount = false
		failoverErr.RequestScopedTransient = false
		failoverErr.Stage = GatewayFailureStageAccountAuth
		failoverErr.Scope = GatewayFailureScopeAccount
		failoverErr.Reason = OpenAIUpstreamAccessStateReason
		failoverErr.NextAccountAction = NextAccountRetry
		failoverErr.ClientStatusCode = http.StatusBadGateway
		failoverErr.ClientMessage = openAIUpstreamAccessUnavailableClientMessage
	} else if requestScopedCapacity {
		failoverErr.ClientStatusCode = http.StatusServiceUnavailable
		failoverErr.ClientMessage = openAICapacityShedClientMessage(upstreamMsg, responseBody)
	}
	return failoverErr
}

// IsOpenAIRequestBodyTooLarge reports whether another account may accept the
// same request even though the selected account rejected its serialized size.
func (e *UpstreamFailoverError) IsOpenAIRequestBodyTooLarge() bool {
	return e != nil && e.Reason == openAIRequestBodyTooLargeReason
}

// NewOpenAIContinuationStateUnavailableError creates a request-scoped terminal
// error. It intentionally carries the original upstream response only for
// internal correlation; clients receive the fixed, non-sensitive message.
func NewOpenAIContinuationStateUnavailableError(
	statusCode int,
	responseHeaders http.Header,
	responseBody []byte,
) *UpstreamFailoverError {
	return &UpstreamFailoverError{
		StatusCode:                   statusCode,
		ResponseBody:                 responseBody,
		ResponseHeaders:              responseHeaders.Clone(),
		Scope:                        GatewayFailureScopeRequest,
		Reason:                       openAIContinuationStateUnavailableReason,
		NextAccountAction:            NextAccountStop,
		ClientStatusCode:             http.StatusBadRequest,
		ClientMessage:                OpenAIContinuationStateUnavailableClientMessage,
		SuppressAccountHealthPenalty: true,
	}
}

// NewOpenAIContinuationStoreUnavailableError distinguishes an authoritative
// state-store failure from a genuine binding miss.
func NewOpenAIContinuationStoreUnavailableError() *UpstreamFailoverError {
	err := NewOpenAIContinuationStateUnavailableError(http.StatusServiceUnavailable, nil, nil)
	err.ClientStatusCode = http.StatusServiceUnavailable
	return err
}

// IsOpenAIContinuationStateUnavailable reports a continuation-state failure
// that must not switch accounts or reduce selected-account health.
func (e *UpstreamFailoverError) IsOpenAIContinuationStateUnavailable() bool {
	return e != nil && e.Reason == openAIContinuationStateUnavailableReason
}

func marshalOpenAIUpstreamJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	if len(out) > 0 && out[len(out)-1] == '\n' {
		out = out[:len(out)-1]
	}
	return out, nil
}

func openAIUpstreamErrorBodyReadLimitForConfig(cfg *config.Config) int64 {
	limit := openAIUpstreamErrorBodyReadLimit
	if cfg != nil && cfg.Gateway.LogUpstreamErrorBody && cfg.Gateway.LogUpstreamErrorBodyMaxBytes > int(limit) {
		limit = int64(cfg.Gateway.LogUpstreamErrorBodyMaxBytes)
	}
	return limit
}

func (s *OpenAIGatewayService) readUpstreamErrorBody(resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	cfg := (*config.Config)(nil)
	if s != nil {
		cfg = s.cfg
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, openAIUpstreamErrorBodyReadLimitForConfig(cfg)))
	return body
}

// handleCindyBalanceHTTPFailover consumes Cindy's exact structured HTTP 429
// before any generic retry, recovery, rewriting, or account-health policy can
// reinterpret it. The permanent runtime block is installed synchronously; DB
// persistence retries remain owned by RateLimitService.
func (s *OpenAIGatewayService) handleCindyBalanceHTTPFailover(
	ctx context.Context,
	account *Account,
	statusCode int,
	headers http.Header,
	body []byte,
	canonicalModel ...string,
) (*UpstreamFailoverError, bool) {
	if ClassifyCindyBalanceInsufficient(account, statusCode, body) != CindyBalanceSignalHTTP429 {
		return nil, false
	}

	s.handleOpenAIAccountUpstreamError(ctx, account, statusCode, headers, body, canonicalModel...)
	failoverErr := newOpenAIUpstreamFailoverError(
		statusCode,
		headers,
		body,
		"Cindy balance exhausted",
		false,
	)
	failoverErr.Scope = GatewayFailureScopeAccount
	failoverErr.NextAccountAction = NextAccountRetry
	failoverErr.CindyBalanceInsufficient = true
	return sanitizeOpenAICindyFailoverError(failoverErr), true
}

func (s *OpenAIGatewayService) handleFailoverSideEffects(ctx context.Context, resp *http.Response, account *Account, responseBody []byte, canonicalModel ...string) bool {
	if len(canonicalModel) > 0 {
		return s.handleOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, responseBody, canonicalModel[0])
	}
	return s.handleOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, responseBody)
}

func (s *OpenAIGatewayService) handleErrorResponse(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *Account,
	requestBody []byte,
	requestedModel ...string,
) (*OpenAIForwardResult, error) {
	body := s.readUpstreamErrorBody(resp)
	if failoverErr, ok := s.handleCindyBalanceHTTPFailover(ctx, account, resp.StatusCode, resp.Header, body, requestedModel...); ok {
		return nil, failoverErr
	}
	body = s.redactAgentIdentitySensitiveBody(ctx, account, body)
	body = s.rewriteBusinessSystemPromptJSONForRequest(c, body, BusinessSystemPromptProtocolResponses)

	// cyber_policy 不冷却账号，并保留内部标记供 handler 事后写风控/邮件。
	// 开关开启时只做请求级账号 failover；关闭时维持原始上游透传。
	if hit, _, cyberMsg := detectOpenAICyberPolicy(body); hit {
		markOpenAICyberPolicyFromResponse(c, resp.StatusCode, body)
		if isOpenAIRefusalRecoveryResponsesRequest(c) {
			runtime := s.openAIRefusalRecoveryRuntime(ctx)
			if runtime.CyberFailoverEnabled() {
				return nil, NewOpenAICyberFailoverError(body, resp.Header)
			}
		}
		setOpsUpstreamError(c, resp.StatusCode, cyberMsg, truncateString(string(body), 2048))
		writeOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json"
		}
		c.Data(resp.StatusCode, contentType, body)
		if cyberMsg == "" {
			return nil, fmt.Errorf("openai cyber_policy: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("openai cyber_policy: %s", cyberMsg)
	}
	if account != nil && account.Platform == PlatformGrok && isGrokContentPolicyRejection(resp.StatusCode, body) {
		clientMsg := grokContentPolicyClientMessage(body)
		setOpsUpstreamError(c, resp.StatusCode, clientMsg, truncateString(string(body), 2048))
		writeOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
		MarkResponseCommitted(c)
		c.JSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"type":    "invalid_request_error",
				"message": clientMsg,
			},
		})
		return nil, fmt.Errorf("grok content policy rejection: %s", clientMsg)
	}

	upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(body))
	upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = truncateString(string(body), maxBytes)
	}
	setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	logOpenAIInstructionsRequiredDebug(ctx, c, account, resp.StatusCode, upstreamMsg, requestBody, body)

	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		logger.LegacyPrintf("service.openai_gateway",
			"OpenAI upstream error %d (account=%d platform=%s type=%s): %s",
			resp.StatusCode,
			account.ID,
			account.Platform,
			account.Type,
			truncateForLog(body, s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes),
		)
	}

	if isOpenAIRequestBodyTooLargeError(resp.StatusCode, upstreamMsg, body) {
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "failover",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})
		s.handleOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, requestedModel...)
		return nil, newOpenAIUpstreamFailoverError(
			resp.StatusCode,
			resp.Header,
			body,
			upstreamMsg,
			false,
		)
	}

	if status, errType, errMsg, matched := applyErrorPassthroughRule(
		c,
		PlatformOpenAI,
		resp.StatusCode,
		body,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	); matched {
		MarkResponseCommitted(c)
		c.JSON(status, gin.H{
			"error": gin.H{
				"type":    errType,
				"message": errMsg,
			},
		})
		if upstreamMsg == "" {
			upstreamMsg = errMsg
		}
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream error: %d (passthrough rule matched)", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d (passthrough rule matched) message=%s", resp.StatusCode, upstreamMsg)
	}

	// Check custom error codes
	if !account.ShouldHandleErrorCode(resp.StatusCode) {
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})
		MarkResponseCommitted(c)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "upstream_error",
				"message": "Upstream gateway error",
			},
		})
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d (not in custom error codes) message=%s", resp.StatusCode, upstreamMsg)
	}

	// Handle upstream error (mark account status)
	var reqModel string
	if len(requestedModel) > 0 {
		reqModel = strings.TrimSpace(requestedModel[0])
	}
	if reqModel == "" {
		reqModel, _, _ = extractOpenAIRequestMetaFromBody(requestBody)
		reqModel = canonicalOpenAIAccountSchedulingModel(account, reqModel)
	}
	shouldDisable := s.handleOpenAIAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, reqModel)
	kind := "http_error"
	if shouldDisable {
		kind = "failover"
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-request-id"),
		Kind:               kind,
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})
	if shouldDisable {
		return nil, &UpstreamFailoverError{
			StatusCode:             resp.StatusCode,
			ResponseBody:           body,
			RetryableOnSameAccount: false,
		}
	}

	MarkResponseCommitted(c)

	// 上游 400 是确定性的请求错误：同一份请求体换账号、重试多少次都会失败。归一成
	// 502 upstream_error 会让下游网关把它当成可重试的上游故障反复重放（#5479 实测
	// 30 个失败请求被放大成 60 次上游调用），同时抹掉客户端定位问题所需的 code/param。
	//
	// 走到这里说明 shouldFailoverOpenAIUpstreamResponse 已判定该 400 不可 failover，
	// 即 server_is_overloaded / at capacity 这类可重试的 400 不会到达此处。
	//
	// 兄弟路径早已这么做：handleCompatErrorResponse（ChatCompletions / Anthropic）
	// 回真实状态码 + invalid_request_error + 真实 message；/v1/images 还额外透传
	// code/param。原生 Responses 是唯一漏掉的一条。
	if isOpenAIDeterministicClientError(resp.StatusCode) {
		writeOpenAIUpstreamClientError(c, resp.StatusCode, body, upstreamMsg)
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream error: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
	}

	// Return appropriate error response
	var errType, errMsg string
	var statusCode int

	switch resp.StatusCode {
	case 401:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream authentication failed, please contact administrator"
	case 402:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream payment required: insufficient balance or billing issue"
	case 403:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream access forbidden, please contact administrator"
	case 429:
		statusCode = http.StatusTooManyRequests
		errType = "rate_limit_error"
		errMsg = "Upstream rate limit exceeded, please retry later"
	default:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream request failed"
	}
	if isOpenAIContextWindowError(upstreamMsg, body) && upstreamMsg != "" {
		errMsg = upstreamMsg
	}

	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": errMsg,
		},
	})

	if upstreamMsg == "" {
		return nil, fmt.Errorf("upstream error: %d", resp.StatusCode)
	}
	return nil, fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
}

// compatErrorWriter is the signature for format-specific error writers used by
// the compat paths (Chat Completions and Anthropic Messages).
type compatErrorWriter func(c *gin.Context, statusCode int, errType, message string)

// handleCompatErrorResponse is the shared non-failover error handler for the
// Chat Completions and Anthropic Messages compat paths. It mirrors the logic of
// handleErrorResponse (passthrough rules, ShouldHandleErrorCode, rate-limit
// tracking, secondary failover) but delegates the final error write to the
// format-specific writer function.
func (s *OpenAIGatewayService) handleCompatErrorResponse(
	resp *http.Response,
	c *gin.Context,
	account *Account,
	writeError compatErrorWriter,
	requestedModel ...string,
) (*OpenAIForwardResult, error) {
	body := s.readUpstreamErrorBody(resp)
	if failoverErr, ok := s.handleCindyBalanceHTTPFailover(
		c.Request.Context(), account, resp.StatusCode, resp.Header, body, requestedModel...,
	); ok {
		return nil, failoverErr
	}
	body = s.redactAgentIdentitySensitiveBody(context.Background(), account, body)
	body = s.rewriteBusinessSystemPromptJSONForAnyRequest(c, body)

	// cyber_policy：兼容路径（Chat Completions / Anthropic）以各自格式回写错误，
	// 不原样透传 responses 格式的 cyber body（否则对下游格式不合法）。cyber 是上游网络
	// 安全策略拦截，不冷却账号，故标记后直接以兼容格式回写错误并返回，跳过下方
	// handleOpenAIAccountUpstreamError（避免自定义 temp-unschedulable 规则误冷却）。
	if hit, code, cyberMsg := detectOpenAICyberPolicy(body); hit {
		MarkOpsCyberPolicy(c, CyberPolicyMark{
			Code:           code,
			Message:        cyberMsg,
			Body:           truncateString(string(body), 4096),
			UpstreamStatus: resp.StatusCode,
		})
		setOpsUpstreamError(c, resp.StatusCode, cyberMsg, truncateString(string(body), 2048))
		clientMsg := cyberMsg
		if clientMsg == "" {
			clientMsg = "Request blocked by upstream cyber-security policy"
		}
		writeError(c, resp.StatusCode, "invalid_request_error", clientMsg)
		if cyberMsg == "" {
			return nil, fmt.Errorf("openai cyber_policy: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("openai cyber_policy: %s", cyberMsg)
	}
	if account != nil && account.Platform == PlatformGrok && isGrokContentPolicyRejection(resp.StatusCode, body) {
		clientMsg := grokContentPolicyClientMessage(body)
		setOpsUpstreamError(c, resp.StatusCode, clientMsg, truncateString(string(body), 2048))
		MarkResponseCommitted(c)
		writeError(c, http.StatusForbidden, "invalid_request_error", clientMsg)
		return nil, fmt.Errorf("grok content policy rejection: %s", clientMsg)
	}

	upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(body))
	if upstreamMsg == "" {
		upstreamMsg = fmt.Sprintf("Upstream error: %d", resp.StatusCode)
	}
	upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)

	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = truncateString(string(body), maxBytes)
	}
	setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)

	// Apply error passthrough rules
	if status, errType, errMsg, matched := applyErrorPassthroughRule(
		c, account.Platform, resp.StatusCode, body,
		http.StatusBadGateway, "api_error", "Upstream request failed",
	); matched {
		MarkResponseCommitted(c)
		writeError(c, status, errType, errMsg)
		if upstreamMsg == "" {
			upstreamMsg = errMsg
		}
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream error: %d (passthrough rule matched)", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d (passthrough rule matched) message=%s", resp.StatusCode, upstreamMsg)
	}

	// Check custom error codes — if the account does not handle this status,
	// return a generic error without exposing upstream details.
	if !account.ShouldHandleErrorCode(resp.StatusCode) {
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})
		MarkResponseCommitted(c)
		writeError(c, http.StatusInternalServerError, "api_error", "Upstream gateway error")
		if upstreamMsg == "" {
			return nil, fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d (not in custom error codes) message=%s", resp.StatusCode, upstreamMsg)
	}

	// Track rate limits and decide whether to trigger secondary failover.
	var modelForCooldown string
	if len(requestedModel) > 0 {
		modelForCooldown = requestedModel[0]
	}
	shouldDisable := s.handleOpenAIAccountUpstreamError(
		c.Request.Context(), account, resp.StatusCode, resp.Header, body, modelForCooldown,
	)
	kind := "http_error"
	if shouldDisable {
		kind = "failover"
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-request-id"),
		Kind:               kind,
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})
	if shouldDisable {
		return nil, &UpstreamFailoverError{
			StatusCode:             resp.StatusCode,
			ResponseBody:           body,
			RetryableOnSameAccount: false,
		}
	}

	MarkResponseCommitted(c)

	// Map status code to error type and write response
	errType := "api_error"
	switch {
	case resp.StatusCode == 400:
		errType = "invalid_request_error"
	case resp.StatusCode == 404:
		errType = "not_found_error"
	case resp.StatusCode == 429:
		errType = "rate_limit_error"
	case resp.StatusCode >= 500:
		errType = "api_error"
	}

	writeError(c, resp.StatusCode, errType, upstreamMsg)
	return nil, fmt.Errorf("upstream error: %d %s", resp.StatusCode, upstreamMsg)
}
