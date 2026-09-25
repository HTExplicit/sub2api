package service

import (
	"encoding/json"
	"fmt"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func applyPromptInstructions(body []byte, application *BusinessSystemPromptApplication, indices []int, applied []bool) ([]byte, error) {
	value, _, unique := uniquePromptField(body, "instructions")
	if !unique || (value.Exists() && value.Type != gjson.String) {
		return nil, fmt.Errorf("%w: instructions must be a unique string", ErrBusinessSystemPromptInvalid)
	}
	application.OriginalInstructionsExists, application.ClientInstructions = value.Exists(), value.String()
	before, after, publicBefore, publicAfter := []string{}, []string{}, []string{}, []string{}
	for _, index := range indices {
		placement := &application.RulesPlan.Placements[index]
		switch placement.Position {
		case extensionv1.PromptPositionControlPrepend:
			before = append(before, placement.Body)
			if placement.PreserveEcho {
				publicBefore = append(publicBefore, placement.Body)
			}
		case extensionv1.PromptPositionControlAppend:
			after = append(after, placement.Body)
			if placement.PreserveEcho {
				publicAfter = append(publicAfter, placement.Body)
			}
		default:
			return nil, ErrPromptDeliveryUnsupported
		}
		applied[index] = true
	}
	parts := append(before, value.String())
	parts = append(parts, after...)
	application.FinalInstructions = strings.Join(nonEmptyPromptParts(parts), "\n\n")
	application.PublicInstructions, application.PublicInstructionsExists = value.String(), value.Exists()
	if len(publicBefore)+len(publicAfter) > 0 {
		parts := append(publicBefore, value.String())
		parts = append(parts, publicAfter...)
		application.PublicInstructions, application.PublicInstructionsExists = strings.Join(nonEmptyPromptParts(parts), "\n\n"), true
	}
	return sjson.SetBytes(body, "instructions", application.FinalInstructions)
}

func promptTextBlock(text string, claude bool) json.RawMessage {
	block := map[string]string{"text": text}
	if claude {
		block["type"] = "text"
	}
	raw, _ := json.Marshal(block)
	return raw
}

func promptStructuredBlocks(placement extensionv1.PromptRulePlacement) ([]json.RawMessage, error) {
	if placement.ContentFormat == "" || placement.ContentFormat == "text" {
		return []json.RawMessage{promptTextBlock(placement.Body, true)}, nil
	}
	if placement.ContentFormat != extensionv1.PromptContentAnthropicSystemBlocks {
		return nil, ErrPromptDeliveryUnsupported
	}
	var blocks []json.RawMessage
	if !gjson.ParseBytes(placement.StructuredContent).IsArray() || json.Unmarshal(placement.StructuredContent, &blocks) != nil {
		return nil, fmt.Errorf("%w: structured system content must be a block array", ErrBusinessSystemPromptInvalid)
	}
	for _, block := range blocks {
		view := gjson.ParseBytes(block)
		if !view.IsObject() || view.Get("type").String() != "text" || view.Get("text").Type != gjson.String {
			return nil, fmt.Errorf("%w: invalid structured system text block", ErrBusinessSystemPromptInvalid)
		}
	}
	return blocks, nil
}

func applyPromptSystemBlocks(body []byte, field string, application *BusinessSystemPromptApplication, indices []int, applied []bool) ([]byte, error) {
	value, _, unique := uniquePromptField(body, field)
	if !unique {
		return nil, fmt.Errorf("%w: duplicate system carrier", ErrBusinessSystemPromptInvalid)
	}
	existing := []json.RawMessage{}
	systemObject := []byte(`{}`)
	if field == "system" {
		if value.Type == gjson.String {
			existing = append(existing, promptTextBlock(value.String(), true))
		} else if value.Exists() {
			if !value.IsArray() || json.Unmarshal([]byte(value.Raw), &existing) != nil {
				return nil, fmt.Errorf("%w: system must be a string or block array", ErrBusinessSystemPromptInvalid)
			}
		}
	} else if value.Exists() {
		if !value.IsObject() {
			return nil, fmt.Errorf("%w: systemInstruction must be an object", ErrBusinessSystemPromptInvalid)
		}
		systemObject = []byte(value.Raw)
		parts, _, unique := uniquePromptField(systemObject, "parts")
		if !unique {
			return nil, fmt.Errorf("%w: duplicate systemInstruction.parts", ErrBusinessSystemPromptInvalid)
		}
		if parts.Exists() && (!parts.IsArray() || json.Unmarshal([]byte(parts.Raw), &existing) != nil) {
			return nil, fmt.Errorf("%w: systemInstruction.parts must be an array", ErrBusinessSystemPromptInvalid)
		}
	}
	before, after := []json.RawMessage{}, []json.RawMessage{}
	beforeRefs, afterRefs := map[int]int{}, map[int]int{}
	for _, index := range indices {
		placement := &application.RulesPlan.Placements[index]
		var blocks []json.RawMessage
		var err error
		if field == "system" {
			blocks, err = promptStructuredBlocks(*placement)
		} else {
			if placement.ContentFormat != "" && placement.ContentFormat != "text" {
				return nil, ErrPromptDeliveryUnsupported
			}
			blocks = []json.RawMessage{promptTextBlock(placement.Body, false)}
		}
		if err != nil {
			return nil, err
		}
		if len(blocks) == 0 {
			application.RulesPlan.Skipped = append(application.RulesPlan.Skipped, extensionv1.PromptRuleDecision{RuleID: placement.RuleID, Reason: "empty_content"})
			continue
		}
		switch placement.Position {
		case extensionv1.PromptPositionControlPrepend:
			beforeRefs[index] = len(before)
			before = append(before, blocks...)
		case extensionv1.PromptPositionControlAppend:
			afterRefs[index] = len(after)
			after = append(after, blocks...)
		default:
			return nil, ErrPromptDeliveryUnsupported
		}
		applied[index] = true
	}
	if len(before)+len(after) == 0 {
		return body, nil
	}
	for index, position := range beforeRefs {
		application.RulesPlan.Placements[index].Index = &position
	}
	for index, position := range afterRefs {
		position += len(before) + len(existing)
		application.RulesPlan.Placements[index].Index = &position
	}
	combined := append(before, existing...)
	combined = append(combined, after...)
	raw, err := json.Marshal(combined)
	if err != nil {
		return nil, err
	}
	if field == "systemInstruction" {
		raw, err = sjson.SetRawBytes(systemObject, "parts", raw)
		if err != nil {
			return nil, err
		}
	}
	return sjson.SetRawBytes(body, field, raw)
}

func applyPromptMessages(body []byte, field string, application *BusinessSystemPromptApplication, indices []int, applied []bool) ([]byte, error) {
	value, _, unique := uniquePromptField(body, field)
	if !unique {
		return nil, fmt.Errorf("%w: duplicate message carrier", ErrBusinessSystemPromptInvalid)
	}
	items := []json.RawMessage{}
	if field == "input" && value.Type == gjson.String {
		message, _ := json.Marshal(map[string]string{"role": "user", "content": value.String()})
		items = append(items, message)
	} else if value.Exists() {
		if !value.IsArray() || json.Unmarshal([]byte(value.Raw), &items) != nil {
			return nil, fmt.Errorf("%w: %s must be a message array", ErrBusinessSystemPromptInvalid, field)
		}
	} else if field == "messages" {
		return nil, fmt.Errorf("%w: messages must be an array", ErrBusinessSystemPromptInvalid)
	}
	controlEnd, lastUser := 0, -1
	for controlEnd < len(items) {
		role := gjson.GetBytes(items[controlEnd], "role").String()
		if role != "system" && role != "developer" {
			break
		}
		controlEnd++
	}
	for index, item := range items {
		if promptItemIsUserInput(item) {
			lastUser = index
		}
	}
	insertions := map[int][]int{}
	for _, index := range indices {
		placement := &application.RulesPlan.Placements[index]
		if placement.Role != "system" && placement.Role != "developer" {
			return nil, ErrPromptDeliveryUnsupported
		}
		at, reason := 0, ""
		switch placement.Position {
		case extensionv1.PromptPositionControlPrepend, extensionv1.PromptPositionConversationHead:
		case extensionv1.PromptPositionControlAppend:
			at = controlEnd
		case extensionv1.PromptPositionConversationTail:
			at = len(items)
		case extensionv1.PromptPositionBeforeLastUser:
			if lastUser < 0 {
				reason = "last_user_missing"
			} else {
				at = lastUser
			}
		case extensionv1.PromptPositionAfterLastUser:
			if lastUser < 0 {
				reason = "last_user_missing"
			} else {
				at = lastUser + 1
			}
		default:
			return nil, ErrPromptDeliveryUnsupported
		}
		if reason == "" && !promptToolBoundarySafe(items, at) {
			reason = "unsafe_tool_boundary"
		}
		if reason == "" && placement.Protocol == BusinessSystemPromptProtocolMessages && !claudeSystemBoundarySafe(items, at) {
			reason = "illegal_message_boundary"
		}
		if reason != "" {
			application.RulesPlan.Skipped = append(application.RulesPlan.Skipped, extensionv1.PromptRuleDecision{RuleID: placement.RuleID, Reason: reason})
			continue
		}
		applied[index] = true
		insertions[at] = append(insertions[at], index)
	}
	if len(insertions) == 0 {
		return body, nil
	}
	combined := make([]json.RawMessage, 0, len(items)+len(indices))
	for at := 0; at <= len(items); at++ {
		queued := insertions[at]
		if len(queued) > 0 && application.RulesPlan.Placements[queued[0]].Protocol == BusinessSystemPromptProtocolMessages {
			// Adjacent Claude system messages violate its position contract.
			// Coalesce only our insertions, preserving rule order and each block.
			blocks := []json.RawMessage{}
			for _, index := range queued {
				placement := &application.RulesPlan.Placements[index]
				position, blockIndex := len(combined), len(blocks)
				placement.Index, placement.BlockIndex = &position, &blockIndex
				blocks = append(blocks, promptTextBlock(placement.Body, true))
			}
			message, _ := json.Marshal(map[string]any{"role": "system", "content": blocks})
			combined = append(combined, message)
		} else {
			for _, index := range queued {
				placement := &application.RulesPlan.Placements[index]
				position := len(combined)
				placement.Index = &position
				message, _ := json.Marshal(map[string]string{"role": placement.Role, "content": placement.Body})
				combined = append(combined, message)
			}
		}
		if at < len(items) {
			combined = append(combined, items[at])
		}
	}
	raw, err := json.Marshal(combined)
	if err != nil {
		return nil, err
	}
	return sjson.SetRawBytes(body, field, raw)
}

func promptItemIsUserInput(raw json.RawMessage) bool {
	item := gjson.ParseBytes(raw)
	if item.Get("role").String() != "user" {
		return false
	}
	content := item.Get("content")
	if content.Type == gjson.String {
		return strings.TrimSpace(content.String()) != ""
	}
	if !content.IsArray() {
		return false
	}
	for _, block := range content.Array() {
		kind := block.Get("type").String()
		if kind == "tool_result" || strings.HasSuffix(kind, "_tool_result") || strings.HasSuffix(kind, "_call_output") || kind == "tool_search_output" {
			continue
		}
		if kind == "text" || kind == "input_text" {
			if strings.TrimSpace(block.Get("text").String()) != "" {
				return true
			}
			continue
		}
		if kind != "" {
			return true
		}
	}
	return false
}

func promptItemHasToolResult(item gjson.Result) bool {
	kind := item.Get("type").String()
	if item.Get("role").String() == "tool" || item.Get("role").String() == "function" || strings.HasSuffix(kind, "_call_output") || kind == "tool_search_output" {
		return true
	}
	for _, block := range item.Get("content").Array() {
		kind := block.Get("type").String()
		if kind == "tool_result" || strings.HasSuffix(kind, "_tool_result") {
			return true
		}
	}
	return false
}

func promptToolBoundarySafe(items []json.RawMessage, index int) bool {
	if index < len(items) && promptItemHasToolResult(gjson.ParseBytes(items[index])) {
		return false
	}
	outstanding := map[string]bool{}
	for _, raw := range items[:index] {
		item := gjson.ParseBytes(raw)
		kind := item.Get("type").String()
		// Server-side search / MCP calls are complete items, not half of an
		// external call/result pair. Only calls with separate outputs open one.
		if kind == "function_call" || kind == "custom_tool_call" || kind == "computer_call" || kind == "tool_search_call" {
			id := item.Get("call_id").String()
			if id == "" {
				id = item.Get("id").String()
			}
			if id != "" {
				outstanding[id] = true
			}
		}
		if strings.HasSuffix(kind, "_call_output") || kind == "tool_search_output" {
			delete(outstanding, item.Get("call_id").String())
		}
		for _, call := range item.Get("tool_calls").Array() {
			if id := call.Get("id").String(); id != "" {
				outstanding[id] = true
			}
		}
		if item.Get("role").String() == "tool" {
			delete(outstanding, item.Get("tool_call_id").String())
		}
		// Legacy Chat function calls have no IDs but still form an atomic pair.
		if name := item.Get("function_call.name").String(); name != "" {
			outstanding["function:"+name] = true
		}
		if item.Get("role").String() == "function" {
			delete(outstanding, "function:"+item.Get("name").String())
		}
		for _, block := range item.Get("content").Array() {
			kind := block.Get("type").String()
			if kind == "tool_use" || kind == "server_tool_use" {
				if id := block.Get("id").String(); id != "" {
					outstanding[id] = true
				}
			}
			if kind == "tool_result" || strings.HasSuffix(kind, "_tool_result") {
				delete(outstanding, block.Get("tool_use_id").String())
			}
		}
	}
	return len(outstanding) == 0
}

func claudeSystemBoundarySafe(items []json.RawMessage, index int) bool {
	if index == 0 {
		return false
	}
	before := gjson.ParseBytes(items[index-1])
	previousAllowed := before.Get("role").String() == "user"
	if before.Get("role").String() == "assistant" {
		blocks := before.Get("content").Array()
		if len(blocks) > 0 {
			previousAllowed = strings.HasSuffix(blocks[len(blocks)-1].Get("type").String(), "_tool_result")
		}
	}
	return previousAllowed && (index == len(items) || gjson.GetBytes(items[index], "role").String() == "assistant")
}
