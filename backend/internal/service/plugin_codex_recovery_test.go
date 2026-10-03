//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecoveryDisableAfterFirstAttemptDoesNotSpendAnotherRequest(t *testing.T) {
	state, _, _ := newReasoningRecoveryTestState(t, context.Background(), reasoningRecoveryFixture)
	state.account.Extra = map[string]any{OpenAIReasoningSignatureRecoveryEnabledExtraKey: false}
	_, retry := state.TryRecover(http.StatusBadRequest, nil, []byte(`{"error":{"code":"invalid_encrypted_content"}}`), false)
	require.False(t, retry)
	require.False(t, state.retryUsed)
	require.Equal(t, "disabled", state.recoveryNotAttemptedReason(nil))
	require.Equal(t, reasoningRecoveryFixture, string(state.wire))
}

func TestSignatureRejectionRemainsRequestScopedWhenPluginIsAbsent(t *testing.T) {
	_, ok := parseOpenAIReasoningRejection([]byte(`{"error":{"code":"thinking_signature_invalid"}}`))
	require.True(t, ok)
	_, ok = parseOpenAIReasoningRejection([]byte(`{"error":{"status":429,"code":"thinking_signature_invalid"}}`))
	require.False(t, ok, "quota/protected failures cannot become recovery markers")
}
