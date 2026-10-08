package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClaudeClassifyStreamError(t *testing.T) {
	tests := []struct {
		name         string
		errorType    string
		message      string
		status       int
		clientStatus int
		clientType   string
		requestError bool
	}{
		{"invalid request", "invalid_request_error", "Invalid request body", 400, 400, "invalid_request_error", true},
		{"authentication", "authentication_error", "Authentication failed", 401, 502, "authentication_error", false},
		{"billing", "billing_error", "Payment required", 402, 502, "billing_error", false},
		{"permission", "permission_error", "Permission denied", 403, 502, "permission_error", false},
		{"not found", "not_found_error", "Model not found", 404, 404, "not_found_error", true},
		{"conflict", "conflict_error", "Request conflict", 409, 409, "conflict_error", true},
		{"too large", "request_too_large", "Request too large", 413, 413, "request_too_large", true},
		{"too large alias", "request_too_large_error", "Request too large", 413, 413, "request_too_large_error", true},
		{"rate limited", "rate_limit_error", "Rate limit exceeded", 429, 429, "rate_limit_error", false},
		{"api error", "api_error", "Internal error", 500, 502, "api_error", false},
		{"third party service", "service_error", "Service temporarily unavailable", 500, 502, "service_error", false},
		{"timeout", "timeout_error", "Request timed out", 504, 502, "timeout_error", false},
		{"overloaded", "overloaded_error", "Overloaded", 529, 503, "overloaded_error", false},
		{"unknown", "provider_specific_error", "Provider failure", 502, 502, "upstream_error", false},
		{"missing type signature", "", "Invalid `signature` in `thinking` block", 400, 400, "invalid_request_error", true},
		{"nil type signature", "<nil>", "Invalid `signature` in `thinking` block", 400, 400, "invalid_request_error", true},
		{"unknown type signature", "provider_specific_error", "Invalid `signature` in `thinking` block", 400, 400, "invalid_request_error", true},
		{"explicit permission beats signature text", "permission_error", "Invalid `signature` in `thinking` block", 403, 502, "permission_error", false},
		{"explicit authentication beats signature text", "authentication_error", "Invalid `signature` in `thinking` block", 401, 502, "authentication_error", false},
		{"explicit service beats signature text", "service_error", "Invalid `signature` in `thinking` block", 500, 502, "service_error", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"type": "error", "error": map[string]string{"type": test.errorType, "message": test.message, "code": "provider_code"}})
			require.NoError(t, err)
			classification := ClassifyClaudeStreamError(body)
			require.Equal(t, test.status, classification.StatusCode)
			require.Equal(t, test.clientStatus, classification.ClientStatusCode)
			require.Equal(t, test.clientType, classification.ClientErrorType)
			require.Equal(t, test.message, classification.ClientMessage)
			require.Equal(t, "provider_code", classification.ClientErrorCode)
			require.Equal(t, test.requestError, classification.RequestError)
			failoverErr := classification.FailoverError(body)
			require.Equal(t, !test.requestError, failoverErr.ShouldRetryNextAccount())
			require.Equal(t, test.requestError, failoverErr.SuppressAccountHealthPenalty)
			require.False(t, failoverErr.RetryableOnSameAccount)
			require.Equal(t, body, failoverErr.ResponseBody)
		})
	}
}

func TestClaudeThinkingSignatureError(t *testing.T) {
	for _, test := range []struct {
		message string
		want    bool
	}{
		{"Invalid `signature` in `thinking` block", true},
		{"messages.2.content.0.thinking.signature: Field required", true},
		{"thinking signature is bound to a different conversation", true},
		{"thought_signature verification failed", true},
		{"Invalid API key signature", false},
		{"thinking signature service is temporarily unavailable", false},
		{"The response uses thinking and a signature", false},
		{"all messages must have non-empty content", false},
	} {
		t.Run(test.message, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"error": map[string]string{"message": test.message}})
			require.NoError(t, err)
			require.Equal(t, test.want, IsClaudeThinkingSignatureError(body))
		})
	}
}

func TestClaudeClassifyStreamError_MalformedBody(t *testing.T) {
	classification := ClassifyClaudeStreamError([]byte("not JSON"))
	require.Equal(t, 502, classification.StatusCode)
	require.Equal(t, "upstream_error", classification.ClientErrorType)
	require.NotEmpty(t, classification.ClientMessage)
	require.False(t, classification.RequestError)
}
