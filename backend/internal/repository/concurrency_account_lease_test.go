//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestObservedAccountLeaseCacheRefreshRetainsThirtyMinuteRequestAndActiveIndex(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	cache := NewConcurrencyCache(client, 0, 0)
	refresher, ok := cache.(service.AccountSlotRefreshCache)
	require.True(t, ok)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	server.SetTime(now)
	acquired, err := cache.AcquireAccountSlot(ctx, 42, 1, "observed-request")
	require.NoError(t, err)
	require.True(t, acquired)
	for i := 0; i < 60; i++ {
		now = now.Add(30 * time.Second)
		server.FastForward(30 * time.Second)
		server.SetTime(now)
		owned, refreshErr := refresher.RefreshAccountSlot(ctx, 42, "observed-request")
		require.NoError(t, refreshErr)
		require.True(t, owned)
		count, countErr := cache.GetAccountConcurrency(ctx, 42)
		require.NoError(t, countErr)
		require.Equal(t, 1, count)
		acquired, err = cache.AcquireAccountSlot(ctx, 42, 1, "business-must-wait")
		require.NoError(t, err)
		require.False(t, acquired, "the same regular account capacity remains reserved for the entire 30-minute observation")
	}
	score, err := client.ZScore(ctx, accountSlotKey(42), "observed-request").Result()
	require.NoError(t, err)
	require.Equal(t, float64(now.Unix()), score)
	indexExpiry, err := client.ZScore(ctx, accountActiveIndexKey, "42").Result()
	require.NoError(t, err)
	require.Equal(t, float64(now.Unix()+15*60), indexExpiry)
	require.Equal(t, 15*time.Minute, server.TTL(accountSlotKey(42)))
	require.NoError(t, cache.ReleaseAccountSlot(ctx, 42, "observed-request"))
	_, err = client.ZScore(ctx, accountSlotKey(42), "observed-request").Result()
	require.ErrorIs(t, err, redis.Nil)
	_, err = client.ZScore(ctx, accountActiveIndexKey, "42").Result()
	require.ErrorIs(t, err, redis.Nil)
	acquired, err = cache.AcquireAccountSlot(ctx, 42, 1, "business-after-release")
	require.NoError(t, err)
	require.True(t, acquired)
}

func TestObservedAccountLeaseCacheCannotRecreateExpiredOrDeletedOwner(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "deleted", true: "expired"}[expired], func(t *testing.T) {
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr()})
			t.Cleanup(func() { require.NoError(t, client.Close()) })
			cache := NewConcurrencyCache(client, 1, 60)
			refresher := cache.(service.AccountSlotRefreshCache)
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Second)
			server.SetTime(now)
			acquired, err := cache.AcquireAccountSlot(ctx, 42, 1, "lost-owner")
			require.NoError(t, err)
			require.True(t, acquired)
			if expired {
				now = now.Add(time.Minute)
				server.FastForward(time.Minute)
				server.SetTime(now)
			} else {
				require.NoError(t, client.ZRem(ctx, accountSlotKey(42), "lost-owner").Err())
			}
			acquired, err = cache.AcquireAccountSlot(ctx, 42, 1, "new-business-owner")
			require.NoError(t, err)
			require.True(t, acquired)
			owned, err := refresher.RefreshAccountSlot(ctx, 42, "lost-owner")
			require.NoError(t, err)
			require.False(t, owned, "refresh must not silently reacquire beyond the account's business limit")
			_, err = client.ZScore(ctx, accountSlotKey(42), "lost-owner").Result()
			require.ErrorIs(t, err, redis.Nil)
			count, err := cache.GetAccountConcurrency(ctx, 42)
			require.NoError(t, err)
			require.Equal(t, 1, count)
			_, err = client.ZScore(ctx, accountSlotKey(42), "new-business-owner").Result()
			require.NoError(t, err)
		})
	}
}
