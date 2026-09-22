package service

import (
	"context"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type quotaActivityMemoryStore struct {
	mu    sync.Mutex
	stamp QuotaActivityStamp
}

func (s *quotaActivityMemoryStore) Begin(context.Context, int64, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stamp.Active++
	s.stamp.Revision++
	return nil
}
func (s *quotaActivityMemoryStore) Refresh(context.Context, int64, string) error { return nil }
func (s *quotaActivityMemoryStore) Finish(_ context.Context, _ int64, _ string, logged bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stamp.Active > 0 {
		s.stamp.Active--
	}
	s.stamp.Revision++
	if !logged {
		s.stamp.Gaps++
	}
	return nil
}
func (s *quotaActivityMemoryStore) Read(context.Context, int64) (QuotaActivityStamp, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stamp, nil
}

func TestQuotaActivityWaitsForQueuedUsageAfterRequestEnds(t *testing.T) {
	store := &quotaActivityMemoryStore{}
	activity := NewQuotaActivityService(store)
	ctx, finishRequest := activity.Attach(context.Background())
	ctx = context.WithValue(ctx, ctxkey.AccountID, int64(7))
	ObserveQuotaAccount(ctx, 7)
	task, discard := TrackQuotaUsageTask(ctx, func(ctx context.Context) { MarkQuotaLogPersisted(ctx, 7) })
	finishRequest()
	stamp, known := activity.Read(context.Background(), 7)
	require.True(t, known)
	require.EqualValues(t, 1, stamp.Active, "HTTP completion must not hide an uncommitted queued bill")
	task(context.Background())
	discard() // completion is once-only, even on cancellation races.
	stamp, known = activity.Read(context.Background(), 7)
	require.True(t, known)
	require.Zero(t, stamp.Active)
	require.Zero(t, stamp.Gaps)
}

func TestQuotaActivityDroppedUsageInvalidatesCalibration(t *testing.T) {
	activity := NewQuotaActivityService(&quotaActivityMemoryStore{})
	ctx, finishRequest := activity.Attach(context.Background())
	ctx = context.WithValue(ctx, ctxkey.AccountID, int64(7))
	ObserveQuotaAccount(ctx, 7)
	_, discard := TrackQuotaUsageTask(ctx, func(context.Context) { t.Fatal("dropped job must not execute") })
	finishRequest()
	discard()
	stamp, known := activity.Read(context.Background(), 7)
	require.True(t, known)
	require.Zero(t, stamp.Active)
	require.EqualValues(t, 1, stamp.Gaps)
}

func TestQuotaActivityOneSuccessfulTurnDoesNotHideLaterDroppedBill(t *testing.T) {
	activity := NewQuotaActivityService(&quotaActivityMemoryStore{})
	ctx, finishRequest := activity.Attach(context.Background())
	ctx = context.WithValue(ctx, ctxkey.AccountID, int64(7))
	ObserveQuotaAccount(ctx, 7)
	first, _ := TrackQuotaUsageTask(ctx, func(ctx context.Context) { MarkQuotaLogPersisted(ctx, 7) })
	first(context.Background())
	_, discard := TrackQuotaUsageTask(ctx, func(context.Context) { t.Fatal("dropped turn must not execute") })
	discard()
	finishRequest()
	stamp, known := activity.Read(context.Background(), 7)
	require.True(t, known)
	require.Zero(t, stamp.Active)
	require.EqualValues(t, 1, stamp.Gaps)
}
