package service

import (
	"fmt"
	"math"
)

// calculateCindyCatalogTextCost prices strict Cindy token usage from the
// versioned capability catalog. The catalog records standard, non-batch rates,
// so upstream service-tier labels must not synthesize an unverified discount or
// surcharge here.
func calculateCindyCatalogTextCost(
	billingService *BillingService,
	model string,
	tokens UsageTokens,
	rateMultiplier float64,
	serviceTier string,
	longContextBillingEnabled bool,
) (*CostBreakdown, error) {
	if billingService == nil {
		return nil, fmt.Errorf("%w for strict Cindy text model %q: billing service is unavailable", ErrModelPricingUnavailable, model)
	}

	pricing, ok := CindyTextPricingForModel(model)
	if !ok {
		pricing, ok = CindyCompatibilityTextPricingForModel(model)
	}
	if !ok {
		if CindyModelUsesExplicitZeroPrice(model) {
			return &CostBreakdown{BillingMode: string(BillingModeToken)}, nil
		}
		return nil, fmt.Errorf("%w for strict Cindy text model %q", ErrModelPricingUnavailable, model)
	}
	discount, err := cindyCatalogCostDiscount(model)
	if err != nil {
		return nil, err
	}
	if discount == 1 {
		return &CostBreakdown{BillingMode: string(BillingModeToken)}, nil
	}
	pricing = applyCindyCatalogCostDiscount(pricing, discount)
	pricing, err = cindyTextPricingForServiceTier(model, pricing, serviceTier)
	if err != nil {
		return nil, err
	}
	// Cindy schema-v4 intentionally has no separate cache-write field for the
	// GPT-5.6 family. The Responses/WS usage contract still reports cache-write
	// tokens as a disjoint bucket, so apply the same established GPT-5.6 policy
	// used by the generic OpenAI billing path: input price × 1.25. This is a
	// downstream protocol-billing rule, not a mutation of the pinned v4 fixture.
	// Other models with an absent cache-write price remain fail-closed.
	if (tokens.CacheCreationTokens > 0 || tokens.CacheCreation5mTokens > 0 || tokens.CacheCreation1hTokens > 0) &&
		!pricing.CacheCreationInputTokenCostPresent {
		pricing = applyCindyGPT56CacheCreationFallback(model, pricing)
	}
	if (tokens.CacheCreationTokens > 0 || tokens.CacheCreation5mTokens > 0 || tokens.CacheCreation1hTokens > 0) &&
		!pricing.CacheCreationInputTokenCostPresent {
		return nil, fmt.Errorf("%w for strict Cindy text model %q: cache-creation price is absent", ErrModelPricingUnavailable, model)
	}

	modelPricing, err := cindyTextPricingAsModelPricing(model, pricing)
	if err != nil {
		return nil, err
	}
	cost := billingService.computeTokenBreakdown(modelPricing, tokens, rateMultiplier, "", longContextBillingEnabled)
	cost.BillingMode = string(BillingModeToken)
	return cost, nil
}

func applyCindyGPT56CacheCreationFallback(model string, pricing CindyTextPricing) CindyTextPricing {
	capability, ok := resolveKnownCindyCapability(model)
	if !ok {
		return pricing
	}
	switch capability.PublicID {
	case "gpt-5.6-luna":
	default:
		return pricing
	}
	if pricing.InputCostPerToken <= 0 {
		return pricing
	}
	pricing.CacheCreationInputTokenCost = pricing.InputCostPerToken * 1.25
	pricing.CacheCreationInputTokenCostPresent = true
	if pricing.LongContextInputTokenThreshold > 0 && pricing.LongContextInputCostPerToken > 0 {
		pricing.LongContextCacheCreationTokenCost = pricing.LongContextInputCostPerToken * 1.25
	}
	return pricing
}

func cindyCatalogCostDiscount(model string) (float64, error) {
	capability, ok := resolveKnownCindyCapability(model)
	if !ok {
		return 0, nil
	}
	if capability.CostDiscount < 0 || capability.CostDiscount > 1 {
		return 0, fmt.Errorf("%w for strict Cindy text model %q: catalog discount is outside [0,1]", ErrModelPricingUnavailable, model)
	}
	return capability.CostDiscount, nil
}

func applyCindyCatalogCostDiscount(pricing CindyTextPricing, discount float64) CindyTextPricing {
	factor := 1 - discount
	pricing.InputCostPerToken *= factor
	pricing.OutputCostPerToken *= factor
	pricing.InputCostPerTokenPriority *= factor
	pricing.OutputCostPerTokenPriority *= factor
	pricing.CacheReadInputTokenCost *= factor
	pricing.CacheReadInputTokenCostPriority *= factor
	pricing.CacheCreationInputTokenCost *= factor
	pricing.CacheCreationInputTokenCostPriority *= factor
	pricing.CacheCreationInputTokenCostAbove1hr *= factor
	pricing.InputCostPerAudioToken *= factor
	pricing.LongContextInputCostPerToken *= factor
	pricing.LongContextOutputCostPerToken *= factor
	pricing.LongContextCacheReadInputTokenCost *= factor
	pricing.LongContextCacheCreationTokenCost *= factor
	pricing.LongContextInputCostPerTokenPriority *= factor
	pricing.LongContextOutputCostPerTokenPriority *= factor
	pricing.LongContextCacheReadInputTokenCostPriority *= factor
	return pricing
}

func cindyTextPricingForServiceTier(model string, pricing CindyTextPricing, serviceTier string) (CindyTextPricing, error) {
	switch normalizeBillingServiceTier(serviceTier) {
	case "", "auto", "default", "scale":
		return pricing, nil
	case "priority", "fast":
		if pricing.InputCostPerTokenPriority <= 0 || pricing.OutputCostPerTokenPriority <= 0 ||
			pricing.CacheReadInputTokenCostPriority <= 0 {
			return CindyTextPricing{}, fmt.Errorf("%w for strict Cindy text model %q: priority prices are incomplete", ErrModelPricingUnavailable, model)
		}
		pricing.InputCostPerToken = pricing.InputCostPerTokenPriority
		pricing.OutputCostPerToken = pricing.OutputCostPerTokenPriority
		pricing.CacheReadInputTokenCost = pricing.CacheReadInputTokenCostPriority
		if pricing.CacheCreationInputTokenCostPriority > 0 {
			pricing.CacheCreationInputTokenCost = pricing.CacheCreationInputTokenCostPriority
			pricing.CacheCreationInputTokenCostPresent = true
		}
		if pricing.LongContextInputTokenThreshold > 0 {
			if pricing.LongContextInputCostPerTokenPriority <= 0 || pricing.LongContextOutputCostPerTokenPriority <= 0 ||
				pricing.LongContextCacheReadInputTokenCostPriority <= 0 {
				return CindyTextPricing{}, fmt.Errorf("%w for strict Cindy text model %q: priority long-context prices are incomplete", ErrModelPricingUnavailable, model)
			}
			pricing.LongContextInputCostPerToken = pricing.LongContextInputCostPerTokenPriority
			pricing.LongContextOutputCostPerToken = pricing.LongContextOutputCostPerTokenPriority
			pricing.LongContextCacheReadInputTokenCost = pricing.LongContextCacheReadInputTokenCostPriority
		}
		return pricing, nil
	default:
		return CindyTextPricing{}, fmt.Errorf("%w for strict Cindy text model %q: service tier %q has no exact catalog price", ErrModelPricingUnavailable, model, serviceTier)
	}
}

func cindyTextPricingAsModelPricing(model string, pricing CindyTextPricing) (*ModelPricing, error) {
	if pricing.InputCostPerToken < 0 || pricing.OutputCostPerToken < 0 ||
		pricing.CacheReadInputTokenCost < 0 || pricing.CacheCreationInputTokenCost < 0 ||
		pricing.CacheCreationInputTokenCostAbove1hr < 0 {
		return nil, fmt.Errorf("%w for strict Cindy text model %q: catalog contains a negative price", ErrModelPricingUnavailable, model)
	}
	if pricing.InputCostPerToken == 0 && pricing.OutputCostPerToken == 0 {
		return nil, fmt.Errorf("%w for strict Cindy text model %q: catalog token prices are empty", ErrModelPricingUnavailable, model)
	}

	converted := &ModelPricing{
		InputPricePerToken:         pricing.InputCostPerToken,
		OutputPricePerToken:        pricing.OutputCostPerToken,
		CacheReadPricePerToken:     pricing.CacheReadInputTokenCost,
		CacheCreationPricePerToken: pricing.CacheCreationInputTokenCost,
		CacheCreationPriceExplicit: true,
	}
	if pricing.CacheCreationInputTokenCostAbove1hr > 0 {
		if pricing.CacheCreationInputTokenCost <= 0 {
			return nil, fmt.Errorf("%w for strict Cindy text model %q: 1h cache price has no 5m base price", ErrModelPricingUnavailable, model)
		}
		converted.SupportsCacheBreakdown = true
		converted.CacheCreation5mPrice = pricing.CacheCreationInputTokenCost
		converted.CacheCreation1hPrice = pricing.CacheCreationInputTokenCostAbove1hr
	}

	if pricing.LongContextInputTokenThreshold <= 0 {
		if pricing.LongContextInputCostPerToken != 0 || pricing.LongContextOutputCostPerToken != 0 ||
			pricing.LongContextCacheReadInputTokenCost != 0 || pricing.LongContextCacheCreationTokenCost != 0 {
			return nil, fmt.Errorf("%w for strict Cindy text model %q: long-context prices have no threshold", ErrModelPricingUnavailable, model)
		}
		return converted, nil
	}
	if pricing.InputCostPerToken <= 0 || pricing.OutputCostPerToken <= 0 ||
		pricing.LongContextInputCostPerToken <= 0 || pricing.LongContextOutputCostPerToken <= 0 {
		return nil, fmt.Errorf("%w for strict Cindy text model %q: long-context prices are incomplete", ErrModelPricingUnavailable, model)
	}

	inputMultiplier := pricing.LongContextInputCostPerToken / pricing.InputCostPerToken
	outputMultiplier := pricing.LongContextOutputCostPerToken / pricing.OutputCostPerToken
	if inputMultiplier <= 0 || outputMultiplier <= 0 {
		return nil, fmt.Errorf("%w for strict Cindy text model %q: long-context multipliers are invalid", ErrModelPricingUnavailable, model)
	}
	if pricing.CacheReadInputTokenCost > 0 {
		if pricing.LongContextCacheReadInputTokenCost <= 0 {
			return nil, fmt.Errorf("%w for strict Cindy text model %q: long-context cache-read price is missing", ErrModelPricingUnavailable, model)
		}
		cacheMultiplier := pricing.LongContextCacheReadInputTokenCost / pricing.CacheReadInputTokenCost
		if !cindyPriceMultipliersEqual(cacheMultiplier, inputMultiplier) {
			return nil, fmt.Errorf("%w for strict Cindy text model %q: long-context cache-read multiplier cannot be represented", ErrModelPricingUnavailable, model)
		}
	} else if pricing.LongContextCacheReadInputTokenCost != 0 {
		return nil, fmt.Errorf("%w for strict Cindy text model %q: long-context cache-read price has no base price", ErrModelPricingUnavailable, model)
	}
	if pricing.CacheCreationInputTokenCost > 0 {
		if pricing.LongContextCacheCreationTokenCost <= 0 {
			return nil, fmt.Errorf("%w for strict Cindy text model %q: long-context cache-write price is missing", ErrModelPricingUnavailable, model)
		}
		cacheCreationMultiplier := pricing.LongContextCacheCreationTokenCost / pricing.CacheCreationInputTokenCost
		if !cindyPriceMultipliersEqual(cacheCreationMultiplier, inputMultiplier) {
			return nil, fmt.Errorf("%w for strict Cindy text model %q: long-context cache-write multiplier cannot be represented", ErrModelPricingUnavailable, model)
		}
	} else if pricing.LongContextCacheCreationTokenCost != 0 {
		return nil, fmt.Errorf("%w for strict Cindy text model %q: long-context cache-write price has no base price", ErrModelPricingUnavailable, model)
	}

	converted.LongContextInputThreshold = pricing.LongContextInputTokenThreshold
	converted.LongContextThresholdInclusive = pricing.LongContextThresholdInclusive
	converted.LongContextInputMultiplier = inputMultiplier
	converted.LongContextOutputMultiplier = outputMultiplier
	return converted, nil
}

func cindyPriceMultipliersEqual(left, right float64) bool {
	scale := math.Max(math.Abs(left), math.Abs(right))
	if scale < 1 {
		scale = 1
	}
	return math.Abs(left-right) <= scale*1e-12
}
