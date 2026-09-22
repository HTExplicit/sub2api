package service

// Runtime path cases follow the downstream retirement contract: legacy profit
// fields cannot filter accounts, but explicit exclusions, model capability,
// sticky identity and billing rates remain effective. Private manually-built
// gate cases preserve the independent numerical/admission helper coverage.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func profitControlWSAccount(id int64, rate float64, now time.Time) Account {
	account := upstreamCostTestAccount(id, UpstreamBillingProbeStatusOK, rate, now.Add(-time.Minute), 30*time.Minute)
	profitControlTestAccountWithRate(account, rate)
	account.Status = StatusActive
	account.Schedulable = true
	account.Concurrency = 2
	account.Extra["openai_apikey_responses_websockets_v2_enabled"] = true
	return *account
}

// Dormant admission never rejects the previous-response binding based on a
// saved legacy margin; both old and subsequently changed rates remain usable.
func TestProfitControl_DormantPreviousResponseKeepsBinding(t *testing.T) {
	ctx := profitControlTestCtx(profitControlTestGroup(23, 0.5, 0))
	groupID := int64(23)
	now := time.Now()
	expensive := profitControlWSAccount(31, 0.8, now)

	cache := &stubGatewayCache{}
	store := NewOpenAIWSStateStore(cache)
	svc := &OpenAIGatewayService{
		accountRepo:        stubOpenAIAccountRepo{accounts: []Account{expensive}},
		cache:              cache,
		cfg:                newOpenAIWSV2TestConfig(),
		concurrencyService: NewConcurrencyService(stubConcurrencyCache{}),
		openaiWSStateStore: store,
	}
	require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_profit", expensive.ID, time.Hour))

	selection, err := svc.SelectAccountByPreviousResponseID(ctx, &groupID, "resp_profit", "gpt-5.1", nil, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, expensive.ID, selection.Account.ID)
	require.False(t, selection.ProfitGateActive())
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}

	// Historical profit data must not invalidate the existing response binding.
	boundAccountID, getErr := store.GetResponseAccount(ctx, groupID, "resp_profit")
	require.NoError(t, getErr)
	require.Equal(t, expensive.ID, boundAccountID)

	// 上游倍率回落（探测刷新）后同一绑定重新可用。
	recovered := profitControlWSAccount(31, 0.3, time.Now())
	svc.accountRepo = stubOpenAIAccountRepo{accounts: []Account{recovered}}
	selection, err = svc.SelectAccountByPreviousResponseID(ctx, &groupID, "resp_profit", "gpt-5.1", nil, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, recovered.ID, selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// legacy 引擎（高级调度器关闭）：候选过滤、全排除错误语义与既有语义一致。
func TestProfitControl_DormantLegacyEngineKeepsEligibleCandidates(t *testing.T) {
	now := time.Now()
	cheap := upstreamCostTestAccount(41, UpstreamBillingProbeStatusOK, 0.3, now.Add(-time.Minute), 30*time.Minute)
	expensive := upstreamCostTestAccount(42, UpstreamBillingProbeStatusOK, 0.8, now.Add(-time.Minute), 30*time.Minute)
	profitControlTestAccountWithRate(cheap, 0.3)
	profitControlTestAccountWithRate(expensive, 0.8)
	for _, account := range []*Account{cheap, expensive} {
		account.Status = StatusActive
		account.Schedulable = true
		account.Concurrency = 2
	}
	svc := &OpenAIGatewayService{
		accountRepo:        stubOpenAIAccountRepo{accounts: []Account{*cheap, *expensive}},
		cfg:                &config.Config{},
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("false"),
		concurrencyService: NewConcurrencyService(stubConcurrencyCache{}),
	}
	groupID := int64(7)

	t.Run("legacy path admits either otherwise eligible account", func(t *testing.T) {
		ctx, pricingAt := svc.WithOpenAIRequestPricingContext(profitControlTestCtx(profitControlTestGroup(groupID, 0.5, 0)), &groupID)
		for _, expected := range []int64{cheap.ID, expensive.ID} {
			excluded := map[int64]struct{}{cheap.ID: {}, expensive.ID: {}}
			delete(excluded, expected)
			selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "gpt-test", excluded, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.Equal(t, expected, selection.Account.ID)
			require.False(t, selection.ProfitGateActive())
			require.Equal(t, pricingAt, OpenAIPricingAtFromContext(ctx))
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		}
	})

	t.Run("explicit exclusions still return the standard error", func(t *testing.T) {
		ctx := profitControlTestCtx(profitControlTestGroup(groupID, 0.7, 0.1))
		selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "gpt-test", map[int64]struct{}{cheap.ID: {}, expensive.ID: {}}, OpenAIUpstreamTransportAny, false)
		require.Nil(t, selection)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrNoAvailableAccounts))
		require.NotContains(t, err.Error(), openAIProfitFilterReasonThreshold)
	})

	t.Run("legacy path keeps official behavior when gate disabled", func(t *testing.T) {
		group := profitControlTestGroup(groupID, 0.7, 0.1)
		group.ProfitControlEnabled = false
		selection, _, err := svc.SelectAccountWithScheduler(profitControlTestCtx(group), &groupID, "", "", "gpt-test", nil, OpenAIUpstreamTransportAny, false)
		require.NoError(t, err)
		require.NotNil(t, selection)
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
	})
}

// Explicit failures stay excluded; historical profit settings must not also
// exclude the remaining otherwise eligible account.
func TestProfitControl_DormantFailoverKeepsExplicitExclusions(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()

	now := time.Now()
	cheap := upstreamCostTestAccount(51, UpstreamBillingProbeStatusOK, 0.3, now.Add(-time.Minute), 30*time.Minute)
	expensive := upstreamCostTestAccount(52, UpstreamBillingProbeStatusOK, 0.8, now.Add(-time.Minute), 30*time.Minute)
	profitControlTestAccountWithRate(cheap, 0.3)
	profitControlTestAccountWithRate(expensive, 0.8)
	for _, account := range []*Account{cheap, expensive} {
		account.Status = StatusActive
		account.Schedulable = true
		account.Concurrency = 2
	}
	cache := &upstreamCostTrackingConcurrencyCache{loadMap: map[int64]*AccountLoadInfo{
		cheap.ID:     {AccountID: cheap.ID},
		expensive.ID: {AccountID: expensive.ID},
	}}
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{*cheap, *expensive}},
		cfg:                &config.Config{},
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
		concurrencyService: NewConcurrencyService(cache),
	}
	groupID := int64(7)
	ctx, pricingAt := svc.WithOpenAIRequestPricingContext(profitControlTestCtx(profitControlTestGroup(groupID, 0.5, 0)), &groupID)

	// Simulate a real failure exclusion; the remaining expensive account is
	// still eligible because profit admission is retired.
	excluded := map[int64]struct{}{cheap.ID: {}}
	selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "gpt-test", excluded, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, expensive.ID, selection.Account.ID)
	require.Contains(t, excluded, cheap.ID)
	require.False(t, selection.ProfitGateActive())
	require.Equal(t, pricingAt, OpenAIPricingAtFromContext(ctx))
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

// 抢槽后终检：候选构建后才变得不合格的账号（状态竞态）在取得槽位前被拦截。
func TestProfitControl_PostSlotRecheckVetoes(t *testing.T) {
	now := time.Now()
	expensive := upstreamCostTestAccount(61, UpstreamBillingProbeStatusOK, 0.8, now.Add(-time.Minute), 30*time.Minute)
	profitControlTestAccountWithRate(expensive, 0.8)
	expensive.Status = StatusActive
	expensive.Schedulable = true
	expensive.Concurrency = 2

	cache := &upstreamCostTrackingConcurrencyCache{}
	scheduler := &defaultOpenAIAccountScheduler{service: &OpenAIGatewayService{
		concurrencyService: NewConcurrencyService(cache),
	}}
	ctx := context.WithValue(context.Background(), openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{
		threshold: 0.5,
		pricingAt: now,
	})
	selectionOrder := []openAIAccountCandidateScore{{
		account:  expensive,
		loadInfo: &AccountLoadInfo{AccountID: expensive.ID},
	}}

	selection, _, err := scheduler.tryAcquireOpenAISelectionOrder(ctx, OpenAIAccountScheduleRequest{Platform: PlatformOpenAI}, selectionOrder)
	require.NoError(t, err)
	require.Nil(t, selection, "抢槽终检必须拦截候选构建后才不合格的账号")
	require.Equal(t, cache.totalAcquires(), cache.releaseCount(expensive.ID), "被拦截账号不得泄漏并发槽位")
}

// Rate changes remain observable account data but do not toggle retired
// admission; the only healthy account is selectable on both sides.
func TestProfitControl_DormantRateChangesKeepAccountEligible(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()

	now := time.Now()
	expensive := upstreamCostTestAccount(71, UpstreamBillingProbeStatusOK, 0.8, now.Add(-time.Minute), 30*time.Minute)
	profitControlTestAccountWithRate(expensive, 0.8)
	expensive.Status = StatusActive
	expensive.Schedulable = true
	expensive.Concurrency = 2
	cache := &upstreamCostTrackingConcurrencyCache{loadMap: map[int64]*AccountLoadInfo{
		expensive.ID: {AccountID: expensive.ID},
	}}
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{*expensive}},
		cfg:                &config.Config{},
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
		concurrencyService: NewConcurrencyService(cache),
	}
	groupID := int64(7)
	ctx := profitControlTestCtx(profitControlTestGroup(groupID, 0.5, 0))

	selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "gpt-test", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, expensive.ID, selection.Account.ID)
	require.False(t, selection.ProfitGateActive())
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}

	// 同步/手工写回：账号倍率回落到 0.3（阈值 0.5 内）后自动恢复参与。
	recovered := upstreamCostTestAccount(71, UpstreamBillingProbeStatusOK, 0.3, time.Now().Add(-time.Minute), 30*time.Minute)
	profitControlTestAccountWithRate(recovered, 0.3)
	recovered.Status = StatusActive
	recovered.Schedulable = true
	recovered.Concurrency = 2
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{*recovered}}

	selection, _, err = svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "gpt-test", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, recovered.ID, selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

type profitControlUserRateRepo struct {
	UserGroupRateRepository
	rate *float64
}

func (r profitControlUserRateRepo) GetByUserAndGroup(context.Context, int64, int64) (*float64, error) {
	return r.rate, nil
}

// Retirement does not alter the ordinary user-specific billing rate resolver.
func TestProfitControl_DormantGatePreservesUserOverrideRate(t *testing.T) {
	override := 0.5
	svc := &OpenAIGatewayService{
		userGroupRateResolver: newUserGroupRateResolver(
			profitControlUserRateRepo{rate: &override}, nil, time.Minute, nil, "test.profit",
		),
	}
	groupID := int64(7)
	group := profitControlTestGroup(groupID, 0, 0)
	group.RateMultiplier = 2.0

	ctx := context.WithValue(profitControlTestCtx(group), ctxkey.UserID, int64(42))
	gate := svc.resolveOpenAIProfitControlGate(ctx, &groupID)
	require.Nil(t, gate)
	require.InDelta(t, 0.5, svc.ResolveUserGroupRateMultiplier(ctx, 42, groupID, group.RateMultiplier), 1e-12,
		"ordinary billing still resolves the saved user override")

	// 无用户身份（内部调用）时按分组默认倍率计算。
	gate = svc.resolveOpenAIProfitControlGate(profitControlTestCtx(group), &groupID)
	require.Nil(t, gate)
	require.Equal(t, 2.0, group.RateMultiplier)
	require.Equal(t, 0.5, override)
}

type profitControlGroupRepo struct {
	GroupRepository
	group *Group
}

func (r profitControlGroupRepo) GetByIDLite(context.Context, int64) (*Group, error) {
	return r.group, nil
}

// GetByID 故意 panic：利润门只需要分组配置，不需要 GetByID 附带的账号计数
// 聚合查询。装门走 GetByID 会在 composite/模型路由/fallback 的每次装门（WS
// 每 turn 一次）上多打一条聚合，且发生在「是否启用利润控制」判定之前。
func (r profitControlGroupRepo) GetByID(context.Context, int64) (*Group, error) {
	panic("profit control gate must read groups via GetByIDLite (no account-count aggregation)")
}

// Cross-group retirement preserves both billing and scheduled-group data.
func TestProfitControl_DormantCompositePreservesBothGroupRates(t *testing.T) {
	memberGroupID := int64(7)
	memberGroup := profitControlTestGroup(memberGroupID, 0.5, 0)
	memberGroup.RateMultiplier = 99 // 若 D 误取成员分组倍率，阈值会是 49.5

	billingGroup := &Group{
		ID:             1001,
		Platform:       PlatformComposite,
		Status:         StatusActive,
		Hydrated:       true,
		RateMultiplier: 1.0,
	}
	svc := &OpenAIGatewayService{
		schedulerSnapshot: &SchedulerSnapshotService{groupRepo: profitControlGroupRepo{group: memberGroup}},
	}

	ctx := profitControlTestCtx(billingGroup)
	gate := svc.resolveOpenAIProfitControlGate(ctx, &memberGroupID)
	require.Nil(t, gate)
	priced, pricingAt := svc.WithOpenAIRequestPricingContext(ctx, &memberGroupID)
	require.Equal(t, pricingAt, OpenAIPricingAtFromContext(priced))
	require.Equal(t, 1.0, billingGroup.RateMultiplier)
	require.Equal(t, 99.0, memberGroup.RateMultiplier)
	require.True(t, memberGroup.ProfitControlEnabled, "retirement must not rewrite persisted legacy configuration")
}

// legacy 引擎与 DB recheck 共用的资格判定直接覆盖利润门。
func TestProfitControl_EligibilityFunctionVetoes(t *testing.T) {
	now := time.Now()
	gateCtx := context.WithValue(context.Background(), openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{
		threshold: 0.5,
		pricingAt: now,
	})
	cheap := upstreamCostTestAccount(81, UpstreamBillingProbeStatusOK, 0.3, now.Add(-time.Minute), 30*time.Minute)
	expensive := upstreamCostTestAccount(82, UpstreamBillingProbeStatusOK, 0.8, now.Add(-time.Minute), 30*time.Minute)
	profitControlTestAccountWithRate(cheap, 0.3)
	profitControlTestAccountWithRate(expensive, 0.8)
	for _, account := range []*Account{cheap, expensive} {
		account.Status = StatusActive
		account.Schedulable = true
	}

	require.True(t, isOpenAICompatibleAccountEligibleForRequest(gateCtx, cheap, PlatformOpenAI, "", false, ""))
	require.False(t, isOpenAICompatibleAccountEligibleForRequest(gateCtx, expensive, PlatformOpenAI, "", false, ""))
	// 无门时保持既有行为。
	require.True(t, isOpenAICompatibleAccountEligibleForRequest(context.Background(), expensive, PlatformOpenAI, "", false, ""))
}

// Legacy engine selection keeps ordinary eager binding even if persisted rows
// still carry retired profit configuration.
func TestProfitControl_DormantLegacyEngineKeepsEagerStickyBinding(t *testing.T) {
	now := time.Now()
	cheap := upstreamCostTestAccount(45, UpstreamBillingProbeStatusOK, 0.3, now.Add(-time.Minute), 30*time.Minute)
	expensive := upstreamCostTestAccount(46, UpstreamBillingProbeStatusOK, 0.8, now.Add(-time.Minute), 30*time.Minute)
	profitControlTestAccountWithRate(cheap, 0.3)
	profitControlTestAccountWithRate(expensive, 0.8)
	for _, account := range []*Account{cheap, expensive} {
		account.Status = StatusActive
		account.Schedulable = true
		account.Concurrency = 2
	}
	groupID := int64(9)
	const sessionHash = "legacy-sticky"
	newSvc := func(bindings map[string]int64) (*OpenAIGatewayService, *schedulerTestGatewayCache) {
		cache := &schedulerTestGatewayCache{sessionBindings: bindings}
		return &OpenAIGatewayService{
			accountRepo:        stubOpenAIAccountRepo{accounts: []Account{*cheap, *expensive}},
			cfg:                &config.Config{},
			rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("false"),
			concurrencyService: NewConcurrencyService(stubConcurrencyCache{}),
			cache:              cache,
		}, cache
	}

	t.Run("legacy enabled values keep eager binding without a runtime gate", func(t *testing.T) {
		svc, cache := newSvc(map[string]int64{})
		ctx := profitControlTestCtx(profitControlTestGroup(groupID, 0.5, 0))
		selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", sessionHash, "gpt-test", nil, OpenAIUpstreamTransportAny, false)
		require.NoError(t, err)
		require.NotNil(t, selection)
		require.Contains(t, []int64{cheap.ID, expensive.ID}, selection.Account.ID)
		require.Contains(t, cache.sessionBindings, "openai:"+sessionHash)
		require.Equal(t, selection.Account.ID, cache.sessionBindings["openai:"+sessionHash])
		require.False(t, selection.ProfitGateActive())
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
	})

	t.Run("ungated selection keeps official eager binding", func(t *testing.T) {
		svc, cache := newSvc(map[string]int64{})
		group := profitControlTestGroup(groupID, 0.5, 0)
		group.ProfitControlEnabled = false
		selection, _, err := svc.SelectAccountWithScheduler(profitControlTestCtx(group), &groupID, "", sessionHash, "gpt-test", nil, OpenAIUpstreamTransportAny, false)
		require.NoError(t, err)
		require.NotNil(t, selection)
		require.NotEmpty(t, cache.sessionBindings, "无门时 legacy 选号保持官方 eager 绑定")
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
	})
}
