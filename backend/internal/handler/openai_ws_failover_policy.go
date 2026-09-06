package handler

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/tidwall/gjson"
)

func openAIWSPreviousResponseCanMove(payload []byte, previousResponseID string, strictCindy bool) bool {
	// This initial frame has no trusted local history baseline. Even paired
	// tool items cannot prove that the rest of a referenced response is present.
	if strings.TrimSpace(previousResponseID) != "" {
		return false
	}
	classification, err := service.ClassifyCindyContinuation(payload, service.CindyContinuationProof{})
	return err == nil && !classification.HasAnchor && classification.CanSwitchAccount() &&
		!openAIWSPayloadHasConversationReference(payload)
}

// openAIWSLegacyLaxaReplaySafe applies the same opaque/anchor boundary to the
// temporary OpenAI-platform Laxa projection as to canonical Cindy. A fresh
// full replay is portable; any previous_response_id, encrypted carrier, or
// external reference is credential-bound and must not be replayed on failover.
func openAIWSLegacyLaxaReplaySafe(payload []byte) bool {
	classification, err := service.ClassifyCindyContinuation(payload, service.CindyContinuationProof{})
	return err == nil && classification.CanSwitchAccount()
}

func openAIWSInitialAccountSwitchReplaySafe(payload []byte, previousResponseCanMove bool, strictCindy bool) bool {
	if !previousResponseCanMove {
		return false
	}
	classification, err := service.ClassifyCindyContinuation(payload, service.CindyContinuationProof{})
	return err == nil && !classification.HasAnchor && classification.CanSwitchAccount() &&
		!openAIWSPayloadHasConversationReference(payload)
}

func openAIWSPayloadHasConversationReference(payload []byte) bool {
	conversation := gjson.GetBytes(payload, "conversation")
	return conversation.Exists() && conversation.Type != gjson.Null &&
		(conversation.Type != gjson.String || strings.TrimSpace(conversation.String()) != "")
}
