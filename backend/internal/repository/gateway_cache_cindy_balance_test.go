//go:build unit

package repository

import (
	"context"
	"testing"

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

	require.NoError(t, cache.MarkCindyBalancePending(ctx, accountID))
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

	require.NoError(t, other.ClearCindyBalancePending(ctx, accountID))
	pendingBatch, err = cache.HasCindyBalancePendingBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.False(t, pendingBatch[accountID])
}
