package service

// Standard API rates verified on 2026-09-23 at:
// https://developers.openai.com/api/docs/models/gpt-6-sol
// https://developers.openai.com/api/docs/models/gpt-6-luna
// Cache writes are 1.25x input; Fast is 2x. The >272K surcharge applies to
// the entire request's input/cache (2x) and output (1.5x).
var (
	openAIGPT6SolFallbackPricing  = newGPT6NamedFallbackPricing(2e-6, 10e-6)
	openAIGPT6LunaFallbackPricing = newGPT6NamedFallbackPricing(0.1e-6, 0.5e-6)
)

func newGPT6NamedFallbackPricing(input, output float64) *LiteLLMModelPricing {
	return &LiteLLMModelPricing{
		InputCostPerToken: input, InputCostPerTokenPriority: input * 2,
		OutputCostPerToken: output, OutputCostPerTokenPriority: output * 2,
		CacheCreationInputTokenCost: input * 1.25, CacheCreationInputTokenCostPriority: input * 2.5,
		CacheReadInputTokenCost: input * 0.1, CacheReadInputTokenCostPriority: input * 0.2,
		LongContextInputTokenThreshold: 272000,
		LongContextInputCostMultiplier: 2, LongContextOutputCostMultiplier: 1.5,
		SupportsServiceTier: true, SupportsPromptCaching: true,
		LiteLLMProvider: "openai", Mode: "chat",
	}
}
