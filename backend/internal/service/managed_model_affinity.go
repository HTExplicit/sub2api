package service

import (
	"context"
	"time"
)

// ManagedModelAffinityBinding contains no response body, opaque reference,
// credential or user prompt. Its key is already scoped and SHA-256 hashed by
// the HTTP boundary; the existing Redis gateway cache supplies its lifetime.
type ManagedModelAffinityBinding struct {
	AccountID      int64
	BranchSelector string
	Ambiguous      bool
}

// ManagedModelAffinityCache is an optional extension of the existing Redis
// GatewayCache. Test-only lightweight gateway caches need not implement it.
// Bind must atomically retain ambiguity when a digest is observed on another
// account or branch; overwriting an existing pin is not a valid implementation.
type ManagedModelAffinityCache interface {
	GetManagedModelAffinity(ctx context.Context, hashes []string) (map[string]ManagedModelAffinityBinding, error)
	BindManagedModelAffinity(ctx context.Context, hashes []string, binding ManagedModelAffinityBinding, ttl time.Duration) error
}

func (s *GatewayService) ManagedModelAffinityCache() ManagedModelAffinityCache {
	if s == nil {
		return nil
	}
	cache, _ := s.cache.(ManagedModelAffinityCache)
	return cache
}

type ManagedModelLegacyResponseBinding struct {
	AccountID  int64
	UserID     int64
	APIKeyID   int64
	OwnerKnown bool
}

// LookupManagedModelLegacyResponse reads the already-deployed response-owner
// and response/account caches. The caller must still prove a unique retained
// legacy branch; an account binding alone is not an exact upstream target.
func (s *OpenAIGatewayService) LookupManagedModelLegacyResponse(ctx context.Context, groupID int64, responseID string) (*ManagedModelLegacyResponseBinding, error) {
	store := s.getOpenAIWSStateStore()
	if store == nil || responseID == "" {
		return nil, nil
	}
	userID, keyID, ownerKnown, err := store.GetHTTPResponseOwner(ctx, groupID, responseID)
	if err != nil {
		return nil, err
	}
	accountID, err := store.GetResponseAccount(ctx, groupID, responseID)
	if err != nil || accountID <= 0 {
		return nil, err
	}
	return &ManagedModelLegacyResponseBinding{AccountID: accountID, UserID: userID, APIKeyID: keyID, OwnerKnown: ownerKnown}, nil
}
