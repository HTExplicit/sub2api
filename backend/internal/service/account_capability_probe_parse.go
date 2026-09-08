package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

type capabilityProbeToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// All wire content below is request-local and is never returned in an API or
// persisted. It exists only to validate semantic completion and, when asked,
// faithfully return the first turn's tool history in the second request.
type capabilityProbeObservation struct {
	terminal         bool
	hasText          bool
	failure          string
	errorCode        string
	usage            AccountCapabilityUsage
	tools            []capabilityProbeToolCall
	toolDeclarations int
	replayItems      []json.RawMessage
	responseItems    map[int]json.RawMessage
	messageBlocks    map[int]map[string]any
	messageInputs    map[int]string
	messageStop      string
	chatMessage      map[string]any
	chatTools        map[int]*capabilityProbeToolCall
	chatText         strings.Builder
	chatReasoning    strings.Builder
	chatStop         string
}

func (o capabilityProbeObservation) singleExpectedTool() (capabilityProbeToolCall, bool) {
	if !o.terminal || o.failure != "" || len(o.tools) != 1 || o.toolDeclarations != 1 {
		return capabilityProbeToolCall{}, false
	}
	if (o.chatStop != "" && o.chatStop != "tool_calls") || (o.messageStop != "" && o.messageStop != "tool_use") {
		return capabilityProbeToolCall{}, false
	}
	tool := o.tools[0]
	if tool.Name != accountCapabilityToolName || tool.ID == "" || strings.TrimSpace(tool.ID) != tool.ID || len(tool.ID) > 512 || strings.ContainsAny(tool.ID, "\r\n\x00") {
		return capabilityProbeToolCall{}, false
	}
	decoder := json.NewDecoder(strings.NewReader(tool.Arguments))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') || !decoder.More() {
		return capabilityProbeToolCall{}, false
	}
	key, err := decoder.Token()
	if err != nil || key != "value" {
		return capabilityProbeToolCall{}, false
	}
	var value string
	if decoder.Decode(&value) != nil || value != "ok" || decoder.More() {
		return capabilityProbeToolCall{}, false
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return capabilityProbeToolCall{}, false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return capabilityProbeToolCall{}, false
	}
	return tool, true
}

func accountCapabilityReadObservation(body io.Reader, protocol string, streaming bool) (capabilityProbeObservation, error) {
	observation := capabilityProbeObservation{}
	limited := &io.LimitedReader{R: body, N: accountCapabilityBodyLimit + 1}
	if !streaming {
		wire, err := io.ReadAll(limited)
		if err != nil {
			return observation, err
		}
		if len(wire) > accountCapabilityBodyLimit || !gjson.ValidBytes(wire) || !gjson.ParseBytes(wire).IsObject() {
			return observation, errors.New("invalid bounded capability response")
		}
		observation.consumeJSON(protocol, wire)
		observation.finalize(protocol)
		return observation, nil
	}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), accountCapabilityBodyLimit)
	var eventData strings.Builder
	eventName := ""
	dispatch := func() (bool, error) {
		if eventData.Len() == 0 {
			eventName = ""
			return false, nil
		}
		wire := []byte(strings.TrimSuffix(eventData.String(), "\n"))
		eventData.Reset()
		if bytes.Equal(bytes.TrimSpace(wire), []byte("[DONE]")) {
			// Transport DONE never creates an authoritative model terminal.
			return true, nil
		}
		if !gjson.ValidBytes(wire) || !gjson.ParseBytes(wire).IsObject() {
			return false, errors.New("invalid capability stream event")
		}
		observation.consumeEvent(protocol, eventName, wire)
		eventName = ""
		return observation.terminal || observation.failure != "", nil
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			done, err := dispatch()
			if err != nil {
				return observation, err
			}
			if done {
				observation.finalize(protocol)
				return observation, nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			value := strings.TrimPrefix(line, "data:")
			value = strings.TrimPrefix(value, " ")
			_, _ = eventData.WriteString(value)
			_ = eventData.WriteByte('\n')
		} else if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
	}
	if err := scanner.Err(); err != nil {
		return observation, err
	}
	if limited.N <= 0 {
		return observation, errors.New("capability stream exceeded body limit")
	}
	// SSE permits a final event without a trailing blank line. Parsing that
	// event is safe, but EOF itself still proves nothing about completion.
	if _, err := dispatch(); err != nil {
		return observation, err
	}
	observation.finalize(protocol)
	return observation, nil
}

func (o *capabilityProbeObservation) consumeJSON(protocol string, body []byte) {
	root := gjson.ParseBytes(body)
	if root.Get("error").Exists() && root.Get("error").Type != gjson.Null {
		o.consumeError(body)
		return
	}
	switch protocol {
	case AccountCapabilityProtocolResponses:
		o.consumeResponse(root)
	case AccountCapabilityProtocolChatCompletions:
		o.consumeChat(root, false)
	case AccountCapabilityProtocolMessages:
		if root.Get("type").String() != "message" || root.Get("role").String() != "assistant" {
			return
		}
		o.consumeUsage(root.Get("usage"))
		o.loadMessageBlocks(root.Get("content"))
		o.messageStop = root.Get("stop_reason").String()
		o.finishMessage()
	}
}

func (o *capabilityProbeObservation) consumeEvent(protocol, eventName string, body []byte) {
	root := gjson.ParseBytes(body)
	kind := root.Get("type").String()
	if kind == "" {
		kind = eventName
	}
	if kind == "error" || (root.Get("error").Exists() && root.Get("error").Type != gjson.Null) {
		o.consumeError(body)
		return
	}
	switch protocol {
	case AccountCapabilityProtocolResponses:
		switch kind {
		case "response.output_text.delta":
			if strings.TrimSpace(root.Get("delta").String()) != "" {
				o.hasText = true
			}
		case "response.output_item.done":
			item := root.Get("item")
			if item.IsObject() {
				if o.responseItems == nil {
					o.responseItems = make(map[int]json.RawMessage)
				}
				o.responseItems[int(root.Get("output_index").Int())] = json.RawMessage(item.Raw)
			}
		case "response.completed":
			o.consumeResponse(root.Get("response"))
		case "response.failed", "response.incomplete":
			o.consumeResponse(root.Get("response"))
			if o.failure == "" {
				o.failure = "incomplete_response"
			}
		}
	case AccountCapabilityProtocolChatCompletions:
		o.consumeChat(root, true)
	case AccountCapabilityProtocolMessages:
		switch kind {
		case "message_start":
			o.consumeUsage(root.Get("message.usage"))
			o.loadMessageBlocks(root.Get("message.content"))
		case "content_block_start":
			if o.messageBlocks == nil {
				o.messageBlocks = make(map[int]map[string]any)
			}
			var block map[string]any
			if json.Unmarshal([]byte(root.Get("content_block").Raw), &block) == nil && block != nil {
				o.messageBlocks[int(root.Get("index").Int())] = block
			}
		case "content_block_delta":
			index := int(root.Get("index").Int())
			block := o.messageBlocks[index]
			if block == nil {
				// A delta without a matching block start cannot form replayable
				// tool history or trusted semantic text.
				return
			}
			delta := root.Get("delta")
			switch delta.Get("type").String() {
			case "text_delta", "thinking_delta", "signature_delta":
				field := strings.TrimSuffix(delta.Get("type").String(), "_delta")
				previous, _ := block[field].(string)
				block[field] = previous + delta.Get(field).String()
			case "input_json_delta":
				if o.messageInputs == nil {
					o.messageInputs = make(map[int]string)
				}
				o.messageInputs[index] += delta.Get("partial_json").String()
			}
		case "message_delta":
			o.messageStop = root.Get("delta.stop_reason").String()
			o.consumeUsage(root.Get("usage"))
		case "message_stop":
			o.finishMessage()
		}
	}
}

func (o *capabilityProbeObservation) consumeError(body []byte) {
	failure := accountCapabilityHTTPFailure(AccountCapabilityProbeAttempt{}, 200, body, false)
	o.failure, o.errorCode = failure.Classification, failure.ErrorCode
}

func (o *capabilityProbeObservation) consumeResponse(response gjson.Result) {
	if !response.IsObject() {
		return
	}
	o.consumeUsage(response.Get("usage"))
	switch response.Get("status").String() {
	case "completed":
		o.terminal = true
	case "incomplete":
		o.failure = "incomplete_response"
		if reason := response.Get("incomplete_details.reason").String(); reason == "max_output_tokens" || reason == "max_tokens" {
			o.failure = "output_budget_exhausted"
		}
	case "failed", "cancelled":
		o.consumeError([]byte(response.Raw))
	}
	if output := response.Get("output"); output.IsArray() {
		o.responseItems = make(map[int]json.RawMessage)
		for index, item := range output.Array() {
			o.responseItems[index] = json.RawMessage(item.Raw)
		}
	}
}

func (o *capabilityProbeObservation) consumeChat(root gjson.Result, streaming bool) {
	o.consumeUsage(root.Get("usage"))
	choices := root.Get("choices").Array()
	for _, choice := range choices {
		if index := choice.Get("index"); index.Exists() && index.Int() != 0 {
			continue
		}
		message := choice.Get("message")
		if streaming {
			message = choice.Get("delta")
		}
		if content := message.Get("content"); content.Type == gjson.String {
			_, _ = o.chatText.WriteString(content.String())
		}
		if reasoning := message.Get("reasoning_content"); reasoning.Type == gjson.String {
			_, _ = o.chatReasoning.WriteString(reasoning.String())
		}
		for index, call := range message.Get("tool_calls").Array() {
			if o.chatTools == nil {
				o.chatTools = make(map[int]*capabilityProbeToolCall)
			}
			if callIndex := call.Get("index"); callIndex.Exists() {
				index = int(callIndex.Int())
			}
			tool := o.chatTools[index]
			if tool == nil {
				tool = &capabilityProbeToolCall{}
				o.chatTools[index] = tool
			}
			if id := call.Get("id").String(); id != "" {
				tool.ID = id
			}
			if name := call.Get("function.name").String(); name != "" {
				tool.Name += name
			}
			tool.Arguments += call.Get("function.arguments").String()
		}
		if reason := choice.Get("finish_reason").String(); reason != "" {
			o.chatStop = reason
			switch reason {
			case "stop", "tool_calls", "function_call":
				o.terminal = true
			case "length":
				o.failure = "output_budget_exhausted"
			case "content_filter":
				o.failure = "safety_rejection"
			}
		}
		// The probe requested one completion, and only that choice is evidence.
		break
	}
}

func (o *capabilityProbeObservation) loadMessageBlocks(content gjson.Result) {
	if !content.IsArray() {
		return
	}
	if o.messageBlocks == nil {
		o.messageBlocks = make(map[int]map[string]any)
	}
	for index, raw := range content.Array() {
		var block map[string]any
		if json.Unmarshal([]byte(raw.Raw), &block) == nil && block != nil {
			o.messageBlocks[index] = block
		}
	}
}

func (o *capabilityProbeObservation) finishMessage() {
	switch o.messageStop {
	case "end_turn", "stop_sequence", "tool_use":
		o.terminal = true
	case "max_tokens", "model_context_window_exceeded":
		o.failure = "output_budget_exhausted"
	case "refusal":
		o.failure = "safety_rejection"
	}
}

func (o *capabilityProbeObservation) finalize(protocol string) {
	switch protocol {
	case AccountCapabilityProtocolResponses:
		for _, index := range accountCapabilitySortedKeys(o.responseItems) {
			raw := o.responseItems[index]
			o.replayItems = append(o.replayItems, raw)
			item := gjson.ParseBytes(raw)
			switch item.Get("type").String() {
			case "message":
				for _, content := range item.Get("content").Array() {
					if content.Get("type").String() == "output_text" && strings.TrimSpace(content.Get("text").String()) != "" {
						o.hasText = true
					}
				}
			case "function_call":
				o.toolDeclarations++
				tool := capabilityProbeToolCall{ID: item.Get("call_id").String(), Name: item.Get("name").String(), Arguments: item.Get("arguments").String()}
				complete := item.Get("status").String() == "" || item.Get("status").String() == "completed"
				if complete && tool.ID != "" && tool.Name != "" && gjson.Valid(tool.Arguments) {
					o.tools = append(o.tools, tool)
				}
			}
		}
	case AccountCapabilityProtocolMessages:
		for _, index := range accountCapabilitySortedKeys(o.messageBlocks) {
			block := o.messageBlocks[index]
			if input, exists := o.messageInputs[index]; exists {
				if !gjson.Valid(input) {
					o.failure = "invalid_response"
					return
				}
				block["input"] = json.RawMessage(input)
			}
			raw, err := json.Marshal(block)
			if err != nil {
				o.failure = "invalid_response"
				return
			}
			o.replayItems = append(o.replayItems, raw)
			switch block["type"] {
			case "text":
				text, _ := block["text"].(string)
				o.hasText = o.hasText || strings.TrimSpace(text) != ""
			case "tool_use":
				o.toolDeclarations++
				id, _ := block["id"].(string)
				name, _ := block["name"].(string)
				input := gjson.GetBytes(raw, "input")
				if id != "" && name != "" && input.IsObject() {
					o.tools = append(o.tools, capabilityProbeToolCall{ID: id, Name: name, Arguments: input.Raw})
				}
			}
		}
	case AccountCapabilityProtocolChatCompletions:
		o.hasText = strings.TrimSpace(o.chatText.String()) != ""
		o.chatMessage = map[string]any{"role": "assistant", "content": o.chatText.String()}
		if o.chatReasoning.Len() != 0 {
			o.chatMessage["reasoning_content"] = o.chatReasoning.String()
		}
		calls := make([]any, 0, len(o.chatTools))
		for _, index := range accountCapabilitySortedKeys(o.chatTools) {
			o.toolDeclarations++
			tool := o.chatTools[index]
			calls = append(calls, map[string]any{"type": "function", "id": tool.ID, "function": map[string]any{"name": tool.Name, "arguments": tool.Arguments}})
			if tool.ID != "" && tool.Name != "" && gjson.Valid(tool.Arguments) {
				o.tools = append(o.tools, *tool)
			}
		}
		if len(calls) != 0 {
			o.chatMessage["tool_calls"] = calls
		}
	}
}

func accountCapabilitySortedKeys[T any](values map[int]T) []int {
	keys := make([]int, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

func (o *capabilityProbeObservation) consumeUsage(usage gjson.Result) {
	copyValue := func(names []string, target **int64) {
		for _, name := range names {
			value := usage.Get(name)
			if value.Type != gjson.Number {
				continue
			}
			parsed, err := strconv.ParseInt(value.Raw, 10, 64)
			if err == nil && parsed >= 0 {
				*target = &parsed
				return
			}
		}
	}
	copyValue([]string{"input_tokens", "prompt_tokens"}, &o.usage.InputTokens)
	copyValue([]string{"output_tokens", "completion_tokens"}, &o.usage.OutputTokens)
	copyValue([]string{"total_tokens"}, &o.usage.TotalTokens)
}
