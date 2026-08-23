package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCalculateCindyCatalogTextCostUsesTargetPricingAndCacheTiers(t *testing.T) {
	t.Parallel()
	billingService := NewBillingService(&config.Config{}, nil)

	t.Run("hidden alias uses mapped target price", func(t *testing.T) {
		tokens := UsageTokens{
			InputTokens:         100,
			OutputTokens:        10,
			CacheCreationTokens: 30,
			CacheReadTokens:     20,
		}
		cost, err := calculateCindyCatalogTextCost(billingService, "gpt-5.4", tokens, 1.5, true)
		require.NoError(t, err)
		require.Equal(t, string(BillingModeToken), cost.BillingMode)
		require.InDelta(t, 100*5e-6, cost.InputCost, 1e-12)
		require.InDelta(t, 10*30e-6, cost.OutputCost, 1e-12)
		require.InDelta(t, 30*6.25e-6, cost.CacheCreationCost, 1e-12)
		require.InDelta(t, 20*0.5e-6, cost.CacheReadCost, 1e-12)
		require.InDelta(t, cost.TotalCost*1.5, cost.ActualCost, 1e-12)
	})

	t.Run("Anthropic 5m and 1h cache creation remain distinct", func(t *testing.T) {
		tokens := UsageTokens{
			CacheCreationTokens:   15,
			CacheCreation5mTokens: 10,
			CacheCreation1hTokens: 5,
		}
		cost, err := calculateCindyCatalogTextCost(billingService, "anthropic/claude-opus-5", tokens, 1, true)
		require.NoError(t, err)
		require.InDelta(t, 10*6.25e-6+5*10e-6, cost.CacheCreationCost, 1e-12)
	})
}

func TestCalculateCindyCatalogTextCostHonorsLongContextPolicy(t *testing.T) {
	t.Parallel()
	billingService := NewBillingService(&config.Config{}, nil)
	tokens := UsageTokens{InputTokens: 100000, CacheReadTokens: 100000, OutputTokens: 10}

	standard, err := calculateCindyCatalogTextCost(billingService, "x-ai/grok-4.6", tokens, 1, false)
	require.NoError(t, err)
	require.False(t, standard.LongContextBillingApplied)
	require.InDelta(t, 100000*2e-6, standard.InputCost, 1e-12)
	require.InDelta(t, 100000*0.5e-6, standard.CacheReadCost, 1e-12)

	longContext, err := calculateCindyCatalogTextCost(billingService, "x-ai/grok-4.6", tokens, 1, true)
	require.NoError(t, err)
	require.True(t, longContext.LongContextBillingApplied, "xAI's 200k threshold is inclusive")
	require.InDelta(t, 100000*4e-6, longContext.InputCost, 1e-12)
	require.InDelta(t, 100000*1e-6, longContext.CacheReadCost, 1e-12)
	require.InDelta(t, 10*12e-6, longContext.OutputCost, 1e-12)
}

func TestCalculateCindyCatalogTextCostOpenAI272KBoundary(t *testing.T) {
	t.Parallel()
	billingService := NewBillingService(&config.Config{}, nil)

	tests := []struct {
		model          string
		baseInput      float64
		baseCacheRead  float64
		baseCacheWrite float64
		baseOutput     float64
		longInput      float64
		longCacheRead  float64
		longCacheWrite float64
		longOutput     float64
	}{
		{model: "gpt-5.6-luna", baseInput: 0.2e-6, baseCacheRead: 0.02e-6, baseCacheWrite: 0.25e-6, baseOutput: 1.2e-6, longInput: 0.4e-6, longCacheRead: 0.04e-6, longCacheWrite: 0.5e-6, longOutput: 1.8e-6},
		{model: "openai/gpt-5.6-sol", baseInput: 5e-6, baseCacheRead: 0.5e-6, baseCacheWrite: 6.25e-6, baseOutput: 30e-6, longInput: 10e-6, longCacheRead: 1e-6, longCacheWrite: 12.5e-6, longOutput: 45e-6},
		{model: "gpt-5.6-terra", baseInput: 2e-6, baseCacheRead: 0.2e-6, baseCacheWrite: 2.5e-6, baseOutput: 12e-6, longInput: 4e-6, longCacheRead: 0.4e-6, longCacheWrite: 5e-6, longOutput: 18e-6},
	}

	for _, test := range tests {
		t.Run(test.model, func(t *testing.T) {
			atBoundary := UsageTokens{InputTokens: 100000, CacheReadTokens: 100000, CacheCreationTokens: 72000, OutputTokens: 11}
			base, err := calculateCindyCatalogTextCost(billingService, test.model, atBoundary, 1, true)
			require.NoError(t, err)
			require.False(t, base.LongContextBillingApplied, "272000 input tokens remain in the base tier")
			require.InDelta(t, float64(atBoundary.InputTokens)*test.baseInput, base.InputCost, 1e-12)
			require.InDelta(t, float64(atBoundary.CacheReadTokens)*test.baseCacheRead, base.CacheReadCost, 1e-12)
			require.InDelta(t, float64(atBoundary.CacheCreationTokens)*test.baseCacheWrite, base.CacheCreationCost, 1e-12)
			require.InDelta(t, float64(atBoundary.OutputTokens)*test.baseOutput, base.OutputCost, 1e-12)

			overBoundary := atBoundary
			overBoundary.InputTokens++
			long, err := calculateCindyCatalogTextCost(billingService, test.model, overBoundary, 1, true)
			require.NoError(t, err)
			require.True(t, long.LongContextBillingApplied, "272001 input tokens switch the full request to the long tier")
			require.InDelta(t, float64(overBoundary.InputTokens)*test.longInput, long.InputCost, 1e-12)
			require.InDelta(t, float64(overBoundary.CacheReadTokens)*test.longCacheRead, long.CacheReadCost, 1e-12)
			require.InDelta(t, float64(overBoundary.CacheCreationTokens)*test.longCacheWrite, long.CacheCreationCost, 1e-12)
			require.InDelta(t, float64(overBoundary.OutputTokens)*test.longOutput, long.OutputCost, 1e-12)
		})
	}
}

func TestCindyTextPricingFailsClosedWhenLongCacheWritePriceIsMissing(t *testing.T) {
	t.Parallel()
	_, err := cindyTextPricingAsModelPricing("incomplete", CindyTextPricing{
		InputCostPerToken:              1,
		OutputCostPerToken:             1,
		CacheCreationInputTokenCost:    1,
		LongContextInputTokenThreshold: 10,
		LongContextInputCostPerToken:   2,
		LongContextOutputCostPerToken:  2,
	})
	require.ErrorIs(t, err, ErrModelPricingUnavailable)
	require.ErrorContains(t, err, "long-context cache-write price is missing")
}

func TestCalculateCindyCatalogTextCostOnlyAllowsExplicitZero(t *testing.T) {
	t.Parallel()
	billingService := NewBillingService(&config.Config{}, nil)

	zero, err := calculateCindyCatalogTextCost(billingService, "seed-2.1-pro", UsageTokens{InputTokens: 10}, 1, true)
	require.NoError(t, err)
	require.Zero(t, zero.TotalCost)
	require.Equal(t, string(BillingModeToken), zero.BillingMode)

	_, err = calculateCindyCatalogTextCost(billingService, "not-in-cindy-catalog", UsageTokens{InputTokens: 10}, 1, true)
	require.ErrorIs(t, err, ErrModelPricingUnavailable)
}

func TestOpenAIRecordUsageTokenCostPrefersExplicitGroupPricingForCindy(t *testing.T) {
	t.Parallel()
	inputPrice := 9e-6
	outputPrice := 11e-6
	groupID := int64(8801)
	billingService := NewBillingService(&config.Config{}, nil)
	svc := &OpenAIGatewayService{
		billingService: billingService,
		resolver:       NewModelPricingResolver(nil, billingService),
	}
	apiKey := &APIKey{
		GroupID: &groupID,
		Group: &Group{ID: groupID, ModelPricing: []ChannelModelPricing{{
			Models:      []string{"gpt-5.4"},
			BillingMode: BillingModeToken,
			InputPrice:  &inputPrice,
			OutputPrice: &outputPrice,
		}}},
	}

	cost, err := svc.calculateOpenAIRecordUsageTokenCost(
		context.Background(), apiKey, cindyTextBillingAccount(), "gpt-5.4", 1,
		time.Time{},
		UsageTokens{InputTokens: 100, OutputTokens: 10}, "", boolPtr(true),
	)
	require.NoError(t, err)
	require.InDelta(t, 100*inputPrice, cost.InputCost, 1e-12)
	require.InDelta(t, 10*outputPrice, cost.OutputCost, 1e-12)
}

func TestOpenAIRecordUsageTokenCostKeepsNonCindyPathUnchanged(t *testing.T) {
	t.Parallel()
	billingService := NewBillingService(&config.Config{}, nil)
	svc := &OpenAIGatewayService{billingService: billingService}
	tokens := UsageTokens{InputTokens: 100, OutputTokens: 10, CacheReadTokens: 5}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.openai.com"}}

	got, err := svc.calculateOpenAIRecordUsageTokenCost(context.Background(), &APIKey{}, account, "gpt-5.1", 1.25, time.Time{}, tokens, "priority", boolPtr(true))
	require.NoError(t, err)
	want, err := billingService.calculateCostWithServiceTierPolicy("gpt-5.1", tokens, 1.25, "priority", true)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestLegacyCindyRuntimeCompatibilityUsesExactModelPricing(t *testing.T) {
	t.Parallel()
	billingService := NewBillingService(&config.Config{}, nil)
	svc := &OpenAIGatewayService{billingService: billingService}
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "not-exposed",
			"base_url": "https://api.laxarouter.ai",
		},
	}

	for _, test := range []struct {
		model       string
		serviceTier string
		inputPrice  float64
		outputPrice float64
	}{
		{model: "gpt-5.4", inputPrice: 5e-6, outputPrice: 30e-6},
		{model: "gpt-5.4-mini", inputPrice: 0.2e-6, outputPrice: 1.2e-6},
		{model: "gpt-5.6-sol", serviceTier: "priority", inputPrice: 5e-6, outputPrice: 30e-6},
		{model: "gpt-5.6-luna", serviceTier: "priority", inputPrice: 0.2e-6, outputPrice: 1.2e-6},
		{model: "openai/gpt-5.6-sol", serviceTier: "priority", inputPrice: 5e-6, outputPrice: 30e-6},
		{model: "openai/gpt-5.6-luna", serviceTier: "priority", inputPrice: 0.2e-6, outputPrice: 1.2e-6},
	} {
		t.Run(test.model, func(t *testing.T) {
			cost, err := svc.calculateOpenAIRecordUsageTokenCost(
				context.Background(), &APIKey{}, account, test.model, 1, time.Time{},
				UsageTokens{InputTokens: 100, OutputTokens: 10}, test.serviceTier, boolPtr(false),
			)
			require.NoError(t, err)
			require.InDelta(t, 100*test.inputPrice, cost.InputCost, 1e-12)
			require.InDelta(t, 10*test.outputPrice, cost.OutputCost, 1e-12)
			require.True(t, shouldFailClosedCindyTextPricing(account, []string{test.model}), test.model)
		})
	}
}

func TestLegacyCindyRuntimeCompatibilityExplicitGroupPricingWins(t *testing.T) {
	t.Parallel()
	inputPrice := 9e-6
	outputPrice := 11e-6
	groupID := int64(8802)
	billingService := NewBillingService(&config.Config{}, nil)
	svc := &OpenAIGatewayService{
		billingService: billingService,
		resolver:       NewModelPricingResolver(nil, billingService),
	}
	apiKey := &APIKey{
		GroupID: &groupID,
		Group: &Group{ID: groupID, ModelPricing: []ChannelModelPricing{{
			Models:      []string{"openai/gpt-5.6-sol"},
			BillingMode: BillingModeToken,
			InputPrice:  &inputPrice,
			OutputPrice: &outputPrice,
		}}},
	}
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "not-exposed", "base_url": "https://api.laxarouter.ai",
		},
	}

	cost, err := svc.calculateOpenAIRecordUsageTokenCost(
		context.Background(), apiKey, account, "openai/gpt-5.6-sol", 1, time.Time{},
		UsageTokens{InputTokens: 100, OutputTokens: 10}, "", boolPtr(false),
	)
	require.NoError(t, err)
	require.InDelta(t, 100*inputPrice, cost.InputCost, 1e-12)
	require.InDelta(t, 10*outputPrice, cost.OutputCost, 1e-12)
}

func TestLegacyCindyRuntimeCompatibilityRecordUsageKeepsAliasPricingAfterLiveMapping(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		requested  string
		live       string
		inputCost  float64
		outputCost float64
	}{
		{requested: "gpt-5.4", live: "openai/gpt-5.6-sol", inputCost: 100 * 5e-6, outputCost: 10 * 30e-6},
		{requested: "gpt-5.4-mini", live: "openai/gpt-5.6-luna", inputCost: 100 * 0.2e-6, outputCost: 10 * 1.2e-6},
	} {
		t.Run(test.requested, func(t *testing.T) {
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Default.RateMultiplier = 1
			repo := &cindyTextUsageLogRepo{}
			svc := &OpenAIGatewayService{
				cfg:             cfg,
				billingService:  NewBillingService(cfg, nil),
				usageLogRepo:    repo,
				deferredService: &DeferredService{},
			}
			account := &Account{
				ID: 9902, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "not-exposed", "base_url": "https://api.laxarouter.ai"},
			}

			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{
					Model: test.requested, BillingModel: test.live, UpstreamModel: test.live,
					Usage: OpenAIUsage{InputTokens: 100, OutputTokens: 10},
				},
				APIKey: &APIKey{}, User: &User{ID: 1}, Account: account,
				ChannelUsageFields: ChannelUsageFields{OriginalModel: test.requested},
			})

			require.NoError(t, err)
			require.NotNil(t, repo.last)
			require.InDelta(t, test.inputCost, repo.last.InputCost, 1e-12)
			require.InDelta(t, test.outputCost, repo.last.OutputCost, 1e-12)
		})
	}
}

func TestOpenAIRecordUsageStrictCindyUnknownPriceFailsClosed(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	svc := &OpenAIGatewayService{cfg: cfg, billingService: NewBillingService(cfg, nil)}

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result:  &OpenAIForwardResult{Model: "not-in-cindy-catalog", Usage: OpenAIUsage{InputTokens: 1}},
		APIKey:  &APIKey{},
		User:    &User{},
		Account: cindyTextBillingAccount(),
	})
	require.True(t, errors.Is(err, ErrModelPricingUnavailable), err)
}

type cindyTextUsageLogRepo struct {
	UsageLogRepository
	last *UsageLog
}

func (r *cindyTextUsageLogRepo) Create(_ context.Context, log *UsageLog) (bool, error) {
	r.last = log
	return true, nil
}

func TestOpenAIRecordUsageCindyCatalogCoversResponsesAndNativeMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		result                *OpenAIForwardResult
		wantInputTokens       int
		wantInputCost         float64
		wantOutputCost        float64
		wantCacheCreationCost float64
		wantCacheReadCost     float64
	}{
		{
			name: "Responses total input is split into mutually exclusive buckets",
			result: &OpenAIForwardResult{
				Model:         "gpt-5.4",
				BillingModel:  "openai/gpt-5.6-sol",
				UpstreamModel: "openai/gpt-5.6-sol",
				Usage: OpenAIUsage{
					InputTokens: 130, OutputTokens: 5,
					CacheCreationInputTokens: 10, CacheReadInputTokens: 20,
				},
			},
			wantInputTokens:       100,
			wantInputCost:         100 * 5e-6,
			wantOutputCost:        5 * 30e-6,
			wantCacheCreationCost: 10 * 6.25e-6,
			wantCacheReadCost:     20 * 0.5e-6,
		},
		{
			name: "native Messages preserves disjoint input and cache TTL buckets",
			result: &OpenAIForwardResult{
				Model:                        "claude-opus-4-5-20251101",
				BillingModel:                 "anthropic/claude-opus-5",
				UpstreamModel:                "anthropic/claude-opus-5",
				UsageInputTokensExcludeCache: true,
				Usage: OpenAIUsage{
					InputTokens: 100, OutputTokens: 5,
					CacheCreationInputTokens: 10, CacheReadInputTokens: 20,
					CacheCreation5mTokens: 4, CacheCreation1hTokens: 6,
				},
			},
			wantInputTokens:       100,
			wantInputCost:         100 * 5e-6,
			wantOutputCost:        5 * 25e-6,
			wantCacheCreationCost: 4*6.25e-6 + 6*10e-6,
			wantCacheReadCost:     20 * 0.5e-6,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Default.RateMultiplier = 1
			repo := &cindyTextUsageLogRepo{}
			svc := &OpenAIGatewayService{
				cfg:             cfg,
				billingService:  NewBillingService(cfg, nil),
				usageLogRepo:    repo,
				deferredService: &DeferredService{},
			}
			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: test.result, APIKey: &APIKey{}, User: &User{ID: 1}, Account: cindyTextBillingAccount(),
			})
			require.NoError(t, err)
			require.NotNil(t, repo.last)
			require.Equal(t, test.wantInputTokens, repo.last.InputTokens)
			require.InDelta(t, test.wantInputCost, repo.last.InputCost, 1e-12)
			require.InDelta(t, test.wantOutputCost, repo.last.OutputCost, 1e-12)
			require.InDelta(t, test.wantCacheCreationCost, repo.last.CacheCreationCost, 1e-12)
			require.InDelta(t, test.wantCacheReadCost, repo.last.CacheReadCost, 1e-12)
			require.Equal(t, test.result.Usage.CacheCreation5mTokens, repo.last.CacheCreation5mTokens)
			require.Equal(t, test.result.Usage.CacheCreation1hTokens, repo.last.CacheCreation1hTokens)
		})
	}
}

func cindyTextBillingAccount() *Account {
	return &Account{
		ID:              9901,
		Platform:        PlatformCindy,
		WirePlatform:    WirePlatformOpenAI,
		ProviderProfile: ProviderProfileCindyLaxaV1,
		Type:            AccountTypeAPIKey,
		Status:          StatusActive,
		Credentials: map[string]any{
			"api_key":  "not-exposed",
			"base_url": "https://api.laxarouter.ai",
		},
	}
}
