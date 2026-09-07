//go:build reasoning_fidelity && reasoning_replay_diagnostic

package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// This adapter intentionally never constructs a listener, Redis/SQL client,
// scheduler or billing writer. The service exercises the production cache
// interface while only this diagnostic process owns the payloads.
func ReasoningReplayDiagnosticAttach(s *OpenAIGatewayService) {
	s.cache = &reasoningReplayDiagnosticStore{
		batches:  make(map[string]reasoningReplayDiagnosticBatch),
		rejected: make(map[string]OpenAIRejectedReasoning),
	}
}

func ReasoningReplayDiagnosticTenant(c *gin.Context, group *Group) {
	c.Set("api_key", &APIKey{ID: 920000015523, UserID: 920000015522, GroupID: &group.ID, Group: group})
}

// Share the exact production structured-rejection contract; the diagnostic
// must not grant a recovery merely because an error string mentions a code.
func ReasoningReplayDiagnosticRejectionCode(payload []byte) string {
	rejection, ok := parseOpenAIReasoningRejection(payload)
	if !ok {
		return ""
	}
	return rejection.code
}

func ReasoningReplayDiagnosticObserve(c *gin.Context) (hits, stored int) {
	number := func(key string) int {
		value, _ := c.Get(key)
		switch v := value.(type) {
		case int:
			return v
		case int64:
			return int(v)
		case bool:
			if v {
				return 1
			}
		}
		return 0
	}
	return number("openai_chat_reasoning_replay_hits"), number("openai_chat_reasoning_replay_stored")
}

type reasoningReplayDiagnosticBatch struct {
	value   OpenAIReasoningBatch
	expires time.Time
}
type reasoningReplayDiagnosticStore struct {
	mu       sync.Mutex
	batches  map[string]reasoningReplayDiagnosticBatch
	rejected map[string]OpenAIRejectedReasoning
}

var _ GatewayCache = (*reasoningReplayDiagnosticStore)(nil)
var _ OpenAIReasoningStateStore = (*reasoningReplayDiagnosticStore)(nil)

func (*reasoningReplayDiagnosticStore) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, ErrStickySessionNotFound
}
func (*reasoningReplayDiagnosticStore) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}
func (*reasoningReplayDiagnosticStore) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}
func (*reasoningReplayDiagnosticStore) DeleteSessionAccountID(context.Context, int64, string) error {
	return nil
}
func (*reasoningReplayDiagnosticStore) DeleteSessionAccountIDIfMatches(context.Context, int64, string, int64) (bool, error) {
	return false, nil
}
func (*reasoningReplayDiagnosticStore) SetGrokVideoPendingBilling(context.Context, string, []byte, time.Duration) error {
	return errors.New("diagnostic_path_unavailable")
}
func (*reasoningReplayDiagnosticStore) GetGrokVideoPendingBilling(context.Context, string) ([]byte, error) {
	return nil, nil
}
func (*reasoningReplayDiagnosticStore) ClaimGrokVideoBilled(context.Context, string, time.Duration) (bool, error) {
	return false, nil
}
func (*reasoningReplayDiagnosticStore) ReleaseGrokVideoBilled(context.Context, string) error {
	return nil
}
func (*reasoningReplayDiagnosticStore) SetReasoningContent(context.Context, string, string, time.Duration) error {
	return nil
}
func (*reasoningReplayDiagnosticStore) GetReasoningContent(context.Context, string) (string, error) {
	return "", ErrReasoningContentNotFound
}

func replayDiagnosticCacheKey(scope OpenAIReasoningCacheScope, key string) (string, error) {
	if !IsOpenAIReasoningCacheDigest(scope.ScopeHash) || !IsOpenAIReasoningCacheDigest(scope.TenantHash) || !IsOpenAIReasoningCacheDigest(key) {
		return "", ErrOpenAIReasoningCacheInput
	}
	return scope.ScopeHash + ":" + scope.TenantHash + ":" + key, nil
}
func cloneReplayDiagnosticBatch(batch OpenAIReasoningBatch) OpenAIReasoningBatch {
	raw, _ := json.Marshal(batch)
	var clone OpenAIReasoningBatch
	_ = json.Unmarshal(raw, &clone)
	clone.PayloadHash = batch.PayloadHash
	return clone
}
func (s *reasoningReplayDiagnosticStore) GetOpenAIReasoningBatches(ctx context.Context, scope OpenAIReasoningCacheScope, keys []string) (map[string]OpenAIReasoningBatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(keys) > OpenAIReasoningStateMaxLookupEntries {
		return nil, ErrOpenAIReasoningCacheInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]OpenAIReasoningBatch)
	for _, key := range keys {
		id, err := replayDiagnosticCacheKey(scope, key)
		if err != nil {
			return nil, err
		}
		if value, ok := s.batches[id]; ok && time.Now().Before(value.expires) {
			out[key] = cloneReplayDiagnosticBatch(value.value)
		}
	}
	return out, nil
}
func (s *reasoningReplayDiagnosticStore) PutOpenAIReasoningBatch(ctx context.Context, scope OpenAIReasoningCacheScope, key string, batch OpenAIReasoningBatch) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	id, err := replayDiagnosticCacheKey(scope, key)
	if err != nil {
		return false, err
	}
	raw, err := json.Marshal(batch)
	if err != nil || len(raw) > OpenAIReasoningBatchMaxBytes || len(batch.Output) > OpenAIReasoningBatchMaxItems {
		return false, ErrOpenAIReasoningCacheInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A stricter study-only 4 MiB/16-batch cap is sufficient for its four
	// function-tool first turns. Redis limits are tested separately offline.
	if len(s.batches) >= 16 {
		return false, nil
	}
	batch.PayloadHash = reasoningStateDigest(raw)
	s.batches[id] = reasoningReplayDiagnosticBatch{cloneReplayDiagnosticBatch(batch), time.Now().Add(OpenAIReasoningStateTTL)}
	return true, nil
}
func (s *reasoningReplayDiagnosticStore) DeleteOpenAIReasoningBatchIfMatch(ctx context.Context, scope OpenAIReasoningCacheScope, key, payloadHash string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	id, err := replayDiagnosticCacheKey(scope, key)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.batches[id]; ok && entry.value.PayloadHash == payloadHash {
		delete(s.batches, id)
		return true, nil
	}
	return false, nil
}
func (s *reasoningReplayDiagnosticStore) GetOpenAIRejectedReasoning(ctx context.Context, scope OpenAIReasoningCacheScope, hashes []string) (map[string]OpenAIRejectedReasoning, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(hashes) > OpenAIReasoningStateMaxLookupEntries {
		return nil, ErrOpenAIReasoningCacheInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]OpenAIRejectedReasoning)
	for _, key := range hashes {
		id, err := replayDiagnosticCacheKey(scope, key)
		if err != nil {
			return nil, err
		}
		if entry, ok := s.rejected[id]; ok && time.Now().Before(entry.ExpiresAt) {
			out[key] = entry
		}
	}
	return out, nil
}
func (s *reasoningReplayDiagnosticStore) PutOpenAIRejectedReasoning(ctx context.Context, scope OpenAIReasoningCacheScope, hashes []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(hashes) > OpenAIReasoningStateMaxLookupEntries {
		return ErrOpenAIReasoningCacheInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.rejected)+len(hashes) > 64 {
		return errors.New("diagnostic_cache_capacity")
	}
	now := time.Now()
	for _, key := range hashes {
		id, err := replayDiagnosticCacheKey(scope, key)
		if err != nil {
			return err
		}
		s.rejected[id] = OpenAIRejectedReasoning{RejectedAt: now, ExpiresAt: now.Add(OpenAIReasoningStateTTL)}
	}
	return nil
}
