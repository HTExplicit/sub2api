package service

import (
	"net/http"
	"strings"
)

const openAIResponsesInputTextMaxChars = 10000000

// validateOpenAIResponsesToolOutputs detects a locally provable missing call
// without deleting tool results. Server-side references and compaction may own
// the missing calls, in which case their actual upstream validates the history.
func validateOpenAIResponsesToolOutputs(input []any, hasPreviousResponseID bool) error {
	if len(input) == 0 || hasPreviousResponseID {
		return nil
	}

	toolCallIDs := make(map[string]struct{}, len(input))
	for _, rawItem := range input {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		itemType := strings.TrimSpace(firstNonEmptyString(item["type"]))
		if itemType == "item_reference" {
			if id := strings.TrimSpace(firstNonEmptyString(item["id"])); id != "" {
				return nil
			}
			continue
		}
		if (itemType == "compaction" || itemType == "compaction_summary") && hasNonEmptyOpenAIContinuationCarrier(item["encrypted_content"]) {
			return nil
		}
		if !isCodexToolCallContextItemType(itemType) {
			continue
		}
		if id := strings.TrimSpace(firstNonEmptyString(item["call_id"], item["id"])); id != "" {
			toolCallIDs[id] = struct{}{}
		}
	}

	for _, rawItem := range input {
		item, ok := rawItem.(map[string]any)
		if !ok || !isCodexToolCallOutputItemType(strings.TrimSpace(firstNonEmptyString(item["type"]))) {
			continue
		}

		callID := strings.TrimSpace(firstNonEmptyString(item["call_id"]))
		_, hasToolCall := toolCallIDs[callID]
		if callID != "" && hasToolCall {
			continue
		}
		return NewOpenAIContinuationStateUnavailableError(http.StatusBadRequest, nil, nil)
	}
	return nil
}

func truncateOpenAIResponsesInputText(_ map[string]any) bool {
	// Do not silently rewrite client or tool output. If an upstream enforces a
	// text limit, forwarding the original value preserves its explicit error for
	// the client and the normal Ops error pipeline. This compatibility shim is
	// retained until the two callers can remove the old mutation hook together.
	return false
}

func openAIResponsesInputMayNeedTruncation(_ []byte) bool {
	// See truncateOpenAIResponsesInputText. Returning false also avoids decoding
	// very large bodies solely for a mutation that must not happen.
	return false
}
