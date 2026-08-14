package service

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

const (
	openAICindyHTTPToWSV2Reason             = "cindy_http_to_wsv2"
	openAICindyHTTPToWSV2RequiredContextKey = "openai_cindy_http_to_wsv2_required"
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

func (s *OpenAIGatewayService) cindyHTTPToWSV2ConfigEligible(account *Account) bool {
	if s == nil || s.cfg == nil || account == nil ||
		!IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) ||
		account.Concurrency <= 0 || account.IsOpenAIWSForceHTTPEnabled() {
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
		return sanitizeOpenAICindyFailoverError(existing), true
	}

	var dialErr *openAIWSDialError
	if errors.As(err, &dialErr) && dialErr != nil && dialErr.StatusCode == http.StatusForbidden {
		if hit, _, _ := detectOpenAICyberPolicy(dialErr.ResponseBody); hit {
			markOpenAICyberPolicyFromResponse(c, dialErr.StatusCode, dialErr.ResponseBody)
			if s.openAIRefusalRecoveryRuntime(ctx).CyberFailoverEnabled() {
				return NewOpenAICyberFailoverError(dialErr.ResponseBody, dialErr.ResponseHeaders), true
			}
			return nil, false
		}
	}

	_, failoverErr := openAIWSInitialDialFailover(err)
	if failoverErr != nil {
		failoverErr.RetryableOnSameAccount = false
		if dialErr != nil && dialErr.StatusCode > 0 {
			_ = s.handleOpenAIAccountUpstreamError(
				ctx, account, dialErr.StatusCode, dialErr.ResponseHeaders, dialErr.ResponseBody, canonicalModel,
			)
			if dialErr.StatusCode == http.StatusForbidden && isHTMLResponse(dialErr.ResponseBody) {
				failoverErr.SuppressAccountHealthPenalty = true
			}
		}
		return sanitizeOpenAICindyFailoverError(failoverErr), true
	}

	reason, retryable := classifyOpenAIWSReconnectReason(err)
	if coderws.CloseStatus(err) == coderws.StatusTryAgainLater || retryable {
		failoverErr := newOpenAIUpstreamFailoverError(
			http.StatusServiceUnavailable, nil, openAITransportFailoverBody, reason, false,
		)
		failoverErr.Reason = OpenAITransientTransportFailureReason
		return failoverErr, true
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

	_ = s.handleOpenAIAccountUpstreamError(ctx, account, statusCode, headers, payload, canonicalModel)
	failoverErr := newOpenAIUpstreamFailoverError(
		statusCode,
		headers,
		openAITransportFailoverBody,
		"websocket_terminal_failure",
		false,
	)
	if statusCode == http.StatusForbidden && isHTMLResponse(payload) {
		failoverErr.SuppressAccountHealthPenalty = true
	}
	return sanitizeOpenAICindyFailoverError(failoverErr), true
}
