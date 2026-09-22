package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestQuotaActivityClockDetectsExpiredWorkAndRedisEpochChanges(t *testing.T) {
	server := miniredis.RunT(t)
	now := time.Now().UTC()
	server.SetTime(now)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewQuotaActivityStore(client)
	ctx := context.Background()
	require.NoError(t, store.Begin(ctx, 7, "request-a"))
	start, err := store.Read(ctx, 7)
	require.NoError(t, err)
	require.EqualValues(t, 1, start.Active)
	require.NotEmpty(t, start.Epoch)
	require.NoError(t, store.Finish(ctx, 7, "request-a", true))
	end, err := store.Read(ctx, 7)
	require.NoError(t, err)
	require.Zero(t, end.Active)
	require.Zero(t, end.Gaps)
	require.Greater(t, end.Revision, start.Revision)
	require.NoError(t, store.Begin(ctx, 7, "abandoned"))
	server.SetTime(now.Add(91 * time.Second))
	expired, err := store.Read(ctx, 7)
	require.NoError(t, err)
	require.Zero(t, expired.Active)
	require.EqualValues(t, 1, expired.Gaps)
	require.Equal(t, start.Epoch, expired.Epoch)
	server.FlushAll() // This is the isolated in-memory test Redis only.
	fresh, err := store.Read(ctx, 7)
	require.NoError(t, err)
	require.NotEqual(t, start.Epoch, fresh.Epoch, "lost observation state must not reuse a persisted calibration")
}
