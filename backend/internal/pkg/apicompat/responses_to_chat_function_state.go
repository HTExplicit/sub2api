package apicompat

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"hash"
)

// The client may have already received a prefix of the function arguments.
// Keep only its length and digest: an authoritative snapshot may append a
// missing suffix, but cannot rewrite emitted bytes or require an unbounded copy.
type responsesChatFunctionState struct {
	chatIndex            int
	itemID, callID, name string
	argumentBytes        int
	argumentHash         hash.Hash
	typeSent, completed  bool
	argumentsDone        bool // a complete argument-done snapshot was observed
}

func responseChatFunctionError(state *ResponsesEventToChatState) {
	state.ProtocolError = "Upstream Responses function-call stream is incomplete or inconsistent"
}

func resToChatFunctionItem(item *ResponsesOutput, outputIndex int, complete bool, state *ResponsesEventToChatState) []ChatCompletionsChunk {
	if item == nil || item.Type != "function_call" || outputIndex < 0 || state.ProtocolError != "" {
		return nil
	}
	if state.functionCalls == nil {
		state.functionCalls = make(map[int]*responsesChatFunctionState)
	}
	if state.OutputIndexToToolIndex == nil {
		state.OutputIndexToToolIndex = make(map[int]int)
	}
	tool := state.functionCalls[outputIndex]
	if tool == nil {
		if _, occupied := state.OutputIndexToToolIndex[outputIndex]; occupied {
			responseChatFunctionError(state)
			return nil
		}
		tool = &responsesChatFunctionState{chatIndex: state.NextToolCallIndex, argumentHash: sha256.New()}
		state.NextToolCallIndex++
		state.functionCalls[outputIndex] = tool
		state.OutputIndexToToolIndex[outputIndex] = tool.chatIndex
	}
	if (tool.itemID != "" && item.ID != "" && tool.itemID != item.ID) ||
		(tool.callID != "" && item.CallID != "" && tool.callID != item.CallID) ||
		(tool.name != "" && item.Name != "" && tool.name != item.Name) {
		responseChatFunctionError(state)
		return nil
	}
	if item.CallID != "" {
		for index, other := range state.functionCalls {
			if index != outputIndex && other.callID == item.CallID {
				responseChatFunctionError(state)
				return nil
			}
		}
	}
	tool.itemID = firstNonemptyToolIdentity(tool.itemID, item.ID)
	delta := ChatToolCall{Index: &tool.chatIndex}
	if !tool.typeSent {
		delta.Type = "function"
		tool.typeSent = true
	}
	if tool.callID == "" && item.CallID != "" {
		tool.callID, delta.ID = item.CallID, item.CallID
	}
	if tool.name == "" && item.Name != "" {
		tool.name, delta.Function.Name = item.Name, item.Name
	}
	hasCompleteArguments := json.Valid([]byte(item.Arguments)) || (item.Arguments == "" && tool.argumentsDone)
	if complete && (tool.callID == "" || tool.name == "" || !hasCompleteArguments ||
		(item.Status != "" && item.Status != "completed")) {
		responseChatFunctionError(state)
		return nil
	}
	if item.Arguments != "" || (complete && !tool.argumentsDone) {
		var ok bool
		delta.Function.Arguments, ok = resToChatArgumentsSnapshotSuffix(item.Arguments, tool, state)
		if !ok {
			return nil
		}
	}
	tool.completed = tool.completed || complete
	state.SawToolCall = true
	if delta.Type == "" && delta.ID == "" && delta.Function.Name == "" && delta.Function.Arguments == "" {
		return nil
	}
	chatDelta := ChatDelta{ToolCalls: []ChatToolCall{delta}}
	if !state.SentRole {
		chatDelta.Role = "assistant"
		state.SentRole = true
	}
	return []ChatCompletionsChunk{makeChatDeltaChunk(state, chatDelta)}
}

func resToChatArgumentsSnapshotSuffix(arguments string, tool *responsesChatFunctionState, state *ResponsesEventToChatState) (string, bool) {
	if tool.argumentBytes > len(arguments) || ((tool.completed || tool.argumentsDone) && tool.argumentBytes != len(arguments)) {
		responseChatFunctionError(state)
		return "", false
	}
	prefix := sha256.Sum256([]byte(arguments[:tool.argumentBytes]))
	if !bytes.Equal(prefix[:], tool.argumentHash.Sum(nil)) {
		responseChatFunctionError(state)
		return "", false
	}
	suffix := arguments[tool.argumentBytes:]
	_, _ = tool.argumentHash.Write([]byte(suffix))
	tool.argumentBytes = len(arguments)
	return suffix, true
}

func firstNonemptyToolIdentity(existing, incoming string) string {
	if existing != "" {
		return existing
	}
	return incoming
}

func resToChatCompleteFunctions(response *ResponsesResponse, state *ResponsesEventToChatState) []ChatCompletionsChunk {
	if response == nil || response.Status != "completed" {
		return nil
	}
	if !state.SentRole && !state.SawToolCall && !state.SawText && response.ID != "" {
		state.ID = response.ID
	}
	var chunks []ChatCompletionsChunk
	seen := make(map[int]bool)
	for position := range response.Output {
		item := &response.Output[position]
		if item.Type != "function_call" {
			continue
		}
		index := position
		// The final output array need not repeat a sparse source output_index.
		// Use actual call/item identity, never a guessed tool name or call ID.
		for candidate, tool := range state.functionCalls {
			if (item.CallID != "" && tool.callID == item.CallID) || (item.ID != "" && tool.itemID == item.ID) {
				index = candidate
				break
			}
		}
		chunks = append(chunks, resToChatFunctionItem(item, index, true, state)...)
		seen[index] = true
		if state.ProtocolError != "" {
			return nil
		}
	}
	if len(response.Output) > 0 {
		for index := range state.functionCalls {
			if !seen[index] {
				responseChatFunctionError(state)
				return nil
			}
		}
	}
	return chunks
}
