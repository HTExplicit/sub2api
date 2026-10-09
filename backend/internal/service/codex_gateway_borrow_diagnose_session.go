package service

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const codexBorrowEchoTool = "codex_borrow_echo"
const codexBorrowEchoValue = "borrow-continuation-check"

// A fixed, side-effect-free tool round trip exercises native client metadata,
// STATE echo and full-history HTTP continuation without reading a user's chat.
// Each diagnostic owns its history; nothing enters business continuation stores.
func (d *codexBorrowDiagnostic) prepareHTTPSessionTurn(headers http.Header, payload map[string]any) {
	headers.Set("User-Agent", CodexCanonicalUserAgent())
	headers.Set("originator", codexTUIOriginator)
	headers.Set("session-id", d.session)
	headers.Set("thread-id", d.session)
	headers.Set("x-client-request-id", uuid.NewString())
	headers.Set("x-codex-window-id", d.session+":0")
	headers.Set("x-codex-installation-id", d.session)
	metadata, _ := json.Marshal(map[string]string{"installation_id": d.session, "session_id": d.session, "thread_id": d.session, "turn_id": uuid.NewString(), "window_id": d.session + ":0"})
	headers.Set("x-codex-turn-metadata", string(metadata))
	if d.turnState != "" {
		headers.Set("x-codex-turn-state", d.turnState)
	}
	payload["client_metadata"] = map[string]string{"session_id": d.session, "thread_id": d.session, "x-codex-installation-id": d.session, "x-codex-turn-metadata": string(metadata)}
	payload["prompt_cache_key"] = d.session
	payload["include"] = []string{"reasoning.encrypted_content"}
	payload["parallel_tool_calls"] = false
	payload["tools"] = []any{map[string]any{"type": "function", "name": codexBorrowEchoTool, "description": "Return value unchanged.", "strict": true, "parameters": map[string]any{"type": "object", "properties": map[string]any{"value": map[string]string{"type": "string"}}, "required": []string{"value"}, "additionalProperties": false}}}
	delete(payload, "previous_response_id") // HTTP sends complete history, as the native client does.
	if len(d.history) == 0 {
		d.history = []any{map[string]any{"role": "user", "content": []any{map[string]string{"type": "input_text", "text": "Call codex_borrow_echo with value borrow-continuation-check. After its result, reply with that value only."}}}}
		payload["tool_choice"] = map[string]string{"type": "function", "name": codexBorrowEchoTool}
	} else {
		payload["tool_choice"] = "none"
	}
	payload["input"] = d.history
}

func codexBorrowSessionTerminal(raw string) (gjson.Result, error) {
	for _, line := range strings.Split(raw, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		event := gjson.Parse(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		switch event.Get("type").String() {
		case "response.completed", "response.done":
			response := event.Get("response")
			if response.Get("status").String() == "completed" && !response.Get("error").IsObject() {
				return response, nil
			}
			return response, errors.New("upstream terminal was not completed")
		case "error", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
			return gjson.Result{}, errors.New(event.Raw)
		}
	}
	return gjson.Result{}, errors.New("upstream SSE ended without a completed response")
}

func (d *codexBorrowDiagnostic) completeHTTPSessionTurn(result *CodexBorrowDiagnosticResult, sendErr error, status int) {
	response, err := codexBorrowSessionTerminal(result.RawResponse)
	if sendErr != nil {
		result.Error = sendErr.Error()
		return
	}
	if err != nil {
		result.Error = err.Error()
		return
	}
	if status < 200 || status >= 300 {
		result.Error = "upstream HTTP request did not succeed"
		return
	}
	result.ResponseID, result.ReportedModel = response.Get("id").String(), response.Get("model").String()
	if len(d.history) == 1 {
		var callID, arguments string
		for _, item := range response.Get("output").Array() {
			d.history = append(d.history, json.RawMessage(item.Raw))
			if item.Get("type").String() == "function_call" && item.Get("name").String() == codexBorrowEchoTool {
				callID, arguments = item.Get("call_id").String(), item.Get("arguments").String()
			}
		}
		if callID == "" || gjson.Get(arguments, "value").String() != codexBorrowEchoValue {
			result.Error = "upstream did not return the requested fixed echo tool call"
			return
		}
		d.history = append(d.history, map[string]string{"type": "function_call_output", "call_id": callID, "output": codexBorrowEchoValue})
		result.Completed, result.Answer = true, "codex_borrow_echo(\""+codexBorrowEchoValue+"\")"
		return
	}
	var answer strings.Builder
	for _, item := range response.Get("output").Array() {
		for _, content := range item.Get("content").Array() {
			if content.Get("type").String() == "output_text" {
				_, _ = answer.WriteString(content.Get("text").String())
			}
		}
	}
	result.Answer = strings.TrimSpace(answer.String())
	result.Completed = result.Answer == codexBorrowEchoValue
	result.ToolRoundTrip = result.Completed
	if !result.Completed {
		result.Error = "continuation did not return the fixed tool result"
	}
}
