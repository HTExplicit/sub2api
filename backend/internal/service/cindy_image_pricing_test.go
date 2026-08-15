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
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
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

func TestValidateCindyImageRequest_UsesEndpointSpecificVerifiedControls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		model   string
		request *OpenAIImagesRequest
		wantErr string
	}{
		{
			name:  "GPT generation accepts the verified single output",
			model: "gpt-image-2",
			request: &OpenAIImagesRequest{
				Endpoint:       openAIImagesGenerationsEndpoint,
				N:              1,
				Size:           "1024x1024",
				Quality:        "low",
				ResponseFormat: "b64_json",
			},
		},
		{
			name:  "GPT generation rejects an unverified second output",
			model: "gpt-image-2",
			request: &OpenAIImagesRequest{
				Endpoint: openAIImagesGenerationsEndpoint,
				N:        2,
				Size:     "1024x1024",
				Quality:  "low",
			},
			wantErr: "between 1 and 1",
		},
		{
			name:  "GPT generation rejects zero outputs",
			model: "gpt-image-2",
			request: &OpenAIImagesRequest{
				Endpoint: openAIImagesGenerationsEndpoint,
				N:        0,
				Size:     "1024x1024",
				Quality:  "low",
			},
			wantErr: "between 1 and 1",
		},
		{
			name:  "GPT edit remains unavailable",
			model: "gpt-image-2",
			request: &OpenAIImagesRequest{
				Endpoint: openAIImagesEditsEndpoint,
				N:        1,
				Size:     "1024x1024",
				Quality:  "low",
				Uploads:  []OpenAIImagesUpload{{FieldName: "image"}},
			},
			wantErr: "not verified for images.edits",
		},
		{
			name:  "Gemini generation is fixed to one output",
			model: "gemini-3-pro-image",
			request: &OpenAIImagesRequest{
				Endpoint: openAIImagesGenerationsEndpoint,
				N:        2,
				Size:     "1024x1024",
				Quality:  "low",
			},
			wantErr: "between 1 and 1",
		},
		{
			name:  "Gemini edit accepts reference and mask",
			model: "google/gemini-3-pro-image",
			request: &OpenAIImagesRequest{
				Endpoint:   openAIImagesEditsEndpoint,
				N:          1,
				Size:       "1024x1024",
				Quality:    "low",
				Uploads:    []OpenAIImagesUpload{{FieldName: "image"}},
				HasMask:    true,
				MaskUpload: &OpenAIImagesUpload{FieldName: "mask"},
			},
		},
		{
			name:  "unverified size fails closed",
			model: "gemini-3-pro-image",
			request: &OpenAIImagesRequest{
				Endpoint: openAIImagesGenerationsEndpoint,
				N:        1,
				Size:     "2048x2048",
				Quality:  "low",
			},
			wantErr: "size",
		},
		{
			name:  "URL response format fails closed",
			model: "gpt-image-2",
			request: &OpenAIImagesRequest{
				Endpoint:       openAIImagesGenerationsEndpoint,
				N:              1,
				Size:           "1024x1024",
				Quality:        "low",
				ResponseFormat: "url",
			},
			wantErr: "b64_json",
		},
		{
			name:  "streaming fails closed",
			model: "gpt-image-2",
			request: &OpenAIImagesRequest{
				Endpoint: openAIImagesGenerationsEndpoint,
				N:        1,
				Size:     "1024x1024",
				Quality:  "low",
				Stream:   true,
			},
			wantErr: "stream",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateCindyImageRequest(test.model, test.request)
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestCalculateOpenAIImageCost_StrictCindyUsesCatalogTokenPrice(t *testing.T) {
	t.Parallel()
	svc := &OpenAIGatewayService{billingService: NewBillingService(&config.Config{}, nil)}
	result := &OpenAIForwardResult{ImageCount: 1, ImageSize: ImageBillingSize2K}
	tokens := UsageTokens{OutputTokens: 1000, ImageOutputTokens: 1000}

	cost, err := svc.calculateOpenAIImageCost(
		context.Background(), "openai/gpt-image-2", &APIKey{Group: &Group{}},
		cindyImagePricingAccount(), result, tokens, 1,
	)

	require.NoError(t, err)
	require.InDelta(t, 0.03, cost.TotalCost, 1e-12)
	require.InDelta(t, 0.03, cost.ActualCost, 1e-12)
	require.Equal(t, string(BillingModeImage), cost.BillingMode)
	require.NotEqual(t, defaultImageGenerationPrice*1.5, cost.TotalCost)
}

func TestCalculateOpenAIImageCost_GPTImage2BillsTextAndImageOutputTokens(t *testing.T) {
	t.Parallel()
	svc := &OpenAIGatewayService{billingService: NewBillingService(&config.Config{}, nil)}
	result := &OpenAIForwardResult{ImageCount: 1, ImageSize: ImageBillingSize2K}
	tokens := UsageTokens{OutputTokens: 100, ImageOutputTokens: 40}

	cost, err := svc.calculateOpenAIImageCost(
		context.Background(), "gpt-image-2", &APIKey{Group: &Group{}},
		cindyImagePricingAccount(), result, tokens, 1,
	)

	require.NoError(t, err)
	require.InDelta(t, 60*10e-6, cost.OutputCost, 1e-12)
	require.InDelta(t, 40*30e-6, cost.ImageOutputCost, 1e-12)
	require.InDelta(t, 0.0018, cost.TotalCost, 1e-12)
	require.InDelta(t, cost.TotalCost, cost.ActualCost, 1e-12)
}

func TestCalculateOpenAIImageCost_StrictCindyUsesCatalogPerImagePrice(t *testing.T) {
	t.Parallel()
	svc := &OpenAIGatewayService{billingService: NewBillingService(&config.Config{}, nil)}
	result := &OpenAIForwardResult{ImageCount: 1, ImageSize: ImageBillingSize2K}

	cost, err := svc.calculateOpenAIImageCost(
		context.Background(), "google/gemini-3-pro-image", &APIKey{Group: &Group{}},
		cindyImagePricingAccount(), result, UsageTokens{}, 1,
	)

	require.NoError(t, err)
	require.InDelta(t, 0.134, cost.TotalCost, 1e-12)
	require.InDelta(t, 0.134, cost.ActualCost, 1e-12)

	result.ImageCount = 1
	result.ImageInputCount = 1
	result.ImageSize = ImageBillingSize4K
	cost, err = svc.calculateOpenAIImageCost(
		context.Background(), "gemini-3-pro-image", &APIKey{Group: &Group{}},
		cindyImagePricingAccount(), result, UsageTokens{}, 1,
	)
	require.NoError(t, err)
	require.InDelta(t, 0.2411, cost.TotalCost, 1e-12)
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
