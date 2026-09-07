package service

import (
	"net/http"
	"strings"
)

const openAIResponsesInputTextMaxChars = 10000000

// sanitizeOpenAIResponsesOrphanToolOutputs removes tool-output items that have
// no matching call or item reference anywhere in the current input. Named
// function outputs without a call ID are standalone inputs, not orphan results.
func sanitizeOpenAIResponsesOrphanToolOutputs(reqBody map[string]any, input []any, hasPreviousResponseID bool) bool {
	keep := openAIResponsesOrphanToolOutputKeepMask(input, hasPreviousResponseID)
	if keep == nil {
		return false
	}
	normalized := make([]any, 0, len(input))
	for index, rawItem := range input {
		if keep[index] {
			normalized = append(normalized, rawItem)
		}
	}
	reqBody["input"] = normalized
	return true
}

// openAIResponsesOrphanToolOutputKeepMask is shared by decoded request adapters
// and raw WS passthrough. A nil mask means no input item needs to be removed.
func openAIResponsesOrphanToolOutputKeepMask(input []any, hasPreviousResponseID bool) []bool {
	if len(input) == 0 || hasPreviousResponseID {
		return nil
	}

	toolCallIDs := make(map[string]struct{}, len(input))
	referenceIDs := make(map[string]struct{}, len(input))
	for _, rawItem := range input {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		itemType := strings.TrimSpace(firstNonEmptyString(item["type"]))
		if itemType == "item_reference" {
			if id := strings.TrimSpace(firstNonEmptyString(item["id"])); id != "" {
				referenceIDs[id] = struct{}{}
			}
			continue
		}
		if !isCodexToolCallContextItemType(itemType) {
			continue
		}
		if id := strings.TrimSpace(firstNonEmptyString(item["call_id"], item["id"])); id != "" {
			toolCallIDs[id] = struct{}{}
		}
	}

	modified := false
	keep := make([]bool, len(input))
	for index, rawItem := range input {
		item, ok := rawItem.(map[string]any)
		if !ok || !isCodexToolCallOutputItemType(strings.TrimSpace(firstNonEmptyString(item["type"]))) {
			keep[index] = true
			continue
		}

		callID := strings.TrimSpace(firstNonEmptyString(item["call_id"]))
		// A named function output without a call ID is an externally supplied
		// standalone input, such as a delegation envelope, not an orphan result.
		if isOpenAINamedStandaloneFunctionOutput(item) {
			keep[index] = true
			continue
		}
		_, hasToolCall := toolCallIDs[callID]
		_, hasReference := referenceIDs[callID]
		if callID != "" && (hasToolCall || hasReference) {
			keep[index] = true
			continue
		}
		modified = true
	}
	if !modified {
		return nil
	}
	return keep
}

func isOpenAINamedStandaloneFunctionOutput(item map[string]any) bool {
	return strings.TrimSpace(firstNonEmptyString(item["call_id"])) == "" &&
		strings.TrimSpace(firstNonEmptyString(item["type"])) == "function_call_output" &&
		strings.TrimSpace(firstNonEmptyString(item["name"])) != ""
}

// validateOpenAIResponsesToolOutputs is only a read-only safety check for the
// narrowly scoped reasoning-signature recovery. It must not replace the
// official orphan cleanup at normal OAuth ingress. Server-side references and
// compaction may own calls that this recovery check cannot prove are missing.
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
		if isOpenAINamedStandaloneFunctionOutput(item) {
			continue
		}
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
