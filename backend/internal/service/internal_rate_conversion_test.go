package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

type internalRateSettingRepo struct {
	SettingRepository
	values map[string]string
	reads  int
	err    error
}

func (r *internalRateSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	r.reads++
	if r.err != nil {
		return "", r.err
	}
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", ErrSettingNotFound
}

func (r *internalRateSettingRepo) GetAll(context.Context) (map[string]string, error) {
	result := make(map[string]string, len(r.values))
	for k, v := range r.values {
		result[k] = v
	}
	return result, nil
}

func (r *internalRateSettingRepo) SetMultiple(_ context.Context, values map[string]string) error {
	for k, v := range values {
		r.values[k] = v
	}
	return nil
}

// Existing billing fixtures can explicitly select the unconverted branch;
// default-on behavior is tested independently with an absent setting below.
func newTestInternalRateConversionSettings(enabled bool) *SettingService {
	return NewSettingService(&internalRateSettingRepo{values: map[string]string{
		SettingKeyInternalRateConversionEnabled: strconv.FormatBool(enabled),
	}}, &config.Config{})
}

func TestInternalRateConversionSettingsDefaultSaveAndCache(t *testing.T) {
	// parseSettings publishes Grok defaults; isolate that existing side effect
	// from unrelated mapping contracts in the same service test process.
	originalGrokMapping := xai.RuntimeModelMappingOptions()
	t.Cleanup(func() { xai.SetRuntimeModelMappingOptions(originalGrokMapping) })
	ctx := context.Background()
	repo := &internalRateSettingRepo{values: map[string]string{}}
	svc := NewSettingService(repo, &config.Config{})
	require.True(t, svc.IsInternalRateConversionEnabled(ctx))
	require.True(t, svc.IsInternalRateConversionEnabled(ctx))
	require.Equal(t, 1, repo.reads, "hot path must use the setting cache")
	settings, err := svc.GetAllSettings(ctx)
	require.NoError(t, err)
	require.True(t, settings.InternalRateConversionEnabled)
	settings.InternalRateConversionEnabled = false
	require.NoError(t, svc.UpdateSettings(ctx, settings))
	require.Equal(t, "false", repo.values[SettingKeyInternalRateConversionEnabled])
	require.False(t, svc.IsInternalRateConversionEnabled(ctx), "save refreshes the cache immediately")
	require.False(t, NewSettingService(repo, &config.Config{}).IsInternalRateConversionEnabled(ctx), "disabled survives restart")

	svc.internalRateConversionCache.Store(&cachedInternalRateConversion{enabled: false, expiresAt: time.Now().Add(-time.Minute)})
	repo.err = errors.New("temporary setting read failure")
	require.False(t, svc.IsInternalRateConversionEnabled(ctx), "a read failure must not turn a cached off setting back on")

	publicJSON, err := json.Marshal(PublicSettings{})
	require.NoError(t, err)
	require.NotContains(t, string(publicJSON), "internal_rate_conversion")
}

func TestInternalRateConversionAmountsAndSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name         string
		amount, want float64
	}{
		{"half of ten", 5, 33.64125},
		{"free", 0, 0},
		{"eight decimals", 0.00000001, 0.00000007},
		{"sub-unit precision", 0.000000001, 0.00000001},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cost := &CostBreakdown{InputCost: 7, CacheReadCost: 1, OutputCost: 2, TotalCost: 10, ActualCost: tc.amount}
			usage := &UsageLog{TotalCost: 10, RateMultiplier: 0.5}
			prepareInternalRateConversion(context.Background(), nil, cost, usage)
			require.Equal(t, tc.want, cost.ActualCost)
			require.Equal(t, tc.want, usage.ActualCost)
			require.Equal(t, 10.0, cost.TotalCost)
			require.Equal(t, 7.0, cost.InputCost)
			require.Equal(t, 1.0, cost.CacheReadCost)
			require.Equal(t, 0.5, usage.RateMultiplier)
			prepareInternalRateConversion(context.Background(), newTestInternalRateConversionSettings(false), cost, usage)
			require.Equal(t, tc.want, cost.ActualCost, "retry keeps its already prepared amount")
		})
	}
	cost := &CostBreakdown{TotalCost: 10, ActualCost: 5}
	prepareInternalRateConversion(context.Background(), newTestInternalRateConversionSettings(false), cost, nil)
	prepareInternalRateConversion(context.Background(), nil, cost, nil)
	require.Equal(t, 5.0, cost.ActualCost, "a prepared disabled request also retains its original price")
}

type internalRateBillingRepo struct {
	UsageBillingRepository
	commands []*UsageBillingCommand
	seen     map[string]string
}

func (r *internalRateBillingRepo) Apply(_ context.Context, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	copy := *cmd
	r.commands = append(r.commands, &copy)
	if r.seen == nil {
		r.seen = map[string]string{}
	}
	if previous, exists := r.seen[cmd.RequestID]; exists {
		if previous != cmd.RequestFingerprint {
			return nil, ErrUsageBillingRequestConflict
		}
		return &UsageBillingApplyResult{}, nil
	}
	r.seen[cmd.RequestID] = cmd.RequestFingerprint
	return &UsageBillingApplyResult{Applied: true}, nil
}

func TestInternalRateConversionAtomicBalanceAndSubscription(t *testing.T) {
	for _, subscription := range []bool{false, true} {
		t.Run(strconv.FormatBool(subscription), func(t *testing.T) {
			repo := &internalRateBillingRepo{}
			deps := &billingDeps{billingCacheService: &BillingCacheService{}, deferredService: &DeferredService{}}
			p := &postUsageBillingParams{
				Cost: &CostBreakdown{TotalCost: 10, ActualCost: 5}, User: &User{ID: 1},
				APIKey:       &APIKey{ID: 2, GroupID: i64p(7), Quota: 100, RateLimit5h: 100},
				Account:      &Account{ID: 3, Type: AccountTypeAPIKey, Extra: map[string]any{"quota_limit": 100.0}},
				Subscription: &UserSubscription{ID: 4}, IsSubscriptionBill: subscription,
				AccountRateMultiplier: 2, APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{},
			}
			usage := &UsageLog{TotalCost: 10, RateMultiplier: 0.5}
			applied, err := applyUsageBilling(context.Background(), "converted-request", usage, p, deps, repo)
			require.NoError(t, err)
			require.True(t, applied)
			cmd := repo.commands[0]
			require.Equal(t, 33.64125, usage.ActualCost)
			require.Equal(t, usage.ActualCost, cmd.APIKeyQuotaCost)
			require.Equal(t, usage.ActualCost, cmd.APIKeyRateLimitCost)
			require.Equal(t, 20.0, cmd.AccountQuotaCost)
			if subscription {
				require.Equal(t, usage.ActualCost, cmd.SubscriptionCost)
				require.Zero(t, cmd.BalanceCost)
			} else {
				require.Equal(t, usage.ActualCost, cmd.BalanceCost)
				require.Zero(t, cmd.SubscriptionCost)
			}
			deps.settingService = newTestInternalRateConversionSettings(false)
			applied, err = applyUsageBilling(context.Background(), "converted-request", usage, p, deps, repo)
			require.NoError(t, err)
			require.False(t, applied)
			require.Equal(t, cmd.RequestFingerprint, repo.commands[1].RequestFingerprint)
			require.Equal(t, 33.64125, usage.ActualCost)
		})
	}
}

type internalRateSubRepo struct {
	UserSubscriptionRepository
	amount float64
}

func (r *internalRateSubRepo) IncrementUsage(_ context.Context, _ int64, amount float64) error {
	r.amount = amount
	return nil
}

func TestInternalRateConversionLegacyBalanceAndSubscription(t *testing.T) {
	for _, subscription := range []bool{false, true} {
		t.Run(strconv.FormatBool(subscription), func(t *testing.T) {
			users, subs := &openAIRecordUsageUserRepoStub{}, &internalRateSubRepo{}
			quota := &openAIRecordUsageAPIKeyQuotaStub{}
			p := &postUsageBillingParams{
				Cost: &CostBreakdown{TotalCost: 10, ActualCost: 5}, User: &User{ID: 1},
				APIKey: &APIKey{ID: 2, Quota: 100, RateLimit5h: 100}, Account: &Account{ID: 3},
				Subscription: &UserSubscription{ID: 4}, IsSubscriptionBill: subscription, APIKeyService: quota,
			}
			usage := &UsageLog{}
			applied, err := applyUsageBilling(context.Background(), "legacy", usage, p,
				&billingDeps{userRepo: users, userSubRepo: subs}, nil)
			require.NoError(t, err)
			require.True(t, applied)
			require.Equal(t, 33.64125, usage.ActualCost)
			require.Equal(t, usage.ActualCost, quota.lastAmount)
			if subscription {
				require.Equal(t, usage.ActualCost, subs.amount)
			} else {
				require.Equal(t, usage.ActualCost, users.lastAmount)
			}
		})
	}
}

func TestInternalRateConversionGatewayWiring(t *testing.T) {
	for _, openAI := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			t.Run(strconv.FormatBool(openAI)+"/"+strconv.FormatBool(enabled), func(t *testing.T) {
				settings := newTestInternalRateConversionSettings(enabled)
				logs, billing := &openAIRecordUsageLogRepoStub{inserted: true}, &internalRateBillingRepo{}
				cfg := &config.Config{}
				bs := NewBillingService(cfg, nil)
				key := &APIKey{ID: 2, GroupID: i64p(7), Group: &Group{ID: 7, RateMultiplier: 0.5}}
				account := &Account{ID: 3, Platform: PlatformAnthropic}
				if openAI {
					svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, billing, nil, nil, nil)
					svc.settingService = settings
					require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
						Result: &OpenAIForwardResult{RequestID: "openai-conversion", Model: "gpt-5.1", Usage: OpenAIUsage{InputTokens: 1000, OutputTokens: 100}},
						APIKey: key, User: &User{ID: 1}, Account: &Account{ID: 3, Platform: PlatformOpenAI},
					}))
				} else {
					svc := &GatewayService{cfg: cfg, settingService: settings, billingService: bs, resolver: NewModelPricingResolver(nil, bs),
						usageLogRepo: logs, usageBillingRepo: billing, billingCacheService: &BillingCacheService{}, deferredService: &DeferredService{}}
					require.NoError(t, svc.RecordUsage(context.Background(), &RecordUsageInput{
						Result: &ForwardResult{RequestID: "gateway-conversion", Model: "claude-sonnet-4", Usage: ClaudeUsage{InputTokens: 1000, OutputTokens: 100, CacheReadInputTokens: 250}},
						APIKey: key, User: &User{ID: 1}, Account: account,
					}))
				}
				require.Len(t, billing.commands, 1)
				require.NotNil(t, logs.lastLog)
				want := convertInternalRateAmount(logs.lastLog.TotalCost*0.5, enabled, UsageBillingMonetaryScale)
				require.Equal(t, want, logs.lastLog.ActualCost)
				require.Equal(t, QuantizeUsageBillingAmount(want), billing.commands[0].BalanceCost)
				require.Equal(t, 0.5, logs.lastLog.RateMultiplier)
				require.Equal(t, 0.5, key.Group.RateMultiplier)
			})
		}
	}
}
