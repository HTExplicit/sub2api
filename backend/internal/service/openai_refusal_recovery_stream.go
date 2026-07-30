package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

const maxOpenAIRefusalStreamBufferBytes = 1 << 20

type openAIRefusalStreamAction uint8

const (
	openAIRefusalStreamHold openAIRefusalStreamAction = iota
	openAIRefusalStreamPass
	openAIRefusalStreamReplaceEarly
	openAIRefusalStreamReplace
)

type openAIRefusalStreamState struct {
	matcher          *OpenAIRefusalMatcher
	visibleText      strings.Builder
	matched          bool
	passthrough      bool
	earlyEligible    bool
	earlyEmitted     bool
	bufferedSize     int
	createdResponse  []byte
	completedMessage []byte
	responseID       string
	messageID        string
}

func newOpenAIRefusalStreamState(matcher *OpenAIRefusalMatcher) *openAIRefusalStreamState {
	return &openAIRefusalStreamState{matcher: matcher}
}

func newOpenAIRefusalStreamStateWithEarlyEmission(matcher *OpenAIRefusalMatcher, eligible bool) *openAIRefusalStreamState {
	return &openAIRefusalStreamState{matcher: matcher, earlyEligible: eligible}
}

func (s *openAIRefusalStreamState) reserveLine(line string) bool {
	if s == nil || s.passthrough || s.earlyEmitted {
		return true
	}
	s.bufferedSize += len(line) + 1
	if s.bufferedSize <= maxOpenAIRefusalStreamBufferBytes {
		return true
	}
	s.passthrough = true
	return false
}

func (s *openAIRefusalStreamState) observe(eventType string, payload []byte) (openAIRefusalStreamAction, []byte, error) {
	if s == nil || s.passthrough {
		return openAIRefusalStreamPass, nil, nil
	}
	s.captureEventMetadata(eventType, payload)
	if s.earlyEmitted {
		if eventType != "response.completed" {
			return openAIRefusalStreamHold, nil, nil
		}
		return s.completeEarlyReplacement(payload)
	}
	if openAIRefusalEventRequiresPassthrough(eventType, payload) {
		s.passthrough = true
		return openAIRefusalStreamPass, nil, nil
	}

	switch eventType {
	case "response.output_text.delta", "response.refusal.delta":
		_, _ = s.visibleText.WriteString(gjson.GetBytes(payload, "delta").String())
		if matched, _ := s.matcher.MatchLeadingParagraphs(s.visibleText.String()); matched {
			s.matched = true
			return s.startEarlyReplacement()
		}
		if openAIRefusalScanWindowComplete(s.visibleText.String()) || utf8.RuneCountInString(s.visibleText.String()) >= maxOpenAIRefusalParagraphRunes {
			s.passthrough = true
			return openAIRefusalStreamPass, nil, nil
		}
	case "response.output_text.done", "response.refusal.done":
		textField := "text"
		if eventType == "response.refusal.done" {
			textField = "refusal"
		}
		text := gjson.GetBytes(payload, textField).String()
		if current := s.visibleText.String(); current == "" || (strings.HasPrefix(text, current) && len(text) > len(current)) {
			s.visibleText.Reset()
			_, _ = s.visibleText.WriteString(text)
		}
		if matched, _ := s.matcher.MatchLeadingParagraphs(s.visibleText.String()); matched {
			s.matched = true
			return s.startEarlyReplacement()
		}
		s.passthrough = true
		return openAIRefusalStreamPass, nil, nil
	case "response.completed":
		response := gjson.GetBytes(payload, "response")
		if !response.Exists() {
			s.passthrough = true
			return openAIRefusalStreamPass, nil, nil
		}
		rewritten, matched, _, err := RewriteOpenAIResponsesJSON([]byte(response.Raw), s.matcher)
		if err != nil {
			return openAIRefusalStreamHold, nil, err
		}
		if !matched {
			s.passthrough = true
			return openAIRefusalStreamPass, nil, nil
		}
		stream, err := buildOpenAIRefusalReplacementSSE(payload, rewritten, s.matcher.Replacement())
		if err != nil {
			return openAIRefusalStreamHold, nil, err
		}
		return openAIRefusalStreamReplace, stream, nil
	case "response.failed", "response.incomplete", "response.cancelled":
		s.passthrough = true
		return openAIRefusalStreamPass, nil, nil
	}
	return openAIRefusalStreamHold, nil, nil
}

func (s *openAIRefusalStreamState) captureEventMetadata(eventType string, payload []byte) {
	if s.responseID == "" {
		s.responseID = strings.TrimSpace(gjson.GetBytes(payload, "response_id").String())
		if s.responseID == "" {
			s.responseID = strings.TrimSpace(gjson.GetBytes(payload, "response.id").String())
		}
	}
	if s.messageID == "" {
		s.messageID = strings.TrimSpace(gjson.GetBytes(payload, "item_id").String())
		if s.messageID == "" && gjson.GetBytes(payload, "item.type").String() == "message" {
			s.messageID = strings.TrimSpace(gjson.GetBytes(payload, "item.id").String())
		}
	}
	if eventType == "response.created" {
		response := gjson.GetBytes(payload, "response")
		if response.Exists() {
			s.createdResponse = append(s.createdResponse[:0], response.Raw...)
		}
	}
	if eventType == "response.output_item.done" {
		item := gjson.GetBytes(payload, "item")
		itemID := strings.TrimSpace(item.Get("id").String())
		if item.Get("type").String() == "message" && itemID != "" && itemID == s.messageID {
			s.completedMessage = append(s.completedMessage[:0], item.Raw...)
		}
	}
}

func (s *openAIRefusalStreamState) startEarlyReplacement() (openAIRefusalStreamAction, []byte, error) {
	if !s.earlyEligible || s.earlyEmitted || len(s.createdResponse) == 0 || s.responseID == "" || s.messageID == "" {
		return openAIRefusalStreamHold, nil, nil
	}
	prefix, err := buildOpenAIRefusalReplacementPrefixSSE(
		s.createdResponse,
		s.responseID,
		s.messageID,
		s.matcher.Replacement(),
	)
	if err != nil {
		return openAIRefusalStreamHold, nil, err
	}
	s.earlyEmitted = true
	return openAIRefusalStreamReplaceEarly, prefix, nil
}

func (s *openAIRefusalStreamState) completeEarlyReplacement(payload []byte) (openAIRefusalStreamAction, []byte, error) {
	response := gjson.GetBytes(payload, "response")
	if !response.Exists() {
		return openAIRefusalStreamHold, nil, errors.New("early replacement terminal event is missing response")
	}
	responseJSON := []byte(response.Raw)
	if len(s.completedMessage) > 0 {
		var responseObject map[string]json.RawMessage
		if err := json.Unmarshal(responseJSON, &responseObject); err != nil {
			return openAIRefusalStreamHold, nil, err
		}
		if rawOutput, ok := responseObject["output"]; ok {
			var output []json.RawMessage
			if err := json.Unmarshal(rawOutput, &output); err == nil && output != nil && len(output) == 0 {
				responseObject["output"], err = json.Marshal([]json.RawMessage{json.RawMessage(s.completedMessage)})
				if err != nil {
					return openAIRefusalStreamHold, nil, err
				}
				responseJSON, err = json.Marshal(responseObject)
				if err != nil {
					return openAIRefusalStreamHold, nil, err
				}
			}
		}
	}
	rewritten, matched, _, err := RewriteOpenAIResponsesJSON(responseJSON, s.matcher)
	if err != nil {
		return openAIRefusalStreamHold, nil, err
	}
	if !matched {
		return openAIRefusalStreamHold, nil, errors.New("early replacement terminal response is not text-only refusal output")
	}
	tail, err := buildOpenAIRefusalReplacementTerminalSSE(
		payload,
		rewritten,
		s.responseID,
		s.messageID,
		s.matcher.Replacement(),
	)
	if err != nil {
		return openAIRefusalStreamHold, nil, err
	}
	return openAIRefusalStreamReplace, tail, nil
}

func openAIRefusalScanWindowComplete(text string) bool {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	paragraphsComplete := 0
	inBlankRun := false
	for index := 1; index < len(lines)-1; index++ {
		if strings.TrimSpace(lines[index]) == "" {
			if inBlankRun {
				continue
			}
			paragraphsComplete++
			if paragraphsComplete >= maxOpenAIRefusalScanParagraphs {
				return true
			}
			inBlankRun = true
			continue
		}
		inBlankRun = false
	}
	return false
}

func openAIRefusalEventRequiresPassthrough(eventType string, payload []byte) bool {
	if strings.Contains(eventType, "function_call") ||
		strings.Contains(eventType, "custom_tool") ||
		strings.Contains(eventType, "tool_call") ||
		strings.Contains(eventType, "image_generation") ||
		strings.Contains(eventType, "web_search") ||
		strings.Contains(eventType, "file_search") ||
		strings.Contains(eventType, "computer_call") ||
		strings.Contains(eventType, "local_shell") {
		return true
	}
	if eventType != "response.output_item.added" && eventType != "response.output_item.done" {
		return false
	}
	itemType := strings.TrimSpace(gjson.GetBytes(payload, "item.type").String())
	return itemType != "" && itemType != "message" && itemType != "reasoning"
}

func buildOpenAIRefusalReplacementSSE(terminalPayload, rewrittenResponse []byte, replacement string) ([]byte, error) {
	responseID := strings.TrimSpace(gjson.GetBytes(rewrittenResponse, "id").String())
	messageID := strings.TrimSpace(gjson.GetBytes(rewrittenResponse, "output.0.id").String())
	if responseID == "" {
		return nil, fmt.Errorf("replacement response is missing id")
	}
	if messageID == "" {
		messageID = "msg_refusal_recovery"
	}
	prefix, err := buildOpenAIRefusalReplacementPrefixSSE(rewrittenResponse, responseID, messageID, replacement)
	if err != nil {
		return nil, err
	}
	tail, err := buildOpenAIRefusalReplacementTerminalSSE(terminalPayload, rewrittenResponse, responseID, messageID, replacement)
	if err != nil {
		return nil, err
	}
	return append(prefix, tail...), nil
}

func buildOpenAIRefusalReplacementPrefixSSE(responseTemplate []byte, responseID, messageID, replacement string) ([]byte, error) {
	if strings.TrimSpace(responseID) == "" || strings.TrimSpace(messageID) == "" {
		return nil, errors.New("early replacement stream is missing response or message id")
	}
	var createdResponse map[string]json.RawMessage
	if err := json.Unmarshal(responseTemplate, &createdResponse); err != nil {
		return nil, fmt.Errorf("decode replacement response: %w", err)
	}
	if templateID := strings.TrimSpace(gjson.GetBytes(responseTemplate, "id").String()); templateID != "" && templateID != responseID {
		return nil, errors.New("replacement response id changed before early emission")
	}
	encodedResponseID, err := json.Marshal(responseID)
	if err != nil {
		return nil, fmt.Errorf("encode replacement response id: %w", err)
	}
	createdResponse["id"] = encodedResponseID
	createdResponse["status"] = json.RawMessage(`"in_progress"`)
	createdResponse["output"] = json.RawMessage(`[]`)
	delete(createdResponse, "usage")
	delete(createdResponse, "error")
	delete(createdResponse, "incomplete_details")
	createdResponseJSON, err := json.Marshal(createdResponse)
	if err != nil {
		return nil, fmt.Errorf("encode created response: %w", err)
	}

	textPartEmpty := map[string]any{"type": "output_text", "text": "", "annotations": []any{}, "logprobs": []any{}}
	messageInProgress := map[string]any{"id": messageID, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}}
	events := []map[string]any{
		{"type": "response.created", "sequence_number": 0, "response": json.RawMessage(createdResponseJSON)},
		{"type": "response.output_item.added", "sequence_number": 1, "response_id": responseID, "output_index": 0, "item": messageInProgress},
		{"type": "response.content_part.added", "sequence_number": 2, "response_id": responseID, "item_id": messageID, "output_index": 0, "content_index": 0, "part": textPartEmpty},
		{"type": "response.output_text.delta", "sequence_number": 3, "response_id": responseID, "item_id": messageID, "output_index": 0, "content_index": 0, "delta": replacement, "logprobs": []any{}},
	}

	var out bytes.Buffer
	for _, event := range events {
		if err := writeOpenAIRefusalSSEMapEvent(&out, event); err != nil {
			return nil, err
		}
	}
	return out.Bytes(), nil
}

func buildOpenAIRefusalReplacementTerminalSSE(terminalPayload, rewrittenResponse []byte, responseID, messageID, replacement string) ([]byte, error) {
	if terminalResponseID := strings.TrimSpace(gjson.GetBytes(rewrittenResponse, "id").String()); terminalResponseID == "" || terminalResponseID != responseID {
		return nil, errors.New("replacement terminal response id does not match early emission")
	}
	if terminalMessageID := strings.TrimSpace(gjson.GetBytes(rewrittenResponse, "output.0.id").String()); terminalMessageID == "" || terminalMessageID != messageID {
		return nil, errors.New("replacement terminal message id does not match early emission")
	}

	textPartDone := map[string]any{"type": "output_text", "text": replacement, "annotations": []any{}, "logprobs": []any{}}
	messageDone := map[string]any{"id": messageID, "type": "message", "status": "completed", "role": "assistant", "content": []any{textPartDone}}
	events := []map[string]any{
		{"type": "response.output_text.done", "sequence_number": 4, "response_id": responseID, "item_id": messageID, "output_index": 0, "content_index": 0, "text": replacement, "logprobs": []any{}},
		{"type": "response.content_part.done", "sequence_number": 5, "response_id": responseID, "item_id": messageID, "output_index": 0, "content_index": 0, "part": textPartDone},
		{"type": "response.output_item.done", "sequence_number": 6, "response_id": responseID, "output_index": 0, "item": messageDone},
	}

	var terminal map[string]json.RawMessage
	if err := json.Unmarshal(terminalPayload, &terminal); err != nil {
		return nil, fmt.Errorf("decode terminal event: %w", err)
	}
	terminal["type"] = json.RawMessage(`"response.completed"`)
	terminal["sequence_number"] = json.RawMessage(`7`)
	terminal["response"] = json.RawMessage(rewrittenResponse)
	terminalJSON, err := json.Marshal(terminal)
	if err != nil {
		return nil, fmt.Errorf("encode terminal event: %w", err)
	}

	var out bytes.Buffer
	for _, event := range events {
		if err := writeOpenAIRefusalSSEMapEvent(&out, event); err != nil {
			return nil, err
		}
	}
	writeOpenAIRefusalSSEEvent(&out, "response.completed", terminalJSON)
	return out.Bytes(), nil
}

func writeOpenAIRefusalSSEMapEvent(out *bytes.Buffer, event map[string]any) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode replacement stream event: %w", err)
	}
	eventType, ok := event["type"].(string)
	if !ok {
		return errors.New("replacement stream event type is not a string")
	}
	writeOpenAIRefusalSSEEvent(out, eventType, payload)
	return nil
}

func writeOpenAIRefusalSSEEvent(out *bytes.Buffer, eventType string, payload []byte) {
	_, _ = out.WriteString("event: ")
	_, _ = out.WriteString(eventType)
	_ = out.WriteByte('\n')
	_, _ = out.WriteString("data: ")
	_, _ = out.Write(payload)
	_, _ = out.WriteString("\n\n")
}
