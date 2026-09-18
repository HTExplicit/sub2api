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

func TestOpenAIAPIKeyHealthCacheTripsWithinRollingWindow(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store, ok := NewTempUnschedCache(client).(service.OpenAIAPIKeyHealthCache)
	require.True(t, ok)

	ctx := context.Background()
	for attempt := 1; attempt <= 3; attempt++ {
		count, tripped, err := store.RecordOpenAIAPIKeyHealthFailure(ctx, 42, 1, 3)
		require.NoError(t, err)
		require.EqualValues(t, attempt, count)
		require.Equal(t, attempt == 3, tripped)
	}
}

func TestOpenAIAPIKeyHealthCacheDropsFailuresOutsideRollingWindow(t *testing.T) {
	server := miniredis.RunT(t)
	now := time.Unix(1_700_000_000, 0)
	server.SetTime(now)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store, ok := NewTempUnschedCache(client).(service.OpenAIAPIKeyHealthCache)
	require.True(t, ok)

	ctx := context.Background()
	count, tripped, err := store.RecordOpenAIAPIKeyHealthFailure(ctx, 42, 1, 3)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	require.False(t, tripped)

	server.SetTime(now.Add(61 * time.Second))
	count, tripped, err = store.RecordOpenAIAPIKeyHealthFailure(ctx, 42, 1, 3)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	require.False(t, tripped)
}

func TestTempUnschedCacheDeleteIfUntilOnlyRemovesMatchingPause(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewTempUnschedCache(client)
	deleter, ok := cache.(service.TempUnschedGuardedCache)
	require.True(t, ok)

	ctx := context.Background()
	until := time.Now().Add(10 * time.Minute).Unix()
	require.NoError(t, cache.SetTempUnsched(ctx, 42, &service.TempUnschedState{UntilUnix: until}))
	_, original, err := deleter.GetTempUnschedSnapshot(ctx, 42)
	require.NoError(t, err)
	require.NotEmpty(t, original)

	// The key can disappear and be recreated with the same deadline but a
	// different payload; a delayed clear must preserve the replacement.
	require.NoError(t, cache.DeleteTempUnsched(ctx, 42))
	require.NoError(t, cache.SetTempUnsched(ctx, 42, &service.TempUnschedState{UntilUnix: until, ErrorMessage: "replacement"}))
	deleted, err := deleter.DeleteTempUnschedIfMatch(ctx, 42, original)
	require.NoError(t, err)
	require.False(t, deleted)
	require.True(t, server.Exists("temp_unsched:account:42"))

	// Matching the current payload deletes it.
	_, current, err := deleter.GetTempUnschedSnapshot(ctx, 42)
	require.NoError(t, err)
	deleted, err = deleter.DeleteTempUnschedIfMatch(ctx, 42, current)
	require.NoError(t, err)
	require.True(t, deleted)
	require.False(t, server.Exists("temp_unsched:account:42"))
}
