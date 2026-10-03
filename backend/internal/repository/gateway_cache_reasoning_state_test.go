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
	return newReasoningStateTestCacheWith(t, &redis.Options{Addr: server.Addr()}), server
}

// The cache is built as production builds it: the rejection memory gets its own
// client derived from the shared one.
func newReasoningStateTestCacheWith(t *testing.T, options *redis.Options) *gatewayCache {
	t.Helper()
	client := redis.NewClient(options)
	cache, ok := NewGatewayCache(client).(*gatewayCache)
	require.True(t, ok)
	t.Cleanup(func() {
		_ = cache.reasoningStateRDB.Close()
		_ = client.Close()
	})
	return cache
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

// The rejection memory keeps its connections: operations that follow each other
// reuse one connection instead of dialing.
func TestGatewayCacheReasoningStateReusesItsConnection(t *testing.T) {
	server := miniredis.RunT(t)
	var dials atomic.Int32
	cache := newReasoningStateTestCacheWith(t, &redis.Options{
		Addr: server.Addr(),
		Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dials.Add(1)
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	})
	scope, key, ctx := reasoningStateTestScope(), strings.Repeat("d", 64), context.Background()
	for range 3 {
		require.NoError(t, cache.PutOpenAIRejectedReasoning(ctx, scope, []string{key}))
		rejected, err := cache.GetOpenAIRejectedReasoning(ctx, scope, []string{key})
		require.NoError(t, err)
		require.Len(t, rejected, 1)
	}
	require.Equal(t, int32(1), dials.Load())
}

// reasoningStateDeadlineConn reports every deadline the client sets on its socket.
type reasoningStateDeadlineConn struct {
	net.Conn
	record func(time.Time)
}

func (c reasoningStateDeadlineConn) SetDeadline(deadline time.Time) error {
	c.record(deadline)
	return c.Conn.SetDeadline(deadline)
}

func (c reasoningStateDeadlineConn) SetReadDeadline(deadline time.Time) error {
	c.record(deadline)
	return c.Conn.SetReadDeadline(deadline)
}

func (c reasoningStateDeadlineConn) SetWriteDeadline(deadline time.Time) error {
	c.record(deadline)
	return c.Conn.SetWriteDeadline(deadline)
}

// A Redis that is slow to connect and then never answers costs the request no
// more than its I/O budget: no write or read gets a deadline later than the
// budget's own, although acquiring the connection already spent part of it.
// The bound belongs to the rejection memory's client: the shared client keeps
// its options.
func TestGatewayCacheReasoningStateBlockedTransportBudget(t *testing.T) {
	var socketDeadlines []time.Time
	cache := newReasoningStateTestCacheWith(t, &redis.Options{
		Addr: "synthetic.invalid:6379",
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			time.Sleep(20 * time.Millisecond)
			local, peer := net.Pipe()
			go func() {
				defer func() { _ = peer.Close() }()
				_, _ = io.Copy(io.Discard, peer) // Accept writes, never send a reply.
			}()
			return reasoningStateDeadlineConn{Conn: local, record: func(deadline time.Time) {
				socketDeadlines = append(socketDeadlines, deadline)
			}}, nil
		},
	})
	sharedOptions := *cache.rdb.Options()
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
	var budgetDeadline time.Time
	err := budget.Do(context.Background(), func(ctx context.Context) error {
		budgetDeadline, _ = ctx.Deadline()
		_, err := cache.GetOpenAIRejectedReasoning(ctx, reasoningStateTestScope(), []string{strings.Repeat("d", 64)})
		return err
	})
	require.Error(t, err)
	require.LessOrEqual(t, time.Since(started), service.OpenAIReasoningStateIOBudget+50*time.Millisecond)
	require.NotEmpty(t, socketDeadlines, "the exchange started inside the budget")
	for _, deadline := range socketDeadlines {
		require.False(t, deadline.After(budgetDeadline), "a write or read may not outlast the budget")
	}
	require.ErrorIs(t, budget.Do(context.Background(), func(context.Context) error { return nil }), service.ErrOpenAIReasoningCacheBudget,
		"a spent budget starts no further cache I/O for the request")
	options := cache.rdb.Options()
	require.Equal(t, sharedOptions.ContextTimeoutEnabled, options.ContextTimeoutEnabled, "shared Redis options must remain unchanged")
	require.Equal(t, sharedOptions.MaxRetries, options.MaxRetries)
	require.Equal(t, sharedOptions.DialerRetries, options.DialerRetries)
	require.Equal(t, sharedOptions.MinIdleConns, options.MinIdleConns)
}

// An unreachable Redis fails the operation at the first refused dial: nothing
// is retried and nothing waits for the budget to run out. A request that is
// already cancelled starts no Redis work.
func TestGatewayCacheReasoningStateRefusedDialFailsAtOnce(t *testing.T) {
	var dials atomic.Int32
	cache := newReasoningStateTestCacheWith(t, &redis.Options{
		Addr: "synthetic.invalid:6379",
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			return nil, errors.New("synthetic cache unavailable")
		},
	})
	started := time.Now()
	_, err := cache.GetOpenAIRejectedReasoning(context.Background(), reasoningStateTestScope(), []string{strings.Repeat("d", 64)})
	require.Error(t, err)
	require.Less(t, time.Since(started), service.OpenAIReasoningStateIOBudget)
	require.Equal(t, int32(1), dials.Load())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = cache.GetOpenAIRejectedReasoning(ctx, reasoningStateTestScope(), []string{strings.Repeat("d", 64)})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, int32(1), dials.Load())
}
