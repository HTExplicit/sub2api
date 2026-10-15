package repository

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func loginProtectionTestStore(t *testing.T) (*miniredis.Miniredis, service.LoginProtectionStore) {
	t.Helper()
	server := miniredis.RunT(t)
	server.SetTime(time.Now().UTC())
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	return server, NewLoginProtectionStore(client)
}

func TestLoginProtectionSlidingWindowBanExpiryAndUnban(t *testing.T) {
	ctx := context.Background()
	server, store := loginProtectionTestStore(t)
	settings := service.DefaultLoginProtectionSettings()
	const ip = "203.0.113.9"
	start := time.Now().UTC().Truncate(time.Second)
	server.SetTime(start)
	for range 5 {
		ban, created, err := store.RecordFailure(ctx, ip, settings)
		require.NoError(t, err)
		require.Nil(t, ban)
		require.False(t, created)
	}
	// Exactly five minutes old falls outside the sliding window, even though
	// repeated failures refreshed the Redis key TTL.
	server.SetTime(start.Add(5 * time.Minute))
	ban, _, err := store.RecordFailure(ctx, ip, settings)
	require.NoError(t, err)
	require.Nil(t, ban)
	for range 4 {
		ban, _, err = store.RecordFailure(ctx, ip, settings)
		require.NoError(t, err)
		require.Nil(t, ban)
	}
	ban, created, err := store.RecordFailure(ctx, ip, settings)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, 24*time.Hour, ban.ExpiresAt.Sub(ban.BlockedAt))
	expiry := ban.ExpiresAt
	server.FastForward(time.Hour)
	server.SetTime(start.Add(65 * time.Minute))
	ban, created, err = store.RecordFailure(ctx, ip, settings)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, expiry, ban.ExpiresAt)
	items, total, err := store.List(ctx, 1, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, ip, items[0].IP)
	// Another application instance reads the same durable Redis ban.
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	restarted := NewLoginProtectionStore(client)
	shared, err := restarted.Check(ctx, ip)
	require.NoError(t, err)
	require.Equal(t, expiry, shared.ExpiresAt)
	require.NoError(t, restarted.Unban(ctx, ip))
	require.NoError(t, restarted.Unban(ctx, ip))
	for range 5 {
		ban, _, err = store.RecordFailure(ctx, ip, settings)
		require.NoError(t, err)
		require.Nil(t, ban)
	}
	ban, _, err = store.RecordFailure(ctx, ip, settings)
	require.NoError(t, err)
	require.NotNil(t, ban)
	server.FastForward(24 * time.Hour)
	server.SetTime(start.Add(25*time.Hour + 5*time.Minute))
	ban, err = store.Check(ctx, ip)
	require.NoError(t, err)
	require.Nil(t, ban)
	items, total, err = store.List(ctx, 1, 20)
	require.NoError(t, err)
	require.Empty(t, items)
	require.Zero(t, total)
}

func TestLoginProtectionConcurrentFailureCreatesOneBanAndPagination(t *testing.T) {
	ctx := context.Background()
	_, store := loginProtectionTestStore(t)
	settings := service.DefaultLoginProtectionSettings()
	var created atomic.Int32
	var failures atomic.Int32
	var group sync.WaitGroup
	for range 24 {
		group.Go(func() {
			_, fresh, err := store.RecordFailure(ctx, "2001:db8::1", settings)
			if err != nil {
				failures.Add(1)
			}
			if fresh {
				created.Add(1)
			}
		})
	}
	group.Wait()
	require.Zero(t, failures.Load())
	require.EqualValues(t, 1, created.Load())
	for range 6 {
		_, _, err := store.RecordFailure(ctx, "203.0.113.8", settings)
		require.NoError(t, err)
	}
	items, total, err := store.List(ctx, 2, 1)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, items, 1)
	ban, err := store.Check(ctx, "203.0.113.7")
	require.NoError(t, err)
	require.Nil(t, ban)
}
