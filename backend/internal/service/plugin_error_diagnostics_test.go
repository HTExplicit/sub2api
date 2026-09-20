package service

import (
	"context"
	"encoding/json"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

func TestPluginErrorDiagnosticsUseStoredAccountsAndRemovePrivateFields(t *testing.T) {
	installation := &PluginInstallation{ID: 7, RuntimeGeneration: 2, State: PluginStateEnabled, Bindings: []PluginBinding{{Capability: extensionv1.CapabilityRecovery, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true, RolloutPercent: 100}}}
	manager := NewPluginManager(&pluginTokenRepository{installation: installation}, pluginTokenEncryptor{}, nil, PluginHostInfo{}, nil)
	manager.accountDirectory = &resourceAccountDirectory{accounts: map[int64]extensionv1.Account{
		1: {ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, 2: {ID: 2, Platform: PlatformCindy, Type: AccountTypeAPIKey},
	}}
	ctx := WithPluginExecution(context.Background(), installation)
	detail := &OpsErrorLogDetail{UpstreamErrors: `[
		{"account_id":1,"account_name":"private-name","upstream_response_body":"private-body","continuation_diagnostic":{"classification":"thinking_signature_invalid","upstream_error":{"error_code":{"kind":"string","value":"thinking_signature_invalid"},"message":{"kind":"string","value":"private-message"},"hints":["private-hint","call_id"]},"incoming":{"instructions":{"kind":"string","value":"private-instructions"}}}},
		{"account_id":2,"platform":"openai","continuation_diagnostic":{"classification":"invalid_encrypted_content"}},
		{"account_id":0,"continuation_diagnostic":{"classification":"invalid_encrypted_content"}}
	]`}
	projected := manager.ProjectErrorDiagnostics(ctx, detail)
	require.Len(t, projected, 1)
	require.EqualValues(t, 1, projected[0].AccountID)
	require.Equal(t, "thinking_signature_invalid", projected[0].Diagnostic.UpstreamError.ErrorCode.Value)
	require.Equal(t, []string{"call_id"}, projected[0].Diagnostic.UpstreamError.Hints)
	raw, err := json.Marshal(projected)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private-")
	require.Empty(t, manager.ProjectErrorDiagnostics(context.Background(), detail), "no execution context grants no account data")
	installation.Bindings[0].RolloutPercent = int(stablePluginBucket(1))
	require.Empty(t, manager.ProjectErrorDiagnostics(ctx, detail), "the actual account must also pass rollout")
	installation.Bindings[0].RolloutPercent = 100
	installation.RuntimeGeneration++
	require.Empty(t, manager.ProjectErrorDiagnostics(ctx, detail), "a replaced UI generation cannot read account facts")
}
