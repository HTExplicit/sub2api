package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
)

var (
	ErrAccountTestIncomplete  = errors.New("connection stream ended before a recognized terminal state")
	ErrAccountTestEmpty       = errors.New("connection completed without visible text")
	ErrAccountTestTerminal    = errors.New("upstream reported an unsuccessful terminal state")
	ErrAccountTestProtocol    = errors.New("invalid connection test protocol response")
	ErrAccountTestPlanChanged = errors.New("connection test plan changed; configure a new test")
)

func AccountTestFailureCode(err error) string {
	var failure *accountTestTransportFailure
	if errors.As(err, &failure) {
		return failure.code
	}
	switch {
	case errors.Is(err, ErrAccountTestModelUnsupported):
		return "test_model_unsupported"
	case errors.Is(err, ErrAccountTestPlanChanged):
		return "test_plan_changed"
	case errors.Is(err, ErrAccountTestIncomplete):
		return "test_incomplete"
	case errors.Is(err, ErrAccountTestEmpty):
		return "test_empty_response"
	case errors.Is(err, ErrAccountTestTerminal):
		return "test_terminal_failed"
	case errors.Is(err, ErrAccountTestProtocol):
		return "test_protocol_invalid"
	case errors.Is(err, context.DeadlineExceeded):
		return "test_timeout"
	default:
		return "test_failed"
	}
}

// AccountTestSupportsTextConversation mirrors the native test dispatchers for
// model identities that do not declare their capabilities in the catalog.
func AccountTestSupportsTextConversation(account *Account, model string) bool {
	if account == nil {
		return false
	}
	mapped := account.GetMappedModel(model)
	for _, id := range []string{model, mapped} {
		if isOpenAIImageModel(id) || isGrokImageGenerationModel(id) || isGrokVideoGenerationModel(id) || isImageGenerationModel(id) {
			return false
		}
		lower := strings.ToLower(id)
		for _, prefix := range []string{"dall-e", "sora", "text-embedding-", "embedding-", "whisper-", "tts-", "omni-moderation-"} {
			if strings.HasPrefix(lower, prefix) {
				return false
			}
		}
		for _, suffix := range []string{"-transcribe", "-tts", "-realtime", "-realtime-preview", "-voice-latest"} {
			if strings.HasSuffix(lower, suffix) {
				return false
			}
		}
	}
	return true
}

type accountTestTransportFailure struct {
	code    string
	message string
	cause   error
}

func (e *accountTestTransportFailure) Error() string { return e.message }
func (e *accountTestTransportFailure) Unwrap() error { return e.cause }

func accountTestRequestFailure(err error) error {
	code := "test_network_failed"
	if errors.Is(err, context.DeadlineExceeded) {
		code = "test_timeout"
	}
	message, _ := AccountBusinessMessage(code)
	return &accountTestTransportFailure{code: code, message: message, cause: err}
}

func accountTestHTTPFailure(status int) error {
	code := "test_upstream_failed"
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		code = "test_authentication_failed"
	}
	if status == http.StatusTooManyRequests {
		code = "test_rate_limited"
	}
	message, _ := AccountBusinessMessage(code)
	return &accountTestTransportFailure{code: code, message: fmt.Sprintf("API returned %d: %s", status, message)}
}

func (s *AccountTestService) sendAccountTestFailure(c *gin.Context, err error) error {
	code := AccountTestFailureCode(err)
	message, _ := AccountBusinessMessage(code)
	var transport *accountTestTransportFailure
	if errors.As(err, &transport) {
		message = transport.message
	}
	s.sendEvent(c, TestEvent{Type: "error", Code: code, Error: message})
	return err
}

func (s *AccountTestService) sendAccountTestRequestError(c *gin.Context, err error) error {
	return s.sendAccountTestFailure(c, accountTestRequestFailure(err))
}

func (s *AccountTestService) sendAccountTestHTTPError(c *gin.Context, status int) error {
	return s.sendAccountTestFailure(c, accountTestHTTPFailure(status))
}

type connectionStreamState struct {
	protocol   string
	visible    bool
	media      bool
	allowMedia bool
	limited    bool
	stopReason string
	emit       func(TestEvent)
}

// ResolveAccountTestExecutionModel mirrors the wire mappings of the probe
// adapters so their execution identity can be persisted before model IO.
func ResolveAccountTestExecutionModel(ctx context.Context, account *Account, model string) (string, error) {
	if account == nil {
		return "", ErrAccountTestPlanChanged
	}
	if IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
		snapshot, err := LoadCindyCatalogSnapshot(ctx, account)
		if err != nil {
			return "", err
		}
		mapped := cindyAccountMappedModel(snapshot, account, model)
		if target, ok := snapshot.CompatibilityMappings[mapped]; ok {
			return target, nil
		}
		if target, ok := snapshot.AvailableMappings[mapped]; ok {
			return target, nil
		}
		return mapped, nil
	}
	if account.Platform == PlatformAntigravity && account.Type != AccountTypeAPIKey {
		return (*AntigravityGatewayService)(nil).getMappedModel(account, model), nil
	}
	if account.IsGemini() || (account.Platform == PlatformAntigravity && strings.HasPrefix(model, "gemini-")) {
		if account.Type == AccountTypeAPIKey || account.Type == AccountTypeServiceAccount {
			if mapped, ok := account.GetModelMapping()[model]; ok {
				return mapped, nil
			}
		}
		return model, nil
	}
	if account.IsBedrock() {
		mapped, ok := ResolveBedrockModelID(account, model)
		if !ok {
			return "", ErrAccountTestModelUnsupported
		}
		return mapped, nil
	}
	if account.Type == AccountTypeServiceAccount && !account.IsOpenAI() && !account.IsCNProvider() {
		if mapped, ok := account.ResolveMappedModel(model); ok {
			return mapped, nil
		}
		return normalizeVertexAnthropicModelID(claude.NormalizeModelID(model)), nil
	}
	if account.IsOpenAI() && account.IsOAuth() {
		return normalizeOpenAIModelForUpstream(account, account.GetMappedModel(model)), nil
	}
	if !account.IsOpenAI() && !account.IsCNProvider() && !account.IsOpenCodeGo() && account.Platform != PlatformGrok && account.Type != AccountTypeAPIKey {
		return model, nil
	}
	return account.GetMappedModel(model), nil
}

func (p *connectionStreamState) text(value any) {
	if text, ok := value.(string); ok && text != "" {
		p.visible = p.visible || strings.TrimSpace(text) != ""
		p.emit(TestEvent{Type: "content", Text: text})
	}
}

func (p *connectionStreamState) complete() (bool, error) {
	if !p.visible && (!p.allowMedia || !p.media) {
		return false, ErrAccountTestEmpty
	}
	return true, nil
}

func (p *connectionStreamState) consume(data map[string]any) (bool, error) {
	if data["error"] != nil {
		return false, ErrAccountTestTerminal
	}
	event, _ := data["type"].(string)
	switch p.protocol {
	case "claude":
		switch event {
		case "content_block_start":
			block, _ := data["content_block"].(map[string]any)
			p.text(block["text"])
		case "content_block_delta":
			delta, _ := data["delta"].(map[string]any)
			p.text(delta["text"])
		case "message_delta":
			delta, _ := data["delta"].(map[string]any)
			p.stopReason, _ = delta["stop_reason"].(string)
		case "message_stop":
			switch p.stopReason {
			case "max_tokens", "model_context_window_exceeded":
				p.limited = true
			case "", "end_turn", "stop_sequence", "tool_use", "pause_turn", "refusal":
			default:
				return false, ErrAccountTestTerminal
			}
			return p.complete()
		case "error":
			return false, ErrAccountTestTerminal
		}
	case "gemini":
		if response, ok := data["response"].(map[string]any); ok {
			data = response
		}
		if data["error"] != nil {
			return false, ErrAccountTestTerminal
		}
		candidates, _ := data["candidates"].([]any)
		if len(candidates) == 0 {
			return false, nil
		}
		candidate, _ := candidates[0].(map[string]any)
		content, _ := candidate["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		for _, raw := range parts {
			part, _ := raw.(map[string]any)
			if thought, _ := part["thought"].(bool); thought {
				continue
			}
			p.text(part["text"])
			inline, _ := part["inlineData"].(map[string]any)
			mime, _ := inline["mimeType"].(string)
			encoded, _ := inline["data"].(string)
			if p.allowMedia && strings.HasPrefix(mime, "image/") && encoded != "" {
				p.media = true
				p.emit(TestEvent{Type: "image", ImageURL: "data:" + mime + ";base64," + encoded, MimeType: mime})
			}
		}
		reason, _ := candidate["finishReason"].(string)
		switch reason {
		case "":
			return false, nil
		case "MAX_TOKENS":
			p.limited = true
		case "STOP", "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII":
		default:
			return false, ErrAccountTestTerminal
		}
		return p.complete()
	case "chat":
		if event == "error" || event == "response.failed" {
			return false, ErrAccountTestTerminal
		}
		choices, _ := data["choices"].([]any)
		for _, raw := range choices {
			choice, _ := raw.(map[string]any)
			// The probe sends n=1. Never combine another choice's text and finish.
			if index, ok := choice["index"].(float64); ok && index != 0 {
				continue
			}
			for _, key := range []string{"delta", "message"} {
				message, _ := choice[key].(map[string]any)
				p.text(message["content"])
				p.text(message["refusal"])
			}
			reason, _ := choice["finish_reason"].(string)
			switch reason {
			case "":
				continue
			case "length":
				p.limited = true
			case "stop", "content_filter", "tool_calls", "function_call":
			default:
				return false, ErrAccountTestTerminal
			}
			return p.complete()
		}
	case "responses":
		switch event {
		case "response.output_text.delta", "response.refusal.delta":
			p.text(data["delta"])
		case "response.completed", "response.done", "response.incomplete":
			response, _ := data["response"].(map[string]any)
			status, _ := response["status"].(string)
			if status == "failed" || status == "cancelled" || status == "canceled" {
				return false, ErrAccountTestTerminal
			}
			if response["error"] != nil {
				return false, ErrAccountTestTerminal
			}
			if !p.visible {
				// Providers may send their only text in the final response object.
				output, _ := response["output"].([]any)
				for _, raw := range output {
					item, _ := raw.(map[string]any)
					parts, _ := item["content"].([]any)
					for _, rawPart := range parts {
						part, _ := rawPart.(map[string]any)
						if part["type"] == "output_text" {
							p.text(part["text"])
						}
						if part["type"] == "refusal" {
							p.text(part["refusal"])
						}
					}
				}
			}
			switch {
			case status == "completed" && event != "response.incomplete":
			case event == "response.completed" && status == "":
			case (status == "incomplete" && event != "response.completed") || (event == "response.incomplete" && status == ""):
				details, _ := response["incomplete_details"].(map[string]any)
				if details["reason"] != "max_output_tokens" {
					return false, ErrAccountTestTerminal
				}
				p.limited = true
			default:
				return false, ErrAccountTestTerminal
			}
			return p.complete()
		case "response.failed", "response.cancelled", "error":
			return false, ErrAccountTestTerminal
		}
	default:
		return false, ErrAccountTestProtocol
	}
	return false, nil
}

// One parser is used by streaming HTTP tests, background tests and the
// Antigravity adapter. EOF and [DONE] alone never certify completion.
func parseAccountConnectionStream(protocol string, body io.Reader, allowMedia bool, emit func(TestEvent), observe ...func(map[string]any)) (bool, error) {
	p := connectionStreamState{protocol: protocol, allowMedia: allowMedia, emit: emit}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), 32<<20)
	var lines []string
	consume := func() (bool, error) {
		if len(lines) == 0 {
			return false, nil
		}
		raw := strings.Join(lines, "\n")
		lines = nil
		if raw == "[DONE]" {
			return false, ErrAccountTestIncomplete
		}
		var data map[string]any
		if json.Unmarshal([]byte(raw), &data) != nil {
			return false, ErrAccountTestProtocol
		}
		for _, observer := range observe {
			observer(data)
		}
		return p.consume(data)
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			done, err := consume()
			if done || err != nil {
				return p.limited, err
			}
		} else if strings.HasPrefix(line, "data:") {
			// A few compatible providers omit blank lines between complete JSON
			// events. A complete JSON value is unambiguous even in that stream.
			if len(lines) > 0 && json.Valid([]byte(strings.Join(lines, "\n"))) {
				done, err := consume()
				if done || err != nil {
					return p.limited, err
				}
			}
			lines = append(lines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if scanner.Err() != nil {
		return false, ErrAccountTestIncomplete
	}
	done, err := consume()
	if err != nil {
		return false, err
	}
	if done {
		return p.limited, nil
	}
	return false, ErrAccountTestIncomplete
}

func (s *AccountTestService) processConnectionStream(c *gin.Context, body io.Reader, protocol string, account *Account) error {
	limited, err := parseAccountConnectionStream(protocol, body, c.GetBool("account_test_allow_media"), func(event TestEvent) { s.sendEvent(c, event) }, func(data map[string]any) {
		if account != nil && (data["error"] != nil || data["type"] == "response.failed" || data["type"] == "error") {
			raw, _ := json.Marshal(data)
			s.markCindyBalanceInsufficientFromTest(c.Request.Context(), account, http.StatusOK, raw)
		}
	})
	if err != nil {
		s.sendEvent(c, TestEvent{Type: "error", Code: AccountTestFailureCode(err), Error: err.Error()})
		return err
	}
	if protocol == "chat" {
		s.sendEvent(c, TestEvent{Type: "status", Text: "已通过 /v1/chat/completions 验证"})
	}
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true, OutputLimited: limited})
	return nil
}
