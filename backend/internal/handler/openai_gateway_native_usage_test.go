package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAIForwardResultFromNativeAnthropicPreservesUsageSemantics(t *testing.T) {
	t.Parallel()
	converted := openAIForwardResultFromNativeAnthropic(&service.ForwardResult{
		Model:         "claude-opus-4-5-20251101",
		UpstreamModel: "anthropic/claude-opus-5",
		Usage: service.ClaudeUsage{
			InputTokens: 100, OutputTokens: 5,
			CacheCreationInputTokens: 10, CacheReadInputTokens: 20,
			CacheCreation5mTokens: 4, CacheCreation1hTokens: 6,
		},
	})

	require.NotNil(t, converted)
	require.True(t, converted.UsageInputTokensExcludeCache)
	require.Equal(t, 100, converted.Usage.InputTokens)
	require.Equal(t, 10, converted.Usage.CacheCreationInputTokens)
	require.Equal(t, 20, converted.Usage.CacheReadInputTokens)
	require.Equal(t, 4, converted.Usage.CacheCreation5mTokens)
	require.Equal(t, 6, converted.Usage.CacheCreation1hTokens)
	require.Equal(t, "anthropic/claude-opus-5", converted.BillingModel)
}
