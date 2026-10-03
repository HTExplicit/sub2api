package repository

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// One stored rejection is charged its payload ("<rejected ms>:<expires ms>",
// 13 digits each) plus its id ("<scope hash>:<cipher hash>").
const reasoningStateTestEntryBytes = 13 + 1 + 13 + 64 + 1 + 64

func newReasoningStateTestCache(t *testing.T) (*gatewayCache, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return &gatewayCache{rdb: client}, server
}

func reasoningStateTestScope() service.OpenAIReasoningCacheScope {
	return service.OpenAIReasoningCacheScope{ScopeHash: strings.Repeat("a", 64), TenantHash: strings.Repeat("b", 64)}
}

func TestGatewayCacheReasoningStateHardTTLAndIsolation(t *testing.T) {
	cache, server := newReasoningStateTestCache(t)
	scope, key, ctx := reasoningStateTestScope(), strings.Repeat("d", 64), context.Background()
	require.NoError(t, cache.PutOpenAIRejectedReasoning(ctx, scope, []string{key}))
	entryKey := rejectedReasoningPrefix + "entry:" + scope.ScopeHash + ":" + key
	require.Equal(t, 24*time.Hour, server.TTL(entryKey))

	server.FastForward(23 * time.Hour)
	rejected, err := cache.GetOpenAIRejectedReasoning(ctx, scope, []string{key, strings.Repeat("e", 64)})
	require.NoError(t, err)
	require.Len(t, rejected, 1)
	require.Equal(t, 24*time.Hour, rejected[key].ExpiresAt.Sub(rejected[key].RejectedAt))
	require.Equal(t, time.Hour, server.TTL(entryKey), "a lookup must not extend the rejection")
	other := scope
	other.ScopeHash = strings.Repeat("f", 64)
	none, err := cache.GetOpenAIRejectedReasoning(ctx, other, []string{key})
	require.NoError(t, err)
	require.Empty(t, none)
	other = scope
	other.TenantHash = strings.Repeat("f", 64)
	none, err = cache.GetOpenAIRejectedReasoning(ctx, other, []string{key})
	require.NoError(t, err)
	require.Empty(t, none)

	// Only another real rejection write renews the evidence.
	require.NoError(t, cache.PutOpenAIRejectedReasoning(ctx, scope, []string{key}))
	require.Equal(t, 24*time.Hour, server.TTL(entryKey))
	server.FastForward(time.Hour + time.Millisecond)
	rejected, err = cache.GetOpenAIRejectedReasoning(ctx, scope, []string{key})
	require.NoError(t, err)
	require.Len(t, rejected, 1)
	server.FastForward(23 * time.Hour)
	rejected, err = cache.GetOpenAIRejectedReasoning(ctx, scope, []string{key})
	require.NoError(t, err)
	require.Empty(t, rejected)
}

func TestGatewayCacheReasoningStateBoundedAtomicLRU(t *testing.T) {
	cache, server := newReasoningStateTestCache(t)
	ctx, scope := context.Background(), reasoningStateTestScope()
	other := service.OpenAIReasoningCacheScope{ScopeHash: strings.Repeat("c", 64), TenantHash: strings.Repeat("d", 64)}
	const unit = reasoningStateTestEntryBytes
	limits := reasoningStateLimits{bytes: 3 * unit, entries: 3, tenantBytes: 2 * unit, entryBytes: unit}
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	put := func(owner service.OpenAIReasoningCacheScope, key string) {
		t.Helper()
		clock = clock.Add(time.Millisecond)
		server.SetTime(clock)
		args := append(rejectedReasoningArguments("put", owner, limits), owner.ScopeHash+":"+key)
		result, err := cache.runRejectedReasoning(ctx, args)
		require.NoError(t, err)
		require.Equal(t, int64(1), result)
	}
	entry := func(owner service.OpenAIReasoningCacheScope, key string) string {
		return rejectedReasoningPrefix + "entry:" + owner.ScopeHash + ":" + key
	}
	first, second, third, fourth, fifth := strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64), strings.Repeat("4", 64), strings.Repeat("5", 64)
	put(scope, first)
	put(scope, second)
	put(other, third)
	clock = clock.Add(time.Millisecond)
	server.SetTime(clock)
	_, err := cache.runRejectedReasoning(ctx, append(rejectedReasoningArguments("get", scope, limits), scope.ScopeHash+":"+first))
	require.NoError(t, err)
	put(scope, fourth) // The per-key bound of two entries evicts second, not the touched first.
	require.True(t, server.Exists(entry(scope, first)))
	require.False(t, server.Exists(entry(scope, second)))
	put(other, fifth) // The global bound of three entries evicts the oldest third.
	require.False(t, server.Exists(entry(other, third)))
	require.Equal(t, strconv.Itoa(3*unit), server.HGet(rejectedReasoningPrefix+"totals", "_global"))
	require.Equal(t, strconv.Itoa(2*unit), server.HGet(rejectedReasoningPrefix+"totals", scope.TenantHash))
	require.Equal(t, strconv.Itoa(unit), server.HGet(rejectedReasoningPrefix+"totals", other.TenantHash))
	count, err := cache.rdb.ZCard(ctx, rejectedReasoningPrefix+"lru").Result()
	require.NoError(t, err)
	require.Equal(t, int64(3), count)

	// Refuse an oversized incoming record without deleting an existing one.
	stored := server.HGet(entry(scope, first), "payload")
	oversized := limits
	oversized.entryBytes = unit - 1
	result, err := cache.runRejectedReasoning(ctx, append(rejectedReasoningArguments("put", scope, oversized), scope.ScopeHash+":"+first))
	require.NoError(t, err)
	require.Equal(t, int64(0), result)
	require.Equal(t, stored, server.HGet(entry(scope, first), "payload"))

	// Lower entry limit independently from bytes to exercise the count gate.
	limits.entries, limits.bytes, limits.tenantBytes = 2, 100*unit, 100*unit
	put(scope, strings.Repeat("6", 64))
	count, err = cache.rdb.ZCard(ctx, rejectedReasoningPrefix+"lru").Result()
	require.NoError(t, err)
	require.Equal(t, int64(2), count)
}

func TestGatewayCacheReasoningStateBoundAndDigestOnly(t *testing.T) {
	cache, server := newReasoningStateTestCache(t)
	scope, ctx := reasoningStateTestScope(), context.Background()
	limits := reasoningStateLimits{bytes: 350, entries: 2, tenantBytes: 350, entryBytes: 256}
	args := rejectedReasoningArguments("put", scope, limits)
	for _, digit := range []string{"1", "2", "3"} {
		args = append(args, scope.ScopeHash+":"+strings.Repeat(digit, 64))
	}
	_, err := cache.runRejectedReasoning(ctx, args)
	require.NoError(t, err)
	count, err := cache.rdb.ZCard(ctx, rejectedReasoningPrefix+"lru").Result()
	require.NoError(t, err)
	require.Equal(t, int64(2), count)
	total, err := strconv.Atoi(server.HGet(rejectedReasoningPrefix+"totals", "_global"))
	require.NoError(t, err)
	require.LessOrEqual(t, total, 350)
	for _, key := range server.Keys() {
		require.True(t, strings.HasPrefix(key, rejectedReasoningPrefix))
		require.Positive(t, server.TTL(key), "every key of the rejection memory expires")
		if strings.Contains(key, ":entry:") {
			require.Regexp(t, `^\d+:\d+$`, server.HGet(key, "payload"))
		}
	}
}

func TestGatewayCacheReasoningStateConcurrentCapacity(t *testing.T) {
	cache, server := newReasoningStateTestCache(t)
	scope, ctx := reasoningStateTestScope(), context.Background()
	const unit = reasoningStateTestEntryBytes
	limits := reasoningStateLimits{bytes: 4 * unit, entries: 4, tenantBytes: 2 * unit, entryBytes: unit}
	results := make(chan error, 8)
	for index := range cap(results) {
		go func() {
			key := strings.Repeat(strconv.Itoa(index), 64)
			_, err := cache.runRejectedReasoning(ctx, append(rejectedReasoningArguments("put", scope, limits), scope.ScopeHash+":"+key))
			results <- err
		}()
	}
	for range cap(results) {
		require.NoError(t, <-results)
	}
	require.Equal(t, strconv.Itoa(2*unit), server.HGet(rejectedReasoningPrefix+"totals", "_global"))
	require.Equal(t, strconv.Itoa(2*unit), server.HGet(rejectedReasoningPrefix+"totals", scope.TenantHash))
	count, err := cache.rdb.ZCard(ctx, rejectedReasoningPrefix+"lru").Result()
	require.NoError(t, err)
	require.Equal(t, int64(2), count)
}

func TestGatewayCacheReasoningStateBlockedTransportBudget(t *testing.T) {
	var dials atomic.Int32
	client := redis.NewClient(&redis.Options{
		Addr: "synthetic.invalid:6379",
		Dialer: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dials.Add(1)
			local, peer := net.Pipe()
			go func() {
				defer func() { _ = peer.Close() }()
				_, _ = io.Copy(io.Discard, peer) // Accept writes, never send a reply.
			}()
			return local, nil
		},
	})
	t.Cleanup(func() { _ = client.Close() })
	// Compare with the initialized shared client, not a dependency version's
	// timeout defaults: only the per-operation clone may tighten these bounds.
	sharedOptions := *client.Options()
	cache := &gatewayCache{rdb: client}
	budget := service.NewOpenAIReasoningCacheBudget()
	started := time.Now()
	require.NoError(t, budget.Do(context.Background(), func(ctx context.Context) error {
		select {
		case <-time.After(20 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}))
	err := budget.Do(context.Background(), func(ctx context.Context) error {
		_, err := cache.GetOpenAIRejectedReasoning(ctx, reasoningStateTestScope(), []string{strings.Repeat("d", 64)})
		return err
	})
	require.Error(t, err)
	require.LessOrEqual(t, time.Since(started), service.OpenAIReasoningStateIOBudget+50*time.Millisecond)
	require.Equal(t, int32(1), dials.Load())
	options := client.Options()
	require.Equal(t, sharedOptions.ContextTimeoutEnabled, options.ContextTimeoutEnabled, "shared Redis options must remain unchanged")
	require.Equal(t, sharedOptions.DialTimeout, options.DialTimeout)
	require.Equal(t, sharedOptions.ReadTimeout, options.ReadTimeout)
	require.Equal(t, sharedOptions.WriteTimeout, options.WriteTimeout)
	require.Equal(t, sharedOptions.PoolTimeout, options.PoolTimeout)
	require.Equal(t, sharedOptions.MaxRetries, options.MaxRetries)
	require.Equal(t, sharedOptions.DialerRetries, options.DialerRetries)
	require.Equal(t, sharedOptions.DialerRetryTimeout, options.DialerRetryTimeout)
	require.Equal(t, sharedOptions.PoolSize, options.PoolSize)
	require.Equal(t, sharedOptions.MaxActiveConns, options.MaxActiveConns)
	require.Equal(t, sharedOptions.MaxIdleConns, options.MaxIdleConns)
	require.Equal(t, sharedOptions.MinIdleConns, options.MinIdleConns)
	require.Equal(t, sharedOptions.MaxConcurrentDials, options.MaxConcurrentDials)
}

func TestGatewayCacheReasoningStateDialFailureDoesNotRetry(t *testing.T) {
	var dials atomic.Int32
	client := redis.NewClient(&redis.Options{
		Addr: "synthetic.invalid:6379",
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			return nil, errors.New("synthetic cache unavailable")
		},
	})
	t.Cleanup(func() { _ = client.Close() })
	cache := &gatewayCache{rdb: client}
	_, err := cache.GetOpenAIRejectedReasoning(context.Background(), reasoningStateTestScope(), []string{strings.Repeat("d", 64)})
	require.Error(t, err)
	require.Equal(t, int32(1), dials.Load())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = cache.GetOpenAIRejectedReasoning(ctx, reasoningStateTestScope(), []string{strings.Repeat("d", 64)})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, int32(1), dials.Load())
}
