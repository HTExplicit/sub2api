//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func cindyImagePricingAccount() *Account {
	return &Account{
		Platform:        PlatformCindy,
		WirePlatform:    WirePlatformOpenAI,
		ProviderProfile: ProviderProfileCindyLaxaV1,
		Type:            AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://api.laxarouter.ai",
		},
	}
}

func TestOpenAIImagesRequestInputImageCount_ExcludesGenerationAndMask(t *testing.T) {
	t.Parallel()
	request := &OpenAIImagesRequest{
		Endpoint:       openAIImagesEditsEndpoint,
		InputImageURLs: []string{"https://example.invalid/reference.png"},
		Uploads:        []OpenAIImagesUpload{{FieldName: "image"}},
		MaskUpload:     &OpenAIImagesUpload{FieldName: "mask"},
	}
	require.Equal(t, 2, request.InputImageCount())
	request.Endpoint = openAIImagesGenerationsEndpoint
	require.Zero(t, request.InputImageCount())
}

func TestValidateCindyImageRequest_RejectsUnavailableFreePoolImageCandidates(t *testing.T) {
	t.Parallel()

	request := &OpenAIImagesRequest{
		Endpoint:       openAIImagesGenerationsEndpoint,
		N:              1,
		Size:           "1024x1024",
		Quality:        "low",
		ResponseFormat: "b64_json",
	}
	for _, model := range []string{
		"gpt-image-2", "openai/gpt-image-2",
		"gemini-3-pro-image", "google/gemini-3-pro-image",
	} {
		err := ValidateCindyImageRequest(model, request)
		require.ErrorContains(t, err, "no verified Cindy image capability", model)
	}
}

func TestCalculateOpenAIImageCost_StrictCindyRejectsUnavailableGPTImageCandidate(t *testing.T) {
	t.Parallel()
	svc := &OpenAIGatewayService{billingService: NewBillingService(&config.Config{}, nil)}
	result := &OpenAIForwardResult{ImageCount: 1, ImageSize: ImageBillingSize2K}
	tokens := UsageTokens{OutputTokens: 1000, ImageOutputTokens: 1000}

	cost, err := svc.calculateOpenAIImageCost(
		context.Background(), "openai/gpt-image-2", &APIKey{Group: &Group{}},
		cindyImagePricingAccount(), result, tokens, 1,
	)

	require.Nil(t, cost)
	require.ErrorIs(t, err, ErrModelPricingUnavailable)
}

func TestCalculateOpenAIImageCost_StrictCindyRejectsUnavailableGPTImagePublicID(t *testing.T) {
	t.Parallel()
	svc := &OpenAIGatewayService{billingService: NewBillingService(&config.Config{}, nil)}
	result := &OpenAIForwardResult{ImageCount: 1, ImageSize: ImageBillingSize2K}
	tokens := UsageTokens{OutputTokens: 100, ImageOutputTokens: 40}

	cost, err := svc.calculateOpenAIImageCost(
		context.Background(), "gpt-image-2", &APIKey{Group: &Group{}},
		cindyImagePricingAccount(), result, tokens, 1,
	)

	require.Nil(t, cost)
	require.ErrorIs(t, err, ErrModelPricingUnavailable)
}

func TestCalculateOpenAIImageCost_StrictCindyRejectsUnavailableGeminiImageCandidate(t *testing.T) {
	t.Parallel()
	svc := &OpenAIGatewayService{billingService: NewBillingService(&config.Config{}, nil)}
	result := &OpenAIForwardResult{ImageCount: 1, ImageSize: ImageBillingSize2K}

	cost, err := svc.calculateOpenAIImageCost(
		context.Background(), "google/gemini-3-pro-image", &APIKey{Group: &Group{}},
		cindyImagePricingAccount(), result, UsageTokens{}, 1,
	)

	require.Nil(t, cost)
	require.ErrorIs(t, err, ErrModelPricingUnavailable)
}

func TestCalculateOpenAIImageCost_StrictCindyGroupPriceWins(t *testing.T) {
	t.Parallel()
	svc := &OpenAIGatewayService{billingService: NewBillingService(&config.Config{}, nil)}
	price := 0.05
	apiKey := &APIKey{Group: &Group{ImagePrice2K: &price}}
	result := &OpenAIForwardResult{ImageCount: 1, ImageSize: ImageBillingSize2K}

	cost, err := svc.calculateOpenAIImageCost(
		context.Background(), "gpt-image-2", apiKey, cindyImagePricingAccount(),
		result, UsageTokens{OutputTokens: 1000, ImageOutputTokens: 1000}, 1,
	)

	require.NoError(t, err)
	require.InDelta(t, price, cost.TotalCost, 1e-12)
}

func TestCalculateOpenAIImageCost_StrictCindyMissingOutputMeterFailsClosed(t *testing.T) {
	t.Parallel()
	svc := &OpenAIGatewayService{billingService: NewBillingService(&config.Config{}, nil)}
	result := &OpenAIForwardResult{ImageCount: 1, ImageSize: ImageBillingSize2K}

	cost, err := svc.calculateOpenAIImageCost(
		context.Background(), "gpt-image-2", &APIKey{Group: &Group{}},
		cindyImagePricingAccount(), result, UsageTokens{}, 1,
	)

	require.Nil(t, cost)
	require.ErrorIs(t, err, ErrModelPricingUnavailable)
}

func TestCalculateCindyCatalogImageCostMissingInputTokenMeterFailsClosed(t *testing.T) {
	t.Parallel()
	svc := &OpenAIGatewayService{billingService: NewBillingService(&config.Config{}, nil)}

	cost, err := svc.calculateCindyCatalogImageCost(
		"gemini-3-pro-image",
		&OpenAIForwardResult{ImageCount: 1, ImageInputCount: 1},
		UsageTokens{InputTokens: 1, ImageInputTokens: 1},
		1,
		CindyImagePricing{OutputCostPerImage: 0.134, InputCostPerImage: 0.0011},
	)

	require.Nil(t, cost)
	require.ErrorIs(t, err, ErrModelPricingUnavailable)
}

func TestCalculateOpenAIImageCost_NonCindyKeepsGenericFallback(t *testing.T) {
	t.Parallel()
	svc := &OpenAIGatewayService{billingService: NewBillingService(&config.Config{}, nil)}
	ordinary := &Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.openai.com"},
	}
	result := &OpenAIForwardResult{ImageCount: 1, ImageSize: ImageBillingSize2K}

	cost, err := svc.calculateOpenAIImageCost(
		context.Background(), "unknown-image-model", &APIKey{Group: &Group{}},
		ordinary, result, UsageTokens{}, 1,
	)

	require.NoError(t, err)
	require.InDelta(t, defaultImageGenerationPrice*1.5, cost.TotalCost, 1e-12)
}
