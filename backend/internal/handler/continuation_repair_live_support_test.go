//go:build continuation_repair_live

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// This fixture exercises the production handler, forwarder and Ops parser, but
// every persistence dependency belongs to this process. Its usage rows prove
// request attribution only: the synthetic balance and projected channel are not
// a production-pricing or live-concurrency acceptance test.
type continuationRepairFixture struct {
	handler *OpenAIGatewayHandler
	ops     *service.OpsService
	gateway *service.OpenAIGatewayService
	billing *service.BillingCacheService
	account *continuationRepairAccountStore
	usage   *continuationRepairUsageStore
	key     *service.APIKey
}

func continuationRepairBuildFixture(ctx context.Context, cfg *config.Config, source continuationRepairSource, upstream service.HTTPUpstream) (*continuationRepairFixture, error) {
	if cfg == nil || upstream == nil || source.Account.ID != 16050 || source.Group.ID != 35 || source.UserID != 920000016050 || source.FastPolicy == nil {
		return nil, errors.New("invalid_fixture_source")
	}
	// The captured publication contains prompt hashes, not the candidate file
	// tree required by Registry.Reload. Never synthesize a hybrid publication or
	// switch an enabled source policy off to make this narrow test proceed.
	if source.BusinessPrompt.Enabled || source.BusinessPrompt.CompactEnabled || source.BusinessPrompt.ExposeServerPrompt {
		return nil, errors.New("unsupported_source_policy")
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		return nil, errors.New("invalid_fixture_source")
	}
	var frozen continuationRepairSource
	if json.Unmarshal(encoded, &frozen) != nil {
		return nil, errors.New("invalid_fixture_source")
	}
	settings := &continuationRepairSettingStore{values: frozen.Settings}
	if settings.values == nil {
		settings.values = make(map[string]string)
	}
	fastJSON, err := json.Marshal(frozen.FastPolicy)
	if err != nil {
		return nil, errors.New("invalid_fast_policy")
	}
	// FastPolicy is captured separately by the broker. If its backing setting
	// was included too, both sources must agree before any model request.
	if raw, ok := settings.values[service.SettingKeyOpenAIFastPolicySettings]; ok {
		var existing service.OpenAIFastPolicySettings
		if strings.TrimSpace(raw) == "" {
			existing = *service.DefaultOpenAIFastPolicySettings()
		} else if json.Unmarshal([]byte(raw), &existing) != nil {
			return nil, errors.New("invalid_fast_policy")
		}
		if !reflect.DeepEqual(existing, *frozen.FastPolicy) {
			return nil, errors.New("fast_policy_snapshot_mismatch")
		}
	}
	settings.values[service.SettingKeyOpenAIFastPolicySettings] = string(fastJSON)
	settingService := service.NewSettingService(settings, cfg)
	account := &continuationRepairAccountStore{account: frozen.Account, groupID: frozen.Group.ID, temp: make(map[int64]*service.TempUnschedState)}
	groups := &continuationRepairGroupStore{group: frozen.Group}
	usage := &continuationRepairUsageStore{}
	user := &service.User{ID: frozen.UserID, Status: service.StatusActive, Balance: 1000000, Concurrency: 1}
	users := &continuationRepairUserStore{user: user}
	key := &service.APIKey{ID: 920000016051, UserID: user.ID, User: user, GroupID: &groups.group.ID, Group: &groups.group, Status: service.StatusActive}
	billing := service.NewBillingCacheService(nil, users, nil, nil, nil, nil, cfg, nil)
	concurrency := service.NewConcurrencyService(&concurrencyCacheMock{
		acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
		acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
	})
	channels := service.NewChannelService(&continuationRepairChannelStore{group: groups.group, models: frozen.ChannelModels}, groups, nil, nil)
	rateLimits := service.NewRateLimitService(account, usage, cfg, nil, account)
	rateLimits.SetSettingService(settingService)
	rateLimits.SetOpenAIAPIKeyHealthCache(account)
	cache := &continuationRepairGatewayCache{sessions: make(map[continuationRepairSessionKey]int64), reasoning: make(map[string]string)}
	gateway := service.NewOpenAIGatewayService(account, usage, &continuationRepairBillingStore{users: users, applied: make(map[string]string)}, users,
		nil, nil, cache, cfg, nil, concurrency, service.NewBillingService(cfg, nil), rateLimits, billing, upstream,
		service.NewDeferredService(account, nil, time.Minute), nil, nil, nil, channels, nil, settingService, nil)
	prompt := service.NewBusinessSystemPromptService(&continuationRepairPromptStore{snapshot: frozen.BusinessPrompt}, nil)
	prompt.SetBusinessSystemPromptSource(nil)
	if err := prompt.Reload(ctx); err != nil {
		billing.Stop()
		gateway.CloseOpenAIWSPool()
		return nil, errors.New("invalid_frozen_prompt")
	}
	gateway.SetBusinessSystemPromptService(prompt)
	ops := service.NewOpsService(nil, settings, cfg, account, users, concurrency, nil, gateway, nil, nil, nil)
	keys := service.NewAPIKeyService(nil, users, groups, nil, nil, nil, cfg)
	h := NewOpenAIGatewayHandler(gateway, concurrency, billing, keys, nil, nil, nil, ops, cfg)
	return &continuationRepairFixture{handler: h, ops: ops, gateway: gateway, billing: billing, account: account, usage: usage, key: key}, nil
}

func (f *continuationRepairFixture) decorateContext(c *gin.Context) {
	c.Set(string(middleware2.ContextKeyAPIKey), f.key)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: f.key.UserID, Concurrency: f.key.User.Concurrency})
	ctx := context.WithValue(c.Request.Context(), ctxkey.Group, f.key.Group)
	ctx = context.WithValue(ctx, ctxkey.UserID, f.key.UserID)
	if id := strings.TrimSpace(c.GetHeader("X-Request-ID")); id != "" {
		ctx = context.WithValue(ctx, ctxkey.RequestID, id)
		c.Set("request_id", id)
	}
	c.Request = c.Request.WithContext(ctx)
	service.SetOpenAIClientTransport(c, service.OpenAIClientTransportHTTP)
}

func (f *continuationRepairFixture) usageSnapshot() []service.UsageLog {
	f.usage.mu.Lock()
	defer f.usage.mu.Unlock()
	return append([]service.UsageLog(nil), f.usage.rows...)
}

func (f *continuationRepairFixture) healthChanges() int { return int(f.account.health.Load()) }

func (f *continuationRepairFixture) close() {
	f.billing.Stop()
	f.gateway.CloseOpenAIWSPool()
}

type continuationRepairSettingStore struct{ values map[string]string }

func (s *continuationRepairSettingStore) Get(ctx context.Context, key string) (*service.Setting, error) {
	v, err := s.GetValue(ctx, key)
	if err != nil {
		return nil, err
	}
	return &service.Setting{Key: key, Value: v}, nil
}
func (s *continuationRepairSettingStore) GetValue(_ context.Context, key string) (string, error) {
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", service.ErrSettingNotFound
}
func (s *continuationRepairSettingStore) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string)
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}
func (s *continuationRepairSettingStore) GetAll(context.Context) (map[string]string, error) {
	out := make(map[string]string, len(s.values))
	for k, v := range s.values {
		out[k] = v
	}
	return out, nil
}
func (*continuationRepairSettingStore) Set(context.Context, string, string) error {
	return errors.New("diagnostic_read_only")
}
func (*continuationRepairSettingStore) SetMultiple(context.Context, map[string]string) error {
	return errors.New("diagnostic_read_only")
}
func (*continuationRepairSettingStore) Delete(context.Context, string) error {
	return errors.New("diagnostic_read_only")
}

type continuationRepairPromptStore struct {
	service.BusinessSystemPromptStore
	snapshot service.BusinessSystemPromptSnapshot
}

func (s *continuationRepairPromptStore) LoadBusinessSystemPrompt(context.Context) (service.BusinessSystemPromptSnapshot, error) {
	return s.snapshot, nil
}

type continuationRepairGroupStore struct {
	service.GroupRepository
	group service.Group
}

func (s *continuationRepairGroupStore) GetByID(_ context.Context, id int64) (*service.Group, error) {
	if id != s.group.ID {
		return nil, service.ErrGroupNotFound
	}
	out := s.group
	return &out, nil
}
func (s *continuationRepairGroupStore) GetByIDLite(ctx context.Context, id int64) (*service.Group, error) {
	return s.GetByID(ctx, id)
}

// The broker freezes resolved channel model names, not a billing contract.
// Keeping a separate channel projection exercises normal mapping exactly once
// without changing any account mapping or claiming a production channel ID.
type continuationRepairChannelStore struct {
	service.ChannelRepository
	group  service.Group
	models map[string]string
}

func (s *continuationRepairChannelStore) ListAll(context.Context) ([]service.Channel, error) {
	return []service.Channel{{ID: -1, Name: "continuation-diagnostic-projection", Status: service.StatusActive,
		BillingModelSource: service.BillingModelSourceRequested, GroupIDs: []int64{s.group.ID},
		ModelMapping: map[string]map[string]string{s.group.Platform: s.models}}}, nil
}
func (s *continuationRepairChannelStore) GetGroupPlatforms(context.Context, []int64) (map[int64]string, error) {
	return map[int64]string{s.group.ID: s.group.Platform}, nil
}

type continuationRepairUserStore struct {
	service.UserRepository
	mu   sync.Mutex
	user *service.User
}

func (s *continuationRepairUserStore) GetByID(_ context.Context, id int64) (*service.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.user.ID {
		return nil, errors.New("diagnostic_unknown_user")
	}
	u := *s.user
	return &u, nil
}

type continuationRepairBillingStore struct {
	service.UsageBillingRepository
	mu      sync.Mutex
	users   *continuationRepairUserStore
	applied map[string]string
}

func (s *continuationRepairBillingStore) Apply(_ context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cmd == nil || cmd.UserID != s.users.user.ID || cmd.APIKeyID != 920000016051 || cmd.AccountID != 16050 {
		return nil, errors.New("diagnostic_billing_scope_mismatch")
	}
	cmd.Normalize()
	if prior, ok := s.applied[cmd.RequestID]; ok {
		if prior != cmd.RequestFingerprint {
			return nil, service.ErrUsageBillingRequestConflict
		}
		return &service.UsageBillingApplyResult{Applied: false}, nil
	}
	s.applied[cmd.RequestID] = cmd.RequestFingerprint
	s.users.mu.Lock()
	s.users.user.Balance -= cmd.BalanceCost
	balance := s.users.user.Balance
	s.users.mu.Unlock()
	return &service.UsageBillingApplyResult{Applied: true, OwnerAccountID: cmd.AccountID, NewBalance: &balance}, nil
}

type continuationRepairUsageStore struct {
	service.UsageLogRepository
	mu   sync.Mutex
	rows []service.UsageLog
}

func (s *continuationRepairUsageStore) Create(_ context.Context, row *service.UsageLog) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if row == nil || row.AccountID != 16050 || row.UserID != 920000016050 || row.APIKeyID != 920000016051 {
		return false, errors.New("diagnostic_usage_scope_mismatch")
	}
	for _, old := range s.rows {
		if old.RequestID == row.RequestID && old.APIKeyID == row.APIKeyID {
			return false, nil
		}
	}
	s.rows = append(s.rows, *row)
	return true, nil
}

type continuationRepairAccountStore struct {
	service.AccountRepository
	account service.Account
	groupID int64
	health  atomic.Int64
	mu      sync.Mutex
	temp    map[int64]*service.TempUnschedState
	fails   int64
}

func (s *continuationRepairAccountStore) GetByID(_ context.Context, id int64) (*service.Account, error) {
	if id != s.account.ID {
		return nil, service.ErrAccountNotFound
	}
	// Forward may enrich its local account view. Do not let that mutate the
	// source snapshot or the next request's immutable scheduling input.
	raw, err := json.Marshal(s.account)
	if err != nil {
		return nil, errors.New("invalid_frozen_account")
	}
	var out service.Account
	if json.Unmarshal(raw, &out) != nil {
		return nil, errors.New("invalid_frozen_account")
	}
	return &out, nil
}
func (s *continuationRepairAccountStore) GetByIDs(ctx context.Context, ids []int64) ([]*service.Account, error) {
	var rows []*service.Account
	for _, id := range ids {
		if id == s.account.ID {
			row, err := s.GetByID(ctx, id)
			if err != nil {
				return nil, err
			}
			rows = append(rows, row)
		}
	}
	return rows, nil
}
func (s *continuationRepairAccountStore) ListByGroup(ctx context.Context, id int64) ([]service.Account, error) {
	if id != s.groupID {
		return nil, nil
	}
	return s.ListByPlatform(ctx, s.account.Platform)
}
func (s *continuationRepairAccountStore) ListByPlatform(ctx context.Context, platform string) ([]service.Account, error) {
	if platform != s.account.Platform {
		return nil, nil
	}
	row, err := s.GetByID(ctx, s.account.ID)
	if err != nil {
		return nil, err
	}
	return []service.Account{*row}, nil
}
func (s *continuationRepairAccountStore) ListSchedulableByPlatform(ctx context.Context, platform string) ([]service.Account, error) {
	if !s.account.IsSchedulable() {
		return nil, nil
	}
	return s.ListByPlatform(ctx, platform)
}
func (s *continuationRepairAccountStore) ListSchedulableByGroupIDAndPlatform(ctx context.Context, id int64, platform string) ([]service.Account, error) {
	if id != s.groupID {
		return nil, nil
	}
	return s.ListSchedulableByPlatform(ctx, platform)
}
func (s *continuationRepairAccountStore) ListSchedulableUngroupedByPlatform(context.Context, string) ([]service.Account, error) {
	return nil, nil
}
func (s *continuationRepairAccountStore) ListModelAvailabilityCandidates(ctx context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]service.Account, error) {
	if (groupID == nil && !includeGrouped) || (groupID != nil && *groupID != s.groupID) || !s.account.IsActive() || !s.account.Schedulable {
		return nil, nil
	}
	for _, p := range platforms {
		if p == s.account.Platform {
			return s.ListByPlatform(ctx, p)
		}
	}
	return nil, nil
}
func (s *continuationRepairAccountStore) SetError(context.Context, int64, string) error {
	s.health.Add(1)
	return nil
}
func (s *continuationRepairAccountStore) SetSchedulable(context.Context, int64, bool) error {
	s.health.Add(1)
	return nil
}
func (s *continuationRepairAccountStore) SetRateLimited(context.Context, int64, time.Time) error {
	s.health.Add(1)
	return nil
}
func (s *continuationRepairAccountStore) SetModelRateLimit(context.Context, int64, string, time.Time, ...string) error {
	s.health.Add(1)
	return nil
}
func (s *continuationRepairAccountStore) SetOverloaded(context.Context, int64, time.Time) error {
	s.health.Add(1)
	return nil
}
func (s *continuationRepairAccountStore) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	s.health.Add(1)
	return nil
}
func (s *continuationRepairAccountStore) UpdateExtra(context.Context, int64, map[string]any) error {
	// Quota/fingerprint telemetry is intentionally not persisted by a fixture.
	return nil
}
func (s *continuationRepairAccountStore) UpdateSessionWindow(context.Context, int64, *time.Time, *time.Time, string) error {
	return nil
}
func (s *continuationRepairAccountStore) GetTempUnsched(_ context.Context, id int64) (*service.TempUnschedState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.temp[id], nil
}
func (s *continuationRepairAccountStore) SetTempUnsched(_ context.Context, id int64, state *service.TempUnschedState) error {
	s.health.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if state != nil {
		cp := *state
		s.temp[id] = &cp
	}
	return nil
}
func (s *continuationRepairAccountStore) DeleteTempUnsched(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.temp, id)
	return nil
}
func (s *continuationRepairAccountStore) RecordOpenAIAPIKeyHealthFailure(_ context.Context, _ int64, _ int, threshold int) (int64, bool, error) {
	s.health.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fails++
	return s.fails, s.fails >= int64(threshold), nil
}

type continuationRepairSessionKey struct {
	groupID int64
	hash    string
}
type continuationRepairGatewayCache struct {
	service.GatewayCache
	mu        sync.Mutex
	sessions  map[continuationRepairSessionKey]int64
	reasoning map[string]string
}

func (s *continuationRepairGatewayCache) GetSessionAccountID(_ context.Context, id int64, hash string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if account, ok := s.sessions[continuationRepairSessionKey{id, hash}]; ok {
		return account, nil
	}
	return 0, service.ErrStickySessionNotFound
}
func (s *continuationRepairGatewayCache) SetSessionAccountID(_ context.Context, groupID int64, hash string, accountID int64, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[continuationRepairSessionKey{groupID, hash}] = accountID
	return nil
}
func (*continuationRepairGatewayCache) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}
func (s *continuationRepairGatewayCache) DeleteSessionAccountID(_ context.Context, groupID int64, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, continuationRepairSessionKey{groupID, hash})
	return nil
}
func (s *continuationRepairGatewayCache) DeleteSessionAccountIDIfMatches(_ context.Context, groupID int64, hash string, accountID int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := continuationRepairSessionKey{groupID, hash}
	if s.sessions[key] != accountID {
		return false, nil
	}
	delete(s.sessions, key)
	return true, nil
}
func (s *continuationRepairGatewayCache) SetReasoningContent(_ context.Context, id, content string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reasoning[id] = content
	return nil
}
func (s *continuationRepairGatewayCache) GetReasoningContent(_ context.Context, id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.reasoning[id]; ok {
		return value, nil
	}
	return "", service.ErrReasoningContentNotFound
}
