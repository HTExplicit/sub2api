package service

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

const (
	openAIUpstreamAccessUnavailableClientMessage = "Upstream access is temporarily unavailable, please retry later"
	OpenAIUpstreamAccessStateReason              = GatewayFailureReason("openai_upstream_access_state")
	openAIImagesVerbatimPromptInstructions       = "When invoking the image_generation tool, use the user's image prompt verbatim. Do not rewrite, expand, summarize, embellish, translate, normalize punctuation, or add or remove visual details or constraints. Preserve the original language, wording, capitalization, quotes, and punctuation exactly."
	defaultAntigravityTestModel                  = "claude-sonnet-4-6"
)

// openAIRequestPayloadView unwraps Responses WebSocket event envelopes while
// leaving ordinary HTTP objects untouched.
func openAIRequestPayloadView(body []byte) gjson.Result {
	root := parseRawJSONView(body)
	eventType := strings.ToLower(strings.TrimSpace(root.Get("type").String()))
	if strings.HasPrefix(eventType, "response.") {
		if response := root.Get("response"); response.Exists() && response.IsObject() {
			return response
		}
	}
	return root
}

func effectiveOpenAISSEEventType(payload []byte, eventType string) string {
	if payloadType := strings.TrimSpace(gjson.GetBytes(payload, "type").String()); payloadType != "" {
		return payloadType
	}
	return strings.TrimSpace(eventType)
}

func mergeOpenAIUsageNonZero(dst *OpenAIUsage, src OpenAIUsage) {
	if dst == nil {
		return
	}
	if src.InputTokens > 0 {
		dst.InputTokens = src.InputTokens
	}
	if src.ImageInputTokens > 0 {
		dst.ImageInputTokens = src.ImageInputTokens
	}
	if src.OutputTokens > 0 {
		dst.OutputTokens = src.OutputTokens
	}
	if src.CacheCreationInputTokens > 0 {
		dst.CacheCreationInputTokens = src.CacheCreationInputTokens
	}
	if src.CacheReadInputTokens > 0 {
		dst.CacheReadInputTokens = src.CacheReadInputTokens
	}
	if src.ImageOutputTokens > 0 {
		dst.ImageOutputTokens = src.ImageOutputTokens
	}
}

func openAIUsageHasTokens(usage *OpenAIUsage) bool {
	return usage != nil && (usage.InputTokens > 0 || usage.ImageInputTokens > 0 ||
		usage.OutputTokens > 0 || usage.CacheCreationInputTokens > 0 ||
		usage.CacheReadInputTokens > 0 || usage.ImageOutputTokens > 0)
}

func antigravityConnectionTestModel(modelID string) string {
	if modelID == "" {
		return defaultAntigravityTestModel
	}
	return modelID
}

func openAICompatTerminalResponse(event *apicompat.ResponsesStreamEvent, payload []byte) *apicompat.ResponsesResponse {
	if event == nil {
		return nil
	}
	if event.Response != nil {
		return event.Response
	}
	switch strings.TrimSpace(event.Type) {
	case "response.failed", "error":
		message := extractOpenAISSEErrorMessage(payload)
		if message == "" {
			message = "Upstream response failed"
		}
		return &apicompat.ResponsesResponse{Status: "failed", Error: &apicompat.ResponsesError{Code: event.Code, Message: message}}
	default:
		return nil
	}
}

const openAIMissingUsageLogInterval = time.Minute

type openAIMissingUsageLogSampler struct {
	total      atomic.Uint64
	suppressed atomic.Uint64
	lastLog    atomic.Int64
}

var openAIMissingUsageSampler openAIMissingUsageLogSampler

func (s *openAIMissingUsageLogSampler) sample(now time.Time) (logNow bool, total uint64, suppressed uint64) {
	total = s.total.Add(1)
	nowNanos := now.UnixNano()
	for {
		last := s.lastLog.Load()
		if last != 0 && nowNanos-last < int64(openAIMissingUsageLogInterval) {
			s.suppressed.Add(1)
			return false, total, 0
		}
		if s.lastLog.CompareAndSwap(last, nowNanos) {
			return true, total, s.suppressed.Swap(0)
		}
	}
}

func logOpenAISuccessMissingUsage(ctx context.Context, c *gin.Context, account *Account, resp *http.Response, usage *OpenAIUsage, terminalEvent string, clientDisconnected bool) {
	if resp == nil || resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices || openAIUsageHasTokens(usage) {
		return
	}
	terminalEvent = strings.TrimSpace(terminalEvent)
	if terminalEvent != "response.completed" && terminalEvent != "response.done" && terminalEvent != "[DONE]" && terminalEvent != "json" {
		return
	}
	logNow, total, suppressed := openAIMissingUsageSampler.sample(time.Now())
	if !logNow {
		return
	}
	accountID := int64(0)
	accountType := ""
	if account != nil {
		accountID = account.ID
		accountType = string(account.Type)
	}
	inboundEndpoint := ""
	if c != nil && c.Request != nil && c.Request.URL != nil {
		inboundEndpoint = c.Request.URL.Path
	}
	logger.FromContext(ctx).With(
		zap.String("component", "service.openai_gateway"),
		zap.Int64("account_id", accountID),
		zap.String("account_type", accountType),
		zap.String("inbound_endpoint", inboundEndpoint),
		zap.String("upstream_request_id", strings.TrimSpace(resp.Header.Get("x-request-id"))),
		zap.Int("upstream_status_code", resp.StatusCode),
		zap.String("terminal_event", terminalEvent),
		zap.Bool("client_disconnected", clientDisconnected),
		zap.Uint64("missing_usage_total", total),
		zap.Uint64("suppressed_since_last", suppressed),
	).Warn("openai_usage.success_missing_usage")
}

func openAIAlphaSearchSchedulingModel(account *Account, requestedModel string) string {
	return canonicalOpenAIAccountSchedulingModel(account, requestedModel)
}

func resolveOpenAIForwardMappedModels(account *Account, requestedModel string, requireCompact bool) (billingModel, upstreamModel string) {
	requestedModel = strings.TrimSpace(requestedModel)
	if account != nil && account.IsOpenAIPassthroughEnabled() {
		billingModel = requestedModel
	} else if account != nil {
		billingModel = strings.TrimSpace(account.GetMappedModel(requestedModel))
	}
	if billingModel == "" {
		billingModel = requestedModel
	}
	upstreamModel = resolveOpenAIAccountUpstreamModelForRequest(account, requestedModel, requireCompact)
	if strings.TrimSpace(upstreamModel) == "" {
		upstreamModel = billingModel
	}
	return billingModel, upstreamModel
}

func resolveOpenAIErrorSchedulingModel(billingModel, upstreamModel string) string {
	if upstreamModel = strings.TrimSpace(upstreamModel); upstreamModel != "" {
		return upstreamModel
	}
	return strings.TrimSpace(billingModel)
}

func isOpenAIUpstreamAccessStateError(_ string, body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	for _, path := range []string{"error.code", "response.error.code", "detail.code", "code"} {
		if isOpenAIUpstreamAccessStateCode(gjson.GetBytes(body, path).String()) {
			return true
		}
	}
	return false
}

func isOpenAIUpstreamAccessStateCode(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "deactivated_workspace" {
		return true
	}
	for _, subject := range []string{"workspace", "account", "organization", "org"} {
		for _, state := range []string{"deactivated", "disabled", "suspended"} {
			if value == subject+"_"+state || value == state+"_"+subject {
				return true
			}
		}
	}
	return false
}

func isOpenAIHTTPUpstreamAccessStateError(_ int, _ string, body []byte) bool {
	return isOpenAIUpstreamAccessStateError("", body)
}

func openAICapacityShedClientMessage(upstreamMsg string, body []byte) string {
	for _, candidate := range []string{
		upstreamMsg,
		gjson.GetBytes(body, "error.message").String(),
		gjson.GetBytes(body, "response.error.message").String(),
		gjson.GetBytes(body, "message").String(),
	} {
		candidate = sanitizeUpstreamErrorMessage(strings.TrimSpace(candidate))
		if candidate != "" && isOpenAICapacityShedMessage(candidate) {
			return candidate
		}
	}
	return "Upstream service is temporarily overloaded, please retry later"
}

func (e *UpstreamFailoverError) IsOpenAICapacityShed() bool {
	return e != nil && e.RequestScopedTransient && isOpenAIRequestScopedCapacityShed("", e.ResponseBody)
}

func openAIStream403AccountFailure(payload []byte, message string) bool {
	return isOpenAIUpstreamAccessStateError(message, payload) || openAIStreamCredentialAuthFailure(payload)
}

func openAIStreamCredentialAuthFailure(payload []byte) bool {
	if len(bytes.TrimSpace(payload)) == 0 || !gjson.ValidBytes(payload) {
		return false
	}
	for _, path := range []string{"response.error.status_code", "error.status_code", "status_code"} {
		if int(gjson.GetBytes(payload, path).Int()) == http.StatusUnauthorized {
			return true
		}
	}
	for _, path := range []string{"response.error.type", "error.type", "type"} {
		errType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, path).String()))
		if errType == "authentication_error" || errType == "authentication_failed" || errType == "unauthorized_error" {
			return true
		}
	}
	for _, path := range []string{"response.error.code", "error.code", "code"} {
		switch strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, path).String())) {
		case "invalid_api_key", "api_key_disabled", "unauthorized", "authentication_error",
			"invalid_token", "access_token_invalid", "token_revoked", "token_invalidated",
			"invalid_credentials", "credential_invalid":
			return true
		}
	}
	return false
}

func markOpenAIWSClientVisibleFailure(c *gin.Context, eventType string, payload []byte) {
	eventType = strings.TrimSpace(eventType)
	if eventType != "error" && eventType != "response.failed" {
		return
	}
	prefix := "error"
	if eventType == "response.failed" {
		prefix = "response.error"
	}
	code := strings.TrimSpace(gjson.GetBytes(payload, prefix+".code").String())
	errType := strings.TrimSpace(gjson.GetBytes(payload, prefix+".type").String())
	message := strings.TrimSpace(gjson.GetBytes(payload, prefix+".message").String())
	if eventType == "response.failed" && code == "" && errType == "" && message == "" {
		prefix = "error"
		code = strings.TrimSpace(gjson.GetBytes(payload, prefix+".code").String())
		errType = strings.TrimSpace(gjson.GetBytes(payload, prefix+".type").String())
		message = strings.TrimSpace(gjson.GetBytes(payload, prefix+".message").String())
	}
	status := int(gjson.GetBytes(payload, prefix+".status_code").Int())
	if status == 0 {
		status = int(gjson.GetBytes(payload, prefix+".status").Int())
	}
	if status == 0 && eventType == "error" {
		status = int(gjson.GetBytes(payload, "status").Int())
	}
	if status == 0 {
		status = openAIWSErrorHTTPStatusFromRaw(code, errType)
	}
	if errType == "" {
		errType = "upstream_error"
	}
	if code == "" {
		code = strings.ReplaceAll(eventType, ".", "_")
	}
	if message == "" {
		message = "upstream websocket request failed"
	}
	MarkOpsStreamFailure(c, errType, code, message, status)
}

const openAIHTTPResponseOwnerContextKey = "openai_http_response_owner"

type openAIHTTPResponseOwner struct {
	userID   int64
	apiKeyID int64
}

func SetOpenAIHTTPResponseOwner(c *gin.Context, userID, apiKeyID int64) {
	if c == nil || userID <= 0 || apiKeyID <= 0 {
		return
	}
	c.Set(openAIHTTPResponseOwnerContextKey, openAIHTTPResponseOwner{userID: userID, apiKeyID: apiKeyID})
}

func (s *OpenAIGatewayService) ValidateOpenAIHTTPResponseOwner(ctx context.Context, groupID int64, responseID string, userID, apiKeyID int64) (bool, error) {
	if s == nil || strings.TrimSpace(responseID) == "" || userID <= 0 || apiKeyID <= 0 {
		return false, nil
	}
	ownerUserID, ownerAPIKeyID, found, err := s.getOpenAIWSStateStore().GetHTTPResponseOwner(ctx, groupID, responseID)
	if err != nil || !found {
		return false, err
	}
	return ownerUserID == userID || (ownerUserID <= 0 && ownerAPIKeyID == apiKeyID), nil
}

func (s *OpenAIGatewayService) BindOpenAIHTTPResponseOwner(ctx context.Context, groupID int64, responseID string, userID, apiKeyID int64) error {
	if s == nil {
		return nil
	}
	return s.getOpenAIWSStateStore().BindHTTPResponseOwner(ctx, groupID, responseID, userID, apiKeyID, s.openAIWSResponseStickyTTL())
}

func (s *OpenAIGatewayService) newOpenAIAccountFailoverError(
	account *Account,
	statusCode int,
	responseHeaders http.Header,
	responseBody []byte,
	upstreamMsg string,
	shouldDisable bool,
	retryableOnSameAccount bool,
) *UpstreamFailoverError {
	return s.newOpenAIAccountFailoverErrorWithClassificationHeaders(
		account, statusCode, responseHeaders, responseHeaders, responseBody, upstreamMsg, shouldDisable, retryableOnSameAccount,
	)
}

func (s *OpenAIGatewayService) newOpenAIAccountFailoverErrorWithClassificationHeaders(
	account *Account,
	statusCode int,
	responseHeaders http.Header,
	classificationHeaders http.Header,
	responseBody []byte,
	upstreamMsg string,
	shouldDisable bool,
	retryableOnSameAccount bool,
) *UpstreamFailoverError {
	oauth429Retry := s.shouldRetryOpenAIOAuth429OnSameAccountWithResponse(
		account, statusCode, shouldDisable, classificationHeaders, responseBody,
	)
	failoverErr := newOpenAIUpstreamFailoverError(
		statusCode, responseHeaders, responseBody, upstreamMsg, retryableOnSameAccount || oauth429Retry,
	)
	if oauth429Retry {
		failoverErr.SameAccountRetryDeadline = s.openAIOAuth429RetryDeadline(account)
		failoverErr.SameAccountRetryDelay = openAIOAuth429SameAccountRetryDelay(responseHeaders, failoverErr.SameAccountRetryDeadline)
	}
	return failoverErr
}

func (s *OpenAIGatewayService) newOpenAIWSRateLimitFailoverError(account *Account, headers http.Header, responseBody []byte, message string) *UpstreamFailoverError {
	return s.newOpenAIAccountFailoverError(
		account,
		http.StatusTooManyRequests,
		headers,
		responseBody,
		strings.TrimSpace(message),
		false,
		false,
	)
}
