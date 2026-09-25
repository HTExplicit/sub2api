package service

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Connection-test failure classes. A failure reported to administrators always
// carries the upstream's own terminal content; errors.Is keeps the class.
var (
	ErrAccountTestIncomplete = errors.New("stream ended before terminal")
	ErrAccountTestEmpty      = errors.New("completed without visible text")
	ErrAccountTestTerminal   = errors.New("upstream reported an unsuccessful terminal state")
	ErrAccountTestProtocol   = errors.New("invalid SSE data")
)

// accountTestTerminalError is a failed protocol terminal whose message is the
// upstream's own error text (or the raw terminal JSON) exactly as received.
type accountTestTerminalError string

func (e accountTestTerminalError) Error() string        { return string(e) }
func (e accountTestTerminalError) Is(target error) bool { return target == ErrAccountTestTerminal }

func accountTestTerminal(format string, args ...any) error {
	return accountTestTerminalError(fmt.Sprintf(format, args...))
}

// accountTestUpstreamError returns the upstream's own error message, or the raw
// JSON it sent when there is no message field.
func accountTestUpstreamError(value any) string {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) != "" {
			return typed
		}
	case map[string]any:
		if message, _ := typed["message"].(string); strings.TrimSpace(message) != "" {
			return message
		}
	}
	raw, _ := json.Marshal(value)
	return string(raw)
}

// sendTestFailure is sendErrorAndEnd for classified failures: the service log
// line and the error event carry the same text and the caller keeps the class.
func (s *AccountTestService) sendTestFailure(c *gin.Context, err error) error {
	_ = s.sendErrorAndEnd(c, err.Error())
	return err
}

type connectionStreamState struct {
	protocol      string
	visible       bool
	media         bool
	allowMedia    bool
	limited       bool
	stopReason    string
	lastEvent     string
	upstreamModel string
	emit          func(TestEvent)
}

func (p *connectionStreamState) text(value any) {
	if text, ok := value.(string); ok && text != "" {
		p.visible = p.visible || strings.TrimSpace(text) != ""
		p.emit(TestEvent{Type: "content", Text: text})
	}
}

// complete accepts a valid terminal only when it produced visible output.
func (p *connectionStreamState) complete(terminal string) (bool, error) {
	if !p.visible && (!p.allowMedia || !p.media) {
		if terminal == "" {
			return false, ErrAccountTestEmpty
		}
		return false, fmt.Errorf("%w (%s)", ErrAccountTestEmpty, terminal)
	}
	return true, nil
}

func (p *connectionStreamState) incomplete() error {
	last := p.lastEvent
	if last == "" {
		last = "none"
	}
	return fmt.Errorf("%w (last event: %s)", ErrAccountTestIncomplete, last)
}

// upstreamError reports an error object or event sent by the upstream with
// the same wording the upstream protocol adapters used before the strict parser.
func (p *connectionStreamState) upstreamError(value any) error {
	text := accountTestUpstreamError(value)
	if p.protocol == "chat" {
		text = "Chat Completions API (/v1/chat/completions) error: " + text
	}
	return accountTestTerminalError(text)
}

func connectionStreamEventName(data map[string]any) string {
	for _, key := range []string{"type", "object"} {
		if name, _ := data[key].(string); name != "" {
			return name
		}
	}
	return "data"
}

// terminalErrorValue prefers the error object of a failed response event.
func terminalErrorValue(data map[string]any) (any, bool) {
	if response, ok := data["response"].(map[string]any); ok && response["error"] != nil {
		return response["error"], true
	}
	if data["error"] != nil {
		return data["error"], true
	}
	return nil, false
}

func (p *connectionStreamState) consume(data map[string]any) (bool, error) {
	p.lastEvent = connectionStreamEventName(data)
	if data["error"] != nil {
		return false, p.upstreamError(data["error"])
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
				return false, accountTestTerminal("message_stop with stop_reason=%s", p.stopReason)
			}
			if p.stopReason == "" {
				return p.complete("")
			}
			return p.complete("stop_reason=" + p.stopReason)
		case "error":
			raw, _ := json.Marshal(data)
			return false, accountTestTerminalError(raw)
		}
	case "gemini":
		if response, ok := data["response"].(map[string]any); ok {
			data = response
		}
		if data["error"] != nil {
			return false, p.upstreamError(data["error"])
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
			return false, accountTestTerminal("finishReason=%s", reason)
		}
		return p.complete("finishReason=" + reason)
	case "chat":
		if event == "error" || event == "response.failed" {
			value, ok := terminalErrorValue(data)
			if !ok {
				value = data
			}
			return false, p.upstreamError(value)
		}
		if model, ok := data["model"].(string); ok && strings.TrimSpace(model) != "" {
			p.upstreamModel = strings.TrimSpace(model)
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
				return false, accountTestTerminal("finish_reason=%s", reason)
			}
			return p.complete("finish_reason=" + reason)
		}
	case "responses":
		if response, ok := data["response"].(map[string]any); ok {
			if model, ok := response["model"].(string); ok && strings.TrimSpace(model) != "" {
				p.upstreamModel = strings.TrimSpace(model)
			}
		}
		switch event {
		case "response.output_text.delta", "response.refusal.delta":
			p.text(data["delta"])
		case "response.completed", "response.done", "response.incomplete":
			response, _ := data["response"].(map[string]any)
			status, _ := response["status"].(string)
			if response["error"] != nil {
				return false, p.upstreamError(response["error"])
			}
			if status == "failed" || status == "cancelled" || status == "canceled" {
				return false, accountTestTerminal("%s with status=%s", event, status)
			}
			var outputTypes []string
			output, _ := response["output"].([]any)
			for _, raw := range output {
				item, _ := raw.(map[string]any)
				if kind, _ := item["type"].(string); kind != "" {
					outputTypes = append(outputTypes, kind)
				}
				if p.visible {
					continue
				}
				// Providers may send their only text in the final response object.
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
			switch {
			case status == "completed" && event != "response.incomplete":
			case event == "response.completed" && status == "":
			case (status == "incomplete" && event != "response.completed") || (event == "response.incomplete" && status == ""):
				details, _ := response["incomplete_details"].(map[string]any)
				if details["reason"] != "max_output_tokens" {
					raw, _ := json.Marshal(response["incomplete_details"])
					return false, accountTestTerminal("%s with incomplete_details=%s", event, raw)
				}
				p.limited = true
			default:
				if status == "" {
					status = "none"
				}
				return false, accountTestTerminal("%s with status=%s", event, status)
			}
			if len(outputTypes) == 0 {
				return p.complete("")
			}
			return p.complete("output: " + strings.Join(outputTypes, ", "))
		case "response.failed", "response.cancelled", "error":
			if value, ok := terminalErrorValue(data); ok {
				return false, p.upstreamError(value)
			}
			response, _ := data["response"].(map[string]any)
			if status, _ := response["status"].(string); status != "" {
				return false, accountTestTerminal("%s with status=%s", event, status)
			}
			return false, accountTestTerminalError(event)
		}
	default:
		return false, fmt.Errorf("%w: unsupported protocol %s", ErrAccountTestProtocol, p.protocol)
	}
	return false, nil
}

// One parser is used by streaming HTTP tests, background tests and the
// Antigravity adapter. EOF and [DONE] alone never certify completion.
func parseAccountConnectionStream(protocol string, body io.Reader, allowMedia bool, emit func(TestEvent), observe ...func(map[string]any)) (limited bool, upstreamModel string, err error) {
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
			p.lastEvent = "[DONE]"
			return false, p.incomplete()
		}
		var data map[string]any
		if json.Unmarshal([]byte(raw), &data) != nil {
			return false, fmt.Errorf("%w: %s", ErrAccountTestProtocol, truncateUTF8(raw, 200))
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
				return p.limited, p.upstreamModel, err
			}
		} else if strings.HasPrefix(line, "data:") {
			// A few compatible providers omit blank lines between complete JSON
			// events. A complete JSON value is unambiguous even in that stream.
			if len(lines) > 0 && json.Valid([]byte(strings.Join(lines, "\n"))) {
				done, err := consume()
				if done || err != nil {
					return p.limited, p.upstreamModel, err
				}
			}
			lines = append(lines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if readErr := scanner.Err(); readErr != nil {
		return false, "", fmt.Errorf("%w: %v", p.incomplete(), readErr)
	}
	done, err := consume()
	if err != nil {
		return false, "", err
	}
	if done {
		return p.limited, p.upstreamModel, nil
	}
	return false, "", p.incomplete()
}

func (s *AccountTestService) processConnectionStream(c *gin.Context, body io.Reader, protocol string, account *Account) error {
	limited, upstreamModel, err := parseAccountConnectionStream(protocol, body, c.GetBool("account_test_allow_media"), func(event TestEvent) { s.sendEvent(c, event) }, func(data map[string]any) {
		if account != nil && (data["error"] != nil || data["type"] == "response.failed" || data["type"] == "error") {
			raw, _ := json.Marshal(data)
			s.markOpenAIBudgetExceededFromTest(c.Request.Context(), account, http.StatusOK, raw)
		}
	})
	if err != nil {
		return s.sendTestFailure(c, err)
	}
	if protocol == "chat" {
		s.sendEvent(c, TestEvent{Type: "status", Text: "已通过 /v1/chat/completions 验证"})
	}
	if upstreamModel != "" {
		s.sendEvent(c, TestEvent{Type: "upstream_model", UpstreamModel: upstreamModel})
	}
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true, OutputLimited: limited})
	return nil
}
