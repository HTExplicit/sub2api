package service

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassifyOpenAIRequestRejection(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		response   string
		want       string
	}{
		{"missing namespace", 400, `{"error":{"type":"invalid_request_error","param":"input[11].namespace","message":"Missing required parameter"}}`, "request_validation"},
		{"structured validation code", 400, `{"error":{"code":"invalid_request","message":"ordinary validation failure"}}`, "request_validation"},
		{"nested validation type", 400, `{"response":{"error":{"type":"validation_error","message":"invalid"}}}`, "request_validation"},
		{"top level validation envelope", 400, `{"type":"invalid_argument","message":"invalid"}`, "request_validation"},
		{"parameter on upstream error envelope", 400, `{"error":{"type":"upstream_error","param":"input[11].namespace","message":"bad request"}}`, "request_validation"},
		{"generic code-less rejection", 400, `{"error":{"message":"generic compatibility failure"}}`, "unclassified_bad_request"},
		{"unrecognized structured code", 400, `{"error":{"code":"provider_specific_invalid_option","message":"rejected"}}`, "unclassified_bad_request"},
		{"plain text rejection", 400, `Bad request`, "unclassified_bad_request"},
		{"previous response unavailable", 400, `{"error":{"code":"previous_response_not_found"}}`, ""},
		{"encrypted state unavailable", 400, `{"error":{"code":"invalid_encrypted_content"}}`, ""},
		{"context limit", 400, `{"error":{"code":"context_length_exceeded"}}`, ""},
		{"capacity", 400, `{"error":{"code":"server_is_overloaded"}}`, ""},
		{"transient processing", 400, `{"error":{"message":"An error occurred while processing your request"}}`, ""},
		{"safety", 400, `{"error":{"code":"cyber_policy","message":"blocked"}}`, ""},
		{"model missing", 400, `{"error":{"code":"model_not_found"}}`, ""},
		{"model missing without code", 400, `{"error":{"message":"unknown provider for model missing-model"}}`, ""},
		{"model unavailable", 400, `{"error":{"type":"model_not_supported","code":400,"message":"model is not supported"}}`, ""},
		{"workspace access", 400, `{"error":{"code":"deactivated_workspace"}}`, ""},
		{"reported upstream failure", 400, `{"error":{"type":"upstream_error","message":"failed"}}`, ""},
		{"authentication HTTP error", 401, `{"error":{"type":"invalid_request_error"}}`, ""},
		{"upstream gateway error", 502, `{"error":{"message":"generic compatibility failure"}}`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyOpenAIRequestRejection(tt.statusCode, "", []byte(tt.response))
			require.Equal(t, tt.want, got)
		})
	}
}

func TestClassifyOpenAIRequestRejectionPreservesLegacyAuthentication400(t *testing.T) {
	for _, tt := range []struct {
		name     string
		response string
		want     string
	}{
		{"organization_disabled", `{"error":{"type":"invalid_request_error","message":"Your organization has been disabled."}}`, ""},
		{"identity_verification", `{"error":{"message":"Identity verification is required before access."}}`, ""},
		{"echoed_auth_message_is_not_authoritative", `{"error":{"type":"invalid_request_error","message":"Invalid parameter"},"debug":{"message":"organization has been disabled; identity verification is required"}}`, "request_validation"},
		{"auth_phrase_beyond_existing_limit", `{"error":{"type":"invalid_request_error","message":"` + strings.Repeat("x", 512) + ` organization has been disabled; identity verification is required"}}`, "request_validation"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, classifyOpenAIRequestRejection(http.StatusBadRequest, "", []byte(tt.response)))
		})
	}
}

func TestNewOpenAIRequestRejectedErrorIsRequestScoped(t *testing.T) {
	headers := http.Header{"X-Test": []string{"value"}}
	err := NewOpenAIRequestRejectedError(http.StatusBadRequest, headers)
	headers.Set("X-Test", "changed")

	require.Equal(t, GatewayFailureScopeRequest, err.Scope)
	require.Equal(t, openAIRequestRejectedReason, err.Reason)
	require.Equal(t, NextAccountStop, err.NextAccountAction)
	require.True(t, err.SuppressAccountHealthPenalty)
	require.True(t, err.IsOpenAIRequestRejected())
	require.False(t, err.IsOpenAIContinuationStateUnavailable())
	require.False(t, err.RetryableOnSameAccount)
	require.False(t, err.ShouldRetryNextAccount())
	require.False(t, err.ShouldReportAccountScheduleFailure())
	require.Equal(t, http.StatusBadRequest, err.ClientStatusCode)
	require.Equal(t, "invalid_request_error", err.ClientErrorType)
	require.Equal(t, OpenAIRequestRejectedCode, err.ClientErrorCode)
	require.Equal(t, OpenAIRequestRejectedClientMessage, err.ClientMessage)
	require.Empty(t, err.ResponseBody)
	require.Equal(t, "value", err.ResponseHeaders.Get("X-Test"))
}

func TestOpenAIContinuationStateErrorFromFailedEvent(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    bool
	}{
		{
			name:    "nested previous response error",
			payload: `{"type":"response.failed","response":{"error":{"code":"previous_response_not_found","message":"previous response not found"}}}`,
			want:    true,
		},
		{
			name:    "top level invalid encrypted error",
			payload: `{"type":"response.failed","error":{"code":"invalid_encrypted_content","message":"encrypted content could not be verified"}}`,
			want:    true,
		},
		{
			name:    "ordinary server failure",
			payload: `{"type":"response.failed","response":{"error":{"code":"server_error","message":"temporary failure"}}}`,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := openAIContinuationStateErrorFromFailedEvent(
				http.StatusOK,
				http.Header{"X-Test": []string{"failed-event"}},
				[]byte(tt.payload),
			)
			if !tt.want {
				require.Nil(t, err)
				return
			}
			require.NotNil(t, err)
			require.True(t, err.IsOpenAIContinuationStateUnavailable())
			require.False(t, err.ShouldRetryNextAccount())
			require.False(t, err.ShouldReportAccountScheduleFailure())
			require.Equal(t, "failed-event", err.ResponseHeaders.Get("X-Test"))
			require.Equal(t, tt.payload, string(err.ResponseBody))
		})
	}
}
