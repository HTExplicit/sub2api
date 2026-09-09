package repository

import (
	"context"
	"encoding/json"
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

func reasoningStateTestBatch() service.OpenAIReasoningBatch {
	return service.OpenAIReasoningBatch{
		Output: []json.RawMessage{
			json.RawMessage(`{"type":"reasoning","id":"rs_fixture","status":"completed","summary":[],"encrypted_content":"synthetic-cipher","future":{"kept":true}}`),
			json.RawMessage(`{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"loading"}]}`),
			json.RawMessage(`{"type":"function_call","call_id":"call_fixture","name":"load_orders","arguments":"{}","status":"completed"}`),
		},
		Projection:      json.RawMessage(`{"role":"assistant","content":"loading","tool_calls":[{"id":"call_fixture","type":"function","function":{"name":"load_orders","arguments":"{}"}}]}`),
		InputPrefixHash: strings.Repeat("c", 64),
	}
}

func TestGatewayCacheReasoningStateRoundTripIsolationAndCAS(t *testing.T) {
	cache, _ := newReasoningStateTestCache(t)
	scope, key, ctx := reasoningStateTestScope(), strings.Repeat("d", 64), context.Background()
	batch := reasoningStateTestBatch()
	put, err := cache.PutOpenAIReasoningBatch(ctx, scope, key, batch)
	require.NoError(t, err)
	require.True(t, put)
	values, err := cache.GetOpenAIReasoningBatches(ctx, scope, []string{key, strings.Repeat("e", 64)})
	require.NoError(t, err)
	require.Len(t, values, 1)
	stored := values[key]
	require.Len(t, stored.Output, len(batch.Output))
	for index := range batch.Output {
		require.JSONEq(t, string(batch.Output[index]), string(stored.Output[index]))
	}
	require.JSONEq(t, string(batch.Projection), string(stored.Projection))
	require.Equal(t, batch.InputPrefixHash, stored.InputPrefixHash)
	require.True(t, service.IsOpenAIReasoningCacheDigest(stored.PayloadHash))

	other := scope
	other.ScopeHash = strings.Repeat("f", 64)
	missing, err := cache.GetOpenAIReasoningBatches(ctx, other, []string{key})
	require.NoError(t, err)
	require.Empty(t, missing)
	other = scope
	other.TenantHash = strings.Repeat("f", 64)
	missing, err = cache.GetOpenAIReasoningBatches(ctx, other, []string{key})
	require.NoError(t, err)
	require.Empty(t, missing)
	deleted, err := cache.DeleteOpenAIReasoningBatchIfMatch(ctx, other, key, stored.PayloadHash)
	require.NoError(t, err)
	require.False(t, deleted)

	batch.Output[0] = json.RawMessage(`{"type":"reasoning","summary":[],"encrypted_content":"new-synthetic-cipher"}`)
	put, err = cache.PutOpenAIReasoningBatch(ctx, scope, key, batch)
	require.NoError(t, err)
	require.True(t, put)
	deleted, err = cache.DeleteOpenAIReasoningBatchIfMatch(ctx, scope, key, stored.PayloadHash)
	require.NoError(t, err)
	require.False(t, deleted)
	values, err = cache.GetOpenAIReasoningBatches(ctx, scope, []string{key})
	require.NoError(t, err)
	require.NotEqual(t, stored.PayloadHash, values[key].PayloadHash)
	deleted, err = cache.DeleteOpenAIReasoningBatchIfMatch(ctx, scope, key, values[key].PayloadHash)
	require.NoError(t, err)
	require.True(t, deleted)
	values, err = cache.GetOpenAIReasoningBatches(ctx, scope, []string{key})
	require.NoError(t, err)
	require.Empty(t, values)
}

func TestGatewayCacheReasoningStateHardTTLAndNegativeSeparation(t *testing.T) {
	cache, server := newReasoningStateTestCache(t)
	scope, key, ctx := reasoningStateTestScope(), strings.Repeat("d", 64), context.Background()
	put, err := cache.PutOpenAIReasoningBatch(ctx, scope, key, reasoningStateTestBatch())
	require.NoError(t, err)
	require.True(t, put)
	require.NoError(t, cache.PutOpenAIRejectedReasoning(ctx, scope, []string{key}))
	positiveKey := reasoningBatchPrefix + "entry:" + scope.ScopeHash + ":" + key
	negativeKey := rejectedReasoningPrefix + "entry:" + scope.ScopeHash + ":" + key
	require.Equal(t, 24*time.Hour, server.TTL(positiveKey))
	require.Equal(t, 24*time.Hour, server.TTL(negativeKey))

	server.FastForward(23 * time.Hour)
	positive, err := cache.GetOpenAIReasoningBatches(ctx, scope, []string{key})
	require.NoError(t, err)
	require.Len(t, positive, 1)
	negative, err := cache.GetOpenAIRejectedReasoning(ctx, scope, []string{key, strings.Repeat("e", 64)})
	require.NoError(t, err)
	require.Len(t, negative, 1)
	require.Equal(t, 24*time.Hour, negative[key].ExpiresAt.Sub(negative[key].RejectedAt))
	require.Equal(t, time.Hour, server.TTL(positiveKey))
	require.Equal(t, time.Hour, server.TTL(negativeKey))
	other := scope
	other.ScopeHash = strings.Repeat("f", 64)
	none, err := cache.GetOpenAIRejectedReasoning(ctx, other, []string{key})
	require.NoError(t, err)
	require.Empty(t, none)

	// Only another real rejection write renews the negative evidence. Positive
	// state remains on its original hard deadline, in its separate namespace.
	require.NoError(t, cache.PutOpenAIRejectedReasoning(ctx, scope, []string{key}))
	require.Equal(t, 24*time.Hour, server.TTL(negativeKey))
	require.Equal(t, time.Hour, server.TTL(positiveKey))
	server.FastForward(time.Hour + time.Millisecond)
	positive, err = cache.GetOpenAIReasoningBatches(ctx, scope, []string{key})
	require.NoError(t, err)
	require.Empty(t, positive)
	negative, err = cache.GetOpenAIRejectedReasoning(ctx, scope, []string{key})
	require.NoError(t, err)
	require.Len(t, negative, 1)
	server.FastForward(23 * time.Hour)
	negative, err = cache.GetOpenAIRejectedReasoning(ctx, scope, []string{key})
	require.NoError(t, err)
	require.Empty(t, negative)
}

func TestGatewayCacheReasoningStateBoundedAtomicLRU(t *testing.T) {
	cache, server := newReasoningStateTestCache(t)
	ctx, scope := context.Background(), reasoningStateTestScope()
	other := service.OpenAIReasoningCacheScope{ScopeHash: strings.Repeat("c", 64), TenantHash: strings.Repeat("d", 64)}
	limits := reasoningStateLimits{bytes: 12, entries: 3, tenantBytes: 8, entryBytes: 8}
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	put := func(owner service.OpenAIReasoningCacheScope, key string) {
		t.Helper()
		clock = clock.Add(time.Millisecond)
		server.SetTime(clock)
		args := reasoningStateArguments(reasoningBatchPrefix, "put", owner, limits)
		args = append(args, owner.ScopeHash+":"+key, "1234", key)
		result, err := cache.runReasoningState(ctx, reasoningBatchPrefix, args)
		require.NoError(t, err)
		require.Equal(t, int64(1), result)
	}
	first, second, third, fourth, fifth := strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64), strings.Repeat("4", 64), strings.Repeat("5", 64)
	put(scope, first)
	put(scope, second)
	put(other, third)
	clock = clock.Add(time.Millisecond)
	server.SetTime(clock)
	_, err := cache.getReasoningState(ctx, reasoningBatchPrefix, scope, []string{first}, limits)
	require.NoError(t, err)
	put(scope, fourth) // Per-key 8-byte bound evicts second, not the touched first.
	require.True(t, server.Exists(reasoningBatchPrefix+"entry:"+scope.ScopeHash+":"+first))
	require.False(t, server.Exists(reasoningBatchPrefix+"entry:"+scope.ScopeHash+":"+second))
	put(other, fifth) // Global 12-byte bound evicts the oldest third.
	require.False(t, server.Exists(reasoningBatchPrefix+"entry:"+other.ScopeHash+":"+third))
	require.Equal(t, "12", server.HGet(reasoningBatchPrefix+"totals", "_global"))
	require.Equal(t, "8", server.HGet(reasoningBatchPrefix+"totals", scope.TenantHash))
	require.Equal(t, "4", server.HGet(reasoningBatchPrefix+"totals", other.TenantHash))
	count, err := cache.rdb.ZCard(ctx, reasoningBatchPrefix+"lru").Result()
	require.NoError(t, err)
	require.Equal(t, int64(3), count)

	// Refuse an oversized incoming record without deleting an existing one.
	args := reasoningStateArguments(reasoningBatchPrefix, "put", scope, limits)
	args = append(args, scope.ScopeHash+":"+first, strings.Repeat("x", 9), first)
	result, err := cache.runReasoningState(ctx, reasoningBatchPrefix, args)
	require.NoError(t, err)
	require.Equal(t, int64(0), result)
	require.Equal(t, "1234", server.HGet(reasoningBatchPrefix+"entry:"+scope.ScopeHash+":"+first, "payload"))

	// Lower entry limit independently from bytes to exercise the count gate.
	limits.entries, limits.bytes, limits.tenantBytes = 2, 100, 100
	put(scope, strings.Repeat("6", 64))
	count, err = cache.rdb.ZCard(ctx, reasoningBatchPrefix+"lru").Result()
	require.NoError(t, err)
	require.Equal(t, int64(2), count)
}

func TestGatewayCacheReasoningStateBatchValidation(t *testing.T) {
	cache, server := newReasoningStateTestCache(t)
	scope, key := reasoningStateTestScope(), strings.Repeat("d", 64)
	cases := map[string]func(*service.OpenAIReasoningBatch){
		"prefix missing": func(batch *service.OpenAIReasoningBatch) { batch.InputPrefixHash = "" },
		"unknown item": func(batch *service.OpenAIReasoningBatch) {
			batch.Output = append(batch.Output, json.RawMessage(`{"type":"custom_tool_call"}`))
		},
		"incomplete": func(batch *service.OpenAIReasoningBatch) {
			batch.Output[0] = json.RawMessage(`{"type":"reasoning","status":"incomplete"}`)
		},
		"too many items": func(batch *service.OpenAIReasoningBatch) {
			for len(batch.Output) <= service.OpenAIReasoningBatchMaxItems {
				batch.Output = append(batch.Output, json.RawMessage(`{"type":"reasoning","summary":[]}`))
			}
		},
		"too many calls": func(batch *service.OpenAIReasoningBatch) {
			batch.Output = nil
			for range service.OpenAIReasoningBatchMaxCalls + 1 {
				batch.Output = append(batch.Output, json.RawMessage(`{"type":"function_call","status":"completed"}`))
			}
		},
		"too many bytes": func(batch *service.OpenAIReasoningBatch) {
			batch.Projection = json.RawMessage(strconv.Quote(strings.Repeat("x", service.OpenAIReasoningBatchMaxBytes)))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			batch := reasoningStateTestBatch()
			mutate(&batch)
			put, err := cache.PutOpenAIReasoningBatch(context.Background(), scope, key, batch)
			require.ErrorIs(t, err, service.ErrOpenAIReasoningCacheInput)
			require.False(t, put)
		})
	}
	require.Empty(t, server.Keys())
}

func TestGatewayCacheReasoningStateNegativeBoundAndDigestOnly(t *testing.T) {
	cache, server := newReasoningStateTestCache(t)
	scope, ctx := reasoningStateTestScope(), context.Background()
	limits := reasoningStateLimits{bytes: 350, entries: 2, tenantBytes: 350, entryBytes: 256}
	args := reasoningStateArguments(rejectedReasoningPrefix, "put", scope, limits)
	for _, digit := range []string{"1", "2", "3"} {
		key := strings.Repeat(digit, 64)
		args = append(args, scope.ScopeHash+":"+key, "", key)
	}
	_, err := cache.runReasoningState(ctx, rejectedReasoningPrefix, args)
	require.NoError(t, err)
	count, err := cache.rdb.ZCard(ctx, rejectedReasoningPrefix+"lru").Result()
	require.NoError(t, err)
	require.Equal(t, int64(2), count)
	total, err := strconv.Atoi(server.HGet(rejectedReasoningPrefix+"totals", "_global"))
	require.NoError(t, err)
	require.LessOrEqual(t, total, 350)
	for _, key := range server.Keys() {
		require.True(t, strings.HasPrefix(key, rejectedReasoningPrefix))
		if strings.Contains(key, ":entry:") {
			require.Regexp(t, `^\d+:\d+$`, server.HGet(key, "payload"))
		}
	}
}

func TestGatewayCacheReasoningStateConcurrentCapacity(t *testing.T) {
	cache, server := newReasoningStateTestCache(t)
	scope, ctx := reasoningStateTestScope(), context.Background()
	limits := reasoningStateLimits{bytes: 16, entries: 4, tenantBytes: 8, entryBytes: 8}
	results := make(chan error, 8)
	for index := range cap(results) {
		go func() {
			key := reasoningPayloadDigest([]byte(strconv.Itoa(index)))
			args := reasoningStateArguments(reasoningBatchPrefix, "put", scope, limits)
			args = append(args, scope.ScopeHash+":"+key, "1234", key)
			_, err := cache.runReasoningState(ctx, reasoningBatchPrefix, args)
			results <- err
		}()
	}
	for range cap(results) {
		require.NoError(t, <-results)
	}
	require.Equal(t, "8", server.HGet(reasoningBatchPrefix+"totals", "_global"))
	require.Equal(t, "8", server.HGet(reasoningBatchPrefix+"totals", scope.TenantHash))
	count, err := cache.rdb.ZCard(ctx, reasoningBatchPrefix+"lru").Result()
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
		_, err := cache.GetOpenAIReasoningBatches(ctx, reasoningStateTestScope(), []string{strings.Repeat("d", 64)})
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
