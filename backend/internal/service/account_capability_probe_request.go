package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
)

func (s *AccountCapabilityProbeService) probeOnce(ctx context.Context, account *Account, protocol string, payload map[string]any) (attempt AccountCapabilityProbeAttempt, observation capabilityProbeObservation) {
	started := time.Now()
	attempt = capabilityProbeFailure("failed", "configuration_error")
	defer func() {
		attempt.LatencyMS = time.Since(started).Milliseconds()
		attempt.ObservedAt = time.Now().UTC()
	}()
	if ctx.Err() != nil {
		attempt.setFailure("canceled", "canceled")
		return attempt, observation
	}
	requestCtx, cancel := context.WithTimeout(ctx, accountCapabilityRequestTimeout)
	defer cancel()
	requestCtx = WithHTTPUpstreamRedirectsDisabled(requestCtx)
	req, failure := s.buildProbeRequest(requestCtx, account, protocol, payload)
	if failure != nil {
		return *failure, observation
	}
	// A nil GetBody disables body replay by the shared transport, including its
	// narrowly scoped Grok compatibility fallback. Redirects are disabled above.
	req.GetBody = nil
	attempt.RequestCount = 1
	response, err := s.accountTests.doUpstreamModelsRequest(req, upstreamModelsProxyURL(account), account)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		attempt = accountCapabilityTransportFailure(err, requestCtx, attempt)
		return attempt, observation
	}
	if response == nil || response.Body == nil {
		attempt.setFailure("uncertain", "invalid_response")
		return attempt, observation
	}
	defer func() { _ = response.Body.Close() }()
	attempt.HTTPStatus = response.StatusCode
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, accountCapabilityBodyLimit))
		attempt = accountCapabilityHTTPFailure(attempt, response.StatusCode, body, false)
		return attempt, observation
	}
	attempt.Streaming = strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream")
	observation, err = accountCapabilityReadObservation(response.Body, protocol, attempt.Streaming)
	attempt.Usage = observation.usage
	if err != nil {
		if requestCtx.Err() != nil {
			attempt = accountCapabilityTransportFailure(err, requestCtx, attempt)
		} else {
			attempt.setFailure("uncertain", "invalid_response")
		}
		return attempt, observation
	}
	if observation.failure != "" {
		attempt.setFailure("failed", observation.failure)
		attempt.ErrorCode = observation.errorCode
		return attempt, observation
	}
	if !observation.terminal {
		attempt.setFailure("uncertain", "incomplete_response")
		return attempt, observation
	}
	if !observation.hasText && len(observation.tools) == 0 {
		attempt.setFailure("failed", "no_semantic_output")
		return attempt, observation
	}
	attempt.setFailure("alive", "text_completed")
	return attempt, observation
}

func (s *AccountCapabilityProbeService) buildProbeRequest(ctx context.Context, account *Account, protocol string, payload map[string]any) (*http.Request, *AccountCapabilityProbeAttempt) {
	fail := func(classification string) (*http.Request, *AccountCapabilityProbeAttempt) {
		failure := capabilityProbeFailure("failed", classification)
		return nil, &failure
	}
	var baseURL string
	switch {
	case account.IsCNProvider() && protocol == AccountCapabilityProtocolMessages:
		baseURL = account.GetAnthropicProtocolBaseURL()
		if baseURL == "" {
			return fail("unsupported_protocol")
		}
	case account.IsCNProvider() && account.IsAnthropicProtocol():
		// Do not invent a second protocol's default official endpoint for an
		// account whose configured endpoint is an Anthropic relay.
		return fail("unsupported_protocol")
	case account.IsCNProvider() && account.IsAdaptiveAPIProtocol():
		baseURL = account.GetCNProtocolBaseURL(protocol)
	case account.IsOpenAI() || account.IsCNProvider():
		baseURL = account.GetOpenAIBaseURL()
	case account.IsAnthropic():
		baseURL = account.GetBaseURL()
	case account.IsGrok():
		baseURL = account.GetGrokBaseURL()
	default:
		return fail("unsupported_protocol")
	}
	validatedBase, err := s.accountTests.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return fail("configuration_error")
	}
	var target string
	switch protocol {
	case AccountCapabilityProtocolResponses:
		target = buildOpenAIResponsesURLForPlatform(account.Platform, validatedBase)
	case AccountCapabilityProtocolChatCompletions:
		target = buildOpenAIChatCompletionsURL(validatedBase)
	case AccountCapabilityProtocolMessages:
		switch {
		case account.IsAnthropic():
			// Match the current Anthropic forwarding path exactly, including
			// its beta query and plain base-path append. Testing a normalized
			// substitute would not prove the route users will actually take.
			target = strings.TrimRight(validatedBase, "/") + "/v1/messages?beta=true"
		case account.IsCNProvider():
			target = strings.TrimRight(validatedBase, "/") + "/v1/messages"
		default:
			target = buildOpenAIEndpointURL(validatedBase, "/v1/messages")
		}
	default:
		return fail("unsupported_protocol")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fail("configuration_error")
	}
	if protocol != AccountCapabilityProtocolMessages || account.IsOpenAI() || account.IsCNProvider() {
		ctx = WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileOpenAI)
	}
	// Supplying an opaque ReadCloser, rather than bytes.Reader directly, keeps
	// this request non-replayable even before probeOnce's explicit GetBody=nil.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		return fail("configuration_error")
	}
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	token := strings.TrimSpace(account.GetCredential("api_key"))
	if protocol == AccountCapabilityProtocolMessages {
		for key, value := range claude.DefaultHeaders {
			req.Header.Set(key, value)
		}
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("anthropic-beta", claude.APIKeyBetaHeader)
		setAnthropicAPIKeyAuthHeader(req.Header, account, token, validatedBase)
	} else {
		req.Header.Set("Authorization", "Bearer "+token)
		if protocol == AccountCapabilityProtocolResponses {
			applyOpenAICodexProbeHeaders(req.Header)
		}
	}
	account.ApplyHeaderOverrides(req.Header)
	return req, nil
}
