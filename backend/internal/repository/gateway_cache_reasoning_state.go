package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	reasoningBatchPrefix    = "openai_reasoning_state:v1:{openai_reasoning_batches}:"
	rejectedReasoningPrefix = "openai_reasoning_state:v1:{openai_rejected_reasoning}:"
)

var _ service.OpenAIReasoningStateStore = (*gatewayCache)(nil)

// All payloads have their own hard TTL. Index TTLs and lazy expiry cleanup are
// bookkeeping only: touching LRU never extends a payload's lifetime. The size,
// owner, expiry, and LRU indexes are updated atomically with every replacement
// and eviction, including records whose Redis payload TTL already elapsed.
var reasoningStateScript = redis.NewScript(`
local prefix, op, tenant = ARGV[1], ARGV[2], ARGV[3]
local ttl, maxBytes, maxEntries = tonumber(ARGV[4]), tonumber(ARGV[5]), tonumber(ARGV[6])
local maxTenantBytes, maxEntryBytes, kind = tonumber(ARGV[7]), tonumber(ARGV[8]), ARGV[9]
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local tick = tonumber(clock[1]) * 1000000 + tonumber(clock[2])

local function remove(id)
  local size = tonumber(redis.call('HGET', KEYS[3], id)) or 0
  local owner = redis.call('HGET', KEYS[4], id)
  redis.call('DEL', prefix .. 'entry:' .. id)
  redis.call('ZREM', KEYS[1], id)
  redis.call('ZREM', KEYS[2], id)
  redis.call('HDEL', KEYS[3], id)
  redis.call('HDEL', KEYS[4], id)
  if size > 0 then
    if redis.call('HINCRBY', KEYS[5], '_global', -size) <= 0 then
      redis.call('HDEL', KEYS[5], '_global')
    end
  end
  if owner then
    redis.call('ZREM', prefix .. 'tenant:' .. owner, id)
    if size > 0 and redis.call('HINCRBY', KEYS[5], owner, -size) <= 0 then
      redis.call('HDEL', KEYS[5], owner)
    end
  end
end

-- Cap opportunistic GC work; stale entries remain bounded by maxEntries and
-- are also removed on direct lookup or while selecting eviction victims.
for _, id in ipairs(redis.call('ZRANGEBYSCORE', KEYS[2], '-inf', now, 'LIMIT', 0, 128)) do
  remove(id)
end

if op == 'get' then
  local result = {}
  for index = 10, #ARGV do
    local id = ARGV[index]
    local value = redis.call('HMGET', prefix .. 'entry:' .. id, 'payload', 'version')
    local owner = redis.call('HGET', KEYS[4], id)
    if value[1] and value[2] and owner == tenant then
      redis.call('ZADD', KEYS[1], tick, id)
      redis.call('ZADD', prefix .. 'tenant:' .. tenant, tick, id)
      table.insert(result, id)
      table.insert(result, value[1])
      table.insert(result, value[2])
    elseif not value[1] then
      remove(id)
    end
  end
  return result
end

if op == 'delete' then
  local id, expected = ARGV[10], ARGV[11]
  local actual = redis.call('HGET', prefix .. 'entry:' .. id, 'version')
  if not actual then remove(id); return 0 end
  if actual ~= expected or redis.call('HGET', KEYS[4], id) ~= tenant then return 0 end
  remove(id)
  return 1
end

if op ~= 'put' then return redis.error_reply('invalid reasoning state operation') end

local records = {}
for index = 10, #ARGV, 3 do
  local id, payload, version = ARGV[index], ARGV[index + 1], ARGV[index + 2]
  if kind == 'negative' then
    payload = string.format('%.0f:%.0f', now, now + ttl)
  end
  local size = string.len(payload)
  if kind == 'negative' then size = size + string.len(id) end
  if size > maxEntryBytes or size > maxBytes or size > maxTenantBytes then return 0 end
  table.insert(records, {id, payload, version, size})
end

for _, record in ipairs(records) do
  local id, payload, version, size = record[1], record[2], record[3], record[4]
  remove(id)
  while (tonumber(redis.call('HGET', KEYS[5], tenant)) or 0) + size > maxTenantBytes do
    local victim = redis.call('ZRANGE', prefix .. 'tenant:' .. tenant, 0, 0)[1]
    if not victim then return redis.error_reply('reasoning tenant index inconsistent') end
    remove(victim)
  end
  while (tonumber(redis.call('HGET', KEYS[5], '_global')) or 0) + size > maxBytes or
        redis.call('ZCARD', KEYS[1]) >= maxEntries do
    local victim = redis.call('ZRANGE', KEYS[1], 0, 0)[1]
    if not victim then return redis.error_reply('reasoning global index inconsistent') end
    remove(victim)
  end
  redis.call('HSET', prefix .. 'entry:' .. id, 'payload', payload, 'version', version)
  redis.call('PEXPIRE', prefix .. 'entry:' .. id, ttl)
  redis.call('HSET', KEYS[3], id, size)
  redis.call('HSET', KEYS[4], id, tenant)
  redis.call('HINCRBY', KEYS[5], '_global', size)
  redis.call('HINCRBY', KEYS[5], tenant, size)
  redis.call('ZADD', KEYS[1], tick, id)
  redis.call('ZADD', KEYS[2], now + ttl, id)
  redis.call('ZADD', prefix .. 'tenant:' .. tenant, tick, id)
  redis.call('PEXPIRE', prefix .. 'tenant:' .. tenant, ttl)
end
for _, key in ipairs(KEYS) do redis.call('PEXPIRE', key, ttl) end
return #records
`)

type reasoningStateLimits struct {
	bytes, entries, tenantBytes, entryBytes int
}

var reasoningBatchLimits = reasoningStateLimits{
	bytes:       service.OpenAIReasoningBatchTotalMaxBytes,
	entries:     service.OpenAIReasoningBatchTotalMaxEntries,
	tenantBytes: service.OpenAIReasoningBatchTenantMaxBytes,
	entryBytes:  service.OpenAIReasoningBatchMaxBytes,
}

var rejectedReasoningLimits = reasoningStateLimits{
	bytes:       service.OpenAIRejectedReasoningMaxBytes,
	entries:     service.OpenAIRejectedReasoningMaxEntries,
	tenantBytes: service.OpenAIRejectedReasoningMaxBytes,
	entryBytes:  256,
}

func reasoningStateKeys(prefix string) []string {
	return []string{prefix + "lru", prefix + "expiry", prefix + "sizes", prefix + "owners", prefix + "totals"}
}

func reasoningStateArguments(prefix, operation string, scope service.OpenAIReasoningCacheScope, limits reasoningStateLimits) []any {
	kind := "positive"
	if prefix == rejectedReasoningPrefix {
		kind = "negative"
	}
	return []any{prefix, operation, scope.TenantHash, service.OpenAIReasoningStateTTL.Milliseconds(),
		limits.bytes, limits.entries, limits.tenantBytes, limits.entryBytes, kind}
}

func validateReasoningStateKeys(scope service.OpenAIReasoningCacheScope, keys []string) error {
	if !service.IsOpenAIReasoningCacheDigest(scope.ScopeHash) || !service.IsOpenAIReasoningCacheDigest(scope.TenantHash) ||
		len(keys) > service.OpenAIReasoningStateMaxLookupEntries {
		return service.ErrOpenAIReasoningCacheInput
	}
	for _, key := range keys {
		if !service.IsOpenAIReasoningCacheDigest(key) {
			return service.ErrOpenAIReasoningCacheInput
		}
	}
	return nil
}

func reasoningPayloadDigest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func encodeReasoningBatch(batch service.OpenAIReasoningBatch) ([]byte, error) {
	if len(batch.Output) == 0 || len(batch.Output) > service.OpenAIReasoningBatchMaxItems ||
		!service.IsOpenAIReasoningCacheDigest(batch.InputPrefixHash) ||
		len(batch.Projection) == 0 || !json.Valid(batch.Projection) {
		return nil, service.ErrOpenAIReasoningCacheInput
	}
	calls := 0
	for _, raw := range batch.Output {
		var item struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		}
		if json.Unmarshal(raw, &item) != nil || (item.Status != "" && item.Status != "completed") {
			return nil, service.ErrOpenAIReasoningCacheInput
		}
		switch item.Type {
		case "reasoning", "message":
		case "function_call":
			calls++
		default:
			// First version does not admit partial unknown/custom-tool batches.
			return nil, service.ErrOpenAIReasoningCacheInput
		}
	}
	if calls == 0 || calls > service.OpenAIReasoningBatchMaxCalls {
		return nil, service.ErrOpenAIReasoningCacheInput
	}
	payload, err := json.Marshal(batch)
	if err != nil || len(payload) > service.OpenAIReasoningBatchMaxBytes {
		return nil, service.ErrOpenAIReasoningCacheInput
	}
	return payload, nil
}

func (c *gatewayCache) GetOpenAIReasoningBatches(ctx context.Context, scope service.OpenAIReasoningCacheScope, keys []string) (map[string]service.OpenAIReasoningBatch, error) {
	values, err := c.getReasoningState(ctx, reasoningBatchPrefix, scope, keys, reasoningBatchLimits)
	if err != nil {
		return nil, err
	}
	result := make(map[string]service.OpenAIReasoningBatch, len(values)/3)
	for index := 0; index < len(values); index += 3 {
		key, keyOK := values[index].(string)
		payload, payloadOK := values[index+1].(string)
		version, versionOK := values[index+2].(string)
		if !keyOK || !payloadOK || !versionOK || reasoningPayloadDigest([]byte(payload)) != version {
			return nil, errors.New("invalid reasoning state cache record")
		}
		var batch service.OpenAIReasoningBatch
		if json.Unmarshal([]byte(payload), &batch) != nil {
			return nil, errors.New("invalid reasoning state cache record")
		}
		if _, err := encodeReasoningBatch(batch); err != nil {
			return nil, errors.New("invalid reasoning state cache record")
		}
		batch.PayloadHash = version
		result[strings.TrimPrefix(key, scope.ScopeHash+":")] = batch
	}
	return result, nil
}

func (c *gatewayCache) PutOpenAIReasoningBatch(ctx context.Context, scope service.OpenAIReasoningCacheScope, key string, batch service.OpenAIReasoningBatch) (bool, error) {
	if err := validateReasoningStateKeys(scope, []string{key}); err != nil {
		return false, err
	}
	payload, err := encodeReasoningBatch(batch)
	if err != nil {
		return false, err
	}
	args := reasoningStateArguments(reasoningBatchPrefix, "put", scope, reasoningBatchLimits)
	args = append(args, scope.ScopeHash+":"+key, payload, reasoningPayloadDigest(payload))
	result, err := c.runReasoningState(ctx, reasoningBatchPrefix, args)
	return result == int64(1), err
}

func (c *gatewayCache) DeleteOpenAIReasoningBatchIfMatch(ctx context.Context, scope service.OpenAIReasoningCacheScope, key, payloadHash string) (bool, error) {
	if err := validateReasoningStateKeys(scope, []string{key, payloadHash}); err != nil {
		return false, err
	}
	args := reasoningStateArguments(reasoningBatchPrefix, "delete", scope, reasoningBatchLimits)
	args = append(args, scope.ScopeHash+":"+key, payloadHash)
	result, err := c.runReasoningState(ctx, reasoningBatchPrefix, args)
	return result == int64(1), err
}

func (c *gatewayCache) GetOpenAIRejectedReasoning(ctx context.Context, scope service.OpenAIReasoningCacheScope, cipherHashes []string) (map[string]service.OpenAIRejectedReasoning, error) {
	values, err := c.getReasoningState(ctx, rejectedReasoningPrefix, scope, cipherHashes, rejectedReasoningLimits)
	if err != nil {
		return nil, err
	}
	result := make(map[string]service.OpenAIRejectedReasoning, len(values)/3)
	for index := 0; index < len(values); index += 3 {
		key, keyOK := values[index].(string)
		payload, payloadOK := values[index+1].(string)
		if !keyOK || !payloadOK {
			return nil, errors.New("invalid rejected reasoning cache record")
		}
		rejected, expires, found := strings.Cut(payload, ":")
		rejectedMS, rejectedErr := strconv.ParseInt(rejected, 10, 64)
		expiresMS, expiresErr := strconv.ParseInt(expires, 10, 64)
		if !found || rejectedErr != nil || expiresErr != nil || expiresMS-rejectedMS != service.OpenAIReasoningStateTTL.Milliseconds() {
			return nil, errors.New("invalid rejected reasoning cache record")
		}
		result[strings.TrimPrefix(key, scope.ScopeHash+":")] = service.OpenAIRejectedReasoning{
			RejectedAt: time.UnixMilli(rejectedMS), ExpiresAt: time.UnixMilli(expiresMS),
		}
	}
	return result, nil
}

func (c *gatewayCache) PutOpenAIRejectedReasoning(ctx context.Context, scope service.OpenAIReasoningCacheScope, cipherHashes []string) error {
	if err := validateReasoningStateKeys(scope, cipherHashes); err != nil {
		return err
	}
	if len(cipherHashes) == 0 {
		return nil
	}
	args := reasoningStateArguments(rejectedReasoningPrefix, "put", scope, rejectedReasoningLimits)
	seen := make(map[string]bool, len(cipherHashes))
	for _, hash := range cipherHashes {
		if !seen[hash] {
			args = append(args, scope.ScopeHash+":"+hash, "", hash)
			seen[hash] = true
		}
	}
	_, err := c.runReasoningState(ctx, rejectedReasoningPrefix, args)
	return err
}

func (c *gatewayCache) getReasoningState(ctx context.Context, prefix string, scope service.OpenAIReasoningCacheScope, keys []string, limits reasoningStateLimits) ([]any, error) {
	if err := validateReasoningStateKeys(scope, keys); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, nil
	}
	args := reasoningStateArguments(prefix, "get", scope, limits)
	for _, key := range keys {
		args = append(args, scope.ScopeHash+":"+key)
	}
	result, err := c.runReasoningState(ctx, prefix, args)
	if err != nil {
		return nil, err
	}
	values, ok := result.([]any)
	if !ok || len(values)%3 != 0 {
		return nil, errors.New("invalid reasoning state cache result")
	}
	return values, nil
}

func (c *gatewayCache) runReasoningState(ctx context.Context, prefix string, args []any) (any, error) {
	if c == nil || c.rdb == nil {
		return nil, errors.New("reasoning state cache unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Callers supply the request-wide remaining I/O budget. Bound direct users
	// too; never inherit the ordinary multi-second Redis defaults for this cache.
	ioCtx, cancel := context.WithTimeout(ctx, service.OpenAIReasoningStateIOBudget)
	defer cancel()
	deadline, _ := ioCtx.Deadline()
	timeout := time.Until(deadline)
	// WithTimeout alone inherits ContextTimeoutEnabled=false from the shared
	// production client. Use one bounded, promptly closed connection instead of
	// changing global Redis options or starting detached timeout goroutines.
	opts := *c.rdb.Options()
	opts.ContextTimeoutEnabled = true
	opts.MaxRetries = -1
	opts.DialerRetries = 1
	opts.DialerRetryTimeout = time.Nanosecond
	// MaxActiveConns is the actual connection bound. The logical size of two
	// avoids go-redis launching its background redial loop after the first dial
	// failure; this one-operation pool is closed immediately instead.
	opts.PoolSize, opts.MaxActiveConns, opts.MaxIdleConns = 2, 1, 1
	opts.MinIdleConns, opts.MaxConcurrentDials = 0, 1
	opts.DialTimeout, opts.ReadTimeout, opts.WriteTimeout, opts.PoolTimeout = timeout, timeout, timeout, timeout
	dial := opts.Dialer
	opts.Dialer = func(_ context.Context, network, addr string) (net.Conn, error) {
		return dial(ioCtx, network, addr)
	}
	client := redis.NewClient(&opts)
	defer func() { _ = client.Close() }()
	return reasoningStateScript.Run(ioCtx, client, reasoningStateKeys(prefix), args...).Result()
}
