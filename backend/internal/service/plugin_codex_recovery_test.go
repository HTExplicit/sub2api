//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	codexrecovery "github.com/Wei-Shaw/sub2api/internal/codexruntime/recovery"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
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
	previous := invokeNativeCodex
	t.Cleanup(func() { invokeNativeCodex = previous })
	fixture := &capturedRecoveryFixture{}
	invokeNativeCodex = fixture.InvokeOperation
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
	previous := invokeNativeCodex
	t.Cleanup(func() { invokeNativeCodex = previous })
	state, _, _ := newReasoningRecoveryTestState(t, context.Background(), reasoningRecoveryFixture)
	invokeNativeCodex = func(context.Context, string, string, extensionv1.Invocation) (extensionv1.Result, error) {
		return extensionv1.Result{}, ErrNativeCodexPolicyDisabled
	}
	_, retry := state.TryRecover(http.StatusBadRequest, nil, []byte(`{"error":{"code":"invalid_encrypted_content"}}`), false)
	require.False(t, retry)
	require.False(t, state.retryUsed)
	require.Equal(t, "disabled", state.recoveryNotAttemptedReason(nil))
	require.Equal(t, reasoningRecoveryFixture, string(state.wire))
}

func TestRecoverySelectionCannotEscapeActualAccountScopeOrRollout(t *testing.T) {
	previous := invokeNativeCodex
	t.Cleanup(func() { invokeNativeCodex = previous })
	var called []extensionv1.Invocation
	enabled := true
	invokeNativeCodex = func(ctx context.Context, platform, kind string, in extensionv1.Invocation) (extensionv1.Result, error) {
		if platform != PlatformOpenAI || kind != AccountTypeOAuth || !enabled {
			return extensionv1.Result{}, ErrNativeCodexPolicyDisabled
		}
		called = append(called, in)
		return codexrecovery.Invoke(ctx, in)
	}

	account := &Account{ID: 37, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	ctx := withCodexRecoveryScope(context.Background(), account)
	_, _, err := readOpenAIRecoveryRejection(ctx, []byte(`{"error":{"code":"invalid_encrypted_content"}}`))
	require.ErrorIs(t, err, ErrNativeCodexPolicyDisabled)
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
	enabled = false
	_, err = selectOpenAIRecoveryIndices(ctx, []byte(reasoningRecoveryFixture), "")
	require.ErrorIs(t, err, ErrNativeCodexPolicyDisabled)
}

func TestSignatureRejectionRemainsRequestScopedWhenPluginIsAbsent(t *testing.T) {
	previous := invokeNativeCodex
	t.Cleanup(func() { invokeNativeCodex = previous })
	invokeNativeCodex = func(context.Context, string, string, extensionv1.Invocation) (extensionv1.Result, error) {
		return extensionv1.Result{}, ErrNativeCodexPolicyDisabled
	}
	_, ok := parseOpenAIReasoningRejection([]byte(`{"error":{"code":"thinking_signature_invalid"}}`))
	require.True(t, ok)
	_, ok = parseOpenAIReasoningRejection([]byte(`{"error":{"status":429,"code":"thinking_signature_invalid"}}`))
	require.False(t, ok, "quota/protected failures cannot become recovery markers")
}
