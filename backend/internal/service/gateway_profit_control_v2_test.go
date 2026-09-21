//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func gatewayProfitTestGroup(id int64, platform string) *Group {
	return &Group{
		ID:                   id,
		Name:                 "profit-" + platform,
		Platform:             platform,
		Status:               StatusActive,
		Hydrated:             true,
		RateMultiplier:       0.5,
		SubscriptionType:     SubscriptionTypeStandard,
		ProfitControlEnabled: true,
		ProfitMinMargin:      0,
		ProfitSafetyBuffer:   0,
	}
}

func gatewayProfitTestContext(group *Group) context.Context {
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	ctx, _ = WithGatewayTokenRequestPricing(ctx)
	return ctx
}

func gatewayProfitTestAccount(id int64, platform string, rate float64, groupID int64) Account {
	return Account{
		ID:             id,
		Name:           "account",
		Platform:       platform,
		Type:           AccountTypeAPIKey,
		Status:         StatusActive,
		Schedulable:    true,
		Concurrency:    2,
		Priority:       1,
		RateMultiplier: &rate,
		AccountGroups:  []AccountGroup{{AccountID: id, GroupID: groupID}},
		GroupIDs:       []int64{groupID},
	}
}

func TestGatewayDormantProfitControlKeepsFivePlatformPricing(t *testing.T) {
	for _, platform := range []string{
		PlatformOpenAI,
		PlatformAnthropic,
		PlatformGemini,
		PlatformGrok,
		PlatformAntigravity,
	} {
		t.Run(platform, func(t *testing.T) {
			group := gatewayProfitTestGroup(101, platform)
			groupID := group.ID
			svc := &GatewayService{}

			base := gatewayProfitTestContext(group)
			pricingAt, ok := gatewayTokenRequestPricingAtFromContext(base)
			require.True(t, ok)
			tokenCtx := svc.withGatewayProfitControlGate(base, &groupID)
			gate, _ := tokenCtx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
			require.Nil(t, gate)
			actual, ok := gatewayTokenRequestPricingAtFromContext(tokenCtx)
			require.True(t, ok)
			require.Equal(t, pricingAt, actual)
			require.False(t, pricingAt.IsZero())
			require.True(t, group.ProfitControlEnabled)
			require.Equal(t, 0.5, group.RateMultiplier)
			expensive := gatewayProfitTestAccount(102, platform, 5, groupID)
			require.True(t, svc.isGatewayAccountProfitEligible(tokenCtx, &expensive))

			metadataCtx := context.WithValue(context.Background(), ctxkey.Group, group)
			metadataCtx = svc.withGatewayProfitControlGate(metadataCtx, &groupID)
			gate, _ = metadataCtx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
			require.Nil(t, gate, "未显式标记为 token 请求的入口不得装门")
		})
	}
}

func TestGatewayDormantProfitControlPreservesCompositeBilling(t *testing.T) {
	billingGroup := &Group{
		ID:               201,
		Platform:         PlatformComposite,
		Status:           StatusActive,
		Hydrated:         true,
		RateMultiplier:   0.4,
		SubscriptionType: SubscriptionTypeStandard,
	}
	memberGroup := gatewayProfitTestGroup(202, PlatformAnthropic)
	memberGroup.RateMultiplier = 99
	memberGroup.ProfitMinMargin = 0.25

	ctx := context.WithValue(context.Background(), ctxkey.Group, billingGroup)
	ctx, pricingAt := WithGatewayTokenRequestPricing(ctx)
	svc := &GatewayService{
		schedulerSnapshot: NewSchedulerSnapshotService(
			nil,
			nil,
			nil,
			profitControlGroupRepo{group: memberGroup},
			nil,
		),
	}
	ctx = svc.withGatewayProfitControlGate(ctx, &memberGroup.ID)
	gate, _ := ctx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
	require.Nil(t, gate)
	actual, ok := gatewayTokenRequestPricingAtFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, pricingAt, actual)
	require.Equal(t, 0.4, billingGroup.RateMultiplier)
	require.Equal(t, 99.0, memberGroup.RateMultiplier)
	require.Equal(t, 0.25, memberGroup.ProfitMinMargin)
}

func TestGatewayProfitControlGroupLoadFailureClearsForeignGate(t *testing.T) {
	billingGroup := &Group{
		ID:               211,
		Platform:         PlatformComposite,
		Status:           StatusActive,
		Hydrated:         true,
		RateMultiplier:   0.4,
		SubscriptionType: SubscriptionTypeStandard,
	}
	targetGroupID := int64(212)
	ctx := gatewayProfitTestContext(billingGroup)
	ctx = context.WithValue(ctx, openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{
		groupID:   210,
		platform:  PlatformAnthropic,
		threshold: 0.1,
	})
	svc := &GatewayService{
		schedulerSnapshot: NewSchedulerSnapshotService(
			nil,
			nil,
			nil,
			profitControlFailingGroupRepo{},
			nil,
		),
	}

	ctx = svc.withGatewayProfitControlGate(ctx, &targetGroupID)
	gate, ok := ctx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
	require.True(t, ok)
	require.Nil(t, gate, "加载新分组失败时必须清除其他分组遗留的门")

	account := gatewayProfitTestAccount(213, PlatformAnthropic, 0.8, targetGroupID)
	require.True(t, svc.isGatewayAccountProfitEligible(ctx, &account), "配置读取失败按既定语义 fail-open")
}

type profitControlFailingGroupRepo struct {
	GroupRepository
}

func (profitControlFailingGroupRepo) GetByIDLite(context.Context, int64) (*Group, error) {
	return nil, errors.New("group cache unavailable")
}

// 见 profitControlGroupRepo.GetByID：利润门必须走不带账号计数聚合的 lite 读取。
func (profitControlFailingGroupRepo) GetByID(context.Context, int64) (*Group, error) {
	panic("profit control gate must read groups via GetByIDLite (no account-count aggregation)")
}

func TestGatewayDormantProfitControlKeepsLegacyMixedAndRoutedSelection(t *testing.T) {
	t.Run("legacy single-platform selection", func(t *testing.T) {
		group := gatewayProfitTestGroup(111, PlatformGrok)
		cheap := gatewayProfitTestAccount(1, PlatformGrok, 0.2, group.ID)
		expensive := gatewayProfitTestAccount(2, PlatformGrok, 0.8, group.ID)
		repo := &mockAccountRepoForPlatform{
			accounts:     []Account{expensive, cheap},
			accountsByID: map[int64]*Account{cheap.ID: &cheap, expensive.ID: &expensive},
		}
		svc := &GatewayService{
			accountRepo: repo,
			cache:       &mockGatewayCacheForPlatform{},
			cfg:         testConfig(),
		}

		selected, err := svc.SelectAccountForModelWithExclusions(
			gatewayProfitTestContext(group), &group.ID, "", "", nil,
		)
		require.NoError(t, err)
		require.Contains(t, []int64{cheap.ID, expensive.ID}, selected.ID)

		selected, err = svc.SelectAccountForModelWithExclusions(
			gatewayProfitTestContext(group), &group.ID, "", "", map[int64]struct{}{cheap.ID: {}},
		)
		require.NoError(t, err)
		require.Equal(t, expensive.ID, selected.ID, "explicitly excluded cheap account must not return")
	})

	t.Run("mixed routing retains its model-routed account", func(t *testing.T) {
		group := gatewayProfitTestGroup(112, PlatformAnthropic)
		group.ModelRoutingEnabled = true
		group.ModelRouting = map[string][]int64{"claude-test": {2, 1}}
		cheap := gatewayProfitTestAccount(1, PlatformAntigravity, 0.2, group.ID)
		cheap.Extra = map[string]any{"mixed_scheduling": true}
		cheap.Credentials = map[string]any{"model_mapping": map[string]any{"claude-test": "claude-test"}}
		expensive := gatewayProfitTestAccount(2, PlatformAnthropic, 0.8, group.ID)
		repo := &mockAccountRepoForPlatform{
			accounts:     []Account{expensive, cheap},
			accountsByID: map[int64]*Account{cheap.ID: &cheap, expensive.ID: &expensive},
		}
		svc := &GatewayService{
			accountRepo: repo,
			cache:       &mockGatewayCacheForPlatform{},
			cfg:         testConfig(),
		}

		selected, err := svc.SelectAccountForModelWithExclusions(
			gatewayProfitTestContext(group), &group.ID, "", "claude-test", nil,
		)
		require.NoError(t, err)
		require.Contains(t, []int64{cheap.ID, expensive.ID}, selected.ID)
		selected, err = svc.SelectAccountForModelWithExclusions(
			gatewayProfitTestContext(group), &group.ID, "", "claude-test", map[int64]struct{}{cheap.ID: {}},
		)
		require.NoError(t, err)
		require.Equal(t, expensive.ID, selected.ID)
	})
}

func TestGatewayDormantProfitControlKeepsLoadAwareSelectionAndFailover(t *testing.T) {
	group := gatewayProfitTestGroup(121, PlatformGrok)
	cheap := gatewayProfitTestAccount(1, PlatformGrok, 0.2, group.ID)
	expensive := gatewayProfitTestAccount(2, PlatformGrok, 0.8, group.ID)
	repo := &mockAccountRepoForPlatform{
		accounts:     []Account{expensive, cheap},
		accountsByID: map[int64]*Account{cheap.ID: &cheap, expensive.ID: &expensive},
	}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	svc := &GatewayService{
		accountRepo:        repo,
		cache:              &mockGatewayCacheForPlatform{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(stubConcurrencyCache{}),
	}

	result, err := svc.SelectAccountWithLoadAwareness(
		gatewayProfitTestContext(group), &group.ID, "", "", nil, "", 0,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, []int64{cheap.ID, expensive.ID}, result.Account.ID)
	require.False(t, result.ProfitGateActive())
	if result.ReleaseFunc != nil {
		result.ReleaseFunc()
	}

	result, err = svc.SelectAccountWithLoadAwareness(
		gatewayProfitTestContext(group),
		&group.ID,
		"",
		"",
		map[int64]struct{}{cheap.ID: {}},
		"",
		0,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, expensive.ID, result.Account.ID)
	require.False(t, result.ProfitGateActive())
	if result.ReleaseFunc != nil {
		result.ReleaseFunc()
	}
}

func TestGatewayDormantProfitControlKeepsStickyBindingAcrossRateChanges(t *testing.T) {
	group := gatewayProfitTestGroup(131, PlatformAnthropic)
	expensive := gatewayProfitTestAccount(1, PlatformAnthropic, 0.8, group.ID)
	cheap := gatewayProfitTestAccount(2, PlatformAnthropic, 0.2, group.ID)
	repo := &mockAccountRepoForPlatform{
		accounts:     []Account{expensive, cheap},
		accountsByID: map[int64]*Account{expensive.ID: &expensive, cheap.ID: &cheap},
	}
	cache := &mockGatewayCacheForPlatform{
		sessionBindings: map[string]int64{"sticky-profit": expensive.ID},
	}
	svc := &GatewayService{
		accountRepo: repo,
		cache:       cache,
		cfg:         testConfig(),
	}
	ctx := gatewayProfitTestContext(group)

	selected, err := svc.SelectAccountForModelWithExclusions(ctx, &group.ID, "sticky-profit", "", nil)
	require.NoError(t, err)
	require.Equal(t, expensive.ID, selected.ID)
	require.Equal(t, expensive.ID, cache.sessionBindings["sticky-profit"], "候选过滤不得覆盖旧粘性绑定")

	require.NoError(t, svc.BindStickySessionAfterProfitAdmission(
		svc.withGatewayProfitControlGate(ctx, &group.ID),
		&group.ID,
		"sticky-profit",
		selected.ID,
	))
	require.Equal(t, expensive.ID, cache.sessionBindings["sticky-profit"], "终检通过的 fallback 账号也不得覆盖旧绑定")
	require.Zero(t, cache.deletedSessions["sticky-profit"])

	recovered := expensive
	recoveredRate := 0.2
	recovered.RateMultiplier = &recoveredRate
	repo.accounts[0] = recovered
	repo.accountsByID[recovered.ID] = &repo.accounts[0]

	selected, err = svc.SelectAccountForModelWithExclusions(ctx, &group.ID, "sticky-profit", "", nil)
	require.NoError(t, err)
	require.Equal(t, recovered.ID, selected.ID, "倍率恢复后应重新命中原粘性账号")
	require.Zero(t, cache.deletedSessions["sticky-profit"])
}

type gatewayProfitSnapshotCache struct {
	SchedulerCache
	account *Account
	err     error
}

func (c *gatewayProfitSnapshotCache) GetAccount(context.Context, int64) (*Account, error) {
	return c.account, c.err
}

type gatewayProfitAccountRepo struct {
	AccountRepository
	account *Account
	err     error
}

func (r gatewayProfitAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, r.err
}

func TestGatewayProfitControlTerminalRefreshUsesReplacementObject(t *testing.T) {
	selected := gatewayProfitTestAccount(141, PlatformGemini, 0.2, 1)
	replacement := selected
	expensiveRate := 0.8
	replacement.RateMultiplier = &expensiveRate

	snapshot := NewSchedulerSnapshotService(
		&gatewayProfitSnapshotCache{account: &replacement},
		nil,
		gatewayProfitAccountRepo{},
		nil,
		nil,
	)
	ctx := context.WithValue(context.Background(), openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{
		groupID:   1,
		platform:  PlatformGemini,
		threshold: 0.5,
	})

	latest, vetoed, reason := profitControlVetoLatest(ctx, &selected, snapshot)
	require.Same(t, &replacement, latest)
	require.True(t, vetoed)
	require.Equal(t, openAIProfitFilterReasonThreshold, reason)
	require.InDelta(t, 0.2, *selected.RateMultiplier, 1e-12, "测试必须替换缓存对象，不能原地修改旧指针")
}

func TestGatewayProfitControlTerminalRefreshFallsBackFromCacheToDatabase(t *testing.T) {
	selected := gatewayProfitTestAccount(145, PlatformAnthropic, 0.2, 1)
	replacement := selected
	expensiveRate := 0.8
	replacement.RateMultiplier = &expensiveRate

	snapshot := NewSchedulerSnapshotService(
		&gatewayProfitSnapshotCache{err: errors.New("cache unavailable")},
		nil,
		gatewayProfitAccountRepo{account: &replacement},
		nil,
		nil,
	)
	ctx := context.WithValue(context.Background(), openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{
		groupID:   1,
		platform:  PlatformAnthropic,
		threshold: 0.5,
	})

	latest, vetoed, reason := profitControlVetoLatest(ctx, &selected, snapshot)
	require.Same(t, &replacement, latest)
	require.True(t, vetoed, "缓存读取失败时必须继续从数据库重读，不能直接使用选号旧对象")
	require.Equal(t, openAIProfitFilterReasonThreshold, reason)
}

func TestGatewayProfitControlTerminalRefreshFailureFallsBackToSelectedObject(t *testing.T) {
	selected := gatewayProfitTestAccount(151, PlatformAntigravity, 0.2, 1)
	snapshot := NewSchedulerSnapshotService(
		&gatewayProfitSnapshotCache{err: errors.New("cache unavailable")},
		nil,
		gatewayProfitAccountRepo{err: errors.New("database unavailable")},
		nil,
		nil,
	)
	ctx := context.WithValue(context.Background(), openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{
		groupID:   1,
		platform:  PlatformAntigravity,
		threshold: 0.5,
	})

	latest, vetoed, reason := profitControlVetoLatest(ctx, &selected, snapshot)
	require.Same(t, &selected, latest)
	require.False(t, vetoed)
	require.Empty(t, reason)
}

// Runtime selection carries no retired gate to the handler, while its pricing
// context and selected account remain unchanged.
func TestGatewayDormantProfitControlSelectionKeepsUngatedHandlerContext(t *testing.T) {
	group := gatewayProfitTestGroup(1, PlatformAnthropic)
	svc := &GatewayService{}
	expensive := gatewayProfitTestAccount(161, PlatformAnthropic, 0.9, group.ID)

	gateCtx := svc.withGatewayProfitControlGate(gatewayProfitTestContext(group), &group.ID)
	selection, err := svc.newSelectionResult(gateCtx, &expensive, true, nil, nil)
	require.NoError(t, err)
	require.False(t, selection.ProfitGateActive())
	pricingAt, ok := gatewayTokenRequestPricingAtFromContext(gateCtx)
	require.True(t, ok)

	// 修复前的缺陷形态：handler 原始 ctx 不含门，终检退化为空操作。
	_, vetoed, _ := svc.GatewayProfitControlVetoLatest(context.Background(), &expensive)
	require.False(t, vetoed, "对照组：不重放门时终检确实看不到门")

	handlerCtx := ContextWithSelectionProfitGate(gateCtx, selection)
	latest, vetoed, reason := svc.GatewayProfitControlVetoLatest(handlerCtx, &expensive)
	require.False(t, vetoed)
	require.Empty(t, reason)
	require.Same(t, &expensive, latest)
	actual, ok := gatewayTokenRequestPricingAtFromContext(handlerCtx)
	require.True(t, ok)
	require.Equal(t, pricingAt, actual)

	// 无门选号不携带门，重放为无操作。
	plain, err := svc.newSelectionResult(context.Background(), &expensive, true, nil, nil)
	require.NoError(t, err)
	require.False(t, plain.ProfitGateActive())
	require.Equal(t, context.Background(), ContextWithSelectionProfitGate(context.Background(), plain))
}

// Image intent is independent of retired profit admission and must not erase
// the existing token-pricing context.
func TestGatewayDormantProfitControlImageIntentKeepsTokenPricing(t *testing.T) {
	group := gatewayProfitTestGroup(2, PlatformAnthropic)
	svc := &GatewayService{}
	expensive := gatewayProfitTestAccount(162, PlatformAnthropic, 0.9, group.ID)

	ctx := gatewayProfitTestContext(group)
	pricingAt, ok := gatewayTokenRequestPricingAtFromContext(ctx)
	require.True(t, ok)
	ctx = WithOpenAIImageGenerationIntent(ctx)
	gateCtx := svc.withGatewayProfitControlGate(ctx, &group.ID)
	require.True(t, svc.isGatewayAccountProfitEligible(gateCtx, &expensive))
	require.False(t, gatewayProfitControlGateActive(gateCtx))
	actual, ok := gatewayTokenRequestPricingAtFromContext(gateCtx)
	require.True(t, ok)
	require.Equal(t, pricingAt, actual)
}

// 无门时准入后绑定回退官方 eager 语义；门下读失败保守不写（评审 M-Bind 回归）。
func TestGatewayProfitControlAfterAdmissionBindSemantics(t *testing.T) {
	groupID := int64(3)
	expensiveID := int64(171)
	cheapID := int64(172)

	t.Run("eager without gate", func(t *testing.T) {
		cache := &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{"s": expensiveID}}
		svc := &GatewayService{cache: cache}
		require.NoError(t, svc.BindStickySessionAfterProfitAdmission(context.Background(), &groupID, "s", cheapID))
		require.Equal(t, cheapID, cache.sessionBindings["s"], "无门时保持既有 eager 绑定行为")
	})

	t.Run("gated read failure is conservative", func(t *testing.T) {
		// mock 的 miss 返回非 sentinel 错误，等价于 Redis 读失败：门下保守不写。
		cache := &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{}}
		svc := &GatewayService{cache: cache}
		gate := &openAIProfitControlGate{groupID: groupID, platform: PlatformAnthropic, threshold: 0.5}
		gateCtx := context.WithValue(context.Background(), openAIProfitControlGateCtxKey{}, gate)
		require.NoError(t, svc.BindStickySessionAfterProfitAdmission(gateCtx, &groupID, "absent", cheapID))
		require.NotContains(t, cache.sessionBindings, "absent")
	})

	t.Run("gated sentinel miss binds", func(t *testing.T) {
		cache := &sentinelMissGatewayCache{mockGatewayCacheForPlatform: &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{}}}
		svc := &GatewayService{cache: cache}
		gate := &openAIProfitControlGate{groupID: groupID, platform: PlatformAnthropic, threshold: 0.5}
		gateCtx := context.WithValue(context.Background(), openAIProfitControlGateCtxKey{}, gate)
		require.NoError(t, svc.BindStickySessionAfterProfitAdmission(gateCtx, &groupID, "fresh", cheapID))
		require.Equal(t, cheapID, cache.sessionBindings["fresh"], "门下无既有绑定（sentinel miss）应建立粘性")
	})
}

// sentinelMissGatewayCache 让 miss 返回与真实仓库一致的 ErrStickySessionNotFound。
type sentinelMissGatewayCache struct {
	*mockGatewayCacheForPlatform
}

func (c *sentinelMissGatewayCache) GetSessionAccountID(ctx context.Context, groupID int64, sessionHash string) (int64, error) {
	if id, ok := c.sessionBindings[sessionHash]; ok {
		return id, nil
	}
	return 0, ErrStickySessionNotFound
}
