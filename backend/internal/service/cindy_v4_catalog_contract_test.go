package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCindyV4CatalogMatchesPinnedInternationalSnapshot(t *testing.T) {
	t.Parallel()

	require.Equal(t, 4, CindyModelMetadataSchemaVersion)
	require.Equal(t, "b2783df6c272fc9851c85f3ffe871c962a6ab701f7698be1893fbfd03a5f28d3", CindyModelMetadataSourceSHA256)

	type expectedModel struct {
		publicID, liveID            string
		kind                        CindyModelKind
		context, output             int
		input, out, cache, discount float64
	}
	want := []expectedModel{
		{"claude-opus-4-8", "anthropic/claude-opus-4-8", CindyModelKindText, 1000000, 128000, 5e-6, 25e-6, 0.5e-6, 0},
		{"claude-opus-5", "anthropic/claude-opus-5", CindyModelKindText, 1000000, 128000, 5e-6, 25e-6, 0.5e-6, 0},
		{"claude-sonnet-5", "anthropic/claude-sonnet-5", CindyModelKindText, 1000000, 128000, 2e-6, 10e-6, 0.2e-6, 0},
		{"seed-2.1-pro", "bytedance-seed/seed-2.1-pro", CindyModelKindText, 256000, 256000, 0.88e-6, 4.4e-6, 0.18e-6, 0},
		{"deepseek-v4-flash", "deepseek/deepseek-v4-flash", CindyModelKindText, 1000000, 384000, 0.44e-6, 1.32e-6, 0.014e-6, 0},
		{"deepseek-v4-flash-vision-exp", "deepseek/deepseek-v4-flash-vision-exp", CindyModelKindText, 1000000, 384000, 0.44e-6, 1.32e-6, 0.014e-6, 0},
		{"deepseek-v4-pro", "deepseek/deepseek-v4-pro", CindyModelKindText, 1000000, 384000, 1.32e-6, 3.96e-6, 0.044e-6, 0},
		{"gemini-3.5-flash", "google/gemini-3.5-flash", CindyModelKindText, 1048576, 65535, 1.5e-6, 9e-6, 0.15e-6, 0},
		{"gemini-3.6-flash", "google/gemini-3.6-flash", CindyModelKindText, 1048576, 65536, 1.5e-6, 7.5e-6, 0.15e-6, 0},
		{"gemini-3.7-flash", "google/gemini-3.7-flash", CindyModelKindText, 1048576, 65536, 0.75e-6, 3.75e-6, 0.075e-6, 0},
		{"kimi-k3", "moonshotai/kimi-k3", CindyModelKindText, 1048576, 131072, 3e-6, 15e-6, 0.3e-6, 0},
		{"gpt-5.6-luna", "openai/gpt-5.6-luna", CindyModelKindText, 1050000, 128000, 0.2e-6, 1.2e-6, 0.02e-6, 0},
		{"gpt-5.6-sol", "openai/gpt-5.6-sol", CindyModelKindText, 1050000, 128000, 5e-6, 30e-6, 0.5e-6, 0},
		{"gpt-5.6-terra", "openai/gpt-5.6-terra", CindyModelKindText, 1050000, 128000, 2e-6, 12e-6, 0.2e-6, 0},
		{"qwen3.8-max", "qwen/qwen3.8-max", CindyModelKindText, 991808, 131072, 2e-6, 6e-6, 0.25e-6, 0},
		{"hy3", "tencent/hy3", CindyModelKindText, 262144, 128000, 0.132e-6, 0.528e-6, 0.033e-6, 0.9},
		{"grok-4.5", "x-ai/grok-4.5", CindyModelKindText, 500000, 500000, 2e-6, 6e-6, 0.3e-6, 0},
		{"grok-4.6", "x-ai/grok-4.6", CindyModelKindText, 500000, 500000, 2e-6, 6e-6, 0.5e-6, 0},
		{"glm-5.2", "z-ai/glm-5.2", CindyModelKindText, 1000000, 131072, 1.4e-6, 4.4e-6, 0.26e-6, 0.3},
		{"glm-5.3", "z-ai/glm-5.3", CindyModelKindText, 1000000, 131072, 1.4e-6, 4.4e-6, 0.26e-6, 0.2},
		{"gemini-3-pro-image", "google/gemini-3-pro-image", CindyModelKindImage, 65536, 32768, 0, 0, 0, 0},
		{"gpt-image-2", "openai/gpt-image-2", CindyModelKindImage, 0, 0, 0, 0, 0, 0},
	}

	got := CindyCapabilities()
	require.Len(t, got, len(want))
	for i, expected := range want {
		actual := got[i]
		require.Equal(t, expected.publicID, actual.PublicID, "catalog row %d", i)
		require.Equal(t, expected.liveID, actual.LiveUpstreamID, expected.publicID)
		require.NotContains(t, actual.PublicID, "/", expected.publicID)
		require.True(t, strings.Contains(actual.LiveUpstreamID, "/"), expected.publicID)
		require.Equal(t, expected.kind, actual.Kind, expected.publicID)
		require.Equal(t, expected.context, actual.MaxInputTokens, expected.publicID)
		require.Equal(t, expected.context, actual.EffectiveCodexContextWindow(), expected.publicID)
		require.Equal(t, expected.output, actual.MaxOutputTokens, expected.publicID)
		require.Equal(t, CindyModelMetadataSourceRevision, actual.MetadataSourceRevision, expected.publicID)
		require.Equal(t, CindyModelMetadataSourceRevision, actual.PricingSource, expected.publicID)
		require.Equal(t, expected.discount, actual.CostDiscount, expected.publicID)
		if expected.kind == CindyModelKindText {
			require.NotNil(t, actual.TextPricing, expected.publicID)
			require.Equal(t, expected.input, actual.TextPricing.InputCostPerToken, expected.publicID)
			require.Equal(t, expected.out, actual.TextPricing.OutputCostPerToken, expected.publicID)
			require.Equal(t, expected.cache, actual.TextPricing.CacheReadInputTokenCost, expected.publicID)
		}
	}
}

func TestCindyV4CatalogStoresPriorityLongContextAndImagePrices(t *testing.T) {
	t.Parallel()

	luna, ok := CindyTextPricingForModel("gpt-5.6-luna")
	require.True(t, ok)
	require.Equal(t, 2e-6, luna.InputCostPerTokenPriority)
	require.Equal(t, 12e-6, luna.OutputCostPerTokenPriority)
	require.Equal(t, 0.2e-6, luna.CacheReadInputTokenCostPriority)
	require.Equal(t, 272000, luna.LongContextInputTokenThreshold)
	require.Equal(t, 2e-6, luna.LongContextInputCostPerToken)
	require.Equal(t, 9e-6, luna.LongContextOutputCostPerToken)
	require.Equal(t, 0.2e-6, luna.LongContextCacheReadInputTokenCost)
	require.Equal(t, 4e-6, luna.LongContextInputCostPerTokenPriority)
	require.Equal(t, 18e-6, luna.LongContextOutputCostPerTokenPriority)
	require.Equal(t, 0.4e-6, luna.LongContextCacheReadInputTokenCostPriority)

	gemini, ok := CindyTextPricingForModel("gemini-3.5-flash")
	require.True(t, ok)
	require.Equal(t, 2.7e-6, gemini.InputCostPerTokenPriority)
	require.Equal(t, 16.2e-6, gemini.OutputCostPerTokenPriority)
	require.Equal(t, 0.27e-6, gemini.CacheReadInputTokenCostPriority)
	require.Equal(t, 1e-6, gemini.InputCostPerAudioToken)

	image, ok := CindyImagePricingForModel("gemini-3-pro-image")
	require.True(t, ok)
	require.Equal(t, 2e-6, image.InputCostPerToken)
	require.Equal(t, 12e-6, image.OutputCostPerToken)
	require.Equal(t, 0.0011, image.InputCostPerImage)
	require.Equal(t, 0.134, image.OutputCostPerImage)
	require.Equal(t, 120e-6, image.OutputCostPerImageToken)
}

func TestCindyV4CatalogProtocolContract(t *testing.T) {
	t.Parallel()

	for _, capability := range CindyCapabilities() {
		if capability.Kind == CindyModelKindImage {
			require.Empty(t, capability.AgentWireProtocols, capability.PublicID)
			require.False(t, CindyModelSupportsEndpoint(capability.PublicID, CindyEndpointResponses), capability.PublicID)
			require.False(t, CindyModelSupportsEndpoint(capability.PublicID, CindyEndpointMessages), capability.PublicID)
			continue
		}
		require.Equal(t, map[string]string{
			"claude-code": "anthropic-messages",
			"codex":       "openai-responses",
			"pi":          "openai-responses",
		}, capability.AgentWireProtocols, capability.PublicID)
		require.Equal(t, []CindyEndpoint{CindyEndpointResponses, CindyEndpointMessages}, capability.VerifiedEndpoints, capability.PublicID)
		require.True(t, CindyModelSupportsEndpoint(capability.PublicID, CindyEndpointChatCompletions), capability.PublicID)
	}

	for _, removed := range []string{"cindy/auto-review", "cindy/web-search"} {
		_, ok := resolveKnownCindyCapability(removed)
		require.False(t, ok, removed)
	}
}
