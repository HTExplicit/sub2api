package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestBusinessSystemPromptRevisionBusStoresBodyFreeResponseRouteMetadata(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = client.Close() }()
	bus := NewBusinessSystemPromptRevisionBus(client)
	store, ok := bus.(service.BusinessSystemPromptRouteMetadataStore)
	require.True(t, ok)

	metadata := service.BusinessSystemPromptBundleMetadata{
		BundleID: "bundle", ManifestSHA256: strings.Repeat("a", 64),
		BaseSHA256: strings.Repeat("b", 64), EffectiveSHA256: strings.Repeat("c", 64),
		ByteLength: 123,
		RouteIDs:   []string{"api-security"}, DocumentPaths: []string{"skills/api-security/SKILL.md"},
	}
	require.NoError(t, store.StoreBusinessSystemPromptRouteMetadata(context.Background(), "resp_secret_identifier", metadata, time.Hour))
	loaded, found, err := store.LoadBusinessSystemPromptRouteMetadata(context.Background(), "resp_secret_identifier")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, metadata, loaded)

	for _, key := range server.Keys() {
		value, err := server.Get(key)
		require.NoError(t, err)
		require.NotContains(t, value, "server prompt body")
		require.NotContains(t, key, "resp_secret_identifier", "raw response IDs must not appear in Redis keys")
	}
	server.FastForward(time.Hour + time.Second)
	_, found, err = store.LoadBusinessSystemPromptRouteMetadata(context.Background(), "resp_secret_identifier")
	require.NoError(t, err)
	require.False(t, found)
}

func TestBusinessSystemPromptRevisionBusPublishesOnlyRevision(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = client.Close() }()
	bus := NewBusinessSystemPromptRevisionBus(client)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	revisions := make(chan int64, 2)
	done := make(chan error, 1)
	go func() {
		done <- bus.Subscribe(ctx, func(revision int64) { revisions <- revision })
	}()

	select {
	case revision := <-revisions:
		require.Zero(t, revision, "subscription handshake must trigger a database reload")
	case <-time.After(time.Second):
		t.Fatal("revision subscription did not become ready")
	}

	require.NoError(t, bus.Publish(context.Background(), 42))
	select {
	case revision := <-revisions:
		require.Equal(t, int64(42), revision)
	case <-time.After(time.Second):
		t.Fatal("published revision was not delivered")
	}

	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("revision subscription did not stop")
	}
}
