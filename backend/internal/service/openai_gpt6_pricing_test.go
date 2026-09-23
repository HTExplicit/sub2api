package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestGPT6NamedPriceCardsAndIsolatedFallbacks(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)
	parser := &PricingService{}
	data, err := parser.parsePricingData(body)
	require.NoError(t, err)
	bundled := &PricingService{pricingData: data}
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		require.Contains(t, bundled.ListModelNamesByProvider("openai"), model)
	}
	misleading := &LiteLLMModelPricing{InputCostPerToken: 100, OutputCostPerToken: 200}
	for _, tc := range []struct {
		model                                string
		input, output, cacheRead, cacheWrite float64
	}{
		{"gpt-6-sol", 2e-6, 10e-6, 0.2e-6, 2.5e-6},
		{"gpt-6-luna", 0.1e-6, 0.5e-6, 0.01e-6, 0.125e-6},
	} {
		for name, catalog := range map[string]*PricingService{
			"bundled":          bundled,
			"missing mirror":   {pricingData: map[string]*LiteLLMModelPricing{"gpt-6": misleading, "gpt-6-astra": misleading, "gpt-5.4": misleading}},
			"billing fallback": nil,
		} {
			t.Run(tc.model+"/"+name, func(t *testing.T) {
				svc := NewBillingService(&config.Config{}, catalog)
				pricing, err := svc.GetModelPricing(tc.model)
				require.NoError(t, err)
				require.InDelta(t, tc.input, pricing.InputPricePerToken, 1e-15)
				require.InDelta(t, tc.output, pricing.OutputPricePerToken, 1e-15)
				require.InDelta(t, tc.cacheRead, pricing.CacheReadPricePerToken, 1e-15)
				require.InDelta(t, tc.cacheWrite, pricing.CacheCreationPricePerToken, 1e-15)
				require.InDelta(t, tc.input*2, pricing.InputPricePerTokenPriority, 1e-15)
				require.InDelta(t, tc.output*2, pricing.OutputPricePerTokenPriority, 1e-15)
				require.Equal(t, 272000, pricing.LongContextInputThreshold)
				require.Equal(t, 2.0, pricing.LongContextInputMultiplier)
				require.Equal(t, 1.5, pricing.LongContextOutputMultiplier)
				alias, err := svc.GetModelPricing("openai/" + tc.model + "-max")
				require.NoError(t, err)
				require.Equal(t, pricing.InputPricePerToken, alias.InputPricePerToken)
			})
		}
	}
	svc := &PricingService{pricingData: map[string]*LiteLLMModelPricing{"gpt-6": misleading, "gpt-6-astra": misleading, "gpt-5.4": misleading}}
	for _, unknown := range []string{"gpt-6-future", "gpt-6-solstice", "gpt-6-luna-2099-01-01"} {
		require.Nil(t, svc.GetModelPricing(unknown), unknown)
	}
	custom := &LiteLLMModelPricing{InputCostPerToken: 7e-6}
	svc.pricingData["gpt-6-sol-provider"] = custom
	require.Same(t, custom, svc.GetModelPricing("gpt-6-sol-provider"), "explicit provider price cards remain authoritative")
}

func TestGPT6NamedPriceBoundaryTiersAndManualOverrides(t *testing.T) {
	for _, tc := range []struct {
		model                                string
		input, output, cacheRead, cacheWrite float64
	}{
		{"gpt-6-sol", 2e-6, 10e-6, 0.2e-6, 2.5e-6},
		{"gpt-6-luna", 0.1e-6, 0.5e-6, 0.01e-6, 0.125e-6},
	} {
		svc := NewBillingService(&config.Config{}, nil)
		for _, inputTotal := range []int{272000, 272001} {
			for _, tier := range []struct {
				name  string
				scale float64
			}{{"", 1}, {"priority", 2}, {"flex", 0.5}} {
				tokens := UsageTokens{InputTokens: 100000, CacheCreationTokens: 100000, CacheReadTokens: inputTotal - 200000, OutputTokens: 10}
				cost, err := svc.CalculateCostWithServiceTier(tc.model, tokens, 1, tier.name)
				require.NoError(t, err)
				long := inputTotal > 272000
				require.Equal(t, long, cost.LongContextBillingApplied)
				inScale, outScale := tier.scale, tier.scale
				if long {
					inScale *= 2
					outScale *= 1.5
				}
				require.InDelta(t, 100000*tc.input*inScale, cost.InputCost, 1e-12)
				require.InDelta(t, 100000*tc.cacheWrite*inScale, cost.CacheCreationCost, 1e-12)
				require.InDelta(t, float64(inputTotal-200000)*tc.cacheRead*inScale, cost.CacheReadCost, 1e-12)
				require.InDelta(t, 10*tc.output*outScale, cost.OutputCost, 1e-12)
			}
		}
		input, output, zero := 17e-6, 19e-6, 0.0
		manual, err := svc.GetModelPricingWithChannel(tc.model, &ChannelModelPricing{InputPrice: &input, OutputPrice: &output, CacheWritePrice: &zero})
		require.NoError(t, err)
		require.Equal(t, input, manual.InputPricePerToken)
		require.Equal(t, output, manual.OutputPricePerToken)
		require.Zero(t, manual.CacheCreationPricePerToken)
		original, err := svc.GetModelPricing(tc.model)
		require.NoError(t, err)
		require.Equal(t, tc.input, original.InputPricePerToken)
	}
}
