//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConversationStandardPricingExactFallbackAndDynamicPriority(t *testing.T) {
	bs := &BillingService{fallbackPrices: make(map[string]*ModelPricing)}
	bs.initFallbackPricing()
	for _, row := range []struct {
		model, namespace           string
		input, output, read, write float64
	}{
		{"hy3", "tencent", .132, .528, .033, 0}, {"hy4-preview", "tencent", .834, 2.501, .042, 0},
		{"qwen3.8-27b", "qwen", .5, 3, .1, .625}, {"qwen3.8-flash", "qwen", .15, .47, .016, .2},
		{"qwen3.8-omni-flash", "qwen", .15, .47, .016, 0}, {"muse-spark-1.3", "meta", 1.25, 4.25, .15, 0},
	} {
		for _, model := range []string{row.model, row.namespace + "/" + row.model} {
			price, err := bs.GetModelPricing(model)
			require.NoError(t, err, model)
			require.InDelta(t, row.input/1e6, price.InputPricePerToken, 1e-14)
			require.InDelta(t, row.output/1e6, price.OutputPricePerToken, 1e-14)
			require.InDelta(t, row.read/1e6, price.CacheReadPricePerToken, 1e-14)
			require.InDelta(t, row.write/1e6, price.CacheCreationPricePerToken, 1e-14)
		}
	}
	for _, unknown := range []string{"qwen3.8-unknown", "qwen3.8-flash-next", "hy4-preview-unknown", "muse-spark-1.3-contributor", "other/hy3"} {
		_, err := bs.GetModelPricing(unknown)
		require.ErrorIs(t, err, ErrModelPricingUnavailable, unknown)
	}
	bs.pricingService = newStubPricingServiceFromJSON(t, `{"hy3":{"input_cost_per_token":0.000001,"output_cost_per_token":0.000002,"litellm_provider":"tencent","mode":"chat"}}`)
	price, err := bs.GetModelPricing("hy3")
	require.NoError(t, err)
	require.Equal(t, 1e-6, price.InputPricePerToken)
}

func TestTokenAllowlistOnlyPreservesDefaultPricing(t *testing.T) {
	bs := newTestBillingServiceForResolver()
	base := bs.fallbackPrices["claude-sonnet-4"]
	fast, flex := 2.0, .5
	base.FastMultiplier, base.FlexMultiplier = &fast, &flex
	base.ReasoningEffortMultipliers = map[string]float64{"high": 1.5}
	card := ChannelModelPricing{Platform: PlatformOpenAI, Models: []string{"claude-sonnet-4"}, BillingMode: BillingModeToken}
	require.True(t, card.IsTokenAllowlistOnly())
	cs := newChannelServiceWithPricings(12, []ChannelModelPricing{card})
	groupID := int64(12)
	resolved := NewModelPricingResolver(cs, bs).Resolve(context.Background(), PricingInput{Model: "claude-sonnet-4", GroupID: &groupID})
	require.Equal(t, PricingSourceLiteLLM, resolved.Source)
	require.Equal(t, base, resolved.BasePricing)
	price, err := bs.GetModelPricingWithChannel("claude-sonnet-4", &card)
	require.NoError(t, err)
	require.Equal(t, base, price)
	zero := 0.0
	card.InputPrice = &zero
	require.False(t, card.IsTokenAllowlistOnly())
	price, err = bs.GetModelPricingWithChannel("claude-sonnet-4", &card)
	require.NoError(t, err)
	require.Zero(t, price.InputPricePerToken)
	require.Equal(t, 3e-6, base.InputPricePerToken, "explicit overrides do not mutate shared defaults")
}
