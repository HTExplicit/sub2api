package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
const cindyBalancePendingPrefix = "cindy_balance_pending:v2:"
const cindyHealthEpisodePrefix = "cindy_health_episode:v1:"
const cindyHealthPendingPrefixV3 = "cindy_health_pending:v3:"
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
var _ service.CindyBalancePendingStore = (*gatewayCache)(nil)
var _ service.CindyHealthEpisodeStore = (*gatewayCache)(nil)
var _ service.CindyHealthStateCleaner = (*gatewayCache)(nil)
var _ service.CindyHealthTerminalPendingManager = (*gatewayCache)(nil)

func cindyBalancePendingKey(accountID int64) string {
	return cindyBalancePendingPrefix + strconv.FormatInt(accountID, 10)
}

func cindyHealthEpisodeKey(accountID int64) string {
	return cindyHealthEpisodePrefix + strconv.FormatInt(accountID, 10)
}

func cindyHealthPendingKeyV3(accountID int64, status ...string) string {
	key := cindyHealthPendingPrefixV3 + strconv.FormatInt(accountID, 10)
	if len(status) > 0 && strings.TrimSpace(status[0]) != "" {
		key += ":" + strings.TrimSpace(status[0])
	}
	return key
}

func cindyHealthStoreKey(episode service.CindyHealthEpisode) string {
	if episode.Status != "" {
		return cindyHealthPendingKeyV3(episode.AccountID, episode.Status)
	}
	return cindyHealthEpisodeKey(episode.AccountID)
}

type cindyHealthStoredEpisode struct {
	AccountID   int64     `json:"account_id"`
	Generation  string    `json:"generation"`
	EpisodeID   string    `json:"episode_id"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	Status      string    `json:"status,omitempty"`
	Evidence    string    `json:"evidence,omitempty"`
	ObservedAt  time.Time `json:"observed_at,omitempty"`
}

func cindyHealthEpisodeValue(episode service.CindyHealthEpisode) (string, error) {
	raw, err := json.Marshal(cindyHealthStoredEpisode{
		AccountID: episode.AccountID, Generation: strconv.FormatInt(episode.Generation, 10),
		EpisodeID: episode.EpisodeID, Fingerprint: episode.Fingerprint, Status: episode.Status,
		Evidence: episode.Evidence, ObservedAt: episode.ObservedAt,
	})
	return string(raw), err
}

func parseCindyHealthEpisodeValue(value string) (service.CindyHealthEpisode, error) {
	var stored cindyHealthStoredEpisode
	if err := json.Unmarshal([]byte(value), &stored); err != nil {
		return service.CindyHealthEpisode{}, err
	}
	generation, err := strconv.ParseInt(stored.Generation, 10, 64)
	if err != nil {
		return service.CindyHealthEpisode{}, err
	}
	episode := service.CindyHealthEpisode{
		AccountID: stored.AccountID, Generation: generation, EpisodeID: stored.EpisodeID,
		Fingerprint: stored.Fingerprint, Status: stored.Status, Evidence: stored.Evidence,
		ObservedAt: stored.ObservedAt,
	}
	return episode, nil
}

var claimCindyHealthEpisodeScript = redis.NewScript(`
	local function valid_generation(value)
		return string.match(value, '^[1-9][0-9]*$') ~= nil
	end
	local function generation_greater(left, right)
		if string.len(left) ~= string.len(right) then
			return string.len(left) > string.len(right)
		end
		return left > right
	end
	if not valid_generation(ARGV[1]) then
		return redis.error_reply('invalid Cindy health generation')
	end
	local current = redis.call('GET', KEYS[1])
	if not current then
		if ARGV[3] == '0' then
			redis.call('SET', KEYS[1], ARGV[2])
		else
			redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
		end
		return 1
	end
	local ok, decoded = pcall(cjson.decode, current)
	if not ok or type(decoded) ~= 'table' then
		return redis.error_reply('invalid Cindy health episode')
	end
	local current_generation = decoded['generation']
	if not valid_generation(current_generation) then
		return redis.error_reply('invalid Cindy health generation')
	end
	if generation_greater(ARGV[1], current_generation) then
		if ARGV[3] == '0' then
			redis.call('SET', KEYS[1], ARGV[2])
		else
			redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
		end
		return 1
	end
	return 0
`)

func (c *gatewayCache) ClaimCindyHealthEpisode(ctx context.Context, episode service.CindyHealthEpisode, ttl time.Duration) (bool, error) {
	if c == nil || c.rdb == nil || episode.AccountID <= 0 || episode.Generation <= 0 ||
		strings.TrimSpace(episode.EpisodeID) == "" || ttl < 0 || (episode.Status == "" && ttl == 0) {
		return false, errors.New("invalid Cindy health episode")
	}
	value, err := cindyHealthEpisodeValue(episode)
	if err != nil {
		return false, err
	}
	result, err := claimCindyHealthEpisodeScript.Run(
		ctx, c.rdb, []string{cindyHealthStoreKey(episode)},
		strconv.FormatInt(episode.Generation, 10), value, ttl.Milliseconds(),
	).Int()
	return result == 1, err
}

var clearCindyHealthEpisodeIfMatchScript = redis.NewScript(`
	if redis.call('GET', KEYS[1]) ~= ARGV[1] then
		return 0
	end
	return redis.call('DEL', KEYS[1])
`)

func (c *gatewayCache) ClearCindyHealthEpisodeIfMatch(ctx context.Context, episode service.CindyHealthEpisode) error {
	if c == nil || c.rdb == nil || episode.AccountID <= 0 || episode.Generation <= 0 || strings.TrimSpace(episode.EpisodeID) == "" {
		return errors.New("invalid Cindy health episode")
	}
	value, err := cindyHealthEpisodeValue(episode)
	if err != nil {
		return err
	}
	return clearCindyHealthEpisodeIfMatchScript.Run(
		ctx, c.rdb, []string{cindyHealthStoreKey(episode)}, value,
	).Err()
}

func (c *gatewayCache) ListCindyHealthEpisodes(ctx context.Context, limit int) ([]service.CindyHealthEpisode, error) {
	if c == nil || c.rdb == nil {
		return nil, errors.New("gateway cache unavailable")
	}
	if limit <= 0 {
		return []service.CindyHealthEpisode{}, nil
	}
	keys := make([]string, 0, limit)
	var cursor uint64
	for {
		batch, next, err := c.rdb.Scan(ctx, cursor, cindyHealthPendingPrefixV3+"*", int64(limit-len(keys))).Result()
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 || len(keys) >= limit {
			break
		}
	}
	if len(keys) > limit {
		keys = keys[:limit]
	}
	out := make([]service.CindyHealthEpisode, 0, len(keys))
	for _, key := range keys {
		value, getErr := c.rdb.Get(ctx, key).Result()
		if errors.Is(getErr, redis.Nil) {
			continue
		}
		if getErr != nil {
			return nil, getErr
		}
		episode, parseErr := parseCindyHealthEpisodeValue(value)
		if parseErr != nil {
			return nil, parseErr
		}
		out = append(out, episode)
	}
	return out, nil
}

func (c *gatewayCache) GetCindyHealthEpisodes(ctx context.Context, accountID int64) ([]service.CindyHealthEpisode, error) {
	if c == nil || c.rdb == nil || accountID <= 0 {
		return nil, errors.New("invalid Cindy health account")
	}
	keys := []string{
		cindyHealthPendingKeyV3(accountID, service.CindyHealthStatusBanned),
		cindyHealthPendingKeyV3(accountID, service.CindyHealthStatusBalanceInsufficient),
	}
	values, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	out := make([]service.CindyHealthEpisode, 0, len(values))
	for _, raw := range values {
		value, ok := raw.(string)
		if !ok || value == "" {
			continue
		}
		episode, parseErr := parseCindyHealthEpisodeValue(value)
		if parseErr != nil {
			return nil, parseErr
		}
		out = append(out, episode)
	}
	return out, nil
}

func (c *gatewayCache) ClearAllCindyHealthState(ctx context.Context, accountID int64) error {
	if c == nil || c.rdb == nil || accountID <= 0 {
		return errors.New("invalid Cindy health cleanup")
	}
	id := strconv.FormatInt(accountID, 10)
	keys := []string{
		"cindy_balance_pending:" + id,
		cindyBalancePendingKey(accountID),
		cindyHealthEpisodeKey(accountID),
		"cindy_health_episode:v2:" + id,
		cindyHealthPendingKeyV3(accountID),
	}
	var cursor uint64
	for {
		matched, next, err := c.rdb.Scan(ctx, cursor, cindyHealthPendingKeyV3(accountID)+":*", 16).Result()
		if err != nil {
			return err
		}
		keys = append(keys, matched...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return c.rdb.Del(ctx, keys...).Err()
}

func (c *gatewayCache) GetCindyHealthTerminalPending(ctx context.Context, accountID int64, status string) (*service.CindyHealthEpisode, error) {
	if c == nil || c.rdb == nil || accountID <= 0 ||
		(status != service.CindyHealthStatusBanned && status != service.CindyHealthStatusBalanceInsufficient) {
		return nil, errors.New("invalid Cindy terminal pending lookup")
	}
	value, err := c.rdb.Get(ctx, cindyHealthPendingKeyV3(accountID, status)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	episode, err := parseCindyHealthEpisodeValue(value)
	if err != nil {
		return nil, err
	}
	return &episode, nil
}

func (c *gatewayCache) ClearCindyHealthTerminalPendingIfMatch(ctx context.Context, episode service.CindyHealthEpisode) (bool, error) {
	if c == nil || c.rdb == nil || !episodeTerminalPendingValid(episode) {
		return false, errors.New("invalid Cindy terminal pending cleanup")
	}
	value, err := cindyHealthEpisodeValue(episode)
	if err != nil {
		return false, err
	}
	deleted, err := clearCindyHealthEpisodeIfMatchScript.Run(
		ctx, c.rdb, []string{cindyHealthStoreKey(episode)}, value,
	).Int()
	return deleted == 1, err
}

func episodeTerminalPendingValid(episode service.CindyHealthEpisode) bool {
	return episode.AccountID > 0 && episode.Generation > 0 && strings.TrimSpace(episode.EpisodeID) != "" &&
		(episode.Status == service.CindyHealthStatusBanned || episode.Status == service.CindyHealthStatusBalanceInsufficient)
}

func (c *gatewayCache) MarkCindyBalancePending(ctx context.Context, accountID int64, credentialsFingerprint string) error {
	if c == nil || c.rdb == nil {
		return errors.New("gateway cache unavailable")
	}
	if accountID <= 0 {
		return errors.New("invalid Cindy balance pending account")
	}
	credentialsFingerprint = service.NormalizeCindyCredentialsFingerprint(credentialsFingerprint)
	if credentialsFingerprint == "" {
		return errors.New("invalid Cindy balance credential fingerprint")
	}
	// A pending exact budget signal must survive process restarts and must not
	// expire before the corresponding database marker is committed.
	return c.rdb.Set(ctx, cindyBalancePendingKey(accountID), credentialsFingerprint, 0).Err()
}

func (c *gatewayCache) GetCindyBalancePendingFingerprint(ctx context.Context, accountID int64) (string, error) {
	if c == nil || c.rdb == nil {
		return "", errors.New("gateway cache unavailable")
	}
	if accountID <= 0 {
		return "", errors.New("invalid Cindy balance pending account")
	}
	value, err := c.rdb.Get(ctx, cindyBalancePendingKey(accountID)).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	value = service.NormalizeCindyCredentialsFingerprint(value)
	if value == "" {
		return "", errors.New("invalid Cindy balance pending fingerprint")
	}
	return value, nil
}

func (c *gatewayCache) HasCindyBalancePendingBatch(ctx context.Context, accountIDs []int64) (map[int64]bool, error) {
	if c == nil || c.rdb == nil {
		return nil, errors.New("gateway cache unavailable")
	}
	if len(accountIDs) == 0 {
		return map[int64]bool{}, nil
	}
	keys := make([]string, len(accountIDs))
	for i, accountID := range accountIDs {
		if accountID <= 0 {
			return nil, errors.New("invalid Cindy balance pending account")
		}
		keys[i] = cindyBalancePendingKey(accountID)
	}
	values, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	pending := make(map[int64]bool, len(accountIDs))
	for i, value := range values {
		if value != nil {
			pending[accountIDs[i]] = true
		}
	}
	return pending, nil
}

func (c *gatewayCache) ClearCindyBalancePending(ctx context.Context, accountID int64) error {
	if c == nil || c.rdb == nil {
		return errors.New("gateway cache unavailable")
	}
	if accountID <= 0 {
		return errors.New("invalid Cindy balance pending account")
	}
	return c.rdb.Del(ctx, cindyBalancePendingKey(accountID)).Err()
}

var clearCindyBalancePendingIfFingerprintMatchesScript = redis.NewScript(`
	if redis.call('GET', KEYS[1]) ~= ARGV[1] then
		return 0
	end
	return redis.call('DEL', KEYS[1])
`)

func (c *gatewayCache) ClearCindyBalancePendingIfFingerprintMatches(
	ctx context.Context,
	accountID int64,
	credentialsFingerprint string,
) error {
	if c == nil || c.rdb == nil {
		return errors.New("gateway cache unavailable")
	}
	if accountID <= 0 {
		return errors.New("invalid Cindy balance pending account")
	}
	credentialsFingerprint = service.NormalizeCindyCredentialsFingerprint(credentialsFingerprint)
	if credentialsFingerprint == "" {
		return errors.New("invalid Cindy balance credential fingerprint")
	}
	return clearCindyBalancePendingIfFingerprintMatchesScript.Run(
		ctx,
		c.rdb,
		[]string{cindyBalancePendingKey(accountID)},
		credentialsFingerprint,
	).Err()
}

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

const reasoningContentPrefix = "reasoning_content:"

// reasoningContentDefaultTTL 是 reasoning 缓存的默认过期时间。Codex 会话可能
// 跨多天恢复，取 7 天；调用方传入非正 TTL 时兜底。
const reasoningContentDefaultTTL = 7 * 24 * time.Hour

// SetReasoningContent 按 reasoning item id 缓存 reasoning 全文。
// itemID 或 content 为空时直接返回 nil（无可缓存内容，属正常情况而非错误）。
func (c *gatewayCache) SetReasoningContent(ctx context.Context, itemID string, content string, ttl time.Duration) error {
	if c == nil || c.rdb == nil {
		return errors.New("gateway cache unavailable")
	}
	itemID = strings.TrimSpace(itemID)
	if itemID == "" || content == "" {
		return nil
	}
	if ttl <= 0 {
		ttl = reasoningContentDefaultTTL
	}
	return c.rdb.Set(ctx, reasoningContentPrefix+itemID, content, ttl).Err()
}

// GetReasoningContent 返回缓存的 reasoning 全文；未命中返回
// service.ErrReasoningContentNotFound。
func (c *gatewayCache) GetReasoningContent(ctx context.Context, itemID string) (string, error) {
	if c == nil || c.rdb == nil {
		return "", errors.New("gateway cache unavailable")
	}
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return "", service.ErrReasoningContentNotFound
	}
	val, err := c.rdb.Get(ctx, reasoningContentPrefix+itemID).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", service.ErrReasoningContentNotFound
		}
		return "", err
	}
	return val, nil
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
