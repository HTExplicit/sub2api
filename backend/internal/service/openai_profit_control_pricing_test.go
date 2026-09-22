package service

// Downstream runtime boundaries keep profit admission dormant while preserving
// pricingAt, endpoint eligibility and sticky behavior. Manually constructed
// private-gate tests below still cover the unchanged numerical/usage helpers.

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

// Downstream retirement keeps request pricing but never enables admission,
// including historical enabled rows and explicitly suppressed requests.
func TestProfitControl_DormantRequestPricingContext(t *testing.T) {
	svc := &OpenAIGatewayService{}
	groupID := int64(61)
	now := time.Now()
	expensive := upstreamCostTestAccount(1, UpstreamBillingProbeStatusOK, 0.8, now.Add(-time.Minute), 30*time.Minute)
	profitControlTestAccountWithRate(expensive, 0.8)

	t.Run("keeps pricing instant without admission", func(t *testing.T) {
		base := profitControlTestCtx(profitControlTestGroup(groupID, 0.5, 0))
		ctx, pricingAt := svc.WithOpenAIRequestPricingContext(base, &groupID)
		require.False(t, pricingAt.IsZero())
		require.Equal(t, pricingAt, OpenAIPricingAtFromContext(ctx))
		vetoed, reason := OpenAIProfitControlVeto(ctx, expensive)
		require.False(t, vetoed)
		require.Empty(t, reason)
		require.False(t, gatewayProfitControlGateActive(ctx))
		require.Equal(t, 0.8, *expensive.RateMultiplier)
	})

	t.Run("suppress marker skips gate everywhere", func(t *testing.T) {
		base := WithOpenAIProfitControlSuppressed(profitControlTestCtx(profitControlTestGroup(groupID, 0.5, 0)))
		ctx, pricingAt := svc.WithOpenAIRequestPricingContext(base, &groupID)
		require.False(t, pricingAt.IsZero(), "跳门时 pricingAt 仍需固定供计费共用")
		vetoed, _ := OpenAIProfitControlVeto(ctx, expensive)
		require.False(t, vetoed)
		// service 层防御性装门也必须被抑制标记挡住。
		reCtx := svc.withOpenAIProfitControlGate(ctx, &groupID)
		vetoed, _ = OpenAIProfitControlVeto(reCtx, expensive)
		require.False(t, vetoed)
	})
}

// Failover keeps the request price instant; neither a legacy setting change
// nor a carried private gate can re-enable admission at a runtime boundary.
func TestProfitControl_DormantFailoverKeepsPricingContext(t *testing.T) {
	svc := &OpenAIGatewayService{}
	groupID := int64(62)
	group := profitControlTestGroup(groupID, 0.5, 0)
	ctx, pricingAt := svc.WithOpenAIRequestPricingContext(profitControlTestCtx(group), &groupID)
	gate, _ := ctx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
	require.Nil(t, gate)

	// 模拟请求进行中管理员改配置（ctx 分组为同一指针，与 auth 快照语义一致）。
	group.ProfitMinMargin = 0.9
	reCtx := svc.withOpenAIProfitControlGate(ctx, &groupID)
	reGate, _ := reCtx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
	require.Nil(t, reGate)
	require.Equal(t, pricingAt, OpenAIPricingAtFromContext(reCtx))
	require.Equal(t, 0.9, group.ProfitMinMargin, "retirement does not rewrite legacy rows")

	// 换分组（composite/模型路由成员调度）重新解析；成员分组无门时必须清除
	// 父分组门，阈值不得跨组泄漏。
	otherID := int64(63)
	carried := context.WithValue(reCtx, openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{groupID: groupID, threshold: 0})
	otherCtx := svc.withOpenAIProfitControlGate(carried, &otherID)
	otherGate, _ := otherCtx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
	require.Nil(t, otherGate, "成员分组未启用利润控制时父分组门必须清除")
	require.Equal(t, pricingAt, OpenAIPricingAtFromContext(otherCtx))
	now := time.Now()
	expensive := upstreamCostTestAccount(8, UpstreamBillingProbeStatusOK, 0.9, now.Add(-time.Minute), 30*time.Minute)
	vetoed, _ := openAIProfitControlVetoReason(otherCtx, expensive)
	require.False(t, vetoed)
}

// Billing still uses pricingAt and its peak factor without a runtime gate.
func TestProfitControl_DormantAdmissionPreservesPeakPricingAt(t *testing.T) {
	svc := &OpenAIGatewayService{}
	groupID := int64(64)
	group := profitControlTestGroup(groupID, 0, 0)
	group.SubscriptionType = SubscriptionTypeSubscription
	group.PeakRateEnabled = true
	group.PeakRateMultiplier = 3.0

	pricingAt := time.Date(2026, time.January, 15, 8, 30, 0, 0, timezone.Location())
	outsideWindow := time.Date(2026, time.January, 15, 10, 30, 0, 0, timezone.Location())
	group.PeakStart = "08:00"
	group.PeakEnd = "09:00"
	require.Equal(t, 1.0, group.PeakMultiplierAt(outsideWindow), "构造前提：对照时刻不在窗口内")
	require.Equal(t, 3.0, group.PeakMultiplierAt(pricingAt), "构造前提：pricingAt 在窗口内")

	ctx := context.WithValue(profitControlTestCtx(group), openAIPricingAtCtxKey{}, pricingAt)
	gate := svc.resolveOpenAIProfitControlGate(ctx, &groupID)
	require.Nil(t, gate)
	require.Equal(t, pricingAt, OpenAIPricingAtFromContext(ctx))
	require.Equal(t, pricingAt, openAIUsagePricingAt(&OpenAIRecordUsageInput{PricingAt: pricingAt}))
	require.Equal(t, 3.0, group.PeakMultiplierAt(pricingAt), "billing peak factor must not change with admission retirement")
	require.Equal(t, 1.0, group.PeakMultiplierAt(outsideWindow))
}

// U 只取账号倍率：探测快照内容和新鲜度不再直接参与利润判断。
func TestProfitControl_UsesAccountRateInsteadOfProbeSnapshot(t *testing.T) {
	gate := &openAIProfitControlGate{threshold: 0.5, pricingAt: time.Now().Add(-12 * time.Hour)}
	ctx := context.WithValue(context.Background(), openAIProfitControlGateCtxKey{}, gate)
	account := upstreamCostTestAccount(9, UpstreamBillingProbeStatusOK, 0.1, time.Now().Add(-3*time.Hour), 30*time.Minute)
	profitControlTestAccountWithRate(account, 0.8)
	vetoed, reason := openAIProfitControlVetoReason(ctx, account)
	require.True(t, vetoed)
	require.Equal(t, openAIProfitFilterReasonThreshold, reason)
}

// Endpoint capability selection remains active; retired admission cannot turn
// otherwise valid Chat/Responses candidates into no-available errors.
func TestProfitControl_DormantAdmissionKeepsResponsesSelection(t *testing.T) {
	now := time.Now()
	expensive := upstreamCostTestAccount(51, UpstreamBillingProbeStatusOK, 0.8, now.Add(-time.Minute), 30*time.Minute)
	expensive.Status = StatusActive
	expensive.Schedulable = true
	expensive.Concurrency = 2
	svc := &OpenAIGatewayService{
		accountRepo:        stubOpenAIAccountRepo{accounts: []Account{*expensive}},
		cfg:                &config.Config{},
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
		concurrencyService: NewConcurrencyService(stubConcurrencyCache{}),
	}
	groupID := int64(77)
	ctx, pricingAt := svc.WithOpenAIRequestPricingContext(profitControlTestCtx(profitControlTestGroup(groupID, 0.5, 0)), &groupID)
	for _, capability := range []OpenAIEndpointCapability{OpenAIEndpointCapabilityChatCompletions, OpenAIEndpointCapabilityResponses} {
		selection, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", "", "gpt-test", nil, OpenAIUpstreamTransportAny, capability, false, false, true)
		require.NoError(t, err)
		require.NotNil(t, selection)
		require.Equal(t, expensive.ID, selection.Account.ID)
		require.False(t, selection.ProfitGateActive())
		require.Equal(t, pricingAt, OpenAIPricingAtFromContext(ctx))
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
	}
}

// Runtime retirement leaves missing/manual rates untouched; the private-gate
// numerical classifier has separate unchanged compatibility tests.
func TestProfitControl_DormantAdmissionDoesNotClassifyAccountRates(t *testing.T) {
	now := time.Now()
	missing := upstreamCostTestOAuthAccount(2)
	manualOAuth := profitControlTestAccountWithRate(upstreamCostTestOAuthAccount(3), 0.3)
	expensive := profitControlTestAccountWithRate(upstreamCostTestAccount(4, UpstreamBillingProbeStatusOK, 0.1, now.Add(-3*time.Hour), 30*time.Minute), 0.8)

	group := profitControlTestGroup(77, 0.5, 0)
	group.RateMultiplier = 1
	base := context.WithValue(profitControlTestCtx(group), openAIPricingAtCtxKey{}, now)
	gate := (&OpenAIGatewayService{}).resolveOpenAIProfitControlGate(base, &group.ID)
	require.Nil(t, gate)
	for _, account := range []*Account{missing, manualOAuth, expensive} {
		vetoed, reason := openAIProfitControlVetoReason(base, account)
		require.False(t, vetoed)
		require.Empty(t, reason)
	}
	require.Nil(t, missing.RateMultiplier)
	require.Equal(t, 0.3, *manualOAuth.RateMultiplier)
	require.Equal(t, 0.8, *expensive.RateMultiplier)
	require.Equal(t, now, OpenAIPricingAtFromContext(base))
}

// 用量记录定价时刻：优先请求级 PricingAt，未装配回退记录时刻。
func TestOpenAIUsagePricingAt(t *testing.T) {
	fixed := time.Now().Add(-2 * time.Hour)
	require.Equal(t, fixed, openAIUsagePricingAt(&OpenAIRecordUsageInput{PricingAt: fixed}))
	fallback := openAIUsagePricingAt(&OpenAIRecordUsageInput{})
	require.WithinDuration(t, timezone.Now(), fallback, 5*time.Second)
	require.WithinDuration(t, timezone.Now(), openAIUsagePricingAt(nil), 5*time.Second)
}

func TestOpenAIProfitControlStickyBindingOccursOnlyAfterTerminalAdmission(t *testing.T) {
	groupID := int64(81)
	expensiveID := int64(901)
	cheapID := int64(902)
	const sessionHash = "profit-sticky"
	const cacheKey = "openai:" + sessionHash
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{cacheKey: expensiveID},
	}
	svc := &OpenAIGatewayService{cache: cache}
	ctx := context.WithValue(context.Background(), openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{
		groupID:   groupID,
		platform:  PlatformOpenAI,
		threshold: 0.5,
	})

	require.NoError(t, svc.bindOpenAIStickySessionDuringSelection(ctx, &groupID, sessionHash, cheapID))
	require.Equal(t, expensiveID, cache.sessionBindings[cacheKey], "选号阶段不得覆盖原粘性绑定")

	require.NoError(t, svc.BindStickySessionAfterProfitAdmission(ctx, &groupID, sessionHash, cheapID))
	require.Equal(t, expensiveID, cache.sessionBindings[cacheKey], "终检通过的 fallback 账号不得覆盖原粘性绑定")

	cache.sessionBindings[cacheKey] = 0
	require.NoError(t, svc.BindStickySessionAfterProfitAdmission(ctx, &groupID, sessionHash, cheapID))
	require.Equal(t, cheapID, cache.sessionBindings[cacheKey], "无既有绑定时应在终检通过后建立粘性")
}

// Turn boundaries refresh pricingAt, clear carried gates, and never revive
// admission from historical configuration.
func TestProfitControl_DormantTurnPricingContext(t *testing.T) {
	svc := &OpenAIGatewayService{}
	groupID := int64(63)
	expensive := upstreamCostTestAccount(3, UpstreamBillingProbeStatusOK, 0.8, time.Now().Add(-time.Minute), 30*time.Minute)
	profitControlTestAccountWithRate(expensive, 0.8)

	t.Run("refreshes instant without reviving changed legacy config", func(t *testing.T) {
		group := profitControlTestGroup(groupID, 0.5, 0)
		base := profitControlTestCtx(group)
		connCtx, connAt := svc.WithOpenAIRequestPricingContext(base, &groupID)
		vetoed, _ := OpenAIProfitControlVeto(connCtx, expensive)
		require.False(t, vetoed)

		// A saved legacy margin change must not reactivate a retired feature.
		group.ProfitMinMargin = 0.1
		turnCtx, turnAt := svc.WithOpenAITurnPricingContext(connCtx, &groupID)
		require.False(t, turnAt.Before(connAt))
		require.Equal(t, turnAt, OpenAIPricingAtFromContext(turnCtx))
		vetoed, _ = OpenAIProfitControlVeto(turnCtx, expensive)
		require.False(t, vetoed)
		require.False(t, gatewayProfitControlGateActive(turnCtx))
		require.Equal(t, 0.1, group.ProfitMinMargin)
	})

	t.Run("clears a carried scheduled-group gate", func(t *testing.T) {
		scheduledGroupID := int64(64)
		scheduled := profitControlTestGroup(scheduledGroupID, 0.5, 0)
		connCtx, _ := svc.WithOpenAIRequestPricingContext(profitControlTestCtx(scheduled), &scheduledGroupID)
		connCtx = context.WithValue(connCtx, openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{groupID: scheduledGroupID, threshold: 0})
		// 入口分组与调度分组不同（composite 成员分组场景）：turn 重装取门的分组。
		entryGroupID := int64(65)
		turnCtx, turnAt := svc.WithOpenAITurnPricingContext(connCtx, &entryGroupID)
		gate, _ := turnCtx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
		require.Nil(t, gate)
		require.Equal(t, turnAt, OpenAIPricingAtFromContext(turnCtx))
	})

	t.Run("suppress marker only refreshes instant", func(t *testing.T) {
		base := WithOpenAIProfitControlSuppressed(profitControlTestCtx(profitControlTestGroup(groupID, 0.5, 0)))
		turnCtx, turnAt := svc.WithOpenAITurnPricingContext(base, &groupID)
		require.False(t, turnAt.IsZero())
		vetoed, _ := OpenAIProfitControlVeto(turnCtx, expensive)
		require.False(t, vetoed)
	})

	t.Run("clears gate when group disables profit control mid-connection", func(t *testing.T) {
		group := profitControlTestGroup(groupID, 0.5, 0)
		connCtx, _ := svc.WithOpenAIRequestPricingContext(profitControlTestCtx(group), &groupID)
		group.ProfitControlEnabled = false
		turnCtx, _ := svc.WithOpenAITurnPricingContext(connCtx, &groupID)
		vetoed, _ := OpenAIProfitControlVeto(turnCtx, expensive)
		require.False(t, vetoed, "关门后 turn 级复核应放行")
	})
}

// 无门时准入后绑定回退官方 eager 语义：等待/抢槽路径不得因利润控制关闭而
// 失去粘性绑定（评审 M-Bind 回归锚点）。
func TestOpenAIProfitControlAfterAdmissionBindEagerWithoutGate(t *testing.T) {
	groupID := int64(82)
	expensiveID := int64(903)
	cheapID := int64(904)
	const sessionHash = "no-gate-sticky"
	const cacheKey = "openai:" + sessionHash
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{cacheKey: expensiveID},
	}
	svc := &OpenAIGatewayService{cache: cache}

	require.NoError(t, svc.BindStickySessionAfterProfitAdmission(context.Background(), &groupID, sessionHash, cheapID))
	require.Equal(t, cheapID, cache.sessionBindings[cacheKey], "无门时保持既有 eager 绑定行为")
}
