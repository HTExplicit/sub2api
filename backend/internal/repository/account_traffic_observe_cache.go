package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// 账号级流量遥测（只观察）缓存。
//
// 键布局（全部 24h TTL，每次写入都刷新）：
//
//	account_traffic_observe:{<accountID>}:<protocol>         HASH  started / completed_2xx / upstream_429 /
//	                                                                upstream_5xx / cancelled / failed_other /
//	                                                                peak_in_flight / observed_since_ms
//	account_traffic_observe:{<accountID>}:<protocol>:minute  ZSET  member "<now_ms>:<started_seq>"，score now_ms；
//	                                                                Begin 时裁掉 60s 之前的成员 → 滚动 60s 计数
//
// protocol ∈ {http, ws}，字段名来自 service 层的封闭常量集合（写入前校验），键与字段集合有界。
//
// peak_in_flight 在 Begin 时只读采样：ZCOUNT concurrency:account:{id}（score ≥ now-slotTTL）
// + ZCOUNT concurrency:live:account:{id}（score ≥ now-60），边界与 acquireScript 的过期规则一致，
// 且永不写这两个并发 ZSET。Concurrency<=0（不限并发）的账号从不写槽位成员，因此其 peak 恒为 0。
const (
	accountTrafficObserveKeyPrefix = "account_traffic_observe:"
	accountTrafficObserveTTL       = time.Duration(service.AccountTrafficObserveTTLSeconds) * time.Second
	accountTrafficObserveWindowMs  = 60_000
)

// accountTrafficObserveBeginScript 原子完成：started+1、滚动窗口成员维护、只读采样并发槽位、
// 刷新 peak_in_flight 与 TTL。返回 {inflight, 窗口内成员数}。
// KEYS[1]=HASH KEYS[2]=minute ZSET KEYS[3]=concurrency:account:{id} KEYS[4]=concurrency:live:account:{id}
// ARGV[1]=slotTTLSeconds ARGV[2]=ttlSeconds ARGV[3]=windowMs
var accountTrafficObserveBeginScript = redis.NewScript(`
	redis.replicate_commands()
	local stateKey = KEYS[1]
	local minuteKey = KEYS[2]
	local slotKey = KEYS[3]
	local liveSlotKey = KEYS[4]
	local slotTTL = tonumber(ARGV[1])
	local ttl = tonumber(ARGV[2])
	local windowMs = tonumber(ARGV[3])

	local t = redis.call('TIME')
	local nowS = tonumber(t[1])
	local nowMs = nowS * 1000 + math.floor(tonumber(t[2]) / 1000)

	local seq = redis.call('HINCRBY', stateKey, 'started', 1)
	redis.call('HSETNX', stateKey, 'observed_since_ms', nowMs)

	redis.call('ZREMRANGEBYSCORE', minuteKey, '-inf', nowMs - windowMs)
	redis.call('ZADD', minuteKey, nowMs, nowMs .. ':' .. seq)

	-- 只读采样：与 acquireScript 的过期边界一致，绝不修改并发键。
	local inflight = redis.call('ZCOUNT', slotKey, nowS - slotTTL, '+inf') + redis.call('ZCOUNT', liveSlotKey, nowS - 60, '+inf')
	local peak = tonumber(redis.call('HGET', stateKey, 'peak_in_flight') or '0') or 0
	if inflight > peak then
		redis.call('HSET', stateKey, 'peak_in_flight', inflight)
	end

	redis.call('EXPIRE', stateKey, ttl)
	redis.call('EXPIRE', minuteKey, ttl)
	return {inflight, redis.call('ZCARD', minuteKey)}
`)

type accountTrafficObserveCache struct {
	rdb            *redis.Client
	slotTTLSeconds int
}

// NewAccountTrafficObserveCache 创建遥测缓存；slotTTLMinutes 与并发缓存共用同一配置，
// 保证 in-flight 采样与 acquireScript 使用相同的过期边界。
func NewAccountTrafficObserveCache(rdb *redis.Client, slotTTLMinutes int) service.AccountTrafficObserveCache {
	if slotTTLMinutes <= 0 {
		slotTTLMinutes = defaultSlotTTLMinutes
	}
	return &accountTrafficObserveCache{rdb: rdb, slotTTLSeconds: slotTTLMinutes * 60}
}

// ProvideAccountTrafficObserveCache 从配置读取槽位 TTL（与 ProvideConcurrencyCache 一致）。
func ProvideAccountTrafficObserveCache(rdb *redis.Client, cfg *config.Config) service.AccountTrafficObserveCache {
	slotTTLMinutes := 0
	if cfg != nil {
		slotTTLMinutes = cfg.Gateway.ConcurrencySlotTTLMinutes
	}
	return NewAccountTrafficObserveCache(rdb, slotTTLMinutes)
}

func accountTrafficObserveStateKey(accountID int64, protocol service.AccountTrafficProtocol) string {
	return fmt.Sprintf("%s{%d}:%s", accountTrafficObserveKeyPrefix, accountID, protocol)
}

func accountTrafficObserveMinuteKey(accountID int64, protocol service.AccountTrafficProtocol) string {
	return accountTrafficObserveStateKey(accountID, protocol) + ":minute"
}

func (c *accountTrafficObserveCache) ready() error {
	if c == nil || c.rdb == nil {
		return errors.New("account traffic observe cache is not configured")
	}
	return nil
}

func (c *accountTrafficObserveCache) Begin(ctx context.Context, accountID int64, protocol service.AccountTrafficProtocol) error {
	if err := c.ready(); err != nil {
		return err
	}
	if !protocol.Valid() {
		return fmt.Errorf("account traffic observe: unknown protocol %q", string(protocol))
	}
	keys := []string{
		accountTrafficObserveStateKey(accountID, protocol),
		accountTrafficObserveMinuteKey(accountID, protocol),
		accountSlotKey(accountID),
		liveAccountSlotKey(accountID),
	}
	_, _, err := runScriptInt64Pair(ctx, c.rdb, accountTrafficObserveBeginScript, keys,
		c.slotTTLSeconds, service.AccountTrafficObserveTTLSeconds, accountTrafficObserveWindowMs)
	return err
}

// Finish 与 rpm_cache 同形：TxPipeline HINCRBY + EXPIRE，无需 Lua。
func (c *accountTrafficObserveCache) Finish(ctx context.Context, accountID int64, protocol service.AccountTrafficProtocol, outcome service.AccountTrafficOutcome) error {
	if err := c.ready(); err != nil {
		return err
	}
	if !protocol.Valid() {
		return fmt.Errorf("account traffic observe: unknown protocol %q", string(protocol))
	}
	if !outcome.Valid() {
		return fmt.Errorf("account traffic observe: unknown outcome %q", string(outcome))
	}
	stateKey := accountTrafficObserveStateKey(accountID, protocol)
	pipe := c.rdb.TxPipeline()
	pipe.HIncrBy(ctx, stateKey, string(outcome), 1)
	pipe.Expire(ctx, stateKey, accountTrafficObserveTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// Snapshot 读取两个协议的 HASH 与滚动 60s 计数；时间取 Redis TIME，避免多实例时钟偏差。
func (c *accountTrafficObserveCache) Snapshot(ctx context.Context, accountID int64) (map[service.AccountTrafficProtocol]service.AccountTrafficObserveState, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	serverTime, err := c.rdb.Time(ctx).Result()
	if err != nil {
		return nil, fmt.Errorf("account traffic observe snapshot: redis TIME: %w", err)
	}
	windowStart := "(" + strconv.FormatInt(serverTime.UnixMilli()-accountTrafficObserveWindowMs, 10)

	protocols := service.AccountTrafficProtocols()
	hashCmds := make(map[service.AccountTrafficProtocol]*redis.MapStringStringCmd, len(protocols))
	countCmds := make(map[service.AccountTrafficProtocol]*redis.IntCmd, len(protocols))
	pipe := c.rdb.Pipeline()
	for _, protocol := range protocols {
		hashCmds[protocol] = pipe.HGetAll(ctx, accountTrafficObserveStateKey(accountID, protocol))
		countCmds[protocol] = pipe.ZCount(ctx, accountTrafficObserveMinuteKey(accountID, protocol), windowStart, "+inf")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("account traffic observe snapshot: %w", err)
	}

	result := make(map[service.AccountTrafficProtocol]service.AccountTrafficObserveState, len(protocols))
	for _, protocol := range protocols {
		fields, _ := hashCmds[protocol].Result()
		last60s, _ := countCmds[protocol].Result()
		result[protocol] = parseAccountTrafficObserveState(fields, last60s)
	}
	return result, nil
}

func parseAccountTrafficObserveState(fields map[string]string, last60s int64) service.AccountTrafficObserveState {
	get := func(name string) int64 {
		value, err := strconv.ParseInt(fields[name], 10, 64)
		if err != nil {
			return 0
		}
		return value
	}
	return service.AccountTrafficObserveState{
		Started:         get("started"),
		Completed2xx:    get(string(service.AccountTrafficOutcomeCompleted2xx)),
		Upstream429:     get(string(service.AccountTrafficOutcomeUpstream429)),
		Upstream5xx:     get(string(service.AccountTrafficOutcomeUpstream5xx)),
		Cancelled:       get(string(service.AccountTrafficOutcomeCancelled)),
		FailedOther:     get(string(service.AccountTrafficOutcomeFailedOther)),
		PeakInFlight:    int(get("peak_in_flight")),
		RequestsLast60s: int(last60s),
		ObservedSinceMs: get("observed_since_ms"),
	}
}
