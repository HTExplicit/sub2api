package handler

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/tidwall/gjson"
)

func openAIWSPreviousResponseCanMove(payload []byte, previousResponseID string) bool {
	if strings.TrimSpace(previousResponseID) == "" {
		return true
	}
	if !gjson.ValidBytes(payload) || openAIWSPayloadHasEncryptedState(payload) {
		return false
	}
	coverage := service.AnalyzeToolCallOutputContextCoverageBytes(payload)
	return coverage.HasFunctionCallOutput && coverage.ContextCoversAllCallIDs
}

func openAIWSPayloadHasEncryptedState(payload []byte) bool {
	input := gjson.GetBytes(payload, "input")
	items := input.Array()
	if input.IsObject() {
		items = []gjson.Result{input}
	}
	for _, item := range items {
		if item.Get("encrypted_content").Exists() {
			return true
		}
	}
	return false
}

func openAIWSInitialAccountSwitchReplaySafe(payload []byte, previousResponseCanMove bool) bool {
	if !previousResponseCanMove || !gjson.ValidBytes(payload) {
		return false
	}
	if strings.TrimSpace(gjson.GetBytes(payload, "previous_response_id").String()) != "" {
		return false
	}
	return !openAIWSPayloadHasEncryptedState(payload)
}
