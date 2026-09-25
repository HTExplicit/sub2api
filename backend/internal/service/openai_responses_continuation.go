package service

import (
	"errors"
	"strings"

	"github.com/tidwall/gjson"
)

// OpenAIContinuationMode classifies how a Responses payload continues a
// conversation. WebSocket replay and account-switch decisions depend on it:
// only a complete, locally verified history is portable.
type OpenAIContinuationMode string

const (
	OpenAIContinuationFullReplay     OpenAIContinuationMode = "FULL_REPLAY"
	OpenAIContinuationAnchorDelta    OpenAIContinuationMode = "ANCHOR_DELTA"
	OpenAIContinuationAnchorPlusFull OpenAIContinuationMode = "ANCHOR_PLUS_FULL"
	OpenAIContinuationReferenceOnly  OpenAIContinuationMode = "REFERENCE_ONLY"
	OpenAIContinuationOpaqueFull     OpenAIContinuationMode = "OPAQUE_FULL"
)

var ErrInvalidOpenAIContinuationPayload = errors.New("invalid continuation payload")

// OpenAIContinuationProof is supplied only by a trusted local accumulator.
// Payload length and shape are never proof of complete history.
type OpenAIContinuationProof struct {
	VerifiedFullHistory bool
}

type OpenAIContinuationClassification struct {
	Mode                 OpenAIContinuationMode
	HasAnchor            bool
	VerifiedFullHistory  bool
	HasOpaqueState       bool
	HasExternalReference bool
}

func (c OpenAIContinuationClassification) CanReplayWithoutAnchor() bool {
	return c.Mode == OpenAIContinuationFullReplay ||
		c.Mode == OpenAIContinuationAnchorPlusFull ||
		c.Mode == OpenAIContinuationOpaqueFull
}

func (c OpenAIContinuationClassification) CanSwitchAccount() bool {
	return (c.Mode == OpenAIContinuationFullReplay || c.Mode == OpenAIContinuationAnchorPlusFull) &&
		!c.HasOpaqueState
}

// isOpenAIRemovableEncryptedPortableContinuationState is shared by recovery
// eligibility and deletion so both agree on type and non-empty carrier semantics.
func isOpenAIRemovableEncryptedPortableContinuationState(itemType string, encryptedContent any) bool {
	switch strings.TrimSpace(itemType) {
	case "reasoning", "compaction", "compaction_summary":
		return hasNonEmptyOpenAIContinuationCarrier(encryptedContent)
	default:
		return false
	}
}

func hasNonEmptyOpenAIContinuationCarrier(value any) bool {
	switch carrier := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(carrier) != ""
	case []any:
		return len(carrier) > 0
	case map[string]any:
		return len(carrier) > 0
	default:
		return true
	}
}

func ClassifyOpenAIContinuation(payload []byte, proof OpenAIContinuationProof) (OpenAIContinuationClassification, error) {
	classification := OpenAIContinuationClassification{}
	if !gjson.ValidBytes(payload) {
		return classification, ErrInvalidOpenAIContinuationPayload
	}

	previousResponseID, err := ParseOpenAIContinuationAnchor(payload)
	if err != nil {
		return classification, err
	}
	classification.HasAnchor = previousResponseID != ""
	classification.VerifiedFullHistory = !classification.HasAnchor || proof.VerifiedFullHistory

	input := gjson.GetBytes(payload, "input")
	items := input.Array()
	if input.IsObject() {
		items = []gjson.Result{input}
	}

	concreteCallIDs := make(map[string]struct{})
	outputCallIDs := make([]string, 0)
	for _, item := range items {
		if !item.IsObject() {
			continue
		}
		itemType := strings.TrimSpace(item.Get("type").String())
		if openAIContinuationItemHasOpaqueState(itemType, item) {
			classification.HasOpaqueState = true
		}
		if itemType == "item_reference" {
			classification.HasExternalReference = true
			continue
		}
		if isCodexToolCallContextItemType(itemType) {
			if callID := strings.TrimSpace(item.Get("call_id").String()); callID != "" {
				concreteCallIDs[callID] = struct{}{}
			}
			continue
		}
		if isCodexToolCallOutputItemType(itemType) {
			callID := strings.TrimSpace(item.Get("call_id").String())
			if callID == "" {
				classification.HasExternalReference = true
				continue
			}
			outputCallIDs = append(outputCallIDs, callID)
			continue
		}
		if isOpenAIIDOnlyContinuationReference(itemType, item) {
			classification.HasExternalReference = true
		}
	}

	for _, callID := range outputCallIDs {
		if _, ok := concreteCallIDs[callID]; !ok {
			classification.HasExternalReference = true
			break
		}
	}

	if classification.HasExternalReference {
		classification.Mode = OpenAIContinuationReferenceOnly
		return classification, nil
	}
	if classification.HasAnchor {
		if classification.VerifiedFullHistory {
			classification.Mode = OpenAIContinuationAnchorPlusFull
		} else {
			classification.Mode = OpenAIContinuationAnchorDelta
		}
		return classification, nil
	}
	if classification.HasOpaqueState {
		classification.Mode = OpenAIContinuationOpaqueFull
		return classification, nil
	}
	classification.Mode = OpenAIContinuationFullReplay
	return classification, nil
}

// openAIContinuationItemHasOpaqueState reports provider-bound encrypted state
// (encrypted_content, or a reasoning/compaction signature).
func openAIContinuationItemHasOpaqueState(itemType string, item gjson.Result) bool {
	if hasNonNullOpenAIContinuationCarrier(item.Get("encrypted_content")) {
		return true
	}
	switch itemType {
	case "reasoning", "compaction", "compaction_summary":
		return hasNonNullOpenAIContinuationCarrier(item.Get("signature"))
	}
	return false
}

func isOpenAIIDOnlyContinuationReference(itemType string, item gjson.Result) bool {
	switch itemType {
	case "reasoning", "compaction", "compaction_summary":
	default:
		return false
	}
	if strings.TrimSpace(item.Get("id").String()) == "" {
		return false
	}
	return !hasNonNullOpenAIContinuationCarrier(item.Get("encrypted_content")) &&
		!hasNonNullOpenAIContinuationCarrier(item.Get("signature")) &&
		!hasNonNullOpenAIContinuationCarrier(item.Get("content")) &&
		!hasNonNullOpenAIContinuationCarrier(item.Get("summary"))
}

func hasNonNullOpenAIContinuationCarrier(value gjson.Result) bool {
	if !value.Exists() || value.Type == gjson.Null {
		return false
	}
	return hasNonEmptyOpenAIContinuationCarrier(value.Value())
}
