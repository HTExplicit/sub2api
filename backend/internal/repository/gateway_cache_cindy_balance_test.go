//go:build unit

package repository

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayCacheCindyBalancePendingIsDurableUntilExplicitClear(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &gatewayCache{rdb: client}
	ctx := context.Background()
	const accountID int64 = 77101
	fingerprint, err := service.CindyCredentialsFingerprint(map[string]any{"api_key": "fixture"})
	require.NoError(t, err)

	require.NoError(t, cache.MarkCindyBalancePending(ctx, accountID, fingerprint))
	pendingBatch, err := cache.HasCindyBalancePendingBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.True(t, pendingBatch[accountID])
	ttl, err := client.TTL(ctx, cindyBalancePendingKey(accountID)).Result()
	require.NoError(t, err)
	require.Equal(t, int64(-1), int64(ttl), "pending marker must not have a TTL")

	// A second cache instance models another service/process sharing Redis.
	other := &gatewayCache{rdb: redis.NewClient(&redis.Options{Addr: server.Addr()})}
	t.Cleanup(func() { _ = other.rdb.Close() })
	pendingBatch, err = other.HasCindyBalancePendingBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.True(t, pendingBatch[accountID])
	storedFingerprint, err := other.GetCindyBalancePendingFingerprint(ctx, accountID)
	require.NoError(t, err)
	require.Equal(t, fingerprint, storedFingerprint)

	require.NoError(t, other.ClearCindyBalancePending(ctx, accountID))
	pendingBatch, err = cache.HasCindyBalancePendingBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.False(t, pendingBatch[accountID])
}

func TestGatewayCacheCindyBalancePendingIgnoresLegacyNamespace(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &gatewayCache{rdb: client}
	ctx := context.Background()
	const accountID int64 = 77102
	legacyKey := fmt.Sprintf("cindy_balance_pending:%d", accountID)
	require.Equal(t, fmt.Sprintf("cindy_balance_pending:v2:%d", accountID), cindyBalancePendingKey(accountID))
	fingerprint, err := service.CindyCredentialsFingerprint(map[string]any{"api_key": "fixture"})
	require.NoError(t, err)

	require.NoError(t, client.Set(ctx, legacyKey, "1", 0).Err())
	pendingBatch, err := cache.HasCindyBalancePendingBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.False(t, pendingBatch[accountID], "legacy single-signal marker must not block scheduling")

	require.NoError(t, cache.MarkCindyBalancePending(ctx, accountID, fingerprint))
	pendingBatch, err = cache.HasCindyBalancePendingBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.True(t, pendingBatch[accountID])
	require.True(t, server.Exists(cindyBalancePendingKey(accountID)))

	require.NoError(t, cache.ClearCindyBalancePending(ctx, accountID))
	require.True(t, server.Exists(legacyKey), "v2 cleanup must not mutate legacy Redis evidence")
}

func TestGatewayCacheCindyBalancePendingCompareDeletePreservesNewerGeneration(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &gatewayCache{rdb: client}
	ctx := context.Background()
	const accountID int64 = 77103
	oldFingerprint, err := service.CindyCredentialsFingerprint(map[string]any{"api_key": "old"})
	require.NoError(t, err)
	newFingerprint, err := service.CindyCredentialsFingerprint(map[string]any{"api_key": "new"})
	require.NoError(t, err)

	require.NoError(t, cache.MarkCindyBalancePending(ctx, accountID, newFingerprint))
	require.NoError(t, cache.ClearCindyBalancePendingIfFingerprintMatches(ctx, accountID, oldFingerprint))
	stored, err := cache.GetCindyBalancePendingFingerprint(ctx, accountID)
	require.NoError(t, err)
	require.Equal(t, newFingerprint, stored)

	require.NoError(t, cache.ClearCindyBalancePendingIfFingerprintMatches(ctx, accountID, newFingerprint))
	stored, err = cache.GetCindyBalancePendingFingerprint(ctx, accountID)
	require.NoError(t, err)
	require.Empty(t, stored)
}
