package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const remoteSkillRegistryRevisionChannel = "business_system_prompt:remote_skill_revision"

type remoteSkillRegistryRevisionBus struct {
	rdb *redis.Client
}

type remoteSkillRevisionEvent struct {
	Revision       int64  `json:"revision"`
	ManifestSHA256 string `json:"manifest_sha256"`
}

func NewRemoteSkillRegistryRevisionBus(rdb *redis.Client) service.RemoteSkillRegistryRevisionBus {
	return &remoteSkillRegistryRevisionBus{rdb: rdb}
}

func (b *remoteSkillRegistryRevisionBus) Publish(ctx context.Context, revision int64, manifestSHA256 string) error {
	manifestSHA256 = strings.ToLower(strings.TrimSpace(manifestSHA256))
	if b == nil || b.rdb == nil {
		return errors.New("remote skill registry redis unavailable")
	}
	if revision < 1 || len(manifestSHA256) != 64 || !isLowerHexRepositoryDigest(manifestSHA256) {
		return service.ErrBusinessSystemPromptInvalid
	}
	payload, err := json.Marshal(remoteSkillRevisionEvent{Revision: revision, ManifestSHA256: manifestSHA256})
	if err != nil {
		return err
	}
	return b.rdb.Publish(ctx, remoteSkillRegistryRevisionChannel, payload).Err()
}

func (b *remoteSkillRegistryRevisionBus) Subscribe(ctx context.Context, handler func(int64, string)) error {
	if b == nil || b.rdb == nil {
		return errors.New("remote skill registry redis unavailable")
	}
	pubsub := b.rdb.Subscribe(ctx, remoteSkillRegistryRevisionChannel)
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return fmt.Errorf("subscribe remote skill revisions: %w", err)
	}
	defer func() { _ = pubsub.Close() }()
	if handler != nil {
		handler(0, "")
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case message, ok := <-pubsub.Channel():
			if !ok {
				return errors.New("remote skill revision channel closed")
			}
			if message == nil || handler == nil {
				continue
			}
			var event remoteSkillRevisionEvent
			if json.Unmarshal([]byte(message.Payload), &event) != nil || event.Revision < 1 ||
				len(event.ManifestSHA256) != 64 || !isLowerHexRepositoryDigest(event.ManifestSHA256) {
				continue
			}
			handler(event.Revision, event.ManifestSHA256)
		}
	}
}

func isLowerHexRepositoryDigest(value string) bool {
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return false
	}
	return true
}
