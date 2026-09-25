package service

import (
	"regexp"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var promptRetryInputIndex = regexp.MustCompile(`input\[(\d+)\]`)

// A provider's error indices describe the sent array. Translate those indices
// to the preserved customer array before running an existing bounded repair.
// A rejection of an inserted prompt is never repaired by editing a customer
// message at the same numeric index.
func normalizeBusinessPromptRejectedFieldRetryBody(c *gin.Context, status int, clean, response []byte) ([]byte, string, bool, error) {
	value, exists := businessSystemPromptRequestGet(c, businessSystemPromptContextKey(c, businessSystemPromptRequestApplicationKey, BusinessSystemPromptProtocolResponses))
	state, ok := value.(businessSystemPromptRequestState)
	if !exists || !ok || !state.application.Applied {
		return normalizeOpenAIResponsesRejectedFieldRetryBody(status, clean, response)
	}
	var indices []int
	for _, proof := range state.rulesUndo {
		if proof.field == "input" {
			if !proof.valid {
				return nil, "", false, ErrBusinessSystemPromptUnavailable
			}
			indices = proof.indices
		}
	}
	if len(indices) == 0 {
		return normalizeOpenAIResponsesRejectedFieldRetryBody(status, clean, response)
	}
	out := response
	owned := false
	for _, field := range []string{"error.param", "error.message", "param", "message"} {
		text := gjson.GetBytes(out, field)
		if text.Type != gjson.String {
			continue
		}
		mapped := promptRetryInputIndex.ReplaceAllStringFunc(text.String(), func(match string) string {
			parts := promptRetryInputIndex.FindStringSubmatch(match)
			index, err := strconv.Atoi(parts[1])
			if err != nil {
				owned = true
				return match
			}
			before := 0
			for _, insertion := range indices {
				if insertion == index {
					owned = true
					return match
				}
				if insertion < index {
					before++
				}
			}
			return "input[" + strconv.Itoa(index-before) + "]"
		})
		if owned {
			return nil, "", false, nil
		}
		var err error
		out, err = sjson.SetBytes(out, field, mapped)
		if err != nil {
			return nil, "", false, err
		}
	}
	return normalizeOpenAIResponsesRejectedFieldRetryBody(status, clean, out)
}
