//go:build reasoning_fidelity && reasoning_recovery_diagnostic

package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// This adapter intentionally never constructs a listener, Redis/SQL client,
// scheduler or billing writer. The service exercises the production cache
// interface while only this diagnostic process owns the payloads.
func ReasoningRecoveryDiagnosticAttach(s *OpenAIGatewayService) {
	s.cache = &reasoningRecoveryDiagnosticStore{rejected: make(map[string]OpenAIRejectedReasoning)}
}

func ReasoningRecoveryDiagnosticTenant(c *gin.Context, group *Group) {
	c.Set("api_key", &APIKey{ID: 920000015523, UserID: 920000015522, GroupID: &group.ID, Group: group})
}

// Share the exact production structured-rejection contract; the diagnostic
// must not grant a recovery merely because an error string mentions a code.
func ReasoningRecoveryDiagnosticRejectionCode(payload []byte) string {
	rejection, ok := parseOpenAIReasoningRejection(payload)
	if !ok {
		return ""
	}
	return rejection.code
}

type reasoningRecoveryDiagnosticStore struct {
	mu       sync.Mutex
	rejected map[string]OpenAIRejectedReasoning
}

var _ GatewayCache = (*reasoningRecoveryDiagnosticStore)(nil)
var _ OpenAIReasoningStateStore = (*reasoningRecoveryDiagnosticStore)(nil)

func (*reasoningRecoveryDiagnosticStore) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, ErrStickySessionNotFound
}
func (*reasoningRecoveryDiagnosticStore) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}
func (*reasoningRecoveryDiagnosticStore) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}
func (*reasoningRecoveryDiagnosticStore) DeleteSessionAccountID(context.Context, int64, string) error {
	return nil
}
func (*reasoningRecoveryDiagnosticStore) DeleteSessionAccountIDIfMatches(context.Context, int64, string, int64) (bool, error) {
	return false, nil
}
func (*reasoningRecoveryDiagnosticStore) SetGrokVideoPendingBilling(context.Context, string, []byte, time.Duration) error {
	return errors.New("diagnostic_path_unavailable")
}
func (*reasoningRecoveryDiagnosticStore) GetGrokVideoPendingBilling(context.Context, string) ([]byte, error) {
	return nil, nil
}
func (*reasoningRecoveryDiagnosticStore) ClaimGrokVideoBilled(context.Context, string, time.Duration) (bool, error) {
	return false, nil
}
func (*reasoningRecoveryDiagnosticStore) ReleaseGrokVideoBilled(context.Context, string) error {
	return nil
}
func (*reasoningRecoveryDiagnosticStore) SetReasoningContent(context.Context, string, string, time.Duration) error {
	return nil
}
func (*reasoningRecoveryDiagnosticStore) GetReasoningContent(context.Context, string) (string, error) {
	return "", ErrReasoningContentNotFound
}

func recoveryDiagnosticCacheKey(scope OpenAIReasoningCacheScope, key string) (string, error) {
	if !IsOpenAIReasoningCacheDigest(scope.ScopeHash) || !IsOpenAIReasoningCacheDigest(scope.TenantHash) || !IsOpenAIReasoningCacheDigest(key) {
		return "", ErrOpenAIReasoningCacheInput
	}
	return scope.ScopeHash + ":" + scope.TenantHash + ":" + key, nil
}
func (s *reasoningRecoveryDiagnosticStore) GetOpenAIRejectedReasoning(ctx context.Context, scope OpenAIReasoningCacheScope, hashes []string) (map[string]OpenAIRejectedReasoning, error) {
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
		id, err := recoveryDiagnosticCacheKey(scope, key)
		if err != nil {
			return nil, err
		}
		if entry, ok := s.rejected[id]; ok && time.Now().Before(entry.ExpiresAt) {
			out[key] = entry
		}
	}
	return out, nil
}
func (s *reasoningRecoveryDiagnosticStore) PutOpenAIRejectedReasoning(ctx context.Context, scope OpenAIReasoningCacheScope, hashes []string) error {
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
		id, err := recoveryDiagnosticCacheKey(scope, key)
		if err != nil {
			return err
		}
		s.rejected[id] = OpenAIRejectedReasoning{RejectedAt: now, ExpiresAt: now.Add(OpenAIReasoningStateTTL)}
	}
	return nil
}
