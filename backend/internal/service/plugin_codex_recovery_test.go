//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	codexrecovery "github.com/HTExplicit/sub2api-plugins/codexruntime/recovery"
	"github.com/Wei-Shaw/sub2api/internal/config"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

type capturedRecoveryFixture struct {
	promptPolicyFixture
	payloads []string
}

func (f *capturedRecoveryFixture) InvokeOperation(ctx context.Context, platform, kind string, in extensionv1.Invocation) (extensionv1.Result, error) {
	if in.Capability == extensionv1.CapabilityRecovery {
		f.payloads = append(f.payloads, string(in.Payload))
	}
	return f.promptPolicyFixture.InvokeOperation(ctx, platform, kind, in)
}

func TestRecoveryPolicyDoesNotReceiveHistoryCiphertextOrCredentialMaterial(t *testing.T) {
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	fixture := &capturedRecoveryFixture{}
	processExtensionOperations.Store(&extensionOperationProvider{invoker: fixture})
	state, _, _ := newReasoningRecoveryTestState(t, context.Background(), reasoningRecoveryFixture)
	_, retry := state.TryRecover(http.StatusBadRequest, nil, []byte(`{"error":{"code":"invalid_encrypted_content","message":"private-upstream-text"}}`), false)
	require.True(t, retry)
	require.NotEmpty(t, fixture.payloads)
	for _, payload := range fixture.payloads {
		for _, private := range []string{"constraints", "opaque-old", "source-token", "private-upstream-text", "visible summary", "load_orders"} {
			require.NotContains(t, payload, private)
		}
	}
}

func TestRecoveryDisableAfterFirstAttemptDoesNotSpendAnotherRequest(t *testing.T) {
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	state, _, _ := newReasoningRecoveryTestState(t, context.Background(), reasoningRecoveryFixture)
	processExtensionOperations.Store(nil)
	_, retry := state.TryRecover(http.StatusBadRequest, nil, []byte(`{"error":{"code":"invalid_encrypted_content"}}`), false)
	require.False(t, retry)
	require.False(t, state.retryUsed)
	require.Equal(t, "disabled", state.recoveryNotAttemptedReason(nil))
	require.Equal(t, reasoningRecoveryFixture, string(state.wire))
}

func TestRecoverySelectionCannotEscapeActualAccountScopeOrRollout(t *testing.T) {
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	var called []extensionv1.Invocation
	manager := ticketTestManager(t, config.OpenAICodexTicketConfig{}, func(in extensionv1.Invocation) (extensionv1.Result, error) {
		called = append(called, in)
		return codexrecovery.Invoke(context.Background(), in)
	})
	installation := manager.extensions.Load().installations[1]
	installation.Bindings = []PluginBinding{{Capability: extensionv1.CapabilityRecovery, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true, RolloutPercent: 100}}
	installation.Manifest.Operations = map[string][]string{extensionv1.CapabilityRecovery: {"codex.recovery.enabled", "codex.recovery.rejection", "codex.recovery.select"}}
	processExtensionOperations.Store(&extensionOperationProvider{invoker: manager})
	account := &Account{ID: 37, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	ctx := withCodexRecoveryScope(context.Background(), account)
	_, _, err := readOpenAIRecoveryRejection(ctx, []byte(`{"error":{"code":"invalid_encrypted_content"}}`))
	require.ErrorIs(t, err, ErrExtensionOperationDisabled)
	require.Empty(t, called)
	account.Type = AccountTypeOAuth
	ctx = withCodexRecoveryScope(context.Background(), account)
	_, ok, err := readOpenAIRecoveryRejection(ctx, []byte(`{"error":{"code":"invalid_encrypted_content"}}`))
	require.NoError(t, err)
	require.True(t, ok)
	_, err = selectOpenAIRecoveryIndices(ctx, []byte(reasoningRecoveryFixture), "")
	require.NoError(t, err)
	for _, in := range called {
		require.EqualValues(t, 37, in.AccountID)
	}
	installation.Bindings[0].RolloutPercent = int(stablePluginBucket(37))
	_, err = selectOpenAIRecoveryIndices(ctx, []byte(reasoningRecoveryFixture), "")
	require.ErrorIs(t, err, ErrExtensionOperationDisabled)
}

func TestSignatureRejectionRemainsRequestScopedWhenPluginIsAbsent(t *testing.T) {
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	processExtensionOperations.Store(nil)
	_, ok := parseOpenAIReasoningRejection([]byte(`{"error":{"code":"thinking_signature_invalid"}}`))
	require.True(t, ok)
	_, ok = parseOpenAIReasoningRejection([]byte(`{"error":{"status":429,"code":"thinking_signature_invalid"}}`))
	require.False(t, ok, "quota/protected failures cannot become recovery markers")
}
