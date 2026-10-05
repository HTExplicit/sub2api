//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIReasoningRecoveryRejectionExcludesProtectedStatuses(t *testing.T) {
	_, ok := parseOpenAIReasoningRejection([]byte(`{"error":{"code":"thinking_signature_invalid"}}`))
	require.True(t, ok)
	_, ok = parseOpenAIReasoningRejection([]byte(`{"error":{"status":429,"code":"thinking_signature_invalid"}}`))
	require.False(t, ok, "quota/protected failures cannot become recovery markers")
}

func TestOpenAIReasoningRecoverySelectionRequiresUnambiguousLocalCipherScope(t *testing.T) {
	const (
		history  = `"input":[{"role":"user","content":"first"},{"type":"reasoning","id":"rs_one","encrypted_content":"a"},{"role":"user","content":"second"},{"type":"reasoning","id":"rs_three","encrypted_content":"b"}]`
		local    = `{` + history + `}`
		held     = `{"previous_response_id":"resp_old",` + history + `}`
		carriers = `{"input":[{"role":"user","content":"first"},{"type":"reasoning","id":"rs_one","encrypted_content":"a"},{"type":"compaction","encrypted_content":"c"},{"type":"reasoning","id":"rs_three","encrypted_content":"b"}]}`
	)
	rejection := func(payload string) openAIReasoningRejection {
		t.Helper()
		parsed, ok := parseOpenAIReasoningRejection([]byte(payload))
		require.True(t, ok, payload)
		return parsed
	}
	unqualified := rejection(`{"error":{"code":"invalid_encrypted_content"}}`)

	indices, reason := selectOpenAIRecoveryIndices([]byte(local), unqualified)
	require.Equal(t, []int{1, 3}, indices, "the unambiguous local set is selected whole")
	require.Equal(t, "recovery_not_dispatched", reason)

	indices, reason = selectOpenAIRecoveryIndices([]byte(held), unqualified)
	require.Empty(t, indices, "server-held context is left alone")
	require.Equal(t, "server_held_context", reason)

	indices, _ = selectOpenAIRecoveryIndices([]byte(held), rejection(`{"error":{"code":"invalid_encrypted_content","param":"input[3].encrypted_content"}}`))
	require.Equal(t, []int{3}, indices, "an explicit target is selected alone")

	indices, reason = selectOpenAIRecoveryIndices([]byte(held), rejection(`{"error":{"code":"invalid_encrypted_content","param":"input[2].encrypted_content"}}`))
	require.Empty(t, indices, "an item without reasoning ciphertext is never selected")
	require.Equal(t, "target_not_reasoning_ciphertext", reason)

	indices, reason = selectOpenAIRecoveryIndices([]byte(carriers), rejection(`{"error":{"code":"invalid_encrypted_content","param":"input"}}`))
	require.Empty(t, indices, "the compaction ciphertext may be the rejected one")
	require.Equal(t, "ambiguous_encrypted_carriers", reason)

	indices, _ = selectOpenAIRecoveryIndices([]byte(carriers), rejection(`{"error":{"code":"invalid_encrypted_content","param":"input","message":"The encrypted content for item rs_three could not be verified."}}`))
	require.Equal(t, []int{1, 3}, indices, "a rejection that names a reasoning item is not ambiguous")
}
