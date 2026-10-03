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
