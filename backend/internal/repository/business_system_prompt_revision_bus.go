package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	businessSystemPromptRevisionChannel        = "business_system_prompt:revision"
	businessSystemPromptResponseRouteKeyPrefix = "business_system_prompt:response_route:"
)

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

func (b *businessSystemPromptRevisionBus) StoreBusinessSystemPromptRouteMetadata(
	ctx context.Context,
	responseID string,
	metadata service.BusinessSystemPromptBundleMetadata,
	ttl time.Duration,
) error {
	if b == nil || b.rdb == nil {
		return errors.New("business system prompt redis unavailable")
	}
	if strings.TrimSpace(responseID) == "" {
		return nil
	}
	if err := service.ValidateBusinessSystemPromptBundleMetadata(metadata); err != nil {
		return err
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal business system prompt route metadata: %w", err)
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	return b.rdb.Set(ctx, businessSystemPromptResponseRouteKey(responseID), payload, ttl).Err()
}

func (b *businessSystemPromptRevisionBus) LoadBusinessSystemPromptRouteMetadata(
	ctx context.Context,
	responseID string,
) (service.BusinessSystemPromptBundleMetadata, bool, error) {
	if b == nil || b.rdb == nil {
		return service.BusinessSystemPromptBundleMetadata{}, false, errors.New("business system prompt redis unavailable")
	}
	if strings.TrimSpace(responseID) == "" {
		return service.BusinessSystemPromptBundleMetadata{}, false, nil
	}
	payload, err := b.rdb.Get(ctx, businessSystemPromptResponseRouteKey(responseID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return service.BusinessSystemPromptBundleMetadata{}, false, nil
	}
	if err != nil {
		return service.BusinessSystemPromptBundleMetadata{}, false, err
	}
	var metadata service.BusinessSystemPromptBundleMetadata
	if err := json.Unmarshal(payload, &metadata); err != nil {
		return service.BusinessSystemPromptBundleMetadata{}, false, fmt.Errorf("decode business system prompt route metadata: %w", err)
	}
	if err := service.ValidateBusinessSystemPromptBundleMetadata(metadata); err != nil {
		return service.BusinessSystemPromptBundleMetadata{}, false, err
	}
	return metadata, true, nil
}

func businessSystemPromptResponseRouteKey(responseID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(responseID)))
	return businessSystemPromptResponseRouteKeyPrefix + hex.EncodeToString(digest[:])
}
