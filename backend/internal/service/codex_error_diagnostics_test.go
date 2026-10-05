package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Every stored attempt diagnostic is projected, whatever its account or
// platform, with its own attempt context and the bounded original values.
func TestCodexErrorDiagnosticsProjectEveryStoredAttempt(t *testing.T) {
	detail := &OpsErrorLogDetail{UpstreamErrors: `[
		{"account_id":1,"account_name":"primary","platform":"openai","kind":"request_rejected","upstream_status_code":400,"upstream_request_id":"req_1","message":"Missing required parameter","continuation_diagnostic":{"classification":"thinking_signature_invalid","upstream_error":{"error_code":{"kind":"string","value":"thinking_signature_invalid"},"message":{"kind":"string","value":"signature invalid for input[3]"},"hints":["unlisted-hint","call_id"]},"incoming":{"instructions":{"kind":"string","value":"You are Codex"}}}},
		{"account_id":2,"platform":"anthropic","continuation_diagnostic":{"classification":"invalid_encrypted_content"}},
		{"account_id":3,"message":"no diagnostic"},
		{"account_id":0,"continuation_diagnostic":{"classification":"previous_response_not_found"}}
	]`}
	projected := ProjectCodexErrorDiagnostics(detail)
	require.Len(t, projected, 3)
	first := projected[0]
	require.EqualValues(t, 1, first.AccountID)
	require.Equal(t, "primary", first.AccountName)
	require.Equal(t, 1, first.Attempt)
	require.Equal(t, "request_rejected", first.Kind)
	require.Equal(t, 400, first.UpstreamStatusCode)
	require.Equal(t, "req_1", first.UpstreamRequestID)
	require.Equal(t, "Missing required parameter", first.Message)
	require.Equal(t, "thinking_signature_invalid", first.Diagnostic.UpstreamError.ErrorCode.Value)
	require.Equal(t, "signature invalid for input[3]", first.Diagnostic.UpstreamError.Message.Value)
	require.Equal(t, "You are Codex", first.Diagnostic.Incoming.Instructions.Value)
	require.Equal(t, []string{"call_id"}, first.Diagnostic.UpstreamError.Hints, "hints stay the fixed derived labels")
	require.Equal(t, 2, projected[1].Attempt)
	require.Equal(t, 4, projected[2].Attempt)
	require.Empty(t, ProjectCodexErrorDiagnostics(nil))
}
