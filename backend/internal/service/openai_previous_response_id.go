package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

const (
	OpenAIPreviousResponseIDKindEmpty         = "empty"
	OpenAIPreviousResponseIDKindResponseID    = "response_id"
	OpenAIPreviousResponseIDKindMessageID     = "message_id"
	OpenAIPreviousResponseIDKindUnknown       = "unknown"
	OpenAIContinuationAnchorValidationMessage = "previous_response_id must be a response.id (resp_*)"
	OpenAIContinuationAnchorMaxLength         = 1024
)

var ErrInvalidOpenAIContinuationAnchor = errors.New(OpenAIContinuationAnchorValidationMessage)

var (
	openAIResponseIDPattern = regexp.MustCompile(`^resp_[A-Za-z0-9_-]+$`)
	openAIMessageIDPattern  = regexp.MustCompile(`^(msg|message|item|chatcmpl)_[A-Za-z0-9_-]{1,256}$`)
)

// ClassifyOpenAIPreviousResponseIDKind classifies previous_response_id to improve diagnostics.
func ClassifyOpenAIPreviousResponseIDKind(id string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return OpenAIPreviousResponseIDKindEmpty
	}
	if len(trimmed) <= OpenAIContinuationAnchorMaxLength && openAIResponseIDPattern.MatchString(trimmed) {
		return OpenAIPreviousResponseIDKindResponseID
	}
	if openAIMessageIDPattern.MatchString(strings.ToLower(trimmed)) {
		return OpenAIPreviousResponseIDKindMessageID
	}
	return OpenAIPreviousResponseIDKindUnknown
}

func IsOpenAIPreviousResponseIDLikelyMessageID(id string) bool {
	return ClassifyOpenAIPreviousResponseIDKind(id) == OpenAIPreviousResponseIDKindMessageID
}

// ParseOpenAIContinuationAnchor validates both the JSON type and the response
// ID format. Missing, null, and blank strings are the protocol's no-anchor
// form; every other present value must be a concrete resp_* string.
func ParseOpenAIContinuationAnchor(payload []byte) (string, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return "", err
	}
	raw, exists := envelope["previous_response_id"]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil
	}

	var anchor string
	if err := json.Unmarshal(raw, &anchor); err != nil {
		return "", ErrInvalidOpenAIContinuationAnchor
	}
	anchor = strings.TrimSpace(anchor)
	if anchor == "" {
		return "", nil
	}
	if ClassifyOpenAIPreviousResponseIDKind(anchor) != OpenAIPreviousResponseIDKindResponseID {
		return "", ErrInvalidOpenAIContinuationAnchor
	}
	return anchor, nil
}
