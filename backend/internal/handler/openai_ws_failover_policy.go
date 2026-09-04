package handler

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/tidwall/gjson"
)

func openAIWSPreviousResponseCanMove(payload []byte, previousResponseID string, strictCindy bool) bool {
	if strictCindy {
		classification, err := service.ClassifyCindyContinuation(payload, service.CindyContinuationProof{})
		if err != nil {
			return false
		}
		return strings.TrimSpace(previousResponseID) == "" && classification.Mode == service.CindyContinuationFullReplay
	}

	if strings.TrimSpace(previousResponseID) == "" {
		return true
	}
	if !gjson.ValidBytes(payload) || openAIWSPayloadHasEncryptedState(payload) {
		return false
	}
	coverage := service.AnalyzeToolCallOutputContextCoverageBytes(payload)
	return coverage.HasFunctionCallOutput && coverage.ContextCoversAllCallIDs
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
	if strictCindy {
		classification, err := service.ClassifyCindyContinuation(payload, service.CindyContinuationProof{})
		if err != nil || classification.HasAnchor {
			return false
		}
		return classification.CanSwitchAccount()
	}

	if !gjson.ValidBytes(payload) || strings.TrimSpace(gjson.GetBytes(payload, "previous_response_id").String()) != "" {
		return false
	}
	return !openAIWSPayloadHasEncryptedState(payload)
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
