//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

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
