package service

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

// ClaudeStreamErrorClassification describes an error carried inside an
// otherwise successful upstream SSE response. StatusCode is its semantic error
// status; it is not the HTTP status of the response carrying the event.
type ClaudeStreamErrorClassification struct {
	StatusCode       int
	ClientStatusCode int
	ClientErrorType  string
	ClientErrorCode  string
	ClientMessage    string
	RequestError     bool
}

// ClassifyClaudeStreamError classifies the original event without using whether
// client output has started. Output safety and signature recovery are separate
// decisions made by the caller.
func ClassifyClaudeStreamError(body []byte) ClaudeStreamErrorClassification {
	errType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "error.type").String()))
	result := ClaudeStreamErrorClassification{
		StatusCode:       http.StatusBadGateway,
		ClientStatusCode: http.StatusBadGateway,
		ClientErrorType:  "upstream_error",
		ClientMessage:    sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body))),
	}
	if code := gjson.GetBytes(body, "error.code"); code.Type == gjson.String {
		result.ClientErrorCode = strings.TrimSpace(code.String())
	}
	switch errType {
	case "invalid_request_error":
		result.StatusCode = http.StatusBadRequest
		result.RequestError = true
	case "authentication_error":
		result.StatusCode = http.StatusUnauthorized
	case "billing_error":
		result.StatusCode = http.StatusPaymentRequired
	case "permission_error":
		result.StatusCode = http.StatusForbidden
	case "not_found_error":
		result.StatusCode = http.StatusNotFound
		result.RequestError = true
	case "conflict_error":
		result.StatusCode = http.StatusConflict
		result.RequestError = true
	case "request_too_large", "request_too_large_error":
		result.StatusCode = http.StatusRequestEntityTooLarge
		result.RequestError = true
	case "rate_limit_error":
		result.StatusCode = http.StatusTooManyRequests
	case "api_error", "service_error":
		result.StatusCode = http.StatusInternalServerError
	case "overloaded_error":
		result.StatusCode = 529
	case "timeout_error":
		result.StatusCode = http.StatusGatewayTimeout
	default:
		errType = "upstream_error"
		if IsClaudeThinkingSignatureError(body) {
			result.StatusCode = http.StatusBadRequest
			result.RequestError = true
			errType = "invalid_request_error"
		}
	}
	result.ClientErrorType = errType
	if result.RequestError || result.StatusCode == http.StatusTooManyRequests {
		result.ClientStatusCode = result.StatusCode
	} else if result.StatusCode == 529 {
		result.ClientStatusCode = http.StatusServiceUnavailable
	}
	if result.ClientMessage == "" {
		result.ClientMessage = "Upstream returned an error in the response stream"
	}
	return result
}

// FailoverError retains the original event for rules and administrator
// diagnostics. Request validation failures must not be replayed on another
// account or counted as account health failures.
func (classification ClaudeStreamErrorClassification) FailoverError(body []byte) *UpstreamFailoverError {
	err := &UpstreamFailoverError{
		StatusCode:       classification.StatusCode,
		ResponseBody:     body,
		ClientStatusCode: classification.ClientStatusCode,
		ClientErrorType:  classification.ClientErrorType,
		ClientErrorCode:  classification.ClientErrorCode,
		ClientMessage:    classification.ClientMessage,
	}
	if classification.RequestError {
		err.NextAccountAction = NextAccountStop
		err.SuppressAccountHealthPenalty = true
	}
	return err
}

// IsClaudeThinkingSignatureError recognizes explicit rejection of a thinking
// signature. Merely mentioning a signature or thinking is not sufficient: such
// messages can describe unrelated provider failures or user content.
func IsClaudeThinkingSignatureError(body []byte) bool {
	message := strings.ToLower(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	if !strings.Contains(message, "signature") ||
		(!strings.Contains(message, "thinking") && !strings.Contains(message, "thought_signature")) {
		return false
	}
	for _, rejection := range []string{
		"invalid", "not valid", "missing", "required", "malformed", "mismatch",
		"does not match", "doesn't match", "cannot verify", "cannot be verified",
		"unable to verify", "verification failed", "failed to verify", "failed to validate",
		"different conversation", "another conversation", "different session", "another session",
	} {
		if strings.Contains(message, rejection) {
			return true
		}
	}
	return false
}
