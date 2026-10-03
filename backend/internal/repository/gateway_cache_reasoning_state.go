package repository

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const rejectedReasoningPrefix = "openai_reasoning_state:v1:{openai_rejected_reasoning}:"

var _ service.OpenAIReasoningStateStore = (*gatewayCache)(nil)

// Every rejection has its own hard TTL. Index TTLs and lazy expiry cleanup are
// bookkeeping only: touching LRU never extends a rejection's lifetime. The size,
// owner, expiry, and LRU indexes are updated atomically with every replacement
// and eviction, including records whose Redis entry TTL already elapsed.
var rejectedReasoningScript = redis.NewScript(`
local prefix, op, tenant = ARGV[1], ARGV[2], ARGV[3]
local ttl, maxBytes, maxEntries = tonumber(ARGV[4]), tonumber(ARGV[5]), tonumber(ARGV[6])
local maxTenantBytes, maxEntryBytes = tonumber(ARGV[7]), tonumber(ARGV[8])
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
  for index = 9, #ARGV do
    local id = ARGV[index]
    local payload = redis.call('HGET', prefix .. 'entry:' .. id, 'payload')
    local owner = redis.call('HGET', KEYS[4], id)
    if payload and owner == tenant then
      redis.call('ZADD', KEYS[1], tick, id)
      redis.call('ZADD', prefix .. 'tenant:' .. tenant, tick, id)
      table.insert(result, id)
      table.insert(result, payload)
    elseif not payload then
      remove(id)
    end
  end
  return result
end

if op ~= 'put' then return redis.error_reply('invalid reasoning state operation') end

local payload = string.format('%.0f:%.0f', now, now + ttl)
local records = {}
for index = 9, #ARGV do
  local id = ARGV[index]
  local size = string.len(payload) + string.len(id)
  if size > maxEntryBytes or size > maxBytes or size > maxTenantBytes then return 0 end
  table.insert(records, {id, size})
end

for _, record in ipairs(records) do
  local id, size = record[1], record[2]
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
  redis.call('HSET', prefix .. 'entry:' .. id, 'payload', payload)
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

var rejectedReasoningLimits = reasoningStateLimits{
	bytes:       service.OpenAIRejectedReasoningMaxBytes,
	entries:     service.OpenAIRejectedReasoningMaxEntries,
	tenantBytes: service.OpenAIRejectedReasoningMaxBytes,
	entryBytes:  256,
}

func rejectedReasoningKeys() []string {
	return []string{
		rejectedReasoningPrefix + "lru", rejectedReasoningPrefix + "expiry", rejectedReasoningPrefix + "sizes",
		rejectedReasoningPrefix + "owners", rejectedReasoningPrefix + "totals",
	}
}

func rejectedReasoningArguments(operation string, scope service.OpenAIReasoningCacheScope, limits reasoningStateLimits) []any {
	return []any{rejectedReasoningPrefix, operation, scope.TenantHash, service.OpenAIReasoningStateTTL.Milliseconds(),
		limits.bytes, limits.entries, limits.tenantBytes, limits.entryBytes}
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

func (c *gatewayCache) GetOpenAIRejectedReasoning(ctx context.Context, scope service.OpenAIReasoningCacheScope, cipherHashes []string) (map[string]service.OpenAIRejectedReasoning, error) {
	if err := validateReasoningStateKeys(scope, cipherHashes); err != nil {
		return nil, err
	}
	if len(cipherHashes) == 0 {
		return nil, nil
	}
	args := rejectedReasoningArguments("get", scope, rejectedReasoningLimits)
	for _, hash := range cipherHashes {
		args = append(args, scope.ScopeHash+":"+hash)
	}
	reply, err := c.runRejectedReasoning(ctx, args)
	if err != nil {
		return nil, err
	}
	values, ok := reply.([]any)
	if !ok || len(values)%2 != 0 {
		return nil, errors.New("invalid rejected reasoning cache result")
	}
	result := make(map[string]service.OpenAIRejectedReasoning, len(values)/2)
	for index := 0; index < len(values); index += 2 {
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
	args := rejectedReasoningArguments("put", scope, rejectedReasoningLimits)
	seen := make(map[string]bool, len(cipherHashes))
	for _, hash := range cipherHashes {
		if !seen[hash] {
			args = append(args, scope.ScopeHash+":"+hash)
			seen[hash] = true
		}
	}
	_, err := c.runRejectedReasoning(ctx, args)
	return err
}

func (c *gatewayCache) runRejectedReasoning(ctx context.Context, args []any) (any, error) {
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
	return rejectedReasoningScript.Run(ioCtx, client, rejectedReasoningKeys(), args...).Result()
}
