package service

import (
	"encoding/json"
	"strings"
)

type openAIResponsesRewriteEnvelope struct {
	Status string                             `json:"status,omitempty"`
	Output []openAIResponsesRewriteOutputItem `json:"output"`
}

type openAIResponsesRewriteOutputItem struct {
	ID      string                              `json:"id,omitempty"`
	Type    string                              `json:"type"`
	Role    string                              `json:"role,omitempty"`
	Status  string                              `json:"status,omitempty"`
	Content []openAIResponsesRewriteContentItem `json:"content,omitempty"`
}

type openAIResponsesRewriteContentItem struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	Refusal string `json:"refusal,omitempty"`
}

func RewriteOpenAIResponsesJSON(body []byte, matcher *OpenAIRefusalMatcher) ([]byte, bool, string, error) {
	var envelope openAIResponsesRewriteEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, false, "", err
	}
	if envelope.Status != "completed" || len(envelope.Output) == 0 {
		return body, false, "", nil
	}

	var visibleText strings.Builder
	firstMessageID := ""
	for _, output := range envelope.Output {
		if output.Type == "reasoning" {
			continue
		}
		if output.Type != "message" || output.Role != "assistant" || len(output.Content) == 0 {
			return body, false, "", nil
		}
		if firstMessageID == "" {
			firstMessageID = output.ID
		}
		for _, content := range output.Content {
			switch content.Type {
			case "output_text":
				_, _ = visibleText.WriteString(content.Text)
			case "refusal":
				_, _ = visibleText.WriteString(content.Refusal)
			default:
				return body, false, "", nil
			}
		}
	}
	if firstMessageID == "" {
		return body, false, "", nil
	}

	matched, keyword := matcher.MatchLeadingParagraphs(visibleText.String())
	if !matched {
		return body, false, "", nil
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, false, "", err
	}
	replacementOutput := []openAIResponsesRewriteOutputItem{{
		ID:     firstMessageID,
		Type:   "message",
		Role:   "assistant",
		Status: "completed",
		Content: []openAIResponsesRewriteContentItem{{
			Type: "output_text",
			Text: matcher.Replacement(),
		}},
	}}
	encodedOutput, err := json.Marshal(replacementOutput)
	if err != nil {
		return nil, false, "", err
	}
	raw["output"] = encodedOutput
	raw["status"] = json.RawMessage(`"completed"`)
	rewritten, err := json.Marshal(raw)
	if err != nil {
		return nil, false, "", err
	}
	return rewritten, true, keyword, nil
}
