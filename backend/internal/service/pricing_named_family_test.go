package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPricingNamedGPT56FamilyPrecedesGenericFallback(t *testing.T) {
	generic := &LiteLLMModelPricing{InputCostPerToken: 4e-6, OutputCostPerToken: 20e-6, CacheCreationInputTokenCost: 5e-6, CacheReadInputTokenCost: .4e-6}
	for _, tc := range []struct {
		model                      string
		input, output, write, read float64
	}{
		{"gpt-5.6-sol-project", 5e-6, 30e-6, 6.25e-6, .5e-6},
		{"gpt-5.6-terra-project", 2e-6, 12e-6, 2.5e-6, .2e-6},
		{"gpt-5.6-luna-project", .2e-6, 1.2e-6, .25e-6, .02e-6},
		{"gpt-5.6-luna-ssvip", .2e-6, 1.2e-6, .25e-6, .02e-6},
		{"models/gpt-5.6-luna-project", .2e-6, 1.2e-6, .25e-6, .02e-6},
	} {
		t.Run(tc.model, func(t *testing.T) {
			svc := &PricingService{pricingData: map[string]*LiteLLMModelPricing{"gpt-5.6": generic}}
			price := svc.GetModelPricing(tc.model)
			require.NotNil(t, price)
			require.InDelta(t, tc.input, price.InputCostPerToken, 1e-12)
			require.InDelta(t, tc.output, price.OutputCostPerToken, 1e-12)
			require.InDelta(t, tc.write, price.CacheCreationInputTokenCost, 1e-12)
			require.InDelta(t, tc.read, price.CacheReadInputTokenCost, 1e-12)
		})
	}
}

func TestPricingNamedGPT56FamilyPreservesExplicitCatalogPrices(t *testing.T) {
	generic := &LiteLLMModelPricing{InputCostPerToken: 4e-6}
	family := &LiteLLMModelPricing{InputCostPerToken: .3e-6, CacheCreationInputTokenCost: .375e-6}
	exact := &LiteLLMModelPricing{InputCostPerToken: .7e-6, CacheCreationInputTokenCost: .875e-6}
	svc := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"gpt-5.6": generic, "gpt-5.6-luna": family, "gpt-5.6-luna-project": exact,
	}}
	require.Same(t, exact, svc.GetModelPricing("gpt-5.6-luna-project"))
	require.Same(t, family, svc.GetModelPricing("gpt-5.6-luna-ssvip"))
	require.Same(t, generic, svc.GetModelPricing("gpt-5.6-lunatic-project"), "do not claim another family by a partial name")

	astra := &LiteLLMModelPricing{InputCostPerToken: 10e-6, CacheCreationInputTokenCost: 12.5e-6}
	svc.pricingData["gpt-6-astra-project"] = astra
	require.Same(t, astra, svc.GetModelPricing("gpt-6-astra-project"))
}
