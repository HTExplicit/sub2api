package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

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
