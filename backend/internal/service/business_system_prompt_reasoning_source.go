package service

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func prepareBusinessPromptReasoningRequest(c *gin.Context, recovery *openAIReasoningRecoveryState, req *http.Request, clean []byte, proxyURL string) (*http.Request, []byte, []byte, error) {
	wire, err := businessPromptWireRequestBody(req, clean)
	if err != nil {
		return nil, nil, nil, err
	}
	prepared, final, err := recovery.PrepareRequest(req, wire, proxyURL)
	if err != nil {
		return nil, nil, nil, err
	}
	clean, err = projectReasoningCipherEdits(clean, wire, final)
	if err != nil {
		return nil, nil, nil, err
	}
	refreshBusinessPromptSendEvidence(c, clean, final, BusinessSystemPromptProtocolResponses)
	return prepared, clean, final, nil
}

func businessPromptWireRequestBody(req *http.Request, fallback []byte) ([]byte, error) {
	if req.GetBody != nil {
		reader, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		wire, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			return nil, err
		}
		return wire, nil
	}
	return fallback, nil
}

// projectReasoningCipherEdits repeats a recovery edit on the clean request.
// It never removes prompt messages from a wire body. Recovery may only delete
// encrypted_content from reasoning items, whose relative order is unchanged by
// prompt placement. Any other difference is rejected instead of becoming the
// next retry's source.
func projectReasoningCipherEdits(clean, before, after []byte) ([]byte, error) {
	if bytes.Equal(before, after) {
		return clean, nil
	}
	wireItems, cleanItems := openAIReasoningCipherItems(before), openAIReasoningCipherItems(clean)
	if len(wireItems) != len(cleanItems) {
		return nil, fmt.Errorf("%w: reasoning source changed", ErrBusinessSystemPromptUnavailable)
	}
	wireIndices, cleanIndices := []int{}, []int{}
	for index, item := range wireItems {
		if item.hash != cleanItems[index].hash {
			return nil, fmt.Errorf("%w: reasoning source changed", ErrBusinessSystemPromptUnavailable)
		}
		value := gjson.GetBytes(after, fmt.Sprintf("input.%d.encrypted_content", item.index))
		if !value.Exists() {
			wireIndices = append(wireIndices, item.index)
			cleanIndices = append(cleanIndices, cleanItems[index].index)
		}
	}
	expected, err := stripOpenAIReasoningCipherIndices(before, wireIndices)
	if err != nil || !bytes.Equal(expected, after) {
		return nil, fmt.Errorf("%w: unsupported reasoning retry edit", ErrBusinessSystemPromptUnavailable)
	}
	return stripOpenAIReasoningCipherIndices(clean, cleanIndices)
}

// refreshBusinessPromptSendEvidence keeps response-echo and integrity proofs
// aligned when the independent reasoning cache removed ciphertext after the
// request builder. The prompt itself is unchanged and is not reapplied.
func refreshBusinessPromptSendEvidence(c *gin.Context, clean, wire []byte, protocol string) {
	key := businessSystemPromptContextKey(c, businessSystemPromptRequestApplicationKey, protocol)
	value, exists := businessSystemPromptRequestGet(c, key)
	state, ok := value.(businessSystemPromptRequestState)
	if !exists || !ok {
		return
	}
	businessSystemPromptRequestSet(c, key, cacheBusinessSystemPromptState(clean, wire, state.snapshot, state.target, state.application))
}
