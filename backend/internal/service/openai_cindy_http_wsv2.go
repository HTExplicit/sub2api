package service

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	openAICindyHTTPToWSV2Reason             = "cindy_http_to_wsv2"
	openAICindyHTTPToWSV2RequiredContextKey = "openai_cindy_http_to_wsv2_required"
	openAICindyHTTPToWSV2BypassContextKey   = "openai_cindy_http_to_wsv2_bypass"
	openAICindyHTTPToWSV2FailoverReason     = GatewayFailureReason("cindy_http_to_wsv2_first_turn_failover")
	openAICindyHTTPToWSV2TerminalReason     = GatewayFailureReason("websocket_terminal_failure")
)

func markOpenAICindyHTTPToWSV2Required(c *gin.Context) {
	if c != nil {
		c.Set(openAICindyHTTPToWSV2RequiredContextKey, true)
	}
}

func isOpenAICindyHTTPToWSV2Required(c *gin.Context) bool {
	if c == nil {
		return false
	}
	required, ok := c.Get(openAICindyHTTPToWSV2RequiredContextKey)
	return ok && required == true
}

func isOpenAICindyHTTPToWSV2Bypassed(c *gin.Context) bool {
	if c == nil {
		return false
	}
	bypassed, ok := c.Get(openAICindyHTTPToWSV2BypassContextKey)
	return ok && bypassed == true
}

func isOpenAICindyHTTPToWSV2HandshakeForbidden(err error) bool {
	var dialErr *openAIWSDialError
	if !errors.As(err, &dialErr) || dialErr == nil || dialErr.StatusCode != http.StatusForbidden {
		return false
	}
	hit, _, _ := detectOpenAICyberPolicy(dialErr.ResponseBody)
	return !hit
}

func prepareOpenAICindyStatelessHTTPFallback(body []byte) ([]byte, bool) {
	classification, err := ClassifyCindyContinuation(body, CindyContinuationProof{})
	if err != nil || classification.HasAnchor ||
		(classification.Mode != CindyContinuationFullReplay && classification.Mode != CindyContinuationOpaqueFull) {
		return body, false
	}
	input := gjson.GetBytes(body, "input")
	switch {
	case input.IsArray():
		return body, len(input.Array()) > 0
	case input.IsObject():
		return body, true
	case input.Type == gjson.String:
		return body, strings.TrimSpace(input.String()) != ""
	default:
		return body, false
	}
}

func newOpenAICindyHTTPToWSV2AccountRequiredError() *UpstreamFailoverError {
	return &UpstreamFailoverError{
		StatusCode:                   http.StatusServiceUnavailable,
		ResponseBody:                 append([]byte(nil), openAITransportFailoverBody...),
		Stage:                        GatewayFailureStageInference,
		Scope:                        GatewayFailureScopeRequest,
		Reason:                       GatewayFailureReason("cindy_http_to_wsv2_account_required"),
		NextAccountAction:            NextAccountRetry,
		ClientStatusCode:             http.StatusServiceUnavailable,
		ClientMessage:                "Temporary upstream failure",
		SuppressAccountHealthPenalty: true,
	}
}

func sanitizeOpenAICindyFailoverError(failoverErr *UpstreamFailoverError) *UpstreamFailoverError {
	if failoverErr == nil || failoverErr.IsOpenAIRefusalRecovery() {
		return failoverErr
	}
	// Classification and account-health updates have already consumed the raw
	// upstream body. Keep only a fixed envelope on the error that crosses into
	// the handler so exhausted failover cannot re-expose an account or WAF body
	// through a configured error-passthrough rule.
	failoverErr.ResponseBody = append([]byte(nil), openAITransportFailoverBody...)
	return failoverErr
}

func finishOpenAICindyHTTPToWSV2Failover(
	c *gin.Context,
	account *Account,
	failoverErr *UpstreamFailoverError,
) (error, bool) {
	if failoverErr == nil || account == nil {
		return nil, false
	}
	stage := failoverErr.Stage
	if stage == "" {
		stage = GatewayFailureStageInference
	}
	scope := failoverErr.Scope
	if scope == "" {
		scope = GatewayFailureScopeRequest
	}
	reason := failoverErr.Reason
	if reason == "" {
		reason = openAICindyHTTPToWSV2FailoverReason
	}
	failoverErr.Stage = stage
	failoverErr.Scope = scope
	failoverErr.Reason = reason
	failoverErr.CindyHTTPToWSV2FirstTurn = true
	message := strings.TrimSpace(failoverErr.ClientMessage)
	if message == "" {
		message = "Temporary upstream failure"
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		UpstreamStatusCode: failoverErr.StatusCode,
		UpstreamRequestID:  failoverErr.ResponseHeaders.Get("x-request-id"),
		Kind:               "failover",
		Stage:              string(stage),
		Scope:              string(scope),
		Reason:             string(reason),
		Message:            message,
	})
	return sanitizeOpenAICindyFailoverError(failoverErr), true
}

func (s *OpenAIGatewayService) cindyHTTPToWSV2ConfigEligible(account *Account) bool {
	if s == nil || s.cfg == nil || account == nil ||
		!IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) ||
		account.Concurrency <= 0 || account.IsOpenAIWSForceHTTPEnabled() ||
		!openai_compat.ShouldUseResponsesAPI(account.Extra) {
		return false
	}
	wsCfg := s.cfg.Gateway.OpenAIWS
	return wsCfg.CindyHTTPToWSV2Enabled && wsCfg.Enabled && wsCfg.APIKeyEnabled &&
		wsCfg.ResponsesWebsocketsV2 && !wsCfg.ForceHTTP
}

func isOpenAICindyHTTPToWSV2RequestEligible(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil ||
		GetOpenAIClientTransport(c) != OpenAIClientTransportHTTP ||
		c.Request.Method != http.MethodPost || isOpenAIResponsesCompactPath(c) {
		return false
	}
	if !openai.IsCodexOfficialClientByHeaders(c.GetHeader("User-Agent"), c.GetHeader("originator")) {
		return false
	}
	path := strings.TrimRight(strings.TrimSpace(c.Request.URL.Path), "/")
	switch path {
	case "/v1/responses", "/openai/v1/responses", "/responses", "/backend-api/codex/responses":
		return true
	default:
		return false
	}
}

func (s *OpenAIGatewayService) resolveCindyHTTPToWSV2Decision(
	c *gin.Context,
	account *Account,
) (OpenAIWSProtocolDecision, bool) {
	if !s.cindyHTTPToWSV2ConfigEligible(account) || !isOpenAICindyHTTPToWSV2RequestEligible(c) {
		return OpenAIWSProtocolDecision{}, false
	}
	return OpenAIWSProtocolDecision{
		Transport: OpenAIUpstreamTransportResponsesWebsocketV2,
		Reason:    openAICindyHTTPToWSV2Reason,
	}, true
}

func (s *OpenAIGatewayService) cindyHTTPToWSV2FirstTurnFailover(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	canonicalModel string,
	err error,
) (error, bool) {
	if err == nil || account == nil {
		return nil, false
	}
	var existing *UpstreamFailoverError
	if errors.As(err, &existing) && existing != nil {
		return finishOpenAICindyHTTPToWSV2Failover(c, account, existing)
	}

	var dialErr *openAIWSDialError
	if errors.As(err, &dialErr) && dialErr != nil && dialErr.StatusCode == http.StatusForbidden {
		if hit, _, _ := detectOpenAICyberPolicy(dialErr.ResponseBody); hit {
			markOpenAICyberPolicyFromResponse(c, dialErr.StatusCode, dialErr.ResponseBody)
			if s.openAIRefusalRecoveryRuntime(ctx).CyberFailoverEnabled() {
				return finishOpenAICindyHTTPToWSV2Failover(
					c, account, NewOpenAICyberFailoverError(dialErr.ResponseBody, dialErr.ResponseHeaders),
				)
			}
			return nil, false
		}
	}

	_, failoverErr := openAIWSInitialDialFailover(err)
	if failoverErr != nil {
		failoverErr.RetryableOnSameAccount = false
		if dialErr != nil && (dialErr.StatusCode == 0 || dialErr.StatusCode == http.StatusRequestTimeout ||
			dialErr.StatusCode >= http.StatusInternalServerError) {
			// Only a first-turn Cindy dial/handshake transport failure is an
			// account-scoped signal. Terminal WS events and request/provider
			// failures must remain request/provider scoped.
			failoverErr.Scope = GatewayFailureScopeAccount
		}
		if dialErr != nil && dialErr.StatusCode > 0 {
			_ = s.handleOpenAIAccountUpstreamError(
				ctx, account, dialErr.StatusCode, dialErr.ResponseHeaders, dialErr.ResponseBody, canonicalModel,
			)
			if dialErr.StatusCode == http.StatusForbidden && isHTMLResponse(dialErr.ResponseBody) {
				failoverErr.SuppressAccountHealthPenalty = true
			}
		}
		return finishOpenAICindyHTTPToWSV2Failover(c, account, failoverErr)
	}

	reason, retryable := classifyOpenAIWSReconnectReason(err)
	if coderws.CloseStatus(err) == coderws.StatusTryAgainLater || retryable {
		failoverErr := newOpenAIUpstreamFailoverError(
			http.StatusServiceUnavailable, nil, openAITransportFailoverBody, reason, false,
		)
		failoverErr.Reason = OpenAITransientTransportFailureReason
		return finishOpenAICindyHTTPToWSV2Failover(c, account, failoverErr)
	}
	return nil, false
}

func (s *OpenAIGatewayService) cindyHTTPToWSV2FirstTurnEventFailover(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	canonicalModel string,
	headers http.Header,
	payload []byte,
) (error, bool) {
	if account == nil {
		return nil, false
	}
	if failoverErr, ok := s.cindyBalanceTerminalFailover(ctx, account, headers, payload, canonicalModel); ok {
		return failoverErr, true
	}
	statusCode := openAIWSPayloadStatus(payload)
	if statusCode != http.StatusForbidden && statusCode != http.StatusTooManyRequests &&
		statusCode < http.StatusInternalServerError {
		return nil, false
	}
	if statusCode == http.StatusForbidden {
		if hit, _, _ := detectOpenAICyberPolicy(payload); hit {
			if s.openAIRefusalRecoveryRuntime(ctx).CyberFailoverEnabled() {
				markOpenAICyberPolicyFromResponse(c, statusCode, payload)
				return NewOpenAICyberFailoverError(payload, headers), true
			}
			return nil, false
		}
	}

	failoverErr := newOpenAIUpstreamFailoverError(
		statusCode,
		headers,
		openAITransportFailoverBody,
		"websocket_terminal_failure",
		false,
	)
	failoverErr.Stage = GatewayFailureStageInference
	failoverErr.Scope = GatewayFailureScopeRequest
	failoverErr.Reason = openAICindyHTTPToWSV2TerminalReason
	// A terminal WS event is scoped to this request/provider response. It may
	// still switch accounts, but it must not be attributed to the selected
	// account by either the cooldown path or the adaptive scheduler.
	failoverErr.SuppressAccountHealthPenalty = true
	return sanitizeOpenAICindyFailoverError(failoverErr), true
}
