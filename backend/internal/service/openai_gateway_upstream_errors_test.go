package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsOpenAIOpaqueContinuationToolChainBadRequest(t *testing.T) {
	requestWithEncryptedToolContinuation := []byte(`{"input":[{"type":"reasoning","encrypted_content":"test-carrier"},{"type":"function_call","call_id":"call_1","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"{}"}]}`)
	requestWithoutEncryptedCarrier := []byte(`{"input":[{"type":"function_call","call_id":"call_1","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"{}"}]}`)
	requestWithoutToolOutput := []byte(`{"input":[{"type":"reasoning","encrypted_content":"test-carrier"}]}`)
	opaqueBody := []byte(`{"error":{"message":"generic compatibility failure"}}`)

	tests := []struct {
		name       string
		statusCode int
		request    []byte
		response   []byte
		want       bool
	}{
		{
			name:       "opaque encrypted tool continuation",
			statusCode: http.StatusBadRequest,
			request:    requestWithEncryptedToolContinuation,
			response:   opaqueBody,
			want:       true,
		},
		{
			name:       "structured non-continuation code stays ordinary",
			statusCode: http.StatusBadRequest,
			request:    requestWithEncryptedToolContinuation,
			response:   []byte(`{"error":{"code":"invalid_request","message":"ordinary validation failure"}}`),
			want:       false,
		},
		{
			name:       "no encrypted continuation carrier",
			statusCode: http.StatusBadRequest,
			request:    requestWithoutEncryptedCarrier,
			response:   opaqueBody,
			want:       false,
		},
		{
			name:       "no tool output can remain eligible for safe recovery",
			statusCode: http.StatusBadRequest,
			request:    requestWithoutToolOutput,
			response:   opaqueBody,
			want:       false,
		},
		{
			name:       "other status is not an opaque 400 wrapper",
			statusCode: http.StatusBadGateway,
			request:    requestWithEncryptedToolContinuation,
			response:   opaqueBody,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isOpenAIOpaqueContinuationToolChainBadRequest(tt.statusCode, tt.request, "", tt.response)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestNewOpenAIOpaqueStreamPreflightErrorIsRequestScoped(t *testing.T) {
	err := newOpenAIOpaqueStreamPreflightError(
		http.StatusBadRequest,
		http.Header{"X-Test": []string{"value"}},
		[]byte(`{"error":{"message":"generic compatibility failure"}}`),
	)

	require.Equal(t, GatewayFailureScopeRequest, err.Scope)
	require.Equal(t, openAIOpaqueStreamPreflightReason, err.Reason)
	require.Equal(t, NextAccountStop, err.NextAccountAction)
	require.True(t, err.SuppressAccountHealthPenalty)
	require.True(t, err.IsOpenAIOpaqueStreamPreflight())
	require.False(t, err.ShouldRetryNextAccount())
	require.False(t, err.ShouldReportAccountScheduleFailure())
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
