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
			group:      &Group{ID: 1, StrictCindyKnown: true},
			classifier: &materializedCindyClassifierStub{err: transient},
		},
		{
			name:       "known Cindy snapshot does not query classifier",
			group:      &Group{ID: 2, StrictCindyKnown: true, StrictCindy: true},
			classifier: &materializedCindyClassifierStub{err: transient},
			wantStrict: true,
		},
		{
			name:       "legacy unknown snapshot explicitly falls back",
			group:      &Group{ID: 3},
			classifier: &materializedCindyClassifierStub{strict: true},
			wantStrict: true,
			wantCalls:  1,
		},
		{
			name:       "legacy fallback failure remains fail closed",
			group:      &Group{ID: 4},
			classifier: &materializedCindyClassifierStub{err: transient},
			wantErr:    transient,
			wantCalls:  1,
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

func TestAPIKeyAuthSnapshotRejectsV19WithoutMaterializedIdentity(t *testing.T) {
	t.Parallel()
	svc := &APIKeyService{}
	apiKey, used, err := svc.applyAuthCacheEntry("sk-v19", &APIKeyAuthCacheEntry{
		Snapshot: &APIKeyAuthSnapshot{Version: 19},
	})
	require.NoError(t, err)
	require.False(t, used)
	require.Nil(t, apiKey)
}
