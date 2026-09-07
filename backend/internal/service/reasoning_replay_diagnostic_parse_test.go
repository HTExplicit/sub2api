//go:build reasoning_fidelity && reasoning_replay_diagnostic

package service_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The complete client-visible assistant projection remains in memory only.
// Text is populated only for a completed final answer, never from reasoning.
type replayDiagnosticChat struct {
	Status  string
	Text    string
	Model   string
	ID      string
	Message json.RawMessage
	HasTool bool
}

func replayDiagnosticChatBody(model, effort string, stream bool) []byte {
	body := map[string]any{
		"model": model, "reasoning_effort": effort, "stream": stream,
		"n": 1, "store": false, "parallel_tool_calls": false,
		"messages": []any{map[string]any{"role": "user", "content": fidelityOrdersPrompt}},
		"tools": []any{map[string]any{
			"type": "function", "function": map[string]any{
				"name": "load_orders", "description": "Read the fixed order catalog.", "strict": true,
				"parameters": map[string]any{"type": "object", "properties": map[string]any{},
					"required": []string{}, "additionalProperties": false},
			},
		}},
		"tool_choice": map[string]any{"type": "function", "function": map[string]any{"name": "load_orders"}},
	}
	if stream {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	raw, _ := json.Marshal(body)
	return raw
}

// Preserve the original request and full observed assistant message. No
// constraints are repeated, no call ID is fabricated, and the only locally
// executable tool is the one empty-object load_orders call specified above.
func replayDiagnosticChatContinuation(firstBody []byte, parsed replayDiagnosticChat) ([]byte, bool) {
	if parsed.Status != "completed" || !parsed.HasTool {
		return nil, false
	}
	_, calls, ok := replayDiagnosticInspectChatMessage(parsed.Message)
	if !ok || len(calls) != 1 {
		return nil, false
	}
	call, _ := fidelityJSONObject(calls[0])
	function, _ := fidelityJSONObject(call["function"])
	var id, name, arguments string
	if json.Unmarshal(call["id"], &id) != nil || json.Unmarshal(function["name"], &name) != nil ||
		json.Unmarshal(function["arguments"], &arguments) != nil || name != "load_orders" {
		return nil, false
	}
	args, err := fidelityJSONObject([]byte(arguments))
	if err != nil || len(args) != 0 {
		return nil, false
	}
	body, err := fidelityJSONObject(firstBody)
	if err != nil {
		return nil, false
	}
	var messages []json.RawMessage
	if json.Unmarshal(body["messages"], &messages) != nil || len(messages) == 0 {
		return nil, false
	}
	for _, message := range messages {
		if _, err := fidelityJSONObject(message); err != nil {
			return nil, false
		}
	}
	toolResult, _ := json.Marshal(map[string]any{"role": "tool", "tool_call_id": id, "content": fidelityOrdersResult})
	messages = append(messages, bytes.Clone(parsed.Message), toolResult)
	body["messages"], err = json.Marshal(messages)
	if err != nil {
		return nil, false
	}
	body["tool_choice"] = json.RawMessage(`"none"`)
	raw, err := json.Marshal(body)
	return raw, err == nil
}

func replayDiagnosticParseChat(body []byte, contentType string, status int) replayDiagnosticChat {
	result := replayDiagnosticChat{Status: "failed"}
	if len(body) > fidelityMaxResponseBytes || status < 200 || status >= 300 {
		return result
	}
	trimmed := bytes.TrimSpace(body)
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") ||
		bytes.HasPrefix(trimmed, []byte("data:")) || bytes.HasPrefix(trimmed, []byte("event:")) ||
		bytes.HasPrefix(trimmed, []byte(":")) {
		return replayDiagnosticParseChatSSE(body)
	}
	object, err := fidelityJSONObject(trimmed)
	if err != nil || !replayDiagnosticChatEnvelope(object, "chat.completion", &result) {
		return result
	}
	var choices []json.RawMessage
	if json.Unmarshal(object["choices"], &choices) != nil || len(choices) != 1 {
		return result
	}
	choice, err := fidelityJSONObject(choices[0])
	var index int
	if err != nil || fidelityNonNull(choice["error"]) || fidelityNonNull(choice["incomplete_details"]) ||
		!fidelityNonNull(choice["index"]) || json.Unmarshal(choice["index"], &index) != nil || index != 0 {
		return result
	}
	finish, ok := replayDiagnosticChatFinish(choice["finish_reason"])
	if !ok {
		result.Status = finish
		return result
	}
	return replayDiagnosticFinishChat(result, choice["message"], finish)
}

// Usage trailers are validated without inventing zero values for absent
// counters. The harness records the authoritative upstream usage separately.
func replayDiagnosticChatEnvelope(object map[string]json.RawMessage, kind string, result *replayDiagnosticChat) bool {
	if fidelityNonNull(object["error"]) || fidelityNonNull(object["incomplete_details"]) {
		return false
	}
	var objectKind, responseStatus, eventType string
	if json.Unmarshal(object["object"], &objectKind) != nil || objectKind != kind {
		return false
	}
	if fidelityNonNull(object["status"]) &&
		(json.Unmarshal(object["status"], &responseStatus) != nil || responseStatus != "completed") {
		return false
	}
	if fidelityNonNull(object["type"]) &&
		(json.Unmarshal(object["type"], &eventType) != nil || eventType == "error" ||
			strings.HasSuffix(eventType, "_error") || strings.HasPrefix(eventType, "response.")) {
		return false
	}
	for key, value := range map[string]*string{"id": &result.ID, "model": &result.Model} {
		var next string
		if json.Unmarshal(object[key], &next) != nil || strings.TrimSpace(next) == "" || (*value != "" && *value != next) {
			return false
		}
		*value = next
	}
	if !fidelityNonNull(object["usage"]) {
		return true
	}
	usage, err := fidelityJSONObject(object["usage"])
	if err != nil {
		return false
	}
	for _, key := range []string{"prompt_tokens", "completion_tokens", "total_tokens"} {
		var count *int64
		if fidelityReadTokenCount(usage[key], &count) != nil {
			return false
		}
	}
	for _, key := range []string{"prompt_tokens_details", "completion_tokens_details"} {
		if !fidelityNonNull(usage[key]) {
			continue
		}
		details, err := fidelityJSONObject(usage[key])
		if err != nil {
			return false
		}
		for _, field := range []string{"cached_tokens", "reasoning_tokens", "audio_tokens", "accepted_prediction_tokens", "rejected_prediction_tokens"} {
			var count *int64
			if fidelityReadTokenCount(details[field], &count) != nil {
				return false
			}
		}
	}
	return true
}

func replayDiagnosticChatFinish(raw json.RawMessage) (string, bool) {
	if !fidelityNonNull(raw) {
		return "incomplete", false
	}
	var finish string
	if json.Unmarshal(raw, &finish) != nil {
		return "failed", false
	}
	switch finish {
	case "stop", "tool_calls":
		return finish, true
	case "", "length":
		return "incomplete", false
	default:
		return "failed", false
	}
}

func replayDiagnosticFinishChat(result replayDiagnosticChat, message json.RawMessage, finish string) replayDiagnosticChat {
	text, calls, valid := replayDiagnosticInspectChatMessage(message)
	if !valid || (finish == "tool_calls") != (len(calls) > 0) {
		result.Status = "failed"
		return result
	}
	result.Status, result.HasTool = "completed", len(calls) > 0
	result.Message = bytes.Clone(message)
	if !result.HasTool {
		result.Text = text
	}
	return result
}

func replayDiagnosticInspectChatMessage(raw json.RawMessage) (string, []json.RawMessage, bool) {
	message, err := fidelityJSONObject(raw)
	var role, refusal string
	if err != nil || json.Unmarshal(message["role"], &role) != nil || role != "assistant" ||
		fidelityNonNull(message["function_call"]) || fidelityNonNull(message["error"]) {
		return "", nil, false
	}
	if fidelityNonNull(message["refusal"]) && (json.Unmarshal(message["refusal"], &refusal) != nil || refusal != "") {
		return "", nil, false
	}
	for _, key := range []string{"reasoning_content", "reasoning"} {
		var reasoning string
		if fidelityNonNull(message[key]) && json.Unmarshal(message[key], &reasoning) != nil {
			return "", nil, false
		}
	}
	text, ok := replayDiagnosticChatText(message["content"])
	if !ok {
		return "", nil, false
	}
	var calls []json.RawMessage
	if fidelityNonNull(message["tool_calls"]) &&
		(json.Unmarshal(message["tool_calls"], &calls) != nil || len(calls) > 32) {
		return "", nil, false
	}
	ids := make(map[string]bool, len(calls))
	for index, rawCall := range calls {
		call, err := fidelityJSONObject(rawCall)
		var id, kind, name, arguments string
		if err != nil || json.Unmarshal(call["id"], &id) != nil || strings.TrimSpace(id) == "" || ids[id] ||
			json.Unmarshal(call["type"], &kind) != nil || kind != "function" {
			return "", nil, false
		}
		if value, exists := call["index"]; exists {
			var actual int
			if !fidelityNonNull(value) || json.Unmarshal(value, &actual) != nil || actual != index {
				return "", nil, false
			}
		}
		function, err := fidelityJSONObject(call["function"])
		if err != nil || json.Unmarshal(function["name"], &name) != nil || strings.TrimSpace(name) == "" ||
			!fidelityNonNull(function["arguments"]) || json.Unmarshal(function["arguments"], &arguments) != nil {
			return "", nil, false
		}
		if _, err := fidelityJSONObject([]byte(arguments)); err != nil {
			return "", nil, false
		}
		ids[id] = true
	}
	return text, calls, true
}

func replayDiagnosticChatText(raw json.RawMessage) (string, bool) {
	if !fidelityNonNull(raw) {
		return "", true
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, true
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return "", false
	}
	var output strings.Builder
	for _, rawPart := range parts {
		part, err := fidelityJSONObject(rawPart)
		var kind, value string
		if err != nil || json.Unmarshal(part["type"], &kind) != nil || kind != "text" ||
			!fidelityNonNull(part["text"]) || json.Unmarshal(part["text"], &value) != nil {
			return "", false
		}
		output.WriteString(value)
	}
	return output.String(), true
}

type replayDiagnosticChatTool struct {
	id, kind, name string
	arguments      strings.Builder
}

type replayDiagnosticChatStream struct {
	message map[string]json.RawMessage
	tools   map[int]*replayDiagnosticChatTool
}

func (s *replayDiagnosticChatStream) applyDelta(raw json.RawMessage) bool {
	delta, err := fidelityJSONObject(raw)
	if err != nil {
		return false
	}
	for key, value := range delta {
		switch key {
		case "role":
			var role string
			if json.Unmarshal(value, &role) != nil || role != "assistant" {
				return false
			}
			s.message[key] = bytes.Clone(value)
		case "content", "reasoning_content", "reasoning", "refusal":
			if !fidelityNonNull(value) {
				if _, exists := s.message[key]; !exists {
					s.message[key] = bytes.Clone(value)
				}
				continue
			}
			var fragment, previous string
			if json.Unmarshal(value, &fragment) != nil || (key == "refusal" && fragment != "") {
				return false
			}
			if fidelityNonNull(s.message[key]) && json.Unmarshal(s.message[key], &previous) != nil {
				return false
			}
			s.message[key], _ = json.Marshal(previous + fragment)
		case "tool_calls":
			if !fidelityNonNull(value) {
				continue
			}
			var calls []json.RawMessage
			if json.Unmarshal(value, &calls) != nil || len(calls) > 32 {
				return false
			}
			seen := make(map[int]bool, len(calls))
			for _, call := range calls {
				if !s.applyTool(call, seen) {
					return false
				}
			}
		default:
			// Unknown delta merge semantics cannot be safely guessed. Fail
			// this diagnostic projection instead of silently losing a field.
			return false
		}
	}
	return true
}

func (s *replayDiagnosticChatStream) applyTool(raw json.RawMessage, seen map[int]bool) bool {
	call, err := fidelityJSONObject(raw)
	var index int
	if err != nil || !fidelityNonNull(call["index"]) || json.Unmarshal(call["index"], &index) != nil ||
		index < 0 || index >= 32 || seen[index] {
		return false
	}
	seen[index] = true
	tool := s.tools[index]
	if tool == nil {
		// The bridge's verified tool projection announces contiguous tool
		// indices with complete identity before any arguments-only delta.
		// Do not turn unannounced or out-of-order data into an apparent
		// protocol success merely because a later chunk fills the holes.
		function, functionErr := fidelityJSONObject(call["function"])
		var id, kind, name string
		if index != len(s.tools) || functionErr != nil ||
			json.Unmarshal(call["id"], &id) != nil || strings.TrimSpace(id) == "" ||
			json.Unmarshal(call["type"], &kind) != nil || kind != "function" ||
			json.Unmarshal(function["name"], &name) != nil || strings.TrimSpace(name) == "" {
			return false
		}
		tool = &replayDiagnosticChatTool{}
		s.tools[index] = tool
	}
	for key, rawValue := range call {
		switch key {
		case "index":
		case "id", "type":
			destination := &tool.id
			if key == "type" {
				destination = &tool.kind
			}
			if !replayDiagnosticStableChatScalar(rawValue, destination) || (key == "type" && tool.kind != "function") {
				return false
			}
		case "function":
			function, err := fidelityJSONObject(rawValue)
			if err != nil {
				return false
			}
			for field, value := range function {
				switch field {
				case "name":
					if !replayDiagnosticStableChatScalar(value, &tool.name) {
						return false
					}
				case "arguments":
					var fragment string
					if !fidelityNonNull(value) || json.Unmarshal(value, &fragment) != nil {
						return false
					}
					tool.arguments.WriteString(fragment)
				default:
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

func replayDiagnosticStableChatScalar(raw json.RawMessage, destination *string) bool {
	var value string
	if !fidelityNonNull(raw) || json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" ||
		(*destination != "" && *destination != value) {
		return false
	}
	*destination = value
	return true
}

func (s *replayDiagnosticChatStream) finalMessage() (json.RawMessage, bool) {
	if len(s.tools) > 0 {
		calls := make([]any, 0, len(s.tools))
		for index := 0; index < len(s.tools); index++ {
			tool := s.tools[index]
			if tool == nil {
				return nil, false
			}
			calls = append(calls, map[string]any{"id": tool.id, "type": tool.kind,
				"function": map[string]any{"name": tool.name, "arguments": tool.arguments.String()}})
		}
		s.message["tool_calls"], _ = json.Marshal(calls)
	}
	raw, err := json.Marshal(s.message)
	return raw, err == nil
}

func replayDiagnosticParseChatSSE(body []byte) replayDiagnosticChat {
	result := replayDiagnosticChat{Status: "incomplete"}
	state := replayDiagnosticChatStream{message: make(map[string]json.RawMessage), tools: make(map[int]*replayDiagnosticChatTool)}
	finish, eventName := "", ""
	var data []byte
	hadData, terminal, done, failed := false, false, false, false
	apply := func() {
		if eventName != "" && eventName != "message" && eventName != "chat.completion.chunk" {
			failed = true
			return
		}
		if !hadData {
			return
		}
		payload := bytes.TrimSpace(data)
		if bytes.Equal(payload, []byte("[DONE]")) {
			if done {
				failed = true
			}
			done = true
			return
		}
		if done {
			failed = true
			return
		}
		object, err := fidelityJSONObject(payload)
		if err != nil || !replayDiagnosticChatEnvelope(object, "chat.completion.chunk", &result) {
			failed = true
			return
		}
		var choices []json.RawMessage
		if !fidelityNonNull(object["choices"]) || json.Unmarshal(object["choices"], &choices) != nil {
			failed = true
			return
		}
		if len(choices) == 0 {
			// A legitimate usage-only trailer can follow the finish chunk.
			// It never establishes or repairs completion by itself.
			if !fidelityNonNull(object["usage"]) {
				failed = true
			}
			return
		}
		if terminal || len(choices) != 1 {
			failed = true
			return
		}
		choice, err := fidelityJSONObject(choices[0])
		var index int
		if err != nil || fidelityNonNull(choice["error"]) || fidelityNonNull(choice["incomplete_details"]) ||
			!fidelityNonNull(choice["index"]) || json.Unmarshal(choice["index"], &index) != nil || index != 0 ||
			!state.applyDelta(choice["delta"]) {
			failed = true
			return
		}
		if fidelityNonNull(choice["finish_reason"]) {
			var valid bool
			finish, valid = replayDiagnosticChatFinish(choice["finish_reason"])
			terminal = true
			if !valid {
				result.Status = finish
			}
		}
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), fidelityMaxResponseBytes+1)
	scanner.Split(fidelitySSELine)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			apply()
			data, eventName, hadData = data[:0], "", false
			continue
		}
		if line[0] == ':' {
			continue
		}
		field, value, _ := bytes.Cut(line, []byte{':'})
		value = bytes.TrimPrefix(value, []byte{' '})
		switch string(field) {
		case "event":
			eventName = string(value)
		case "data":
			data = append(data, value...)
			data = append(data, '\n')
			hadData = true
		}
	}
	if scanner.Err() != nil || failed {
		result.Status = "failed"
		return result
	}
	if hadData || eventName != "" || !terminal {
		result.Status = "incomplete"
		return result
	}
	if finish != "stop" && finish != "tool_calls" {
		return result
	}
	message, ok := state.finalMessage()
	if !ok {
		result.Status = "failed"
		return result
	}
	return replayDiagnosticFinishChat(result, message, finish)
}

func TestReasoningReplayDiagnosticChatParse(t *testing.T) {
	const answer = `{"selected":["A","B","E"],"hours":11,"value":36}`
	const toolMessage = `{"role":"assistant","content":"loading","reasoning_content":"visible summary","reasoning":"original alias","phase":"commentary","tool_calls":[{"id":"call_real","type":"function","function":{"name":"load_orders","arguments":"{}"}}]}`
	document := func(message, finish string) []byte {
		return []byte(fmt.Sprintf(`{"object":"chat.completion","id":"chat_real","model":"gpt-5.6-sol","choices":[{"index":0,"message":%s,"finish_reason":%s}]}`, message, finish))
	}
	chunk := func(delta, finish string) string {
		return fmt.Sprintf("data: {\"object\":\"chat.completion.chunk\",\"id\":\"chat_real\",\"model\":\"gpt-5.6-sol\",\"choices\":[{\"index\":0,\"delta\":%s,\"finish_reason\":%s}]}\n\n", delta, finish)
	}
	usage := "data: {\"object\":\"chat.completion.chunk\",\"id\":\"chat_real\",\"model\":\"gpt-5.6-sol\",\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\n\n"
	toolStart := chunk(`{"role":"assistant","content":null,"reasoning_content":"visible ","reasoning":"alias ","tool_calls":[{"index":0,"id":"call_real","type":"function","function":{"name":"load_orders","arguments":"{"}}]}`, "null")
	toolEnd := chunk(`{"reasoning_content":"summary","reasoning":"summary","tool_calls":[{"index":0,"function":{"arguments":"}"}}]}`, `"tool_calls"`)
	toolStream := ": keepalive\n\n" + toolStart + toolEnd + usage + "data: [DONE]\n\n"
	answerRaw, _ := json.Marshal(answer)
	answerStream := chunk(`{"role":"assistant","reasoning_content":"not final text"}`, "null") +
		chunk(`{"content":`+string(answerRaw)+`}`, `"stop"`) + usage + "data: [DONE]\n\n"

	t.Run("complete final answer excludes reasoning", func(t *testing.T) {
		for _, tc := range []struct {
			body        []byte
			contentType string
		}{
			{document(`{"role":"assistant","content":`+string(answerRaw)+`,"reasoning_content":"not final text"}`, `"stop"`), "application/json"},
			{[]byte(answerStream), "text/event-stream; charset=utf-8"},
			{[]byte(strings.ReplaceAll(answerStream, "\n", "\r\n")), "text/event-stream"},
			{[]byte(strings.ReplaceAll(answerStream, "\n", "\r")), "text/event-stream"},
		} {
			got := replayDiagnosticParseChat(tc.body, tc.contentType, 200)
			if got.Status != "completed" || got.HasTool || got.ID != "chat_real" || got.Model != "gpt-5.6-sol" || !fidelityScore("tool_final", got.Text) {
				t.Fatal("completed final answer was not scored independently of reasoning")
			}
		}
	})
	t.Run("complete tool projection and continuation", func(t *testing.T) {
		for _, stream := range []bool{false, true} {
			body, contentType := document(toolMessage, `"tool_calls"`), "application/json"
			if stream {
				body, contentType = []byte(toolStream), "text/event-stream"
			}
			got := replayDiagnosticParseChat(body, contentType, 200)
			if got.Status != "completed" || !got.HasTool || got.Text != "" {
				t.Fatal("valid tool turn rejected or reasoning exposed as answer")
			}
			first := replayDiagnosticChatBody("gpt-5.6-sol", "xhigh", stream)
			before, observed := bytes.Clone(first), bytes.Clone(got.Message)
			next, ok := replayDiagnosticChatContinuation(first, got)
			if !ok || !bytes.Equal(first, before) || !bytes.Equal(got.Message, observed) {
				t.Fatal("continuation rejected or mutated its source")
			}
			request, _ := fidelityJSONObject(next)
			var messages []json.RawMessage
			_ = json.Unmarshal(request["messages"], &messages)
			assistant, _ := fidelityJSONObject(messages[1])
			tool, _ := fidelityJSONObject(messages[2])
			var result, id, summary, alias string
			_ = json.Unmarshal(tool["content"], &result)
			_ = json.Unmarshal(tool["tool_call_id"], &id)
			_ = json.Unmarshal(assistant["reasoning_content"], &summary)
			_ = json.Unmarshal(assistant["reasoning"], &alias)
			if len(messages) != 3 || result != fidelityOrdersResult || id != "call_real" || summary != "visible summary" || alias == "" ||
				string(request["tool_choice"]) != `"none"` || string(request["reasoning_effort"]) != `"xhigh"` ||
				bytes.Count(next, []byte("Remember these constraints")) != 1 || bytes.Contains(next, []byte("encrypted_content")) {
				t.Fatal("continuation lost projection, repeated constraints or fabricated state")
			}
			if !stream && string(assistant["phase"]) != `"commentary"` {
				t.Fatal("complete nonstream projection lost an extension field")
			}
		}
	})
	t.Run("explicit finish needs no EOF inference or done marker", func(t *testing.T) {
		got := replayDiagnosticParseChat([]byte(toolStart+toolEnd), "text/event-stream", 200)
		if got.Status != "completed" {
			t.Fatal("authoritative completed finish incorrectly depended on DONE trailer")
		}
	})
	t.Run("strict failures never yield completed output", func(t *testing.T) {
		cases := []struct{ name, body, contentType string }{
			{"bare done", "data: [DONE]\n\n", "text/event-stream"},
			{"partial tool EOF", toolStart, "text/event-stream"},
			{"unterminated final event", toolStart + strings.TrimSuffix(toolEnd, "\n"), "text/event-stream"},
			{"late partial trailer", toolStream + "data: {", "text/event-stream"},
			{"late content", toolStart + toolEnd + chunk(`{"content":"late"}`, "null"), "text/event-stream"},
			{"duplicate finish", toolStart + toolEnd + toolEnd, "text/event-stream"},
			{"late usage after done", toolStream + usage, "text/event-stream"},
			{"error event", toolStart + "event: error\ndata: {\"error\":{\"code\":\"server_error\"}}\n\n" + toolEnd, "text/event-stream"},
			{"error envelope with finish", strings.Replace(toolStart+toolEnd, `"object":"chat.completion.chunk"`, `"error":{"code":"server_error"},"object":"chat.completion.chunk"`, 1), "text/event-stream"},
			{"identity conflict", toolStart + strings.Replace(toolEnd, `"index":0,"function"`, `"index":0,"id":"call_changed","function"`, 1), "text/event-stream"},
			{"missing tool identity", strings.ReplaceAll(toolStart+toolEnd, `"id":"call_real",`, ""), "text/event-stream"},
			{"identity arrives too late", strings.Replace(toolStart, `"id":"call_real",`, "", 1) + strings.Replace(toolEnd, `"index":0,"function"`, `"index":0,"id":"call_real","function"`, 1), "text/event-stream"},
			{"missing tool index", strings.ReplaceAll(toolStart+toolEnd, `"index":0,"id"`, `"id"`), "text/event-stream"},
			{"index gap", strings.ReplaceAll(toolStart+toolEnd, `"index":0,"function"`, `"index":1,"function"`), "text/event-stream"},
			{"partial arguments", toolStart + chunk(`{}`, `"tool_calls"`), "text/event-stream"},
			{"unsupported delta", toolStart + chunk(`{"unknown":{"part":1}}`, `"tool_calls"`), "text/event-stream"},
			{"changed model", toolStart + strings.ReplaceAll(toolEnd, "gpt-5.6-sol", "gpt-6-astra"), "text/event-stream"},
			{"negative usage", strings.Replace(toolStream, `"total_tokens":3`, `"total_tokens":-1`, 1), "text/event-stream"},
			{"stream choice error", strings.Replace(toolStart+toolEnd, `"delta":`, `"error":{"code":"server_error"},"delta":`, 1), "text/event-stream"},
			{"stream choice incomplete", strings.Replace(toolStart+toolEnd, `"delta":`, `"incomplete_details":{"reason":"max_output_tokens"},"delta":`, 1), "text/event-stream"},
			{"no finish", string(document(toolMessage, "null")), "application/json"},
			{"length", string(document(toolMessage, `"length"`)), "application/json"},
			{"filtered", string(document(toolMessage, `"content_filter"`)), "application/json"},
			{"wrong finish", string(document(toolMessage, `"stop"`)), "application/json"},
			{"no tools", string(document(`{"role":"assistant","content":""}`, `"tool_calls"`)), "application/json"},
			{"refusal", string(document(`{"role":"assistant","content":"","refusal":"denied"}`, `"stop"`)), "application/json"},
			{"error with answer", strings.Replace(string(document(`{"role":"assistant","content":"ok"}`, `"stop"`)), `"object":`, `"error":{"code":"server_error"},"object":`, 1), "application/json"},
			{"choice error", strings.Replace(string(document(toolMessage, `"tool_calls"`)), `"message":`, `"error":{"code":"server_error"},"message":`, 1), "application/json"},
			{"choice incomplete", strings.Replace(string(document(toolMessage, `"tool_calls"`)), `"message":`, `"incomplete_details":{"reason":"max_output_tokens"},"message":`, 1), "application/json"},
			{"incomplete envelope", strings.Replace(string(document(toolMessage, `"tool_calls"`)), `"object":`, `"status":"incomplete","object":`, 1), "application/json"},
			{"duplicate object key", string(document(strings.Replace(toolMessage, `"role":"assistant"`, `"role":"assistant","role":"assistant"`, 1), `"tool_calls"`)), "application/json"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got := replayDiagnosticParseChat([]byte(tc.body), tc.contentType, 200)
				if got.Status == "completed" || got.Text != "" || len(got.Message) != 0 {
					t.Fatal("invalid response became a completed replayable message")
				}
			})
		}
		for _, status := range []int{0, 199, 400, 401, 429, 500} {
			if replayDiagnosticParseChat(document(toolMessage, `"tool_calls"`), "application/json", status).Status == "completed" {
				t.Fatal("HTTP failure accepted as success")
			}
		}
		if replayDiagnosticParseChat(bytes.Repeat([]byte{' '}, fidelityMaxResponseBytes+1), "application/json", 200).Status != "failed" {
			t.Fatal("bounded body limit not enforced")
		}
	})
	t.Run("only exact diagnostic tool executes", func(t *testing.T) {
		for _, message := range []string{
			strings.Replace(toolMessage, "load_orders", "another_tool", 1),
			strings.Replace(toolMessage, `"arguments":"{}"`, `"arguments":"{\"answer\":true}"`, 1),
			strings.Replace(toolMessage, `"arguments":"{}"`, `"arguments":"{\"a\":1,\"a\":2}"`, 1),
			strings.Replace(toolMessage, `"arguments":"{}"`, `"arguments":"null"`, 1),
		} {
			got := replayDiagnosticParseChat(document(message, `"tool_calls"`), "application/json", 200)
			if _, ok := replayDiagnosticChatContinuation(replayDiagnosticChatBody("gpt-5.6-sol", "xhigh", false), got); ok {
				t.Fatal("unexpected or ambiguous tool call executed")
			}
		}
	})
	t.Run("request contract preserves selected model and strength", func(t *testing.T) {
		for _, target := range []struct{ model, effort string }{{"gpt-5.6-sol", "xhigh"}, {"gpt-6-astra", "max"}} {
			for _, stream := range []bool{false, true} {
				body, _ := fidelityJSONObject(replayDiagnosticChatBody(target.model, target.effort, stream))
				var model, effort string
				_ = json.Unmarshal(body["model"], &model)
				_ = json.Unmarshal(body["reasoning_effort"], &effort)
				if model != target.model || effort != target.effort || fidelityNonNull(body["stream_options"]) != stream {
					t.Fatal("model, explicit effort or stream usage contract changed")
				}
				if stream && !bytes.Equal(body["stream_options"], []byte(`{"include_usage":true}`)) {
					t.Fatal("stream usage was not requested")
				}
			}
		}
	})
}
