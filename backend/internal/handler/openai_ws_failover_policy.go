package handler

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/tidwall/gjson"
)

func openAIWSPreviousResponseCanMove(payload []byte, previousResponseID string) bool {
	// This initial frame has no trusted local history baseline. Even paired
	// tool items cannot prove that the rest of a referenced response is present.
	if strings.TrimSpace(previousResponseID) != "" {
		return false
	}
	classification, err := service.ClassifyOpenAIContinuation(payload, service.OpenAIContinuationProof{})
	return err == nil && !classification.HasAnchor && classification.CanSwitchAccount() &&
		!openAIWSPayloadHasConversationReference(payload)
}

func openAIWSInitialAccountSwitchReplaySafe(payload []byte, previousResponseCanMove bool) bool {
	if !previousResponseCanMove {
		return false
	}
	classification, err := service.ClassifyOpenAIContinuation(payload, service.OpenAIContinuationProof{})
	return err == nil && !classification.HasAnchor && classification.CanSwitchAccount() &&
		!openAIWSPayloadHasConversationReference(payload)
}

func openAIWSPayloadHasConversationReference(payload []byte) bool {
	conversation := gjson.GetBytes(payload, "conversation")
	return conversation.Exists() && conversation.Type != gjson.Null &&
		(conversation.Type != gjson.String || strings.TrimSpace(conversation.String()) != "")
}
