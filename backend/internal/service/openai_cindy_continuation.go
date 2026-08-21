package service

import (
	"errors"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type CindyContinuationMode string

const (
	CindyContinuationFullReplay     CindyContinuationMode = "FULL_REPLAY"
	CindyContinuationAnchorDelta    CindyContinuationMode = "ANCHOR_DELTA"
	CindyContinuationAnchorPlusFull CindyContinuationMode = "ANCHOR_PLUS_FULL"
	CindyContinuationReferenceOnly  CindyContinuationMode = "REFERENCE_ONLY"
	CindyContinuationOpaqueFull     CindyContinuationMode = "OPAQUE_FULL"
)

var ErrInvalidCindyContinuationPayload = errors.New("invalid Cindy continuation payload")

// CindyContinuationProof is supplied only by a trusted local accumulator.
// Payload length and shape are never proof of complete history.
type CindyContinuationProof struct {
	VerifiedFullHistory bool
}

type CindyContinuationClassification struct {
	Mode                 CindyContinuationMode
	HasAnchor            bool
	VerifiedFullHistory  bool
	HasOpaqueState       bool
	HasExternalReference bool
	OpaqueBindingIDs     []string
}

func verifiedCindyContinuationHistory(currentPreviousResponseID, baselineResponseID string, baselineExists bool) bool {
	anchor := strings.TrimSpace(currentPreviousResponseID)
	if anchor == "" {
		return true
	}
	return baselineExists && anchor == strings.TrimSpace(baselineResponseID)
}

func (c CindyContinuationClassification) CanReplayWithoutAnchor() bool {
	return c.Mode == CindyContinuationFullReplay ||
		c.Mode == CindyContinuationAnchorPlusFull ||
		c.Mode == CindyContinuationOpaqueFull
}

func (c CindyContinuationClassification) CanSwitchAccount() bool {
	return (c.Mode == CindyContinuationFullReplay || c.Mode == CindyContinuationAnchorPlusFull) &&
		!c.HasOpaqueState
}

func EnsureCindyResponsesStoreFalse(payload []byte) ([]byte, error) {
	if !gjson.ValidBytes(payload) {
		return nil, ErrInvalidCindyContinuationPayload
	}
	if store := gjson.GetBytes(payload, "store"); store.Exists() && store.Type == gjson.False {
		return payload, nil
	}
	updated, err := sjson.SetBytes(payload, "store", false)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func ClassifyCindyContinuation(payload []byte, proof CindyContinuationProof) (CindyContinuationClassification, error) {
	classification := CindyContinuationClassification{}
	if !gjson.ValidBytes(payload) {
		return classification, ErrInvalidCindyContinuationPayload
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
		if bindingIDs := cindyOpaqueBindingIDsFromItem(itemType, item); len(bindingIDs) > 0 {
			classification.HasOpaqueState = true
			classification.OpaqueBindingIDs = append(classification.OpaqueBindingIDs, bindingIDs...)
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
		if isCindyIDOnlyContinuationReference(itemType, item) {
			classification.HasExternalReference = true
		}
	}
	classification.OpaqueBindingIDs = normalizeCindyOpaqueBindingIDs(classification.OpaqueBindingIDs)

	for _, callID := range outputCallIDs {
		if _, ok := concreteCallIDs[callID]; !ok {
			classification.HasExternalReference = true
			break
		}
	}

	if classification.HasExternalReference {
		classification.Mode = CindyContinuationReferenceOnly
		return classification, nil
	}
	if classification.HasAnchor {
		if classification.VerifiedFullHistory {
			classification.Mode = CindyContinuationAnchorPlusFull
		} else {
			classification.Mode = CindyContinuationAnchorDelta
		}
		return classification, nil
	}
	if classification.HasOpaqueState {
		classification.Mode = CindyContinuationOpaqueFull
		return classification, nil
	}
	classification.Mode = CindyContinuationFullReplay
	return classification, nil
}

func isCindyIDOnlyContinuationReference(itemType string, item gjson.Result) bool {
	switch itemType {
	case "reasoning", "compaction", "compaction_summary":
	default:
		return false
	}
	if strings.TrimSpace(item.Get("id").String()) == "" {
		return false
	}
	return !hasNonNullCindyContinuationCarrier(item.Get("encrypted_content")) &&
		!hasNonNullCindyContinuationCarrier(item.Get("signature")) &&
		!hasNonNullCindyContinuationCarrier(item.Get("content")) &&
		!hasNonNullCindyContinuationCarrier(item.Get("summary"))
}

func hasNonNullCindyContinuationCarrier(value gjson.Result) bool {
	if !value.Exists() || value.Type == gjson.Null {
		return false
	}
	switch {
	case value.Type == gjson.String:
		return strings.TrimSpace(value.String()) != ""
	case value.IsArray():
		return len(value.Array()) > 0
	case value.IsObject():
		return len(value.Map()) > 0
	default:
		return strings.TrimSpace(value.Raw) != ""
	}
}
