package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

const (
	AccountCapabilityProtocolResponses          = "responses"
	AccountCapabilityProtocolChatCompletions    = "chat_completions"
	AccountCapabilityProtocolMessages           = "messages"
	AccountCapabilityProtocolResponsesWebSocket = "responses_websocket"
	AccountCapabilityProfileText                = "text"
	AccountCapabilityProfileToolRoundtrip       = "tool_roundtrip"

	accountCapabilityRequestTimeout = 60 * time.Second
	accountCapabilityOutputLimit    = 256
	accountCapabilityBodyLimit      = 2 << 20
	accountCapabilityToolName       = "capability_ping"
	accountCapabilityTextPrompt     = "Reply with OK only."
	accountCapabilityToolPrompt     = "Call capability_ping exactly once with value set to ok. After receiving its result, reply with OK only."
)

// AccountCapabilityUsage contains only usage explicitly reported by the
// upstream. Missing values remain absent rather than being inferred as zero.
type AccountCapabilityUsage struct {
	InputTokens  *int64 `json:"input_tokens,omitempty"`
	OutputTokens *int64 `json:"output_tokens,omitempty"`
	TotalTokens  *int64 `json:"total_tokens,omitempty"`
}

// AccountCapabilityProbeAttempt deliberately excludes request/response bodies,
// headers, URLs, credentials, and free-form upstream errors. AccountFailure is a
// positive, structured account-level finding, not a synonym for HTTP 401/403.
type AccountCapabilityProbeAttempt struct {
	Status         string                 `json:"status"`
	Classification string                 `json:"classification"`
	HTTPStatus     int                    `json:"http_status,omitempty"`
	ErrorCode      string                 `json:"error_code,omitempty"`
	Reason         string                 `json:"reason"`
	RequestCount   int                    `json:"request_count"`
	LatencyMS      int64                  `json:"latency_ms"`
	ObservedAt     time.Time              `json:"observed_at"`
	Usage          AccountCapabilityUsage `json:"usage"`
	AccountFailure bool                   `json:"account_failure"`
	Streaming      bool                   `json:"streaming"`
}

// AccountCapabilityProbeResult is durable evidence, not an instruction to
// change account state. Only Status == "alive" means the selected profile
// passed. Attempts preserve separate evidence and observed usage for both
// requests of an explicitly selected tool roundtrip.
type AccountCapabilityProbeResult struct {
	Status         string                          `json:"status"`
	Classification string                          `json:"classification"`
	HTTPStatus     int                             `json:"http_status,omitempty"`
	ErrorCode      string                          `json:"error_code,omitempty"`
	Reason         string                          `json:"reason"`
	RequestCount   int                             `json:"request_count"`
	HandshakeCount int                             `json:"handshake_count,omitempty"`
	LatencyMS      int64                           `json:"latency_ms"`
	ObservedAt     time.Time                       `json:"observed_at"`
	Usage          AccountCapabilityUsage          `json:"usage"`
	AccountFailure bool                            `json:"account_failure"`
	UpstreamModel  string                          `json:"upstream_model"`
	Protocol       string                          `json:"protocol"`
	Profile        string                          `json:"profile"`
	Streaming      bool                            `json:"streaming"`
	Attempts       []AccountCapabilityProbeAttempt `json:"attempts"`
}

// AccountCapabilityProbeService borrows the existing request builders,
// transport, URL policy and TLS settings, but never calls the legacy test/sync
// operations: those operations can persist errors, quotas and model metadata.
type AccountCapabilityProbeService struct {
	accountTests *AccountTestService
}

func NewAccountCapabilityProbeService(accountTests *AccountTestService) *AccountCapabilityProbeService {
	return &AccountCapabilityProbeService{accountTests: accountTests}
}

// Probe sends the exact frozen upstream model ID without account mapping or
// provider-name normalization. Text makes one attempt; tool_roundtrip makes at
// most two, never retries, and never changes protocol, model, account or budget.
func (s *AccountCapabilityProbeService) Probe(ctx context.Context, account *Account, model, protocol, profile string) (result AccountCapabilityProbeResult) {
	started := time.Now()
	if profile == "" {
		profile = AccountCapabilityProfileText
	}
	result = AccountCapabilityProbeResult{
		Status: "failed", UpstreamModel: model, Protocol: protocol, Profile: profile,
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
	if model == "" || strings.TrimSpace(model) != model || len(model) > 512 || strings.ContainsAny(model, "\r\n\x00") {
		result.takeAttempt(capabilityProbeFailure("failed", "invalid_model"))
		return result
	}
	if protocol != AccountCapabilityProtocolResponses && protocol != AccountCapabilityProtocolChatCompletions && protocol != AccountCapabilityProtocolMessages && protocol != AccountCapabilityProtocolResponsesWebSocket && protocol != AccountCapabilityProtocolResponsesInputTokens && protocol != AccountCapabilityProtocolMessagesCountTokens {
		result.takeAttempt(capabilityProbeFailure("unsupported", "unsupported_protocol"))
		return result
	}
	if profile != AccountCapabilityProfileText && profile != AccountCapabilityProfileToolRoundtrip {
		result.takeAttempt(capabilityProbeFailure("unsupported", "unsupported_profile"))
		return result
	}
	if !account.IsOpenAI() && !account.IsCNProvider() && !account.IsAnthropic() && !account.IsGrok() {
		result.takeAttempt(capabilityProbeFailure("unsupported", "unsupported_protocol"))
		return result
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// Header overrides lazily cache their parsed value on Account. A value copy
	// prevents even that incidental mutation of the caller's account snapshot.
	snapshot := *account
	if protocol == AccountCapabilityProtocolResponsesWebSocket {
		return s.probeWebSocket(ctx, &snapshot, model, profile)
	}
	if protocol == AccountCapabilityProtocolResponsesInputTokens || protocol == AccountCapabilityProtocolMessagesCountTokens {
		return s.probeMetadata(ctx, &snapshot, model, protocol, profile)
	}
	firstPayload := accountCapabilityInitialPayload(model, protocol, profile)
	first, observation := s.probeOnce(ctx, &snapshot, protocol, firstPayload)
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
	secondPayload, ok := accountCapabilityToolContinuation(firstPayload, protocol, observation)
	if !ok {
		result.Status = "failed"
		result.Classification = "tool_history_unavailable"
		result.Reason = accountCapabilityReason(result.Classification)
		return result
	}
	second, finalObservation := s.probeOnce(ctx, &snapshot, protocol, secondPayload)
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

func (s *AccountCapabilityProbeService) validateAccount(account *Account) *AccountCapabilityProbeAttempt {
	var failure AccountCapabilityProbeAttempt
	switch {
	case s == nil || s.accountTests == nil || s.accountTests.httpUpstream == nil || account == nil:
		failure = capabilityProbeFailure("failed", "configuration_error")
	case account.Type != AccountTypeAPIKey || account.IsCredentialShadow() || IsCindyRuntimeCompatibleAPIKeyAccount(account.Platform, account.Type, account.Credentials):
		// Refreshing OAuth or special provider identities can persist state. The
		// first managed-public release intentionally observes only API keys.
		failure = capabilityProbeFailure("unsupported", "unsupported_account")
	case strings.TrimSpace(account.GetCredential("api_key")) == "":
		failure = capabilityProbeFailure("failed", "missing_credentials")
	default:
		return nil
	}
	return &failure
}

func (r *AccountCapabilityProbeResult) takeAttempt(attempt AccountCapabilityProbeAttempt) {
	r.Status, r.Classification, r.Reason = attempt.Status, attempt.Classification, attempt.Reason
	r.HTTPStatus, r.ErrorCode = attempt.HTTPStatus, attempt.ErrorCode
	r.AccountFailure = r.AccountFailure || attempt.AccountFailure
	if r.RequestCount == 0 {
		r.Streaming = attempt.Streaming
	} else {
		r.Streaming = r.Streaming && attempt.Streaming
	}
	r.RequestCount += attempt.RequestCount
	r.Usage = accountCapabilityAddUsage(r.Usage, attempt.Usage)
	if attempt.RequestCount != 0 {
		r.Attempts = append(r.Attempts, attempt)
	}
}

func accountCapabilityAddUsage(a, b AccountCapabilityUsage) AccountCapabilityUsage {
	add := func(x, y *int64) *int64 {
		if x == nil && y == nil {
			return nil
		}
		var n int64
		if x != nil {
			n += *x
		}
		if y != nil {
			n += *y
		}
		return &n
	}
	return AccountCapabilityUsage{InputTokens: add(a.InputTokens, b.InputTokens), OutputTokens: add(a.OutputTokens, b.OutputTokens), TotalTokens: add(a.TotalTokens, b.TotalTokens)}
}

func accountCapabilityInitialPayload(model, protocol, profile string) map[string]any {
	prompt := accountCapabilityTextPrompt
	toolRoundtrip := profile == AccountCapabilityProfileToolRoundtrip
	if toolRoundtrip {
		prompt = accountCapabilityToolPrompt
	}
	payload := map[string]any{"model": model, "stream": true}
	schema := map[string]any{
		"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string", "enum": []string{"ok"}}},
		"required": []string{"value"}, "additionalProperties": false,
	}
	switch protocol {
	case AccountCapabilityProtocolResponses:
		payload["input"] = []any{map[string]any{"role": "user", "content": prompt}}
		payload["max_output_tokens"] = accountCapabilityOutputLimit
		payload["store"] = false
		if toolRoundtrip {
			payload["tools"] = []any{map[string]any{"type": "function", "name": accountCapabilityToolName, "description": "Return the supplied fixed value.", "parameters": schema, "strict": true}}
			payload["tool_choice"] = map[string]any{"type": "function", "name": accountCapabilityToolName}
			payload["parallel_tool_calls"] = false
		}
	case AccountCapabilityProtocolChatCompletions:
		payload["messages"] = []any{map[string]any{"role": "user", "content": prompt}}
		// GPT reasoning models reject the legacy max_tokens spelling. Other
		// OpenAI-compatible providers retain the broadly supported spelling.
		leaf := model[strings.LastIndex(model, "/")+1:]
		if strings.HasPrefix(strings.ToLower(leaf), "gpt-") || strings.HasPrefix(leaf, "o1") || strings.HasPrefix(leaf, "o3") || strings.HasPrefix(leaf, "o4") {
			payload["max_completion_tokens"] = accountCapabilityOutputLimit
		} else {
			payload["max_tokens"] = accountCapabilityOutputLimit
		}
		if toolRoundtrip {
			payload["tools"] = []any{map[string]any{"type": "function", "function": map[string]any{"name": accountCapabilityToolName, "description": "Return the supplied fixed value.", "parameters": schema}}}
			payload["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": accountCapabilityToolName}}
		}
	case AccountCapabilityProtocolMessages:
		payload["messages"] = []any{map[string]any{"role": "user", "content": prompt}}
		payload["max_tokens"] = accountCapabilityOutputLimit
		if toolRoundtrip {
			payload["tools"] = []any{map[string]any{"name": accountCapabilityToolName, "description": "Return the supplied fixed value.", "input_schema": schema}}
			payload["tool_choice"] = map[string]any{"type": "tool", "name": accountCapabilityToolName}
		}
	}
	return payload
}

func accountCapabilityToolContinuation(first map[string]any, protocol string, observation capabilityProbeObservation) (map[string]any, bool) {
	tool, ok := observation.singleExpectedTool()
	if !ok {
		return nil, false
	}
	payload := make(map[string]any, len(first))
	for key, value := range first {
		payload[key] = value
	}
	switch protocol {
	case AccountCapabilityProtocolResponses:
		if len(observation.replayItems) == 0 {
			return nil, false
		}
		initialInput, valid := first["input"].([]any)
		if !valid {
			return nil, false
		}
		input := append([]any(nil), initialInput...)
		for _, raw := range observation.replayItems {
			input = append(input, json.RawMessage(raw))
		}
		payload["input"] = append(input, map[string]any{"type": "function_call_output", "call_id": tool.ID, "output": `{"value":"ok"}`})
		payload["tool_choice"] = "none"
	case AccountCapabilityProtocolChatCompletions:
		if observation.chatMessage == nil {
			return nil, false
		}
		initialMessages, valid := first["messages"].([]any)
		if !valid {
			return nil, false
		}
		history := append([]any(nil), initialMessages...)
		payload["messages"] = append(history, observation.chatMessage, map[string]any{"role": "tool", "tool_call_id": tool.ID, "content": `{"value":"ok"}`})
		payload["tool_choice"] = "none"
	case AccountCapabilityProtocolMessages:
		if len(observation.replayItems) == 0 {
			return nil, false
		}
		initialMessages, valid := first["messages"].([]any)
		if !valid {
			return nil, false
		}
		history := append([]any(nil), initialMessages...)
		payload["messages"] = append(history, map[string]any{"role": "assistant", "content": observation.replayItems}, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": tool.ID, "content": `{"value":"ok"}`}}})
		// "auto" is supported by older Anthropic-compatible gateways. A second
		// tool request still fails validation; it is never executed or retried.
		payload["tool_choice"] = map[string]any{"type": "auto"}
	default:
		return nil, false
	}
	return payload, true
}
