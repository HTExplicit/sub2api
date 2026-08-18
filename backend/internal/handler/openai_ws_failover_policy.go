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
	if !input.IsArray() {
		return false
	}
	for _, item := range input.Array() {
		if strings.TrimSpace(item.Get("encrypted_content").String()) != "" {
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

func resetStrictCindyCrossGroupContinuation(
	strictCindy bool,
	payload []byte,
	previousResponseID string,
	currentGroupAccountID int64,
) ([]byte, string, bool) {
	previousResponseID = strings.TrimSpace(previousResponseID)
	if !strictCindy || previousResponseID == "" || currentGroupAccountID > 0 ||
		!gjson.ValidBytes(payload) || openAIWSPayloadHasEncryptedState(payload) {
		return payload, previousResponseID, false
	}

	validation := service.ValidateFunctionCallOutputContextBytes(payload)
	if validation.HasFunctionCallOutput && !openAIWSPreviousResponseCanMove(payload, previousResponseID) {
		return payload, previousResponseID, false
	}

	updated := service.RemovePreviousResponseIDFromBody(payload)
	if gjson.GetBytes(updated, "previous_response_id").Exists() {
		return payload, previousResponseID, false
	}
	return updated, "", true
}
