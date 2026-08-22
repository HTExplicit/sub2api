package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

type materializedCindyClassifierStub struct {
	strict bool
	err    error
	calls  atomic.Int64
}

type legacyCindyGroupReaderStub struct {
	accounts []Account
}

func (s legacyCindyGroupReaderStub) ListCindyGroupIdentityMembers(context.Context, int64) ([]Account, error) {
	return s.accounts, nil
}

func (legacyCindyGroupReaderStub) CindyGroupIdentityReaderMarker() {}

func (s *materializedCindyClassifierStub) ClassifyStrictCindyGroup(context.Context, int64) (bool, error) {
	s.calls.Add(1)
	return s.strict, s.err
}

func TestClassifyAuthenticatedStrictCindyGroupUsesMaterializedIdentity(t *testing.T) {
	t.Parallel()

	transient := errors.New("classifier unavailable")
	tests := []struct {
		name       string
		group      *Group
		classifier *materializedCindyClassifierStub
		wantStrict bool
		wantErr    error
		wantCalls  int64
	}{
		{
			name:       "known ordinary snapshot does not query classifier",
			group:      &Group{ID: 1, Platform: PlatformOpenAI, StrictCindyKnown: true},
			classifier: &materializedCindyClassifierStub{err: transient},
		},
		{
			name:       "known Cindy snapshot does not query classifier",
			group:      &Group{ID: 2, Platform: PlatformOpenAI, StrictCindyKnown: true, StrictCindy: true},
			classifier: &materializedCindyClassifierStub{err: transient},
			wantStrict: true,
		},
		{
			name:       "legacy unknown snapshot explicitly falls back",
			group:      &Group{ID: 3, Platform: PlatformOpenAI},
			classifier: &materializedCindyClassifierStub{strict: true},
			wantStrict: true,
			wantCalls:  1,
		},
		{
			name:       "legacy fallback failure remains fail closed",
			group:      &Group{ID: 4, Platform: PlatformOpenAI},
			classifier: &materializedCindyClassifierStub{err: transient},
			wantErr:    transient,
			wantCalls:  1,
		},
		{
			name:       "non OpenAI group rejects a stale Cindy marker",
			group:      &Group{ID: 5, Platform: PlatformGemini, StrictCindyKnown: true, StrictCindy: true},
			classifier: &materializedCindyClassifierStub{strict: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			strict, err := classifyAuthenticatedStrictCindyGroup(context.Background(), test.classifier, test.group)
			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, test.wantStrict, strict)
			require.Equal(t, test.wantCalls, test.classifier.calls.Load())
		})
	}
}

func TestLegacyLaxaOpenAIGroupCompatibilityKeepsMixedGroupOrdinary(t *testing.T) {
	t.Parallel()
	legacyLaxa := Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: cindyCredentials(),
	}
	ordinary := Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://api.openai.com",
			"api_key":  "ordinary-key",
		},
	}
	group := &Group{ID: 71, Platform: PlatformOpenAI}

	pure, err := classifyAuthenticatedCindyIdentityGroup(context.Background(), legacyCindyGroupReaderStub{
		accounts: []Account{legacyLaxa},
	}, group)
	require.NoError(t, err)
	require.True(t, pure, "pure legacy Laxa groups retain the temporary compatibility classification")

	mixed, err := classifyAuthenticatedCindyIdentityGroup(context.Background(), legacyCindyGroupReaderStub{
		accounts: []Account{legacyLaxa, ordinary},
	}, group)
	require.NoError(t, err)
	require.False(t, mixed, "mixed OpenAI groups must stay on generic OpenAI continuation and scheduling")
}

func TestAPIKeyAuthSnapshotRoundTripsMaterializedCindyIdentity(t *testing.T) {
	t.Parallel()
	groupID := int64(50)
	svc := &APIKeyService{}
	apiKey := &APIKey{
		ID: 1, UserID: 2, GroupID: &groupID, Status: StatusActive,
		User: &User{ID: 2, Status: StatusActive},
		Group: &Group{
			ID: groupID, Name: "cindy", Platform: PlatformOpenAI, Status: StatusActive,
			Hydrated: true, StrictCindyKnown: true, StrictCindy: true,
		},
	}

	snapshot := svc.snapshotFromAPIKey(context.Background(), apiKey)
	require.NotNil(t, snapshot)
	require.Equal(t, apiKeyAuthSnapshotVersion, snapshot.Version)
	require.NotNil(t, snapshot.Group)
	require.True(t, snapshot.Group.StrictCindyKnown)
	require.True(t, snapshot.Group.StrictCindy)

	materialized, used, err := svc.applyAuthCacheEntry("sk-cindy", &APIKeyAuthCacheEntry{Snapshot: snapshot})
	require.NoError(t, err)
	require.True(t, used)
	require.NotNil(t, materialized.Group)
	require.True(t, materialized.Group.StrictCindyKnown)
	require.True(t, materialized.Group.StrictCindy)
}

func TestAPIKeyAuthSnapshotRejectsV20WithActiveOnlyCindyIdentity(t *testing.T) {
	t.Parallel()
	svc := &APIKeyService{}
	apiKey, used, err := svc.applyAuthCacheEntry("sk-v20", &APIKeyAuthCacheEntry{
		Snapshot: &APIKeyAuthSnapshot{Version: 20},
	})
	require.NoError(t, err)
	require.False(t, used)
	require.Nil(t, apiKey)
}
