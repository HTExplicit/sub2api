package service

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCindyFreeCatalogContainsOnlyNineOrdinaryAndTwoSpecialModels(t *testing.T) {
	t.Parallel()

	require.Equal(t, "2026-08-30.1", CindyCapabilityCatalogVersion)
	require.Equal(t, "a292efade35e63895aa20b63beed0d1ec712d93d030c6a2300cc9e739e926d46", CindyFreeModelCatalogSHA256)
	require.Equal(t, "laxarouter-free-key@2026-08-29", CindyFreeModelCatalogSourceRevision)

	type expectedModel struct {
		publicID, liveID            string
		kind                        CindyModelKind
		context, output             int
		input, out, cache, discount float64
	}
	wantOrdinary := []expectedModel{
		{"deepseek-v4-flash", "deepseek/deepseek-v4-flash", CindyModelKindText, 1000000, 384000, 0.44e-6, 1.32e-6, 0.014e-6, 0},
		{"deepseek-v4-flash-vision-exp", "deepseek/deepseek-v4-flash-vision-exp", CindyModelKindText, 1000000, 384000, 0.44e-6, 1.32e-6, 0.014e-6, 0},
		{"deepseek-v4-pro", "deepseek/deepseek-v4-pro", CindyModelKindText, 1000000, 384000, 1.32e-6, 3.96e-6, 0.044e-6, 0},
		{"gemini-3.6-flash", "google/gemini-3.6-flash", CindyModelKindText, 1048576, 65536, 1.5e-6, 7.5e-6, 0.15e-6, 0},
		{"gpt-5.6-luna", "openai/gpt-5.6-luna", CindyModelKindText, 1050000, 128000, 0.2e-6, 1.2e-6, 0.02e-6, 0},
		{"qwen3.8-27b", "qwen/qwen3.8-27b", CindyModelKindText, 991808, 131072, 0.425e-6, 2.55e-6, 0.085e-6, 0},
		{"qwen3.8-flash", "qwen/qwen3.8-flash", CindyModelKindText, 991808, 131072, 0.16e-6, 0.47e-6, 0.016e-6, 0},
		{"hy3", "tencent/hy3", CindyModelKindText, 262144, 128000, 0.132e-6, 0.528e-6, 0.033e-6, 0.9},
		{"glm-5.3-flash", "z-ai/glm-5.3-flash", CindyModelKindText, 1000000, 131072, 0.15e-6, 0.5e-6, 0.03e-6, 0.5},
	}

	got := CindyCapabilities()
	require.Len(t, got, 11)
	byID := make(map[string]CindyCapability, len(got))
	for _, capability := range got {
		_, duplicate := byID[capability.PublicID]
		require.False(t, duplicate, capability.PublicID)
		byID[capability.PublicID] = capability
	}

	for _, expected := range wantOrdinary {
		actual := byID[expected.publicID]
		require.Equal(t, expected.liveID, actual.LiveUpstreamID, expected.publicID)
		require.Equal(t, expected.kind, actual.Kind, expected.publicID)
		require.True(t, actual.PublicModel, expected.publicID)
		require.Equal(t, expected.context, actual.MaxInputTokens, expected.publicID)
		require.Equal(t, expected.context, actual.EffectiveCodexContextWindow(), expected.publicID)
		require.Equal(t, expected.output, actual.MaxOutputTokens, expected.publicID)
		require.Equal(t, CindyModelMetadataSourceRevision, actual.MetadataSourceRevision, expected.publicID)
		require.Equal(t, CindyModelMetadataSourceRevision, actual.PricingSource, expected.publicID)
		require.Equal(t, expected.discount, actual.CostDiscount, expected.publicID)
		require.NotNil(t, actual.TextPricing, expected.publicID)
		require.Equal(t, expected.input, actual.TextPricing.InputCostPerToken, expected.publicID)
		require.Equal(t, expected.out, actual.TextPricing.OutputCostPerToken, expected.publicID)
		require.Equal(t, expected.cache, actual.TextPricing.CacheReadInputTokenCost, expected.publicID)
	}

	search := byID[CindyWebSearchModel]
	require.Equal(t, CindyModelKindSpecial, search.Kind)
	require.False(t, search.PublicModel)
	require.Equal(t, []CindyEndpoint{CindyEndpointAlphaSearch}, search.VerifiedEndpoints)
	require.Equal(t, CindyFreeModelCatalogSourceRevision, search.MetadataSourceRevision)

	review := byID[CindyAutoReviewModel]
	require.Equal(t, CindyModelKindSpecial, review.Kind)
	require.False(t, review.PublicModel)
	require.Empty(t, review.VerifiedEndpoints)
	require.Equal(t, CindyFreeModelCatalogSourceRevision, review.MetadataSourceRevision)
}

func TestCindyFreeCatalogRoutingAndCompatibilityStayNinePlusTwo(t *testing.T) {
	t.Parallel()

	wantOrdinary := []string{
		"deepseek-v4-flash",
		"deepseek-v4-flash-vision-exp",
		"deepseek-v4-pro",
		"gemini-3.6-flash",
		"glm-5.3-flash",
		"gpt-5.6-luna",
		"hy3",
		"qwen3.8-27b",
		"qwen3.8-flash",
	}
	require.Equal(t, wantOrdinary, CindyPublicModelIDs())
	require.Equal(t, wantOrdinary, CindyCodexPublicModelIDs())
	require.Equal(t, [...]string{"tencent/hy3", "z-ai/glm-5.3-flash"}, cindyBalanceProbeModels)

	for _, modelID := range wantOrdinary {
		capability, known := resolveKnownCindyCapability(modelID)
		require.True(t, known, modelID)
		require.True(t, capability.PublicModel, modelID)
		_, routable := ResolveCindyCapability(modelID)
		require.True(t, routable, modelID)
		require.True(t, CindyModelSupportsEndpoint(modelID, CindyEndpointResponses), modelID)
		require.True(t, CindyModelSupportsEndpoint(modelID, CindyEndpointMessages), modelID)
		require.True(t, CindyModelSupportsEndpoint(modelID, CindyEndpointChatCompletions), modelID)
		require.Equal(t, map[string]string{
			"claude-code": "anthropic-messages",
			"codex":       "openai-responses",
			"pi":          "openai-responses",
		}, capability.AgentWireProtocols, modelID)
	}

	for _, specialID := range []string{CindyWebSearchModel, CindyAutoReviewModel} {
		capability, known := resolveKnownCindyCapability(specialID)
		require.True(t, known, specialID)
		require.Equal(t, CindyModelKindSpecial, capability.Kind, specialID)
		require.False(t, capability.PublicModel, specialID)
		_, routable := ResolveCindyCapability(specialID)
		require.False(t, routable, specialID)
		require.False(t, CindyModelSupportsEndpoint(specialID, CindyEndpointResponses), specialID)
		require.False(t, CindyModelSupportsEndpoint(specialID, CindyEndpointMessages), specialID)
		require.False(t, CindyModelSupportsEndpoint(specialID, CindyEndpointChatCompletions), specialID)
	}

	aliases := CindyManagedModelMappings()
	require.Len(t, aliases, 1)
	require.Equal(t, "gpt-5.4-mini", aliases[0].ID)
	require.Equal(t, "gpt-5.6-luna", aliases[0].AliasTarget)
	require.Equal(t, "openai/gpt-5.6-luna", aliases[0].LiveUpstreamID)
	_, mapsSol := CindyCompatibilityMappedUpstreamModel("gpt-5.4")
	require.False(t, mapsSol)
	require.Equal(t, "openai/gpt-5.6-luna", mustCindyCompatibilityTarget(t, "gpt-5.4-mini"))
}

func TestCindyFreeCatalogManagementProjectionIsExactlyEleven(t *testing.T) {
	t.Parallel()

	models := CindyCatalogModels()
	require.Len(t, models, 11)
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
		require.True(t, model.Managed, model.ID)
	}
	sort.Strings(ids)
	require.Equal(t, []string{
		"cindy/auto-review",
		"cindy/web-search",
		"deepseek-v4-flash",
		"deepseek-v4-flash-vision-exp",
		"deepseek-v4-pro",
		"gemini-3.6-flash",
		"glm-5.3-flash",
		"gpt-5.6-luna",
		"hy3",
		"qwen3.8-27b",
		"qwen3.8-flash",
	}, ids)

	for _, removed := range []string{
		"claude-opus-5", "gemini-3.5-flash", "gpt-5.6-sol", "gpt-5.6-terra",
		"qwen3.8-max", "gemini-3-pro-image", "gpt-image-2",
	} {
		_, known := resolveKnownCindyCapability(removed)
		require.False(t, known, removed)
	}
}

func TestCindyFreeLunaPricingPreservesLongContextContract(t *testing.T) {
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
}

func mustCindyCompatibilityTarget(t *testing.T, model string) string {
	t.Helper()
	target, ok := CindyCompatibilityMappedUpstreamModel(model)
	require.True(t, ok)
	return target
}
