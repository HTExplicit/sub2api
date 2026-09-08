package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/tidwall/gjson"
)

// probeWebSocket observes one explicitly enabled API-key WebSocket route. It
// borrows the gateway's URL/header/dialer policy, but not its connection pool,
// retry, identity-refresh, accounting or account-error mutation paths.
func (s *AccountCapabilityProbeService) probeWebSocket(ctx context.Context, account *Account, model, profile string) (result AccountCapabilityProbeResult) {
	started := time.Now()
	if profile == "" {
		profile = AccountCapabilityProfileText
	}
	result = AccountCapabilityProbeResult{
		Status: "failed", UpstreamModel: model, Protocol: AccountCapabilityProtocolResponsesWebSocket, Profile: profile,
		Attempts: make([]AccountCapabilityProbeAttempt, 0, 2),
	}
	defer func() {
		result.LatencyMS = time.Since(started).Milliseconds()
		result.ObservedAt = time.Now().UTC()
	}()
	if failure := s.validateAccount(account); failure != nil {
		result.takeAttempt(*failure)
		return result
	}
	if !account.IsOpenAIResponsesWebSocketV2Enabled() || account.IsOpenAIWSForceHTTPEnabled() {
		result.takeAttempt(capabilityProbeFailure("unsupported", "unsupported_protocol"))
		return result
	}
	mode := account.ResolveOpenAIResponsesWebSocketV2Mode(OpenAIWSIngressModeCtxPool)
	if mode == OpenAIWSIngressModeOff || mode == OpenAIWSIngressModeHTTPBridge {
		result.takeAttempt(capabilityProbeFailure("unsupported", "unsupported_protocol"))
		return result
	}
	if profile != AccountCapabilityProfileText && profile != AccountCapabilityProfileToolRoundtrip {
		result.takeAttempt(capabilityProbeFailure("unsupported", "unsupported_profile"))
		return result
	}
	gateway := s.accountTests.openAIGatewayService
	if gateway == nil {
		result.takeAttempt(capabilityProbeFailure("failed", "configuration_error"))
		return result
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		result.takeAttempt(capabilityProbeFailure("canceled", "canceled"))
		return result
	}
	// ApplyHeaderOverrides lazily caches parsing on Account. Keep that cache
	// local too; observing a route must not alter the supplied account snapshot.
	snapshot := *account
	wsURL, err := gateway.buildOpenAIResponsesWSURL(&snapshot)
	if err != nil {
		result.takeAttempt(capabilityProbeFailure("failed", "configuration_error"))
		return result
	}
	firstCtx, cancelFirst := context.WithTimeout(ctx, accountCapabilityRequestTimeout)
	defer cancelFirst()
	headers, _, err := gateway.buildOpenAIWSHeaders(
		firstCtx, nil, &snapshot, strings.TrimSpace(snapshot.GetCredential("api_key")),
		OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2},
		true, "", "", "", model, "",
	)
	if err != nil {
		result.takeAttempt(capabilityProbeFailure("failed", "configuration_error"))
		return result
	}
	dialer := gateway.getOpenAIWSPassthroughDialer()
	if dialer == nil {
		result.takeAttempt(capabilityProbeFailure("failed", "configuration_error"))
		return result
	}
	result.HandshakeCount = 1
	conn, status, _, err := accountCapabilityDialWebSocket(firstCtx, dialer, wsURL, headers, upstreamModelsProxyURL(&snapshot))
	if err != nil {
		if conn != nil {
			_ = conn.Close()
		}
		attempt := capabilityProbeFailure("uncertain", "network_error")
		if status != 0 {
			var handshakeError *openAIWSHandshakeError
			var body []byte
			if errors.As(err, &handshakeError) && handshakeError != nil {
				body = handshakeError.Body
			}
			attempt = accountCapabilityHTTPFailure(attempt, status, body, false)
		} else {
			attempt = accountCapabilityTransportFailure(err, firstCtx, attempt)
		}
		result.takeAttempt(attempt)
		return result
	}
	if conn == nil {
		result.takeAttempt(capabilityProbeFailure("uncertain", "invalid_response"))
		return result
	}
	defer func() { _ = conn.Close() }()

	// The shared WS builder only adds response.create and applies its existing
	// transport fields. No model mapping, effort escalation or HTTP fallback is
	// performed; max_output_tokens, stream, store and unknown fields survive.
	firstPayload := accountCapabilityInitialPayload(model, AccountCapabilityProtocolResponses, profile)
	first, observation := accountCapabilityProbeWebSocketTurn(firstCtx, conn, gateway.buildOpenAIWSCreatePayload(firstPayload, &snapshot))
	cancelFirst()
	if first.Status == "alive" && profile == AccountCapabilityProfileText && !observation.hasText {
		first.setFailure("failed", "no_semantic_output")
	}
	if first.Status == "alive" && profile == AccountCapabilityProfileToolRoundtrip {
		if _, ok := observation.singleExpectedTool(); !ok {
			first.setFailure("failed", "tool_contract_mismatch")
		} else {
			first.setFailure("alive", "tool_call_completed")
		}
	}
	result.takeAttempt(first)
	if first.Status != "alive" || profile == AccountCapabilityProfileText {
		return result
	}
	secondPayload, ok := accountCapabilityToolContinuation(firstPayload, AccountCapabilityProtocolResponses, observation)
	if !ok {
		result.Status, result.Classification = "failed", "tool_history_unavailable"
		result.Reason = accountCapabilityReason(result.Classification)
		return result
	}
	secondCtx, cancelSecond := context.WithTimeout(ctx, accountCapabilityRequestTimeout)
	defer cancelSecond()
	second, finalObservation := accountCapabilityProbeWebSocketTurn(secondCtx, conn, gateway.buildOpenAIWSCreatePayload(secondPayload, &snapshot))
	if second.Status == "alive" && (!finalObservation.hasText || len(finalObservation.tools) != 0) {
		second.setFailure("failed", "tool_continuation_incomplete")
	}
	result.takeAttempt(second)
	if second.Status == "alive" {
		result.Classification = "tool_roundtrip_passed"
		result.Reason = accountCapabilityReason(result.Classification)
	}
	return result
}

// accountCapabilityProbeWebSocketTurn issues exactly one response.create. A
// failed write is still a delivery attempt: no reconnect or replay is safe.
func accountCapabilityProbeWebSocketTurn(ctx context.Context, conn openAIWSClientConn, payload map[string]any) (attempt AccountCapabilityProbeAttempt, observation capabilityProbeObservation) {
	started := time.Now()
	attempt = capabilityProbeFailure("uncertain", "incomplete_response")
	defer func() {
		attempt.LatencyMS = time.Since(started).Milliseconds()
		attempt.ObservedAt = time.Now().UTC()
		attempt.Usage = observation.usage
	}()
	if ctx.Err() != nil {
		attempt.setFailure("canceled", "canceled")
		return attempt, observation
	}
	attempt.RequestCount, attempt.Streaming = 1, true
	if err := conn.WriteJSON(ctx, payload); err != nil {
		attempt = accountCapabilityTransportFailure(err, ctx, attempt)
		return attempt, observation
	}
	observedBytes := 0
	for {
		frame, err := conn.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				attempt = accountCapabilityTransportFailure(err, ctx, attempt)
			} else if errors.Is(err, io.EOF) || coderws.CloseStatus(err) >= 0 {
				attempt.setFailure("uncertain", "incomplete_response")
			} else {
				attempt = accountCapabilityTransportFailure(err, ctx, attempt)
			}
			return attempt, observation
		}
		observedBytes += len(frame)
		if observedBytes > accountCapabilityBodyLimit || !gjson.ValidBytes(frame) || !gjson.ParseBytes(frame).IsObject() {
			attempt.setFailure("uncertain", "invalid_response")
			return attempt, observation
		}
		observation.consumeEvent(AccountCapabilityProtocolResponses, "", frame)
		if observation.terminal || observation.failure != "" {
			observation.finalize(AccountCapabilityProtocolResponses)
			break
		}
	}
	if observation.failure != "" {
		attempt.setFailure("failed", observation.failure)
		attempt.ErrorCode = observation.errorCode
		return attempt, observation
	}
	if !observation.terminal {
		return attempt, observation
	}
	if !observation.hasText && len(observation.tools) == 0 {
		attempt.setFailure("failed", "no_semantic_output")
		return attempt, observation
	}
	attempt.setFailure("alive", "text_completed")
	return attempt, observation
}

// The production dialer uses the standard TLS-verifying client, or its cached
// proxy client. Clone only the client policy for observations: following a
// handshake redirect would test an endpoint other than the frozen account.
// Test/custom dialers keep their existing contract and are called just once.
func accountCapabilityDialWebSocket(ctx context.Context, dialer openAIWSClientDialer, wsURL string, headers http.Header, proxyURL string) (openAIWSClientConn, int, http.Header, error) {
	standard, ok := dialer.(*coderOpenAIWSClientDialer)
	if !ok {
		return dialer.Dial(ctx, wsURL, headers, proxyURL)
	}
	client := *http.DefaultClient
	if proxy := strings.TrimSpace(proxyURL); proxy != "" {
		proxyClient, err := standard.proxyHTTPClient(proxy)
		if err != nil {
			return nil, 0, nil, err
		}
		client = *proxyClient
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	conn, response, err := coderws.Dial(ctx, wsURL, &coderws.DialOptions{
		HTTPClient: &client, HTTPHeader: cloneHeader(headers), CompressionMode: coderws.CompressionContextTakeover,
	})
	status := 0
	var responseHeaders http.Header
	if response != nil {
		status, responseHeaders = response.StatusCode, cloneHeader(response.Header)
	}
	if err != nil {
		var body []byte
		if response != nil && response.Body != nil {
			body, _ = io.ReadAll(io.LimitReader(response.Body, 8<<10))
			_ = response.Body.Close()
		}
		return nil, status, responseHeaders, &openAIWSHandshakeError{Body: body, Err: err}
	}
	conn.SetReadLimit(accountCapabilityBodyLimit)
	return &coderOpenAIWSClientConn{conn: conn}, 0, responseHeaders, nil
}
