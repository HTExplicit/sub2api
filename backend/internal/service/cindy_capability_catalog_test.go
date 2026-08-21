package service

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCindyCapabilityCatalogHasExactFixedCandidates(t *testing.T) {
	t.Parallel()

	catalog := CindyCapabilities()
	require.Len(t, catalog, 23)
	expectedRegistryIDs := map[string]string{
		"claude-opus-4-8":          "anthropic/claude-opus-4-8",
		"claude-opus-5":            "anthropic/claude-opus-5",
		"claude-sonnet-5":          "anthropic/claude-sonnet-5",
		"seed-2.1-pro":             "bytedance-seed/seed-2.1-pro",
		"deepseek-v4-flash":        "deepseek/deepseek-v4-flash",
		"deepseek-v4-pro":          "deepseek/deepseek-v4-pro",
		"gemini-3.5-flash":         "google/gemini-3.5-flash",
		"kimi-k3":                  "moonshotai/kimi-k3",
		"gpt-5.6-luna":             "openai/gpt-5.6-luna",
		"gpt-5.6-sol":              "openai/gpt-5.6-sol",
		"gpt-5.6-terra":            "openai/gpt-5.6-terra",
		"qwen/qwen3.8-max-preview": "qwen/qwen3.8-max-preview",
		"grok-4.5":                 "xai/grok-4.5",
		"glm-5.2":                  "z-ai/glm-5.2",
	}
	publicIDs := make(map[string]struct{}, len(catalog))
	upstreamIDs := make(map[string]struct{}, len(catalog))
	registryCanonicalCount := 0
	registryEmptyCount := 0
	for _, capability := range catalog {
		require.NotEmpty(t, capability.PublicID)
		require.NotEmpty(t, capability.LiveUpstreamID)
		require.Equal(t, expectedRegistryIDs[capability.PublicID], capability.RegistryID, capability.PublicID)
		if capability.RegistryID == "" {
			registryEmptyCount++
		} else {
			registryCanonicalCount++
		}
		require.NotEmpty(t, capability.PricingSource, capability.PublicID)
		if capability.ExplicitZeroPrice {
			require.Contains(t, capability.PricingSource, "explicit-zero")
		}
		if capability.Kind == CindyModelKindImage {
			require.NotNil(t, capability.ImagePricing, capability.PublicID)
			require.Nil(t, capability.TextPricing, capability.PublicID)
			require.False(t, capability.ExplicitZeroPrice, capability.PublicID)
		}
		if capability.Kind == CindyModelKindText {
			require.NotEqual(t, capability.TextPricing != nil, capability.ExplicitZeroPrice, capability.PublicID)
		}
		_, publicDuplicate := publicIDs[capability.PublicID]
		_, upstreamDuplicate := upstreamIDs[capability.LiveUpstreamID]
		require.False(t, publicDuplicate, capability.PublicID)
		require.False(t, upstreamDuplicate, capability.LiveUpstreamID)
		publicIDs[capability.PublicID] = struct{}{}
		upstreamIDs[capability.LiveUpstreamID] = struct{}{}
	}
	require.Contains(t, publicIDs, "gpt-image-2")
	require.Contains(t, publicIDs, "cindy/web-search")
	require.Contains(t, upstreamIDs, "x-ai/grok-4.6")
	require.Equal(t, 14, registryCanonicalCount)
	require.Equal(t, 9, registryEmptyCount)
}

func TestCindyCatalogMetadataMatchesPinnedCindySource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		model         string
		displayName   string
		baseContext   int
		codexContext  int
		maxOutput     int
		defaultEffort string
		baseEfforts   []string
		codexEfforts  []string
		source        string
	}{
		{model: "gpt-5.6-luna", displayName: "GPT-5.6-Luna", baseContext: 1050000, codexContext: 1050000, maxOutput: 128000, defaultEffort: "high", baseEfforts: []string{"low", "medium", "high", "xhigh", "max"}, codexEfforts: []string{"low", "medium", "high", "xhigh", "max"}, source: CindyModelMetadataSourceRevision},
		{model: "gpt-5.6-sol", displayName: "GPT-5.6-Sol", baseContext: 1050000, codexContext: 1050000, maxOutput: 128000, defaultEffort: "high", baseEfforts: []string{"low", "medium", "high", "xhigh", "max"}, codexEfforts: []string{"low", "medium", "high", "xhigh", "max", "ultra"}, source: CindyModelMetadataSourceRevision},
		{model: "gpt-5.6-terra", displayName: "GPT-5.6-Terra", baseContext: 1050000, codexContext: 1050000, maxOutput: 128000, defaultEffort: "high", baseEfforts: []string{"low", "medium", "high", "xhigh", "max"}, codexEfforts: []string{"low", "medium", "high", "xhigh", "max", "ultra"}, source: CindyModelMetadataSourceRevision},
		{model: "grok-4.5", displayName: "Grok 4.5", baseContext: 500000, codexContext: 500000, defaultEffort: "high", baseEfforts: []string{"low", "medium", "high"}, codexEfforts: []string{"low", "medium", "high"}, source: CindyModelMetadataSourceRevision},
		{model: "glm-5.2", displayName: "GLM-5.2", baseContext: 1000000, codexContext: 1000000, maxOutput: 131072, defaultEffort: "max", baseEfforts: []string{"minimal", "high", "max"}, codexEfforts: []string{"minimal", "high", "max"}, source: cindyCompositeModelMetadataSourceRevision},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			capability, ok := resolveKnownCindyCapability(tt.model)
			require.True(t, ok)
			require.Equal(t, tt.displayName, capability.DisplayName)
			require.Equal(t, tt.baseContext, capability.MaxInputTokens)
			require.Equal(t, tt.codexContext, capability.CodexContextWindow)
			require.Equal(t, tt.maxOutput, capability.MaxOutputTokens)
			require.Equal(t, tt.defaultEffort, capability.DefaultReasoningEffort)
			require.Equal(t, tt.baseEfforts, capability.ReasoningEfforts)
			require.Equal(t, tt.codexEfforts, capability.CodexReasoningEfforts())
			require.Equal(t, tt.source, capability.MetadataSourceRevision)
		})
	}
}

func TestCindyRegistryBackedModelsHavePinnedDisplayAndContextMetadata(t *testing.T) {
	t.Parallel()

	registryBacked := 0
	for _, capability := range CindyCapabilities() {
		if capability.RegistryID == "" {
			continue
		}
		registryBacked++
		require.NotEmpty(t, capability.DisplayName, capability.PublicID)
		require.Positive(t, capability.MaxInputTokens, capability.PublicID)
		require.Positive(t, capability.EffectiveCodexContextWindow(), capability.PublicID)
		require.NotEmpty(t, capability.MetadataSourceRevision, capability.PublicID)
	}
	require.Equal(t, 14, registryBacked)
}

func TestCindyKnownPublicTextContextsDoNotUseDesktopFallback(t *testing.T) {
	t.Parallel()

	want := map[string]int{
		"claude-opus-4-8":  1000000,
		"claude-opus-5":    1000000,
		"claude-sonnet-5":  1000000,
		"gpt-5.6-luna":     1050000,
		"gpt-5.6-sol":      1050000,
		"gpt-5.6-terra":    1050000,
		"gemini-3.6-flash": 1000000,
		"grok-4.5":         500000,
		"grok-4.6":         500000,
		"glm-5.2":          1000000,
	}
	for modelID, contextWindow := range want {
		capability, ok := resolveKnownCindyCapability(modelID)
		require.True(t, ok)
		require.Equal(t, contextWindow, capability.MaxInputTokens, modelID)
		require.NotEqual(t, 200000, capability.MaxInputTokens, modelID)
	}
}

func TestCindyLiveFirstMetadataProvenanceIsExplicit(t *testing.T) {
	t.Parallel()

	for modelID, contextWindow := range map[string]int{
		"deepseek-v4-flash": 1048576,
		"deepseek-v4-pro":   1048576,
		"kimi-k3":           1000000,
	} {
		capability, ok := resolveKnownCindyCapability(modelID)
		require.True(t, ok)
		require.Equal(t, contextWindow, capability.MaxInputTokens)
		require.Equal(t, cindyCompositeModelMetadataSourceRevision, capability.MetadataSourceRevision)
	}
	require.Equal(t, "cindy-gateway-v1-models@2026-08-17", cindyObservedModelMetadataSourceRevision)
}

func TestCindyApprovedManualAndUnknownMetadataRemainExplicit(t *testing.T) {
	t.Parallel()

	for modelID, contextWindow := range cindyApprovedManualContextWindows {
		capability, ok := resolveKnownCindyCapability(modelID)
		require.True(t, ok)
		require.Equal(t, contextWindow, capability.MaxInputTokens, modelID)
		require.Equal(t, contextWindow, capability.CodexContextWindow, modelID)
		require.Zero(t, capability.MaxOutputTokens, modelID)
		require.Empty(t, capability.ReasoningEfforts, modelID)
		require.Empty(t, capability.CodexReasoningEffortLevels, modelID)
		require.Empty(t, capability.DefaultReasoningEffort, modelID)
	}

	seed, ok := resolveKnownCindyCapability("seed-2.1-pro")
	require.True(t, ok)
	require.Equal(t, 256000, seed.MaxInputTokens)
	require.Zero(t, seed.MaxOutputTokens)
	require.Equal(t, CindyModelMetadataSourceRevision, seed.MetadataSourceRevision)

	hy3, ok := resolveKnownCindyCapability("hy3")
	require.True(t, ok)
	require.Zero(t, hy3.MaxInputTokens)
	require.Zero(t, hy3.CodexContextWindow)

	qwen, ok := resolveKnownCindyCapability("qwen/qwen3.8-max-preview")
	require.True(t, ok)
	require.Equal(t, []string{"text"}, qwen.InputModalities)
	require.Equal(t, cindyCompositeModelMetadataSourceRevision, qwen.MetadataSourceRevision)
}

func TestCindyCatalogModelsAndManagedMappingsAreCompleteDefensiveProjections(t *testing.T) {
	t.Parallel()

	models := CindyCatalogModels()
	require.Len(t, models, 23)
	for i, model := range models {
		require.True(t, model.Managed)
		require.Empty(t, model.AliasTarget)
		require.NotEmpty(t, model.ID)
		require.NotEmpty(t, model.LiveUpstreamID)
		require.NotEmpty(t, model.DisplayName)
		require.NotEmpty(t, model.SourceRevision)
		require.Equal(t, model.CodexContextWindow, model.ContextWindow)
		require.Equal(t, len(model.Endpoints) > 0, model.Verified)
		if i > 0 {
			require.Less(t, models[i-1].ID, model.ID)
		}
	}

	byID := make(map[string]CindyCatalogModel, len(models))
	for _, model := range models {
		byID[model.ID] = model
	}
	require.Equal(t, 1050000, byID["gpt-5.6-sol"].BaseContextWindow)
	require.Equal(t, 1050000, byID["gpt-5.6-sol"].ContextWindow)
	require.Equal(t, 1050000, byID["gpt-5.6-sol"].CodexContextWindow)
	require.Equal(t, CindyModelMetadataSourceRevision, byID["gpt-5.6-sol"].SourceRevision)
	require.False(t, byID["deepseek-v4-pro"].PublicModel)
	require.Equal(t, cindyCompositeModelMetadataSourceRevision, byID["qwen/qwen3.8-max-preview"].SourceRevision)

	models[0].Endpoints = append(models[0].Endpoints, CindyEndpointReview)
	require.NotEqual(t, models[0].Endpoints, CindyCatalogModels()[0].Endpoints)

	mappings := CindyManagedModelMappings()
	require.Len(t, mappings, 2)
	require.Equal(t, "gpt-5.4", mappings[0].ID)
	require.Equal(t, "gpt-5.6-sol", mappings[0].AliasTarget)
	require.Equal(t, "openai/gpt-5.6-sol", mappings[0].LiveUpstreamID)
	require.Equal(t, 1050000, mappings[0].BaseContextWindow)
	require.Equal(t, 1050000, mappings[0].ContextWindow)
	require.Equal(t, CindyCompatibilityAliasSourceRevision, mappings[0].SourceRevision)
	require.NotEqual(t, CindyModelMetadataSourceRevision, mappings[0].SourceRevision)
	require.Equal(t, "gpt-5.4-mini", mappings[1].ID)
	require.Equal(t, "gpt-5.6-luna", mappings[1].AliasTarget)
	require.Equal(t, "openai/gpt-5.6-luna", mappings[1].LiveUpstreamID)
	require.Equal(t, 1050000, mappings[1].ContextWindow)
	require.Equal(t, CindyCompatibilityAliasSourceRevision, mappings[1].SourceRevision)
	require.NotEqual(t, CindyModelMetadataSourceRevision, mappings[1].SourceRevision)
}

func TestCindyVerifiedEndpointMatrixMatchesThreeAccountProbe(t *testing.T) {
	t.Parallel()

	wantMessages := []string{
		"claude-opus-4-8", "claude-opus-5", "claude-sonnet-5",
		"gemini-3.6-flash",
		"gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra", "hy3",
		"grok-4.5", "grok-4.6", "glm-5.2",
	}
	wantResponses := []string{
		"gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra",
		"grok-4.5", "glm-5.2",
	}

	byEndpoint := make(map[CindyEndpoint][]string)
	for _, capability := range CindyCapabilities() {
		for _, endpoint := range capability.VerifiedEndpoints {
			byEndpoint[endpoint] = append(byEndpoint[endpoint], capability.PublicID)
		}
	}
	require.ElementsMatch(t, wantMessages, byEndpoint[CindyEndpointMessages])
	require.ElementsMatch(t, wantResponses, byEndpoint[CindyEndpointResponses])
	require.ElementsMatch(t, wantResponses, byEndpoint[CindyEndpointChatCompletions])
	require.ElementsMatch(t, []string{"gpt-image-2", "gemini-3-pro-image"}, byEndpoint[CindyEndpointImagesGenerate])
	require.Equal(t, []string{"gemini-3-pro-image"}, byEndpoint[CindyEndpointImagesEdit])
	require.Equal(t, []string{"cindy/web-search"}, byEndpoint[CindyEndpointAlphaSearch])
	require.Empty(t, byEndpoint[CindyEndpointReview], "auto-review remains fail-closed without a verified public schema")
}

func TestCindyTextPricingIsExactAndDefensivelyCopied(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		model string
		want  CindyTextPricing
	}{
		{
			name:  "hidden GPT alias",
			model: "gpt-5.4",
			want: CindyTextPricing{
				InputCostPerToken: 5e-6, OutputCostPerToken: 30e-6,
				CacheReadInputTokenCost: 0.5e-6, CacheCreationInputTokenCost: 6.25e-6,
				LongContextInputTokenThreshold: 272000, LongContextInputCostPerToken: 10e-6,
				LongContextOutputCostPerToken: 45e-6, LongContextCacheReadInputTokenCost: 1e-6,
				LongContextCacheCreationTokenCost: 12.5e-6,
			},
		},
		{
			name:  "GPT Luna long context",
			model: "gpt-5.6-luna",
			want: CindyTextPricing{
				InputCostPerToken: 0.2e-6, OutputCostPerToken: 1.2e-6,
				CacheReadInputTokenCost: 0.02e-6, CacheCreationInputTokenCost: 0.25e-6,
				LongContextInputTokenThreshold: 272000, LongContextInputCostPerToken: 0.4e-6,
				LongContextOutputCostPerToken: 1.8e-6, LongContextCacheReadInputTokenCost: 0.04e-6,
				LongContextCacheCreationTokenCost: 0.5e-6,
			},
		},
		{
			name:  "GPT Terra long context",
			model: "openai/gpt-5.6-terra",
			want: CindyTextPricing{
				InputCostPerToken: 2e-6, OutputCostPerToken: 12e-6,
				CacheReadInputTokenCost: 0.2e-6, CacheCreationInputTokenCost: 2.5e-6,
				LongContextInputTokenThreshold: 272000, LongContextInputCostPerToken: 4e-6,
				LongContextOutputCostPerToken: 18e-6, LongContextCacheReadInputTokenCost: 0.4e-6,
				LongContextCacheCreationTokenCost: 5e-6,
			},
		},
		{
			name:  "Anthropic cache tiers",
			model: "anthropic/claude-opus-5",
			want:  CindyTextPricing{InputCostPerToken: 5e-6, OutputCostPerToken: 25e-6, CacheReadInputTokenCost: 0.5e-6, CacheCreationInputTokenCost: 6.25e-6, CacheCreationInputTokenCostAbove1hr: 10e-6},
		},
		{
			name:  "xAI long context",
			model: "grok-4.6",
			want:  CindyTextPricing{InputCostPerToken: 2e-6, OutputCostPerToken: 6e-6, CacheReadInputTokenCost: 0.5e-6, LongContextInputTokenThreshold: 200000, LongContextThresholdInclusive: true, LongContextInputCostPerToken: 4e-6, LongContextOutputCostPerToken: 12e-6, LongContextCacheReadInputTokenCost: 1e-6},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := CindyTextPricingForModel(test.model)
			require.True(t, ok)
			require.Equal(t, test.want, got)
		})
	}

	first, ok := ResolveCindyCapability("gpt-5.6-luna")
	require.True(t, ok)
	first.TextPricing.InputCostPerToken = 99
	second, ok := ResolveCindyCapability("gpt-5.6-luna")
	require.True(t, ok)
	require.Equal(t, 0.2e-6, second.TextPricing.InputCostPerToken)

	_, ok = CindyTextPricingForModel("seed-2.1-pro")
	require.False(t, ok)
	require.True(t, CindyModelUsesExplicitZeroPrice("seed-2.1-pro"))
	_, ok = CindyTextPricingForModel("deepseek-v4-flash")
	require.False(t, ok)
	require.True(t, CindyModelUsesExplicitZeroPrice("deepseek-v4-flash"))
}

func TestCindyCapabilityAliasesAreExactAndHidden(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"gpt-5.4":                    "openai/gpt-5.6-sol",
		"gpt-5.4-mini":               "openai/gpt-5.6-luna",
		"claude-opus-4-5-20251101":   "anthropic/claude-opus-5",
		"claude-sonnet-4-5-20250929": "anthropic/claude-sonnet-5",
		"claude-haiku-4-5-20251001":  "anthropic/claude-sonnet-5",
		"claude-opus-4-8":            "anthropic/claude-opus-4-8",
		"openai/gpt-5.6-terra":       "openai/gpt-5.6-terra",
	}
	for requested, want := range tests {
		got, ok := CindyMappedUpstreamModel(requested)
		require.True(t, ok, requested)
		require.Equal(t, want, got, requested)
	}

	_, ok := CindyMappedUpstreamModel("claude-opus-4-5-20251101-extra")
	require.False(t, ok, "aliases must not use family or prefix matching")
	public := CindyPublicModelIDs()
	require.NotContains(t, public, "gpt-5.4")
	require.NotContains(t, public, "gpt-5.4-mini")
	require.NotContains(t, public, "openai/gpt-5.6-sol")
	require.NotContains(t, public, "cindy/web-search")
}

func TestCindyCompatibilityAliasesAreNarrowAndExact(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"gpt-5.4":      "openai/gpt-5.6-sol",
		"gpt-5.4-mini": "openai/gpt-5.6-luna",
	}
	for requested, want := range tests {
		got, ok := CindyCompatibilityMappedUpstreamModel(requested)
		require.True(t, ok, requested)
		require.Equal(t, want, got, requested)
	}
	for _, unsupported := range []string{
		"gpt-5.4-mini-extra",
		"GPT-5.4-MINI",
		" gpt-5.4-mini",
		"gpt-5.4-mini ",
		"claude-opus-4-6",
		"gpt-5.6-luna",
	} {
		_, ok := CindyCompatibilityMappedUpstreamModel(unsupported)
		require.False(t, ok, unsupported)
	}
}

func TestCindyCompatibilityRoutingTargetsAreNarrow(t *testing.T) {
	t.Parallel()

	for _, model := range []string{
		"openai/gpt-5.6-sol",
		"openai/gpt-5.6-luna",
	} {
		require.True(t, CindyCompatibilityRoutingTarget(model), model)
		_, ok := CindyCompatibilityTextPricingForModel(model)
		require.True(t, ok, model)
	}
	for _, model := range []string{"gpt-5.6-sol", "gpt-5.6-luna"} {
		require.False(t, CindyCompatibilityRoutingTarget(model), model)
		_, ok := CindyCompatibilityTextPricingForModel(model)
		require.True(t, ok, model)
	}
	for _, model := range []string{
		"gpt-5.4",
		"gpt-5.4-mini",
		"openai/gpt-5.6-terra",
		" gpt-5.6-luna",
	} {
		require.False(t, CindyCompatibilityRoutingTarget(model), model)
	}
}

func TestCindyImageModelCapabilitiesAreClientSafe(t *testing.T) {
	t.Parallel()

	capabilities := CindyImageModelCapabilities()
	require.NotEmpty(t, capabilities)
	ids := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		require.Equal(t, "model_capability", capability.Object)
		require.Equal(t, CindyModelKindImage, capability.Kind)
		require.NotEmpty(t, capability.Endpoints)
		ids = append(ids, capability.ID)
	}
	require.Equal(t, []string{"gemini-3-pro-image", "gpt-image-2"}, ids)
}

func TestCindyCodexPublicModelIDsContainOnlyResponsesSurface(t *testing.T) {
	if !runCindyCodexCatalogEnabledTest(t) {
		return
	}

	require.Equal(t, []string{
		"glm-5.2",
		"gpt-5.6-luna",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-image-2",
		"grok-4.5",
	}, CindyCodexPublicModelIDs())
	for _, forbidden := range []string{
		"claude-opus-5",
		"claude-sonnet-5",
		"gemini-3.6-flash",
		"hy3",
		"gemini-3-pro-image",
		"deepseek-v4-pro",
		"seed-2.1-pro",
		"gpt-5.4",
		"openai/gpt-5.6-sol",
	} {
		require.NotContains(t, CindyCodexPublicModelIDs(), forbidden)
	}
}

func TestCindyImagePricingIsExactAndDefensivelyCopied(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		model string
		want  CindyImagePricing
	}{
		{
			name:  "OpenAI public ID",
			model: "gpt-image-2",
			want: CindyImagePricing{
				InputCostPerToken:            5e-6,
				OutputCostPerToken:           10e-6,
				CacheReadInputTokenCost:      1.25e-6,
				InputCostPerImageToken:       8e-6,
				OutputCostPerImageToken:      3e-5,
				CacheReadInputImageTokenCost: 2e-6,
			},
		},
		{
			name:  "Gemini live upstream ID",
			model: "google/gemini-3-pro-image",
			want: CindyImagePricing{
				InputCostPerToken:        2e-6,
				OutputCostPerToken:       1.2e-5,
				InputCostPerImage:        0.0011,
				OutputCostPerImage1KOr2K: 0.134,
				OutputCostPerImage4K:     0.24,
				OutputCostPerImageToken:  1.2e-4,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := CindyImagePricingForModel(test.model)
			require.True(t, ok)
			require.Equal(t, test.want, got)
		})
	}

	first, ok := ResolveCindyCapability("gpt-image-2")
	require.True(t, ok)
	first.ImagePricing.InputCostPerToken = 99
	second, ok := ResolveCindyCapability("gpt-image-2")
	require.True(t, ok)
	require.Equal(t, 5e-6, second.ImagePricing.InputCostPerToken)

	_, ok = CindyImagePricingForModel("gpt-5.6-luna")
	require.False(t, ok)
}

func TestCindyImageControlsAreEndpointSpecificAndDefensivelyCopied(t *testing.T) {
	t.Parallel()

	gpt, ok := ResolveCindyCapability("gpt-image-2")
	require.True(t, ok)
	require.Equal(t, &CindyImageRequestControls{Sizes: []string{"1024x1024"}, Qualities: []string{"low"}, MaxOutputCount: 1}, gpt.Controls.Generation)
	require.Nil(t, gpt.Controls.Edit)

	gemini, ok := ResolveCindyCapability("gemini-3-pro-image")
	require.True(t, ok)
	require.Equal(t, &CindyImageRequestControls{Sizes: []string{"1024x1024"}, Qualities: []string{"low"}, MaxOutputCount: 1}, gemini.Controls.Generation)
	require.Equal(t, &CindyImageRequestControls{Sizes: []string{"1024x1024"}, Qualities: []string{"low"}, MaxOutputCount: 1, SupportsReferenceImage: true, SupportsMask: true}, gemini.Controls.Edit)

	gpt.Controls.Generation.Sizes[0] = "mutated"
	again, ok := ResolveCindyCapability("gpt-image-2")
	require.True(t, ok)
	require.Equal(t, []string{"1024x1024"}, again.Controls.Generation.Sizes)
}

func TestCindyEndpointVerificationGatesScheduling(t *testing.T) {
	t.Parallel()

	require.True(t, CindyModelSupportsEndpoint("gpt-5.4", CindyEndpointResponses))
	require.True(t, CindyModelSupportsEndpoint("gpt-5.4", CindyEndpointMessages))
	require.True(t, CindyModelSupportsEndpoint("claude-opus-5", CindyEndpointMessages))
	require.False(t, CindyModelSupportsEndpoint("claude-opus-5", CindyEndpointResponses))
	require.False(t, CindyModelSupportsEndpoint("seed-2.1-pro", CindyEndpointMessages), "A-account tool closure failed in the corrected probe")
	require.False(t, CindyModelSupportsEndpoint("seed-2.1-pro", CindyEndpointResponses), "latest A-account Responses tool closure failed")
	require.False(t, CindyModelSupportsEndpoint("seed-2.1-pro", CindyEndpointChatCompletions), "Chat bridge requires verified Responses")
	require.False(t, CindyModelHasVerifiedEndpoint("seed-2.1-pro"))
	require.NotContains(t, CindyPublicModelIDs(), "seed-2.1-pro")
	require.NotContains(t, CindyCodexPublicModelIDs(), "seed-2.1-pro")
	require.False(t, CindyModelSupportsEndpoint("gemini-3.7-flash", CindyEndpointMessages), "A-account sync failed in the corrected probe")
	require.False(t, CindyModelHasVerifiedEndpoint("gemini-3.7-flash"), "a split three-account result must remain hidden")
	require.True(t, CindyModelSupportsEndpoint("cindy/web-search", CindyEndpointAlphaSearch))
	require.True(t, CindyModelSupportsEndpoint("gpt-image-2", CindyEndpointImagesGenerate))
	require.False(t, CindyModelSupportsEndpoint("gpt-image-2", CindyEndpointImagesEdit))
	require.True(t, CindyModelSupportsEndpoint("gemini-3-pro-image", CindyEndpointImagesEdit))
	require.False(t, CindyModelHasVerifiedEndpoint("deepseek-v4-pro"), "unprobed candidates stay hidden")
	require.True(t, CindyModelUsesExplicitZeroPrice("cindy/web-search"))
	require.False(t, CindyModelUsesExplicitZeroPrice("gpt-5.6-sol"))
	require.False(t, CindyModelHasVerifiedEndpoint("cindy/auto-review"), "auto-review remains fail-closed without a public schema")

	account := &Account{
		Platform:        PlatformCindy,
		WirePlatform:    WirePlatformOpenAI,
		ProviderProfile: ProviderProfileCindyLaxaV1,
		Type:            AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "secret",
			"base_url": "https://api.laxarouter.ai",
			"model_mapping": map[string]any{
				"gpt-5.6-sol": "openai/gpt-5.6-sol",
			},
		},
	}
	require.True(t, account.IsModelSupported("gpt-5.4"))
	require.Equal(t, "openai/gpt-5.6-sol", account.GetMappedModel("gpt-5.4"))
	require.False(t, account.IsModelSupported("deepseek-v4-pro"))
	require.Equal(t, "deepseek/deepseek-v4-pro", account.GetMappedModel("deepseek-v4-pro"), "manual probes can still resolve fixed candidates")
}

type cindyGroupReaderStub struct {
	accounts []Account
	err      error
}

type mutableCindyGroupReaderStub struct {
	accounts []Account
	err      error
	calls    atomic.Int64
}

func (s *mutableCindyGroupReaderStub) ListByGroup(context.Context, int64) ([]Account, error) {
	s.calls.Add(1)
	return append([]Account(nil), s.accounts...), s.err
}

func (s *mutableCindyGroupReaderStub) ListCindyGroupIdentityMembers(ctx context.Context, groupID int64) ([]Account, error) {
	return s.ListByGroup(ctx, groupID)
}

func (s *mutableCindyGroupReaderStub) CindyGroupIdentityReaderMarker() {}

func (s cindyGroupReaderStub) ListByGroup(context.Context, int64) ([]Account, error) {
	return append([]Account(nil), s.accounts...), s.err
}

func (s cindyGroupReaderStub) ListCindyGroupIdentityMembers(ctx context.Context, groupID int64) ([]Account, error) {
	return s.ListByGroup(ctx, groupID)
}

func (s cindyGroupReaderStub) CindyGroupIdentityReaderMarker() {}

type unmarkedCindyGroupReaderStub struct {
	called *atomic.Bool
}

func (s unmarkedCindyGroupReaderStub) ListByGroup(context.Context, int64) ([]Account, error) {
	s.called.Store(true)
	panic("unmarked group reader must not be called")
}

func TestIsStrictCindyGroupRequiresEveryNonDeletedMember(t *testing.T) {
	t.Parallel()
	groupID := int64(42)
	cindy := Account{Platform: PlatformCindy, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"base_url": "https://api.laxarouter.ai"}}
	other := Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"base_url": "https://api.openai.com"}}

	require.True(t, isStrictCindyGroup(context.Background(), cindyGroupReaderStub{accounts: []Account{cindy, cindy}}, &groupID))
	exhausted := cindy
	exhausted.Schedulable = false
	now := time.Now()
	exhausted.CindyBalanceInsufficientAt = &now
	require.True(t, isStrictCindyGroup(context.Background(), cindyGroupReaderStub{accounts: []Account{exhausted}}, &groupID), "persistent group identity must survive a fully exhausted pool")
	require.False(t, isStrictCindyGroup(context.Background(), cindyGroupReaderStub{accounts: []Account{cindy, other}}, &groupID))
	disabledOther := other
	disabledOther.Status = StatusDisabled
	disabledOther.Schedulable = false
	require.False(t, isStrictCindyGroup(context.Background(), cindyGroupReaderStub{accounts: []Account{cindy, disabledOther}}, &groupID))
	require.False(t, isStrictCindyGroup(context.Background(), cindyGroupReaderStub{}, &groupID))
	var typedNil *cindyGroupReaderStub
	require.False(t, isStrictCindyGroup(context.Background(), typedNil, &groupID))
	var unmarkedCalled atomic.Bool
	require.False(t, isStrictCindyGroup(context.Background(), unmarkedCindyGroupReaderStub{called: &unmarkedCalled}, &groupID))
	require.False(t, unmarkedCalled.Load(), "unmarked repositories must not be queried")
}

func TestStrictCindyGroupIdentityIsUncachedAndPropagatesErrors(t *testing.T) {
	t.Parallel()
	groupID := int64(43)
	cindy := Account{Platform: PlatformCindy, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.laxarouter.ai"}}
	repo := &mutableCindyGroupReaderStub{accounts: []Account{cindy}}

	strict, err := classifyStrictCindyGroup(context.Background(), repo, &groupID)
	require.NoError(t, err)
	require.True(t, strict)

	repo.accounts = []Account{{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.openai.com"}}}
	strict, err = classifyStrictCindyGroup(context.Background(), repo, &groupID)
	require.NoError(t, err)
	require.False(t, strict, "the next request must observe an identity change without a TTL window")
	require.Equal(t, int64(2), repo.calls.Load())

	repo.err = context.DeadlineExceeded
	strict, err = classifyStrictCindyGroup(context.Background(), repo, &groupID)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, strict)
}

func TestCindyAnthropicAuthAlwaysUsesBearer(t *testing.T) {
	t.Parallel()
	account := &Account{Platform: PlatformCindy, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.laxarouter.ai"}}
	header := make(http.Header)
	setAnthropicAPIKeyAuthHeader(header, account, "upstream-secret")
	require.Equal(t, "Bearer upstream-secret", header.Get("Authorization"))
	require.Empty(t, header.Get("x-api-key"))
}
