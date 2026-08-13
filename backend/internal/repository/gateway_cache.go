package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const stickySessionPrefix = "sticky_session:"
const liveCallPrefix = "live:call:"
const openAIRuntimeBreakerPrefix = "openai_runtime_breaker:"
const openAIRuntimeBreakerHalfOpenRetention = 5 * time.Minute

type gatewayCache struct {
	rdb *redis.Client
}

func NewGatewayCache(rdb *redis.Client) service.GatewayCache {
	return &gatewayCache{rdb: rdb}
}

// buildSessionKey 构建 session key，包含 groupID 实现分组隔离
// 格式: sticky_session:{groupID}:{sessionHash}
func buildSessionKey(groupID int64, sessionHash string) string {
	return fmt.Sprintf("%s%d:%s", stickySessionPrefix, groupID, sessionHash)
}

func (c *gatewayCache) GetSessionAccountID(ctx context.Context, groupID int64, sessionHash string) (int64, error) {
	key := buildSessionKey(groupID, sessionHash)
	accountID, err := c.rdb.Get(ctx, key).Int64()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, service.ErrStickySessionNotFound
		}
		return 0, err
	}
	return accountID, nil
}

func (c *gatewayCache) SetSessionAccountID(ctx context.Context, groupID int64, sessionHash string, accountID int64, ttl time.Duration) error {
	key := buildSessionKey(groupID, sessionHash)
	return c.rdb.Set(ctx, key, accountID, ttl).Err()
}

func (c *gatewayCache) RefreshSessionTTL(ctx context.Context, groupID int64, sessionHash string, ttl time.Duration) error {
	key := buildSessionKey(groupID, sessionHash)
	return c.rdb.Expire(ctx, key, ttl).Err()
}

// DeleteSessionAccountID 删除粘性会话与账号的绑定关系。
// 当检测到绑定的账号不可用（如状态错误、禁用、不可调度等）时调用，
// 以便下次请求能够重新选择可用账号。
//
// DeleteSessionAccountID removes the sticky session binding for the given session.
// Called when the bound account becomes unavailable (e.g., error status, disabled,
// or unschedulable), allowing subsequent requests to select a new available account.
func (c *gatewayCache) DeleteSessionAccountID(ctx context.Context, groupID int64, sessionHash string) error {
	key := buildSessionKey(groupID, sessionHash)
	return c.rdb.Del(ctx, key).Err()
}

var deleteSessionAccountIDIfMatchesScript = redis.NewScript(`
	if redis.call('GET', KEYS[1]) ~= ARGV[1] then
		return 0
	end
	return redis.call('DEL', KEYS[1])
`)

// DeleteSessionAccountIDIfMatches removes a stale sticky binding only when it
// still points at the account that the scheduler just rejected. This prevents
// a concurrent successful rebind from being deleted by an older request.
func (c *gatewayCache) DeleteSessionAccountIDIfMatches(ctx context.Context, groupID int64, sessionHash string, expectedAccountID int64) (bool, error) {
	key := buildSessionKey(groupID, sessionHash)
	deleted, err := deleteSessionAccountIDIfMatchesScript.Run(
		ctx,
		c.rdb,
		[]string{key},
		strconv.FormatInt(expectedAccountID, 10),
	).Int()
	return deleted == 1, err
}

const (
	grokVideoPendingBillingPrefix = "grok_video_pending:"
	grokVideoBilledPrefix         = "grok_video_billed:"
)

func (c *gatewayCache) SetGrokVideoPendingBilling(ctx context.Context, key string, payload []byte, ttl time.Duration) error {
	if c == nil || c.rdb == nil {
		return errors.New("gateway cache unavailable")
	}
	key = strings.TrimSpace(key)
	if key == "" || len(payload) == 0 {
		return errors.New("invalid grok video pending billing payload")
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return c.rdb.Set(ctx, grokVideoPendingBillingPrefix+key, payload, ttl).Err()
}

func (c *gatewayCache) GetGrokVideoPendingBilling(ctx context.Context, key string) ([]byte, error) {
	if c == nil || c.rdb == nil {
		return nil, errors.New("gateway cache unavailable")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("invalid grok video pending billing key")
	}
	val, err := c.rdb.Get(ctx, grokVideoPendingBillingPrefix+key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	return val, nil
}

func (c *gatewayCache) ClaimGrokVideoBilled(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if c == nil || c.rdb == nil {
		return false, errors.New("gateway cache unavailable")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return false, errors.New("invalid grok video billed key")
	}
	if ttl <= 0 {
		ttl = 48 * time.Hour
	}
	return c.rdb.SetNX(ctx, grokVideoBilledPrefix+key, "1", ttl).Result()
}

func (c *gatewayCache) ReleaseGrokVideoBilled(ctx context.Context, key string) error {
	if c == nil || c.rdb == nil {
		return errors.New("gateway cache unavailable")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("invalid grok video billed key")
	}
	return c.rdb.Del(ctx, grokVideoBilledPrefix+key).Err()
}

// Compile-time assertion: gatewayCache must implement CyberSessionBlockStore.
var _ service.CyberSessionBlockStore = (*gatewayCache)(nil)
var _ service.LiveCallStore = (*gatewayCache)(nil)
var _ service.OpenAIRuntimeBreakerStore = (*gatewayCache)(nil)
var _ service.OpenAIRuntimeBreakerLeaseStore = (*gatewayCache)(nil)

func openAIRuntimeBreakerScope(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return "account"
	}
	sum := sha256.Sum256([]byte(model))
	return hex.EncodeToString(sum[:16])
}

func openAIRuntimeBreakerBaseKey(accountID int64, model string) string {
	return fmt.Sprintf("%s%d:%s", openAIRuntimeBreakerPrefix, accountID, openAIRuntimeBreakerScope(model))
}

func openAIRuntimeBreakerBlockKey(accountID int64, model string) string {
	return openAIRuntimeBreakerBaseKey(accountID, model) + ":block"
}

func openAIRuntimeBreakerMarkerKey(accountID int64, model string) string {
	return openAIRuntimeBreakerBaseKey(accountID, model) + ":marker"
}

func openAIRuntimeBreakerClaimKey(accountID int64, model string) string {
	return openAIRuntimeBreakerBaseKey(accountID, model) + ":claim"
}

func openAIRuntimeBreakerIndexKey(accountID int64) string {
	return fmt.Sprintf("%s%d:index", openAIRuntimeBreakerPrefix, accountID)
}

var blockOpenAIRuntimeBreakerScript = redis.NewScript(`
	local requested = tonumber(ARGV[2])
	local retention = tonumber(ARGV[3])
	local current = redis.call('PTTL', KEYS[1])
	local effective = requested
	if current < requested then
		redis.call('SET', KEYS[1], ARGV[1], 'PX', requested)
	else
		effective = current
	end
	local marker_ttl = effective + retention
	local marker_current = redis.call('PTTL', KEYS[2])
	if marker_current < marker_ttl then
		redis.call('SET', KEYS[2], ARGV[1], 'PX', marker_ttl)
	end
	redis.call('DEL', KEYS[3])
	redis.call('SADD', KEYS[4], ARGV[4])
	local index_current = redis.call('PTTL', KEYS[4])
	if index_current < marker_ttl then
		redis.call('PEXPIRE', KEYS[4], marker_ttl)
	end
	return effective
`)

func (c *gatewayCache) BlockOpenAIRuntimeBreaker(ctx context.Context, accountID int64, model, reason string, ttl time.Duration) error {
	if accountID <= 0 || ttl <= 0 {
		return nil
	}
	baseKey := openAIRuntimeBreakerBaseKey(accountID, model)
	ttlMillis := ttl.Milliseconds()
	if ttlMillis <= 0 {
		ttlMillis = 1
	}
	return blockOpenAIRuntimeBreakerScript.Run(
		ctx,
		c.rdb,
		[]string{baseKey + ":block", baseKey + ":marker", baseKey + ":claim", openAIRuntimeBreakerIndexKey(accountID)},
		reason,
		ttlMillis,
		openAIRuntimeBreakerHalfOpenRetention.Milliseconds(),
		baseKey,
	).Err()
}

var allowOpenAIRuntimeBreakerProbeScript = redis.NewScript(`
	if redis.call('EXISTS', KEYS[1]) == 1 then
		return 0
	end
	if redis.call('EXISTS', KEYS[2]) == 0 then
		return 1
	end
	local current = redis.call('GET', KEYS[3])
	if current == ARGV[1] then
		return 1
	end
	if current ~= false then
		return 0
	end
	if redis.call('SET', KEYS[3], ARGV[1], 'NX', 'PX', ARGV[2]) then
		return 1
	end
	return 0
`)

func (c *gatewayCache) AllowOpenAIRuntimeBreakerProbe(ctx context.Context, accountID int64, model, owner string, claimTTL time.Duration) (bool, error) {
	if accountID <= 0 {
		return true, nil
	}
	if strings.TrimSpace(owner) == "" {
		return false, fmt.Errorf("openai runtime breaker probe owner is required")
	}
	claimMillis := claimTTL.Milliseconds()
	if claimMillis <= 0 {
		claimMillis = 1
	}
	baseKey := openAIRuntimeBreakerBaseKey(accountID, model)
	allowed, err := allowOpenAIRuntimeBreakerProbeScript.Run(
		ctx,
		c.rdb,
		[]string{baseKey + ":block", baseKey + ":marker", baseKey + ":claim"},
		owner,
		claimMillis,
	).Int()
	return allowed == 1, err
}

var allowOpenAIRuntimeBreakerProbesScript = redis.NewScript(`
	local owner = ARGV[1]
	local claim_ttl = tonumber(ARGV[2])
	local scope_count = tonumber(ARGV[3])
	for index = 1, scope_count do
		local offset = (index - 1) * 3
		if redis.call('EXISTS', KEYS[offset + 1]) == 1 then
			return {0}
		end
		if redis.call('EXISTS', KEYS[offset + 2]) == 1 then
			local current = redis.call('GET', KEYS[offset + 3])
			if current ~= false and current ~= owner then
				return {0}
			end
		end
	end

	local result = {1}
	for index = 1, scope_count do
		local offset = (index - 1) * 3
		if redis.call('EXISTS', KEYS[offset + 2]) == 1 then
			local current = redis.call('GET', KEYS[offset + 3])
			if current == owner then
				local current_ttl = redis.call('PTTL', KEYS[offset + 3])
				if current_ttl < claim_ttl then
					redis.call('PEXPIRE', KEYS[offset + 3], claim_ttl)
				end
			elseif not redis.call('SET', KEYS[offset + 3], owner, 'NX', 'PX', claim_ttl) then
				return {0}
			end
			table.insert(result, index)
		end
	end
	return result
`)

func normalizeOpenAIRuntimeBreakerModels(models []string) []string {
	if len(models) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		model = strings.ToLower(strings.TrimSpace(model))
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		normalized = append(normalized, model)
	}
	return normalized
}

func openAIRuntimeBreakerScopeKeys(accountID int64, models []string) ([]string, []string) {
	models = normalizeOpenAIRuntimeBreakerModels(models)
	keys := make([]string, 0, len(models)*3)
	for _, model := range models {
		baseKey := openAIRuntimeBreakerBaseKey(accountID, model)
		keys = append(keys, baseKey+":block", baseKey+":marker", baseKey+":claim")
	}
	return models, keys
}

func redisScriptInteger(value any) (int64, bool) {
	switch typed := value.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		return parsed, err == nil
	case []byte:
		parsed, err := strconv.ParseInt(string(typed), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func (c *gatewayCache) AllowOpenAIRuntimeBreakerProbes(ctx context.Context, accountID int64, models []string, owner string, claimTTL time.Duration) (bool, []string, error) {
	if accountID <= 0 {
		return true, nil, nil
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return false, nil, fmt.Errorf("openai runtime breaker probe owner is required")
	}
	models, keys := openAIRuntimeBreakerScopeKeys(accountID, models)
	if len(models) == 0 {
		return true, nil, nil
	}
	claimMillis := claimTTL.Milliseconds()
	if claimMillis <= 0 {
		claimMillis = 1
	}
	values, err := allowOpenAIRuntimeBreakerProbesScript.Run(
		ctx,
		c.rdb,
		keys,
		owner,
		claimMillis,
		len(models),
	).Slice()
	if err != nil {
		return false, nil, err
	}
	decision, ok := redisScriptInteger(values[0])
	if !ok || decision != 1 {
		return false, nil, nil
	}
	claimed := make([]string, 0, len(values)-1)
	for _, value := range values[1:] {
		index, valid := redisScriptInteger(value)
		if !valid || index <= 0 || index > int64(len(models)) {
			return false, nil, fmt.Errorf("invalid openai runtime breaker scope index %v", value)
		}
		claimed = append(claimed, models[index-1])
	}
	return true, claimed, nil
}

var renewOpenAIRuntimeBreakerProbesScript = redis.NewScript(`
	local owner = ARGV[1]
	local claim_ttl = tonumber(ARGV[2])
	local marker_ttl = tonumber(ARGV[3])
	local scope_count = tonumber(ARGV[4])
	local index_key = KEYS[scope_count * 3 + 1]
	for index = 1, scope_count do
		local offset = (index - 1) * 3
		local block_exists = redis.call('EXISTS', KEYS[offset + 1]) == 1
		local marker_exists = redis.call('EXISTS', KEYS[offset + 2]) == 1
		local current = redis.call('GET', KEYS[offset + 3])
		if block_exists then
			return 0
		end
		if not marker_exists or current ~= owner then
			return 0
		end
	end

	for index = 1, scope_count do
		local offset = (index - 1) * 3
		if redis.call('EXISTS', KEYS[offset + 2]) == 1 and redis.call('GET', KEYS[offset + 3]) == owner then
			local claim_current = redis.call('PTTL', KEYS[offset + 3])
			if claim_current < claim_ttl then
				redis.call('PEXPIRE', KEYS[offset + 3], claim_ttl)
			end
			local marker_current = redis.call('PTTL', KEYS[offset + 2])
			if marker_current < marker_ttl then
				redis.call('PEXPIRE', KEYS[offset + 2], marker_ttl)
			end
			redis.call('SADD', index_key, ARGV[4 + index])
		end
	end
	local index_current = redis.call('PTTL', index_key)
	if index_current < marker_ttl then
		redis.call('PEXPIRE', index_key, marker_ttl)
	end
	return 1
`)

func (c *gatewayCache) RenewOpenAIRuntimeBreakerProbes(ctx context.Context, accountID int64, models []string, owner string, claimTTL time.Duration) (bool, error) {
	if accountID <= 0 {
		return true, nil
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return false, fmt.Errorf("openai runtime breaker probe owner is required")
	}
	models, keys := openAIRuntimeBreakerScopeKeys(accountID, models)
	if len(models) == 0 {
		return true, nil
	}
	claimMillis := claimTTL.Milliseconds()
	if claimMillis <= 0 {
		claimMillis = 1
	}
	markerMillis := claimMillis + openAIRuntimeBreakerHalfOpenRetention.Milliseconds()
	args := make([]any, 0, 4+len(models))
	args = append(args, owner, claimMillis, markerMillis, len(models))
	for _, model := range models {
		args = append(args, openAIRuntimeBreakerBaseKey(accountID, model))
	}
	renewed, err := renewOpenAIRuntimeBreakerProbesScript.Run(ctx, c.rdb, append(keys, openAIRuntimeBreakerIndexKey(accountID)), args...).Int()
	return renewed == 1, err
}

var releaseOpenAIRuntimeBreakerProbesScript = redis.NewScript(`
	local owner = ARGV[1]
	local scope_count = tonumber(ARGV[2])
	for index = 1, scope_count do
		local current = redis.call('GET', KEYS[(index - 1) * 3 + 3])
		if current ~= false and current ~= owner then
			return 0
		end
	end
	local released = 0
	for index = 1, scope_count do
		local claim_key = KEYS[(index - 1) * 3 + 3]
		if redis.call('GET', claim_key) == owner then
			redis.call('DEL', claim_key)
			released = released + 1
		end
	end
	return released > 0 and 1 or 0
`)

func (c *gatewayCache) ReleaseOpenAIRuntimeBreakerProbes(ctx context.Context, accountID int64, models []string, owner string) (bool, error) {
	if accountID <= 0 {
		return true, nil
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return false, fmt.Errorf("openai runtime breaker probe owner is required")
	}
	models, keys := openAIRuntimeBreakerScopeKeys(accountID, models)
	if len(models) == 0 {
		return true, nil
	}
	released, err := releaseOpenAIRuntimeBreakerProbesScript.Run(ctx, c.rdb, keys, owner, len(models)).Int()
	return released == 1, err
}

var clearOpenAIRuntimeBreakerScript = redis.NewScript(`
	if redis.call('EXISTS', KEYS[1]) == 1 then
		return 0
	end
	if redis.call('EXISTS', KEYS[2]) == 0 or redis.call('GET', KEYS[3]) ~= ARGV[1] then
		return 0
	end
	redis.call('DEL', KEYS[2], KEYS[3])
	redis.call('SREM', KEYS[4], ARGV[2])
	return 1
`)

func (c *gatewayCache) ClearOpenAIRuntimeBreaker(ctx context.Context, accountID int64, model, owner string) error {
	if accountID <= 0 {
		return nil
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return nil
	}
	baseKey := openAIRuntimeBreakerBaseKey(accountID, model)
	return clearOpenAIRuntimeBreakerScript.Run(
		ctx,
		c.rdb,
		[]string{baseKey + ":block", baseKey + ":marker", baseKey + ":claim", openAIRuntimeBreakerIndexKey(accountID)},
		owner,
		baseKey,
	).Err()
}

var clearAllOpenAIRuntimeBreakersScript = redis.NewScript(`
	local members = redis.call('SMEMBERS', KEYS[1])
	for _, base in ipairs(members) do
		redis.call('DEL', base .. ':block', base .. ':marker', base .. ':claim')
	end
	redis.call('DEL', KEYS[1])
	return #members
`)

func (c *gatewayCache) ClearAllOpenAIRuntimeBreakers(ctx context.Context, accountID int64) error {
	if accountID <= 0 {
		return nil
	}
	return clearAllOpenAIRuntimeBreakersScript.Run(ctx, c.rdb, []string{openAIRuntimeBreakerIndexKey(accountID)}).Err()
}

const cyberSessionBlockPrefix = "cyber_session_block:"

// SetCyberSessionBlocked 把被 cyber_policy 命中的会话写入屏蔽表（TTL 自动过期）。
// 存储值 "1" 作为存在标记（IsCyberSessionBlocked 只检查 key 是否存在，不读值）。
func (c *gatewayCache) SetCyberSessionBlocked(ctx context.Context, key string, ttl time.Duration) error {
	return c.rdb.Set(ctx, cyberSessionBlockPrefix+key, "1", ttl).Err()
}

// IsCyberSessionBlocked 查询会话是否在屏蔽表中。
func (c *gatewayCache) IsCyberSessionBlocked(ctx context.Context, key string) (bool, error) {
	n, err := c.rdb.Exists(ctx, cyberSessionBlockPrefix+key).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

var claimLiveControllerScript = redis.NewScript(`
	local key = KEYS[1]
	local target = ARGV[1]
	local owner = ARGV[2]
	local current = redis.call('HGET', key, 'controller')
	if current == false or current == 'closed' then
		return 0
	end
	if target == 'observer' and current ~= 'pending' then
		return 0
	end
	if target == 'proxy' and current ~= 'pending' and current ~= 'observer' and
		(current ~= 'proxy' or redis.call('HGET', key, 'controller_owner') ~= owner) then
		return 0
	end
	redis.call('HSET', key, 'controller', target, 'controller_owner', owner)
	return 1
`)

var markLiveCallClosedScript = redis.NewScript(`
	local key = KEYS[1]
	if redis.call('EXISTS', key) == 0 then
		return 0
	end
	if redis.call('HGET', key, 'controller') == 'closed' then
		return 0
	end
	redis.call('HSET', key, 'controller', 'closed', 'controller_owner', '')
	redis.call('EXPIRE', key, ARGV[1])
	return 1
`)

var releaseLiveControllerScript = redis.NewScript(`
	local key = KEYS[1]
	if redis.call('HGET', key, 'controller') ~= 'proxy' or
		redis.call('HGET', key, 'controller_owner') ~= ARGV[1] then
		return 0
	end
	redis.call('HSET', key, 'controller', 'pending', 'controller_owner', '')
	return 1
`)

func liveCallKey(callHash string) string {
	return liveCallPrefix + callHash
}

func HashLiveCallID(callID string) string {
	sum := sha256.Sum256([]byte(callID))
	return hex.EncodeToString(sum[:])
}

func (c *gatewayCache) SaveLiveCall(ctx context.Context, record *service.LiveCallRecord, ttl time.Duration) error {
	if record == nil || record.CallHash == "" || record.CallID == "" {
		return fmt.Errorf("invalid live call record")
	}
	values := map[string]any{
		"call_id":          record.CallID,
		"account_id":       record.AccountID,
		"api_key_id":       record.APIKeyID,
		"user_id":          record.UserID,
		"group_id":         record.GroupID,
		"subscription_id":  record.SubscriptionID,
		"lease_id":         record.LeaseID,
		"model":            record.Model,
		"created_at":       record.CreatedAt.UnixMilli(),
		"expires_at":       record.ExpiresAt.UnixMilli(),
		"controller":       record.Controller,
		"controller_owner": record.ControllerOwner,
		"user_agent":       record.UserAgent,
		"ip_address":       record.IPAddress,
		"inbound_endpoint": record.InboundEndpoint,
		"attestation":      record.AttestationCiphertext,
	}
	key := liveCallKey(record.CallHash)
	pipe := c.rdb.TxPipeline()
	pipe.HSet(ctx, key, values)
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (c *gatewayCache) GetLiveCall(ctx context.Context, callHash string) (*service.LiveCallRecord, error) {
	values, err := c.rdb.HGetAll(ctx, liveCallKey(callHash)).Result()
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, service.ErrLiveCallNotFound
	}
	parseInt := func(field string) int64 {
		value, _ := strconv.ParseInt(values[field], 10, 64)
		return value
	}
	createdAt := time.UnixMilli(parseInt("created_at"))
	expiresAt := time.UnixMilli(parseInt("expires_at"))
	return &service.LiveCallRecord{
		CallID:                values["call_id"],
		CallHash:              callHash,
		AccountID:             parseInt("account_id"),
		APIKeyID:              parseInt("api_key_id"),
		UserID:                parseInt("user_id"),
		GroupID:               parseInt("group_id"),
		SubscriptionID:        parseInt("subscription_id"),
		LeaseID:               values["lease_id"],
		Model:                 values["model"],
		CreatedAt:             createdAt,
		ExpiresAt:             expiresAt,
		Controller:            values["controller"],
		ControllerOwner:       values["controller_owner"],
		UserAgent:             values["user_agent"],
		IPAddress:             values["ip_address"],
		InboundEndpoint:       values["inbound_endpoint"],
		AttestationCiphertext: values["attestation"],
	}, nil
}

func (c *gatewayCache) ClaimLiveController(ctx context.Context, callHash, controller, owner string) (bool, error) {
	result, err := claimLiveControllerScript.Run(ctx, c.rdb, []string{liveCallKey(callHash)}, controller, owner).Int()
	return result == 1, err
}

func (c *gatewayCache) GetLiveController(ctx context.Context, callHash string) (string, error) {
	value, err := c.rdb.HGet(ctx, liveCallKey(callHash), "controller").Result()
	if err == redis.Nil {
		return "", service.ErrLiveCallNotFound
	}
	return value, err
}

func (c *gatewayCache) ReleaseLiveController(ctx context.Context, callHash, owner string) (bool, error) {
	result, err := releaseLiveControllerScript.Run(ctx, c.rdb, []string{liveCallKey(callHash)}, owner).Int()
	return result == 1, err
}

func (c *gatewayCache) MarkLiveCallClosed(ctx context.Context, callHash string, ttl time.Duration) (bool, error) {
	result, err := markLiveCallClosedScript.Run(ctx, c.rdb, []string{liveCallKey(callHash)}, int64(ttl.Seconds())).Int()
	return result == 1, err
}
