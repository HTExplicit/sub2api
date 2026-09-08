package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	AccountCapabilityProtocolResponsesInputTokens = "responses_input_tokens"
	AccountCapabilityProtocolMessagesCountTokens  = "messages_count_tokens"
)

// probeMetadata observes a single, explicitly selected token-count endpoint.
// A valid result is available, never alive: this proves metadata support but
// cannot establish generation, streaming, tools, or a publishable model route.
func (s *AccountCapabilityProbeService) probeMetadata(ctx context.Context, account *Account, model, protocol, profile string) (result AccountCapabilityProbeResult) {
	started := time.Now()
	if profile == "" {
		profile = AccountCapabilityProfileText
	}
	result = AccountCapabilityProbeResult{
		Status: "failed", UpstreamModel: model, Protocol: protocol, Profile: profile,
		Attempts: make([]AccountCapabilityProbeAttempt, 0, 1),
	}
	attempt := capabilityProbeFailure("failed", "configuration_error")
	defer func() {
		attempt.LatencyMS = time.Since(started).Milliseconds()
		attempt.ObservedAt = time.Now().UTC()
		result.takeAttempt(attempt)
		result.LatencyMS, result.ObservedAt = attempt.LatencyMS, attempt.ObservedAt
	}()
	if failure := s.validateAccount(account); failure != nil {
		attempt = *failure
		return result
	}
	if profile != AccountCapabilityProfileText {
		attempt.setFailure("unsupported", "unsupported_profile")
		return result
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		attempt.setFailure("canceled", "canceled")
		return result
	}
	requestCtx, cancel := context.WithTimeout(ctx, accountCapabilityRequestTimeout)
	defer cancel()
	requestCtx = WithHTTPUpstreamRedirectsDisabled(requestCtx)
	// Header parsing may cache on Account. Keep that incidental cache local and
	// never invoke the legacy test/forward paths that persist account failures.
	snapshot := *account
	req, failure := s.buildMetadataProbeRequest(requestCtx, &snapshot, model, protocol)
	if failure != nil {
		attempt = *failure
		return result
	}
	req.GetBody = nil
	attempt.RequestCount = 1
	response, err := s.accountTests.doUpstreamModelsRequest(req, upstreamModelsProxyURL(&snapshot), &snapshot)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		attempt = accountCapabilityTransportFailure(err, requestCtx, attempt)
		return result
	}
	if response == nil || response.Body == nil {
		attempt.setFailure("uncertain", "invalid_response")
		return result
	}
	defer func() { _ = response.Body.Close() }()
	attempt.HTTPStatus = response.StatusCode
	body, err := io.ReadAll(io.LimitReader(response.Body, accountCapabilityBodyLimit+1))
	if err != nil {
		if requestCtx.Err() != nil {
			attempt = accountCapabilityTransportFailure(err, requestCtx, attempt)
		} else {
			attempt.setFailure("uncertain", "invalid_response")
		}
		return result
	}
	if len(body) > accountCapabilityBodyLimit {
		attempt.setFailure("uncertain", "invalid_response")
		return result
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		attempt = accountCapabilityHTTPFailure(attempt, response.StatusCode, body, false)
		return result
	}
	inputTokens, valid, errorBody := accountCapabilityMetadataInputTokens(body)
	if errorBody {
		attempt.setFailure("failed", "request_rejected")
		attempt.ErrorCode = accountCapabilitySafeErrorCode(body)
		return result
	}
	if !valid {
		attempt.setFailure("uncertain", "invalid_response")
		return result
	}
	attempt.Usage.InputTokens = &inputTokens
	attempt.setFailure("available", "metadata_available")
	return result
}

// buildMetadataProbeRequest reuses the current inference request's account
// authentication, headers and transport profile, but supplies only the small
// count request body and the real count endpoint. It never sends an inference
// request first, resolves a model alias, or changes an account's configured URL.
func (s *AccountCapabilityProbeService) buildMetadataProbeRequest(ctx context.Context, account *Account, model, protocol string) (*http.Request, *AccountCapabilityProbeAttempt) {
	fail := func(classification string) (*http.Request, *AccountCapabilityProbeAttempt) {
		status := "failed"
		if classification == "unsupported_protocol" {
			status = "unsupported"
		}
		attempt := capabilityProbeFailure(status, classification)
		return nil, &attempt
	}
	payload := map[string]any{"model": model}
	baseProtocol := ""
	switch protocol {
	case AccountCapabilityProtocolResponsesInputTokens:
		// The real OpenAI count forwarder obtains its base from this getter,
		// not the platform-specific Responses inference endpoint. In particular,
		// do not silently borrow a different provider's default OpenAI URL.
		if strings.TrimSpace(account.GetOpenAIBaseURL()) == "" {
			return fail("unsupported_protocol")
		}
		baseProtocol = AccountCapabilityProtocolResponses
		payload["input"] = []any{map[string]any{"role": "user", "content": accountCapabilityTextPrompt}}
	case AccountCapabilityProtocolMessagesCountTokens:
		baseProtocol = AccountCapabilityProtocolMessages
		payload["messages"] = []any{map[string]any{"role": "user", "content": accountCapabilityTextPrompt}}
	default:
		return fail("unsupported_protocol")
	}
	req, failure := s.buildProbeRequest(ctx, account, baseProtocol, payload)
	if failure != nil {
		return nil, failure
	}
	if protocol == AccountCapabilityProtocolResponsesInputTokens {
		baseURL, err := s.accountTests.validateUpstreamBaseURL(account.GetOpenAIBaseURL())
		if err != nil {
			return fail("configuration_error")
		}
		target, err := url.Parse(buildOpenAIResponsesInputTokensURL(baseURL))
		if err != nil {
			return fail("configuration_error")
		}
		req.URL = target
	} else if account.IsAnthropic() {
		// Match both existing Anthropic API-key count forwarders, including the
		// beta query and their current base-path semantics. A successful probe
		// on a differently normalized path would not prove that runtime route.
		baseURL, err := s.accountTests.validateUpstreamBaseURL(account.GetBaseURL())
		if err != nil {
			return fail("configuration_error")
		}
		target, err := url.Parse(baseURL + "/v1/messages/count_tokens?beta=true")
		if err != nil {
			return fail("configuration_error")
		}
		req.URL = target
	} else {
		req.URL.Path = strings.TrimRight(req.URL.Path, "/") + "/count_tokens"
		req.URL.RawPath = ""
	}
	// NewRequest copies its initial URL host into Host. Adaptive accounts can
	// use distinct inference/count bases, so update both together.
	req.Host = req.URL.Host
	req.Header.Set("Accept", "application/json")
	account.ApplyHeaderOverrides(req.Header)
	return req, nil
}

// accountCapabilityMetadataInputTokens accepts only a complete JSON object
// containing a non-negative int64. Zero is a real observation; null, a string,
// fractional/exponential notation, overflow, and error envelopes are not.
func accountCapabilityMetadataInputTokens(body []byte) (tokens int64, valid, errorBody bool) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil || envelope == nil {
		return 0, false, false
	}
	if accountCapabilityMetadataErrorEnvelope(envelope) || accountCapabilitySafeErrorCode(body) != "" {
		return 0, false, true
	}
	var response map[string]json.RawMessage
	if json.Unmarshal(envelope["response"], &response) == nil && accountCapabilityMetadataErrorEnvelope(response) {
		return 0, false, true
	}
	var inputTokens *int64
	if err := json.Unmarshal(envelope["input_tokens"], &inputTokens); err != nil || inputTokens == nil || *inputTokens < 0 {
		return 0, false, false
	}
	return *inputTokens, true, false
}

func accountCapabilityMetadataErrorEnvelope(envelope map[string]json.RawMessage) bool {
	for _, key := range []string{"error", "errors"} {
		if raw, ok := envelope[key]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return true
		}
	}
	for _, key := range []string{"type", "status"} {
		var value string
		if json.Unmarshal(envelope[key], &value) == nil {
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "error", "failed", "failure", "canceled", "cancelled", "incomplete":
				return true
			}
		}
	}
	for _, key := range []string{"success", "ok"} {
		if raw, ok := envelope[key]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("false")) {
			return true
		}
	}
	return false
}
