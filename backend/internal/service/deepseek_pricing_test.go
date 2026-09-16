//go:build unit

package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// deepseekPeakMultiplierAt：官方峰谷口径（2026-08-23 起生效）
// 高峰时段 01:00–04:00 与 06:00–10:00 UTC（半开区间，仅工作日）；
// 北京时间周六/周日全天低谷；高峰价 = 2× 低谷价。
// 2026-08-24 为周一（工作日），2026-08-22 周六、2026-08-23 周日。
// ---------------------------------------------------------------------------

func TestDeepseekPeakMultiplierAt(t *testing.T) {
	mon := func(hour, min int) time.Time { return time.Date(2026, 8, 24, hour, min, 0, 0, time.UTC) }
	sat := func(hour, min int) time.Time { return time.Date(2026, 8, 22, hour, min, 0, 0, time.UTC) }
	sun := func(hour, min int) time.Time { return time.Date(2026, 8, 23, hour, min, 0, 0, time.UTC) }

	tests := []struct {
		name string
		now  time.Time
		want float64
	}{
		// 工作日高峰窗口边界（半开区间）
		{"weekday 01:00 peak start", mon(1, 0), 2.0},
		{"weekday 03:59 peak upper bound", mon(3, 59), 2.0},
		{"weekday 04:00 peak end", mon(4, 0), 1.0},
		{"weekday 06:00 peak start", mon(6, 0), 2.0},
		{"weekday 09:59 peak upper bound", mon(9, 59), 2.0},
		{"weekday 10:00 peak end", mon(10, 0), 1.0},
		// 工作日低谷时段
		{"weekday 00:00 off-peak", mon(0, 0), 1.0},
		{"weekday 05:00 off-peak", mon(5, 0), 1.0},
		{"weekday 12:00 off-peak", mon(12, 0), 1.0},
		{"weekday 23:59 off-peak", mon(23, 59), 1.0},
		// 北京时间周末全天低谷（即使 UTC 处于高峰时段）
		{"saturday utc 02:00 beijing sat 10:00", sat(2, 0), 1.0},
		{"sunday utc 07:00 beijing sun 15:00", sun(7, 0), 1.0},
		// 北京时间与 UTC 跨日边界：UTC 周六 16:30 = 北京周日 00:30 → 周末低谷
		{"utc saturday 16:30 = beijing sunday 00:30", sat(16, 30), 1.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, deepseekPeakMultiplierAt(tt.now))
		})
	}
}

func TestIsDeepSeekModel(t *testing.T) {
	deepseek := []string{
		"deepseek-flash", "deepseek-v4-flash", "deepseek-v4-pro", "deepseek-v4-flash-vision-exp",
		"deepseek-chat", "deepseek-reasoner", "deepseek-v3-2-251201",
		"deepseek-coder", "deepseek-foo", "deepseek-v4-pro-0813",
		"DEEPSEEK-V4-PRO", " deepseek-v4-flash ",
	}
	for _, m := range deepseek {
		require.True(t, isDeepSeekModel(m), "model %q should be deepseek", m)
	}

	nonDeepseek := []string{
		"gpt-5.4", "claude-sonnet-4", "deepseekcoder", // 无连字符不算 deepseek- 前缀
		"", " deepseek", // 无连字符后缀
	}
	for _, m := range nonDeepseek {
		require.False(t, isDeepSeekModel(m), "model %q should not be deepseek", m)
	}
}

// ---------------------------------------------------------------------------
// 默认价卡（Source=LiteLLM）按官方峰谷倍率计费；分组/渠道自定义定价不叠加
// ---------------------------------------------------------------------------

func TestCalculateCostUnified_DeepseekDefaultCardPeakMultiplier(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)

	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500, CacheReadTokens: 1000}
	// 低谷成本（2026-09-10 官方新价）：1000*1.5e-7 + 500*6e-7 + 1000*3e-9 = 4.53e-4
	offPeakTotal := 1000*1.5e-7 + 500*6e-7 + 1000*3e-9

	offPeak, err := bs.CalculateCostUnified(CostInput{
		Ctx: context.Background(), Model: "deepseek-v4-flash", Tokens: tokens,
		RateMultiplier: 1.0, Resolver: resolver,
		PricingAt: time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC), // 周一低谷
	})
	require.NoError(t, err)
	require.InDelta(t, offPeakTotal, offPeak.TotalCost, 1e-10)

	peak, err := bs.CalculateCostUnified(CostInput{
		Ctx: context.Background(), Model: "deepseek-v4-flash", Tokens: tokens,
		RateMultiplier: 1.0, Resolver: resolver,
		PricingAt: time.Date(2026, 8, 24, 2, 0, 0, 0, time.UTC), // 周一高峰
	})
	require.NoError(t, err)
	require.InDelta(t, offPeakTotal*2, peak.TotalCost, 1e-10)
}

func TestCalculateCostUnified_DeepseekProDefaultCardPeakMultiplier(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)

	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500, CacheReadTokens: 1000}
	offPeakTotal := 1000*6.6e-7 + 500*1.98e-6 + 1000*2.2e-8

	offPeak, err := bs.CalculateCostUnified(CostInput{
		Ctx: context.Background(), Model: "deepseek-v4-pro", Tokens: tokens,
		RateMultiplier: 1.0, Resolver: resolver,
		PricingAt: time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.InDelta(t, offPeakTotal, offPeak.TotalCost, 1e-10)

	peak, err := bs.CalculateCostUnified(CostInput{
		Ctx: context.Background(), Model: "deepseek-v4-pro", Tokens: tokens,
		RateMultiplier: 1.0, Resolver: resolver,
		PricingAt: time.Date(2026, 8, 24, 6, 30, 0, 0, time.UTC), // 周一高峰
	})
	require.NoError(t, err)
	require.InDelta(t, offPeakTotal*2, peak.TotalCost, 1e-10)
}

func TestCalculateCostUnified_DeepseekVersionedNamePeakMultiplier(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)

	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500, CacheReadTokens: 1000}
	offPeakTotal := 1000*1.5e-7 + 500*6e-7 + 1000*3e-9

	offPeak, err := bs.CalculateCostUnified(CostInput{
		Ctx: context.Background(), Model: "deepseek-v4-flash-0731", Tokens: tokens,
		RateMultiplier: 1.0, Resolver: resolver,
		PricingAt: time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC), // 周一低谷
	})
	require.NoError(t, err)
	require.InDelta(t, offPeakTotal, offPeak.TotalCost, 1e-10)

	peak, err := bs.CalculateCostUnified(CostInput{
		Ctx: context.Background(), Model: "deepseek-v4-flash-0731", Tokens: tokens,
		RateMultiplier: 1.0, Resolver: resolver,
		PricingAt: time.Date(2026, 8, 24, 2, 0, 0, 0, time.UTC), // 周一高峰
	})
	require.NoError(t, err)
	require.InDelta(t, offPeakTotal*2, peak.TotalCost, 1e-10)
}

func TestCalculateCostUnified_DeepseekGroupPricingNotScaledByPeak(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)

	inputPrice := 1e-6
	outputPrice := 2e-6
	group := &Group{
		ID: 1, Name: "ds-group", Platform: PlatformDeepseek, Status: StatusActive,
		ModelPricing: []ChannelModelPricing{{
			Models: []string{"deepseek-v4-flash"}, BillingMode: BillingModeToken,
			InputPrice: &inputPrice, OutputPrice: &outputPrice,
		}},
	}
	resolved := resolver.Resolve(context.Background(), PricingInput{Model: "deepseek-v4-flash", Group: group})
	require.Equal(t, PricingSourceGroup, resolved.Source)

	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500, CacheReadTokens: 1000}
	// 分组自定义价：1000*1e-6 + 500*2e-6 + 1000*3e-9（缓存读沿用官方 flash 价）
	groupTotal := 1000*1e-6 + 500*2e-6 + 1000*3e-9

	for _, pricingAt := range []time.Time{
		time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC), // 低谷
		time.Date(2026, 8, 24, 2, 0, 0, 0, time.UTC),  // 高峰
	} {
		cost, err := bs.CalculateCostUnified(CostInput{
			Ctx: context.Background(), Model: "deepseek-v4-flash", Group: group,
			Tokens: tokens, RateMultiplier: 1.0, Resolver: resolver, PricingAt: pricingAt,
		})
		require.NoError(t, err)
		require.InDelta(t, groupTotal, cost.TotalCost, 1e-10,
			"分组自定义定价不应叠加官方峰谷倍率（pricingAt=%v）", pricingAt)
	}
}

func TestCalculateCostUnified_NonDeepseekDefaultCardNotScaledByPeak(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)

	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500}
	total := 1000*3e-6 + 500*15e-6 // claude-sonnet-4 fallback

	for _, pricingAt := range []time.Time{
		time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 24, 2, 0, 0, 0, time.UTC),
	} {
		cost, err := bs.CalculateCostUnified(CostInput{
			Ctx: context.Background(), Model: "claude-sonnet-4", Tokens: tokens,
			RateMultiplier: 1.0, Resolver: resolver, PricingAt: pricingAt,
		})
		require.NoError(t, err)
		require.InDelta(t, total, cost.TotalCost, 1e-10,
			"非 DeepSeek 模型不应受官方峰谷倍率影响（pricingAt=%v）", pricingAt)
	}
}

func TestCalculateCostUnified_DeepseekPricingAtZeroFallsBackToNow(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)

	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500}
	base := CostInput{
		Ctx: context.Background(), Model: "deepseek-v4-flash", Tokens: tokens,
		RateMultiplier: 1.0, Resolver: resolver,
	}

	// PricingAt 零值 → 回退 timezone.Now()，与显式传入当前时刻结果一致。
	costZero, err := bs.CalculateCostUnified(base)
	require.NoError(t, err)

	costNow, err := bs.CalculateCostUnified(CostInput{
		Ctx: base.Ctx, Model: base.Model, Tokens: base.Tokens,
		RateMultiplier: base.RateMultiplier, Resolver: base.Resolver,
		PricingAt: timezone.Now(),
	})
	require.NoError(t, err)
	require.Equal(t, costZero.TotalCost, costNow.TotalCost)
}

// Price ownership is established once at catalog construction, not by
// overwriting amounts in individual billing callers.
func TestDeepseekEffectiveCatalogAndIdentity(t *testing.T) {
	raw := []byte(`{"deepseek-flash":{"input_cost_per_token":9,"output_cost_per_token":8},"deepseek-v4-pro":{"input_cost_per_token":7,"output_cost_per_token":6}}`)
	svc := &PricingService{}
	data, _, err := svc.buildPricingData(raw)
	require.NoError(t, err)
	svc.pricingData = data
	bs := NewBillingService(&config.Config{}, svc)
	for _, card := range deepseekPricingRegistry {
		for _, alias := range card.aliases {
			for _, name := range []string{alias, "deepseek/" + alias, " " + strings.ToUpper("deepseek/"+alias) + " "} {
				t.Run(name, func(t *testing.T) {
					price, err := bs.GetModelPricing(name)
					require.NoError(t, err)
					require.Equal(t, card.canonical, price.deepseekIdentity.CanonicalModel)
					require.NotEqual(t, "fallback", price.deepseekIdentity.Match)
					require.Equal(t, deepseekPricingSource, price.deepseekIdentity.Source)
					require.InDelta(t, card.input, price.InputPricePerToken, 1e-15)
					require.InDelta(t, card.output, price.OutputPricePerToken, 1e-15)
					require.InDelta(t, card.cacheRead, price.CacheReadPricePerToken, 1e-15)
					require.True(t, bs.HasIdentifiedTokenPricing(name))
				})
			}
		}
	}
	for _, name := range []string{"deepseek-foo", "deepseek/deepseek-foo", "deepseek-chat", "deepseek-reasoner", "deepseek-v4.1-flash-20990101"} {
		price, err := bs.GetModelPricing(name)
		require.NoError(t, err)
		require.Equal(t, "fallback", price.deepseekIdentity.Match)
		require.False(t, bs.HasIdentifiedTokenPricing(name))
		resolved := NewModelPricingResolver(nil, bs).Resolve(context.Background(), PricingInput{Model: name})
		require.Equal(t, PricingSourceFallback, resolved.Source)
		require.InDelta(t, 1.5e-7, price.InputPricePerToken, 1e-15)
	}
	for _, name := range []string{"other/deepseek-v4.1-flash", "nested/deepseek/deepseek-v4.1-flash", "not-deepseek-v4-pro"} {
		require.False(t, bs.HasIdentifiedTokenPricing(name))
	}
	// Protocol-family classification remains independent from billing aliases.
	require.False(t, isDeepSeekModel("deepseek/deepseek-v4.1-flash"))
	require.True(t, isDeepSeekModel("deepseek-foo"))
}

func TestDeepseekPriceLoadAndHotReload(t *testing.T) {
	svc := newHotReloadPricingService(t, "", `{"deepseek-flash":{"input_cost_per_token":0.000004}}`)
	alias := "deepseek/deepseek-v4.1-flash"
	price := svc.GetModelPricing(alias)
	require.InDelta(t, 4e-6, price.InputCostPerToken, 1e-15)
	require.InDelta(t, 6e-7, price.OutputCostPerToken, 1e-15)
	require.Equal(t, "override", price.deepseekIdentity.Source)
	require.False(t, price.deepseekIdentity.OfficialPeak)
	anchor := svc.localHash
	require.NoError(t, os.WriteFile(svc.cfg.Pricing.OverrideFile, []byte(`{"deepseek-flash":{"input_cost_per_token":0,"output_cost_per_token":0,"cache_read_input_token_cost":0}}`), 0644))
	svc.reloadIfCustomFilesChanged()
	price = svc.GetModelPricing(alias)
	require.Zero(t, price.InputCostPerToken)
	require.Zero(t, price.OutputCostPerToken)
	require.Zero(t, price.CacheReadInputTokenCost)
	require.Equal(t, anchor, svc.localHash)
	require.NoError(t, os.Remove(svc.cfg.Pricing.OverrideFile))
	svc.reloadIfCustomFilesChanged()
	require.InDelta(t, 1.5e-7, svc.GetModelPricing(alias).InputCostPerToken, 1e-15)
	require.True(t, svc.GetModelPricing(alias).deepseekIdentity.OfficialPeak)
	// The bundled catalog contains metadata, while its effective prices come
	// from the same registry as remote updates and offline billing.
	body, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)
	loaded, _, err := svc.buildPricingData(body)
	require.NoError(t, err)
	require.Equal(t, svc.GetModelPricing(alias), loaded[alias])
}

func TestDeepseekAllCostEntrypointsAndProRetention(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)
	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500, CacheReadTokens: 1000}
	for _, model := range []struct {
		name string
		base float64
	}{
		{"deepseek/deepseek-v4.1-flash", 1000*1.5e-7 + 500*6e-7 + 1000*3e-9},
		{"deepseek/deepseek-v4-pro-0813", 1000*6.6e-7 + 500*1.98e-6 + 1000*2.2e-8},
	} {
		for _, at := range []time.Time{
			time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC),
			time.Date(2026, 9, 14, 4, 0, 0, 0, time.UTC),
			time.Date(2026, 9, 16, 2, 0, 0, 0, time.UTC),
			time.Date(2026, 9, 16, 6, 0, 0, 0, time.UTC),
		} {
			want := model.base * deepseekPeakMultiplierAt(at)
			for _, r := range []*ModelPricingResolver{nil, resolver} {
				result, err := bs.CalculateCostUnified(CostInput{Model: model.name, Tokens: tokens, RateMultiplier: 1, PricingAt: at, Resolver: r})
				require.NoError(t, err)
				require.InDelta(t, want, result.TotalCost, 1e-12)
				viaRequest, err := bs.CalculateTokenCostForRequest(TokenCostRequest{Model: model.name, Tokens: tokens, RateMultiplier: 1, PricingAt: at, Resolver: r})
				require.NoError(t, err)
				require.InDelta(t, want, viaRequest.TotalCost, 1e-12)
			}
			stats := tryModelFilePricing(bs, model.name, tokens, "", at)
			require.NotNil(t, stats)
			require.InDelta(t, want, *stats, 1e-12)
		}
	}
	schedule, err := bs.ResolveContextPricingSchedule(context.Background(), resolver, ContextPricingScheduleInput{Model: "deepseek/deepseek-v4.1-flash"})
	require.NoError(t, err)
	require.Len(t, schedule.Tiers, 1)
	require.InDelta(t, 1.5e-7, *schedule.Tiers[0].Input, 1e-15)
	require.Equal(t, deepseekTimePricingSchedule(), schedule.TimePricing)
}

func TestDeepseekCanonicalGroupPriceAndZeroOverride(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)
	zero := 0.0
	group := &Group{ID: 52, ModelPricing: []ChannelModelPricing{{Models: []string{"deepseek-flash"}, InputPrice: &zero, OutputPrice: &zero, CacheReadPrice: &zero}}}
	cost, err := bs.CalculateCostUnified(CostInput{Model: "deepseek/deepseek-v4.1-flash", Group: group, Tokens: UsageTokens{InputTokens: 1000, OutputTokens: 500, CacheReadTokens: 1000}, RateMultiplier: 1, Resolver: resolver, PricingAt: time.Date(2026, 9, 16, 2, 0, 0, 0, time.UTC)})
	require.NoError(t, err)
	require.Zero(t, cost.TotalCost)
	schedule, err := bs.ResolveContextPricingSchedule(context.Background(), resolver, ContextPricingScheduleInput{Model: "deepseek/deepseek-v4.1-flash", Group: group})
	require.NoError(t, err)
	require.Nil(t, schedule.TimePricing)
}

func TestDeepseekOpenAIAccount16367BillingRegression(t *testing.T) {
	for _, withResolver := range []bool{true, false} {
		usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
		billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
		svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
		if withResolver {
			svc.resolver = NewModelPricingResolver(nil, svc.billingService)
		}
		groupID := int64(52)
		model := "deepseek/deepseek-v4.1-flash"
		// Request started off-peak even though asynchronous billing can happen later.
		at := time.Date(2026, 9, 16, 5, 59, 59, 0, time.UTC)
		err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
			Result: &OpenAIForwardResult{RequestID: "deepseek-regression", Model: model, UpstreamModel: model, Usage: OpenAIUsage{InputTokens: 2000, OutputTokens: 500, CacheReadInputTokens: 1000}},
			APIKey: &APIKey{ID: 1, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformOpenAI, RateMultiplier: 1}},
			User:   &User{ID: 1}, Account: &Account{ID: 16367, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, PricingAt: at,
		})
		require.NoError(t, err)
		want := 1000*1.5e-7 + 500*6e-7 + 1000*3e-9
		require.NotNil(t, usageRepo.lastLog)
		require.InDelta(t, want, usageRepo.lastLog.TotalCost, 1e-12)
		require.InDelta(t, want, usageRepo.lastLog.ActualCost, 1e-12)
		require.Equal(t, model, usageRepo.lastLog.Model)
		require.Equal(t, model, *usageRepo.lastLog.UpstreamModel)
		require.Equal(t, 1, billingRepo.calls)
		require.InDelta(t, want, billingRepo.lastCmd.BalanceCost, 1e-12)
		price, err := svc.billingService.GetModelPricing(model)
		require.NoError(t, err)
		require.NotEqual(t, "fallback", price.deepseekIdentity.Match)
	}
}

func TestDeepseekConfiguredAliasPricing(t *testing.T) {
	model := " DEEPSEEK/DEEPSEEK-V4.1-FLASH "
	at := time.Date(2026, 9, 16, 2, 0, 0, 0, time.UTC)
	tp := &ChannelTimePricing{Timezone: "Asia/Shanghai", Periods: []ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 3}}}
	card := ChannelModelPricing{Platform: PlatformOpenAI, Models: []string{"deepseek-v4-flash"}, InputPrice: testPtrFloat64(4e-6), TimePricing: tp}
	bs, resolver := newTokenCostTestEnv(t, PlatformOpenAI, []ChannelModelPricing{card}, nil)
	group := enabledGroup(PlatformOpenAI)
	gid := group.ID
	resolved := resolver.Resolve(context.Background(), PricingInput{Model: model, Group: group, GroupID: &gid})
	require.Equal(t, PricingSourceChannel, resolved.Source)
	cost, err := bs.CalculateCostUnified(CostInput{Model: model, Group: group, GroupID: &gid, Tokens: UsageTokens{InputTokens: 1000}, RateMultiplier: 1, Resolver: resolver, PricingAt: at})
	require.NoError(t, err)
	require.InDelta(t, .012, cost.TotalCost, 1e-12) // custom 3x, no extra official 2x
	schedule, err := bs.ResolveContextPricingSchedule(context.Background(), resolver, ContextPricingScheduleInput{Model: model, Group: group})
	require.NoError(t, err)
	require.InDelta(t, 4e-6, *schedule.Tiers[0].Input, 1e-15)
	require.Equal(t, 3.0, schedule.TimePricing.Periods[0].Multiplier)
	group.ModelPricing = []ChannelModelPricing{card}
	resolved = resolver.Resolve(context.Background(), PricingInput{Model: model, Group: group, GroupID: &gid})
	require.Equal(t, PricingSourceGroup, resolved.Source)
	// An explicit alias card retains priority over the canonical/other aliases.
	exact := card.Clone()
	exact.Models = []string{model}
	exact.InputPrice = testPtrFloat64(7e-6)
	group.ModelPricing = append(group.ModelPricing, exact)
	resolved = resolver.Resolve(context.Background(), PricingInput{Model: model, Group: group, GroupID: &gid})
	require.InDelta(t, 7e-6, resolved.BasePricing.InputPricePerToken, 1e-15)
}

func TestDeepseekCacheOnlyAdminOverride(t *testing.T) {
	svc := newHotReloadPricingService(t, "", `{"deepseek-flash":{"cache_read_input_token_cost":0.000004}}`)
	bs := NewBillingService(&config.Config{}, svc)
	cost, err := bs.CalculateCostUnified(CostInput{Model: "deepseek/deepseek-v4.1-flash", Tokens: UsageTokens{CacheReadTokens: 1000}, RateMultiplier: 1, PricingAt: time.Date(2026, 9, 16, 2, 0, 0, 0, time.UTC)})
	require.NoError(t, err)
	require.InDelta(t, .004, cost.TotalCost, 1e-12)
	require.Equal(t, "override", svc.GetModelPricing("deepseek-flash").deepseekIdentity.Source)
}
