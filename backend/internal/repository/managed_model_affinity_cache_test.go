package repository

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newManagedModelAffinityTestCache(t *testing.T) (*gatewayCache, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	return &gatewayCache{rdb: client}, server
}

func TestManagedModelAffinityCacheRoundTripAcrossInstances(t *testing.T) {
	cache, server := newManagedModelAffinityTestCache(t)
	ctx := context.Background()
	first, second, missing := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	// This ID cannot be represented exactly by Lua's floating-point numbers.
	binding := service.ManagedModelAffinityBinding{AccountID: 9007199254740993, BranchSelector: strings.Repeat("s", 256)}
	require.NoError(t, cache.BindManagedModelAffinity(ctx, []string{first, second, first}, binding, 24*time.Hour))
	require.ElementsMatch(t, []string{managedModelAffinityPrefix + first, managedModelAffinityPrefix + second}, server.Keys())
	raw, err := server.Get(managedModelAffinityPrefix + first)
	require.NoError(t, err)
	require.Equal(t, "9007199254740993\n"+binding.BranchSelector, raw)
	require.Equal(t, 24*time.Hour, server.TTL(managedModelAffinityPrefix+first))

	// A fresh Redis client and gateway cache have no process-local state.
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	restarted := NewGatewayCache(client).(service.ManagedModelAffinityCache)
	got, err := restarted.GetManagedModelAffinity(ctx, []string{first, missing, second, first})
	require.NoError(t, err)
	require.Equal(t, map[string]service.ManagedModelAffinityBinding{first: binding, second: binding}, got)
}

func TestManagedModelAffinityCacheConflictsStayAmbiguous(t *testing.T) {
	original := service.ManagedModelAffinityBinding{AccountID: 41, BranchSelector: "branch-a"}
	for name, competing := range map[string]service.ManagedModelAffinityBinding{
		"same_account_different_branch": {AccountID: 41, BranchSelector: "branch-b"},
		"different_account":             {AccountID: 42, BranchSelector: "branch-a"},
		"explicit_ambiguity":            {Ambiguous: true},
	} {
		t.Run(name, func(t *testing.T) {
			cache, server := newManagedModelAffinityTestCache(t)
			ctx, digest := context.Background(), strings.Repeat("a", 64)
			require.NoError(t, cache.BindManagedModelAffinity(ctx, []string{digest}, original, time.Hour))
			for _, binding := range []service.ManagedModelAffinityBinding{competing, original, competing} {
				require.NoError(t, cache.BindManagedModelAffinity(ctx, []string{digest}, binding, time.Hour))
				got, err := cache.GetManagedModelAffinity(ctx, []string{digest})
				require.NoError(t, err)
				require.Equal(t, service.ManagedModelAffinityBinding{Ambiguous: true}, got[digest])
				raw, err := server.Get(managedModelAffinityPrefix + digest)
				require.NoError(t, err)
				require.Equal(t, "!", raw)
			}
			server.FastForward(time.Hour)
			got, err := cache.GetManagedModelAffinity(ctx, []string{digest})
			require.NoError(t, err)
			require.Empty(t, got)
		})
	}
}

func TestManagedModelAffinityCacheConcurrentConflicts(t *testing.T) {
	cache, _ := newManagedModelAffinityTestCache(t)
	ctx, digest := context.Background(), strings.Repeat("a", 64)
	var workers sync.WaitGroup
	errs := make(chan error, 8)
	for worker := range 8 {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			binding := service.ManagedModelAffinityBinding{AccountID: int64(41 + worker%2), BranchSelector: "branch-a"}
			errs <- cache.BindManagedModelAffinity(ctx, []string{digest}, binding, time.Hour)
		}(worker)
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	got, err := cache.GetManagedModelAffinity(ctx, []string{digest})
	require.NoError(t, err)
	require.Equal(t, service.ManagedModelAffinityBinding{Ambiguous: true}, got[digest])
}

func TestManagedModelAffinityCacheTTLReadsDoNotRefresh(t *testing.T) {
	cache, server := newManagedModelAffinityTestCache(t)
	ctx, digest := context.Background(), strings.Repeat("a", 64)
	binding := service.ManagedModelAffinityBinding{AccountID: 41, BranchSelector: "branch-a"}
	key := managedModelAffinityPrefix + digest
	require.NoError(t, cache.BindManagedModelAffinity(ctx, []string{digest}, binding, 2*time.Second))
	server.FastForward(1500 * time.Millisecond)
	got, err := cache.GetManagedModelAffinity(ctx, []string{digest})
	require.NoError(t, err)
	require.Equal(t, binding, got[digest])
	require.Equal(t, 500*time.Millisecond, server.TTL(key), "reading must not extend evidence lifetime")
	require.NoError(t, cache.BindManagedModelAffinity(ctx, []string{digest}, binding, time.Second))
	require.Equal(t, time.Second, server.TTL(key), "only another observation refreshes the binding")
	server.FastForward(time.Second)
	got, err = cache.GetManagedModelAffinity(ctx, []string{digest})
	require.NoError(t, err)
	require.Empty(t, got)

	require.NoError(t, cache.BindManagedModelAffinity(ctx, []string{digest}, binding, time.Nanosecond))
	require.Equal(t, time.Millisecond, server.TTL(key), "positive TTLs use Redis millisecond precision")
	server.FastForward(time.Millisecond)
	got, err = cache.GetManagedModelAffinity(ctx, []string{digest})
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestManagedModelAffinityCacheInvalidInputDoesNotMutate(t *testing.T) {
	cache, server := newManagedModelAffinityTestCache(t)
	ctx, digest := context.Background(), strings.Repeat("a", 64)
	binding := service.ManagedModelAffinityBinding{AccountID: 41, BranchSelector: "branch-a"}
	for name, invalid := range map[string]string{
		"empty":        "",
		"short":        strings.Repeat("a", 63),
		"long":         strings.Repeat("a", 65),
		"uppercase":    strings.Repeat("A", 64),
		"non_hex":      strings.Repeat("g", 64),
		"plaintext":    "resp_plaintext",
		"newline":      strings.Repeat("a", 63) + "\n",
		"prefixed_key": managedModelAffinityPrefix + digest,
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, cache.BindManagedModelAffinity(ctx, []string{digest, invalid}, binding, time.Hour), errManagedModelAffinityInput)
			got, err := cache.GetManagedModelAffinity(ctx, []string{digest, invalid})
			require.ErrorIs(t, err, errManagedModelAffinityInput)
			require.Nil(t, got)
			require.Empty(t, server.Keys())
		})
	}
	tooMany := make([]string, managedModelAffinityBatchMax+1)
	for index := range tooMany {
		tooMany[index] = digest
	}
	require.ErrorIs(t, cache.BindManagedModelAffinity(ctx, tooMany, binding, time.Hour), errManagedModelAffinityInput)
	_, err := cache.GetManagedModelAffinity(ctx, tooMany)
	require.ErrorIs(t, err, errManagedModelAffinityInput)
	for _, invalid := range []service.ManagedModelAffinityBinding{
		{AccountID: 0, BranchSelector: "branch-a"},
		{AccountID: -1, BranchSelector: "branch-a"},
		{AccountID: 41},
		{AccountID: 41, BranchSelector: strings.Repeat("s", 257)},
		{AccountID: 41, BranchSelector: "branch\nother"},
		{AccountID: 41, BranchSelector: "branch\rother"},
	} {
		require.ErrorIs(t, cache.BindManagedModelAffinity(ctx, []string{digest}, invalid, time.Hour), errManagedModelAffinityInput)
	}
	for _, ttl := range []time.Duration{0, -time.Nanosecond, 24*time.Hour + time.Nanosecond} {
		require.ErrorIs(t, cache.BindManagedModelAffinity(ctx, []string{digest}, binding, ttl), errManagedModelAffinityInput)
	}
	require.Empty(t, server.Keys(), "all validation completes before the batch is sent")
}

func TestManagedModelAffinityCacheCorruptRecordsFailClosed(t *testing.T) {
	for name, corrupt := range map[string]string{
		"empty":           "",
		"no_separator":    "41",
		"plaintext":       "response payload",
		"zero_id":         "0\nbranch-a",
		"negative_id":     "-1\nbranch-a",
		"positive_sign":   "+41\nbranch-a",
		"leading_zero":    "041\nbranch-a",
		"id_overflow":     "9223372036854775808\nbranch-a",
		"empty_selector":  "41\n",
		"long_selector":   "41\n" + strings.Repeat("s", 257),
		"extra_newline":   "41\nbranch-a\nbranch-b",
		"carriage_return": "41\nbranch-a\rbranch-b",
		"false_sentinel":  "!\nbranch-a",
	} {
		t.Run(name, func(t *testing.T) {
			cache, server := newManagedModelAffinityTestCache(t)
			ctx, good, bad := context.Background(), strings.Repeat("a", 64), strings.Repeat("b", 64)
			binding := service.ManagedModelAffinityBinding{AccountID: 41, BranchSelector: "branch-a"}
			require.NoError(t, cache.BindManagedModelAffinity(ctx, []string{good}, binding, time.Hour))
			require.NoError(t, server.Set(managedModelAffinityPrefix+bad, corrupt))
			got, err := cache.GetManagedModelAffinity(ctx, []string{good, bad})
			require.ErrorIs(t, err, errManagedModelAffinityCorrupt)
			require.Nil(t, got, "a corrupt record must not yield a partial successful lookup")

			// New output cannot reinterpret corrupted evidence as a valid pin.
			require.NoError(t, cache.BindManagedModelAffinity(ctx, []string{bad}, binding, time.Hour))
			got, err = cache.GetManagedModelAffinity(ctx, []string{bad})
			require.NoError(t, err)
			require.Equal(t, service.ManagedModelAffinityBinding{Ambiguous: true}, got[bad])
		})
	}
}

func TestManagedModelAffinityCacheWrongRedisTypeDoesNotPartiallyBind(t *testing.T) {
	cache, server := newManagedModelAffinityTestCache(t)
	ctx, first, bad := context.Background(), strings.Repeat("a", 64), strings.Repeat("b", 64)
	binding := service.ManagedModelAffinityBinding{AccountID: 41, BranchSelector: "branch-a"}
	require.NoError(t, cache.rdb.RPush(ctx, managedModelAffinityPrefix+bad, "not-a-string-record").Err())
	require.Error(t, cache.BindManagedModelAffinity(ctx, []string{first, bad}, binding, time.Hour))
	require.False(t, server.Exists(managedModelAffinityPrefix+first), "a later GET failure must happen before any SET")
	typ, err := cache.rdb.Type(ctx, managedModelAffinityPrefix+bad).Result()
	require.NoError(t, err)
	require.Equal(t, "list", typ)
}

func TestManagedModelAffinityCacheUnavailableAndCanceled(t *testing.T) {
	ctx, digest := context.Background(), strings.Repeat("a", 64)
	binding := service.ManagedModelAffinityBinding{AccountID: 41, BranchSelector: "branch-a"}
	for _, cache := range []*gatewayCache{nil, {}} {
		_, err := cache.GetManagedModelAffinity(ctx, []string{digest})
		require.ErrorIs(t, err, errManagedModelAffinityUnavailable)
		require.ErrorIs(t, cache.BindManagedModelAffinity(ctx, []string{digest}, binding, time.Hour), errManagedModelAffinityUnavailable)
	}
	cache, server := newManagedModelAffinityTestCache(t)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err := cache.GetManagedModelAffinity(canceled, []string{digest})
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, cache.BindManagedModelAffinity(canceled, []string{digest}, binding, time.Hour), context.Canceled)
	require.Empty(t, server.Keys())
	got, err := cache.GetManagedModelAffinity(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, got)
	require.NoError(t, cache.BindManagedModelAffinity(ctx, nil, binding, time.Hour))
	require.Empty(t, server.Keys())
}
