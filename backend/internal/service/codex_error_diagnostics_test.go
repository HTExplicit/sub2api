package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNativeCodexErrorDiagnosticsUseStoredAccountsAndRemovePrivateFields(t *testing.T) {
	read := func(_ context.Context, id int64) (*Account, error) {
		if id == 1 {
			return &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, nil
		}
		if id == 2 {
			return &Account{ID: 2, Platform: PlatformCindy, Type: AccountTypeAPIKey}, nil
		}
		return nil, nil
	}
	ctx := context.Background()

	detail := &OpsErrorLogDetail{UpstreamErrors: `[
		{"account_id":1,"account_name":"private-name","upstream_response_body":"private-body","continuation_diagnostic":{"classification":"thinking_signature_invalid","upstream_error":{"error_code":{"kind":"string","value":"thinking_signature_invalid"},"message":{"kind":"string","value":"private-message"},"hints":["private-hint","call_id"]},"incoming":{"instructions":{"kind":"string","value":"private-instructions"}}}},
		{"account_id":2,"platform":"openai","continuation_diagnostic":{"classification":"invalid_encrypted_content"}},
		{"account_id":0,"continuation_diagnostic":{"classification":"invalid_encrypted_content"}}
	]`}
	projected := ProjectCodexErrorDiagnostics(ctx, detail, read)
	require.Len(t, projected, 1)
	require.EqualValues(t, 1, projected[0].AccountID)
	require.Equal(t, "thinking_signature_invalid", projected[0].Diagnostic.UpstreamError.ErrorCode.Value)
	require.Equal(t, []string{"call_id"}, projected[0].Diagnostic.UpstreamError.Hints)
	raw, err := json.Marshal(projected)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private-")
	require.Empty(t, ProjectCodexErrorDiagnostics(ctx, detail, nil), "without an account reader no facts can be projected")
	require.Empty(t, ProjectCodexErrorDiagnostics(ctx, detail, func(context.Context, int64) (*Account, error) { return nil, nil }), "deleted accounts are not resurrected from event metadata")
}
