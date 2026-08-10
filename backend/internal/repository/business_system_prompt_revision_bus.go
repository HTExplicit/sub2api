package repository

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const businessSystemPromptRevisionChannel = "business_system_prompt:revision"

type businessSystemPromptRevisionBus struct {
	rdb *redis.Client
}

func NewBusinessSystemPromptRevisionBus(rdb *redis.Client) service.BusinessSystemPromptRevisionBus {
	return &businessSystemPromptRevisionBus{rdb: rdb}
}

func (b *businessSystemPromptRevisionBus) Publish(ctx context.Context, revision int64) error {
	if b == nil || b.rdb == nil {
		return errors.New("business system prompt redis unavailable")
	}
	return b.rdb.Publish(ctx, businessSystemPromptRevisionChannel, strconv.FormatInt(revision, 10)).Err()
}

func (b *businessSystemPromptRevisionBus) Subscribe(ctx context.Context, handler func(int64)) error {
	if b == nil || b.rdb == nil {
		return errors.New("business system prompt redis unavailable")
	}
	pubsub := b.rdb.Subscribe(ctx, businessSystemPromptRevisionChannel)
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return fmt.Errorf("subscribe business system prompt revisions: %w", err)
	}
	// A zero revision is a local reconnect signal. The service callback reloads
	// from PostgreSQL; no prompt content is ever published through Redis.
	if handler != nil {
		handler(0)
	}
	defer func() {
		if err := pubsub.Close(); err != nil {
			log.Printf("business system prompt revision pubsub close failed: %v", err)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case message, ok := <-pubsub.Channel():
			if !ok {
				return errors.New("business system prompt revision pubsub channel closed")
			}
			if message == nil || handler == nil {
				continue
			}
			revision, err := strconv.ParseInt(message.Payload, 10, 64)
			if err != nil || revision < 1 {
				continue
			}
			handler(revision)
		}
	}
}
