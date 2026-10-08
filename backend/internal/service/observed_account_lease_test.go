//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type observedAccountLeaseCacheForTest struct {
	stubConcurrencyCacheForTest
	mu         sync.Mutex
	now        time.Time
	slots      map[int64]map[string]time.Time
	releases   map[int64]int
	refreshErr error
	refreshes  atomic.Int64
	refreshed  chan struct{}
}

func newObservedAccountLeaseCacheForTest() *observedAccountLeaseCacheForTest {
	return &observedAccountLeaseCacheForTest{now: time.Now().UTC(), slots: make(map[int64]map[string]time.Time),
		releases: make(map[int64]int), refreshed: make(chan struct{}, 64)}
}

func (c *observedAccountLeaseCacheForTest) AcquireAccountSlot(_ context.Context, id int64, limit int, requestID string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.slots[id] == nil {
		c.slots[id] = make(map[string]time.Time)
	}
	for member, expires := range c.slots[id] {
		if !expires.After(c.now) {
			delete(c.slots[id], member)
		}
	}
	if len(c.slots[id]) >= limit {
		return false, nil
	}
	c.slots[id][requestID] = c.now.Add(15 * time.Minute)
	return true, nil
}

func (c *observedAccountLeaseCacheForTest) RefreshAccountSlot(ctx context.Context, id int64, requestID string) (bool, error) {
	defer func() { c.refreshed <- struct{}{} }()
	c.refreshes.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if c.refreshErr != nil {
		return false, c.refreshErr
	}
	if expires, found := c.slots[id][requestID]; !found || !expires.After(c.now) {
		return false, nil
	}
	c.slots[id][requestID] = c.now.Add(15 * time.Minute)
	return true, nil
}

func (c *observedAccountLeaseCacheForTest) ReleaseAccountSlot(_ context.Context, id int64, requestID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.slots[id], requestID)
	c.releases[id]++
	return nil
}

func TestObservedAccountLeaseKeepsThirtyMinuteCapacityAndStopsOnRelease(t *testing.T) {
	cache := newObservedAccountLeaseCacheForTest()
	svc := NewConcurrencyService(cache)
	// Ordinary acquisition retains its current non-renewing behavior even
	// though this cache also supports the optional observation extension.
	normal, err := svc.AcquireAccountSlot(context.Background(), 2, 1)
	require.NoError(t, err)
	require.True(t, normal.Acquired)
	defer normal.ReleaseFunc()
	owned, err := cache.AcquireAccountSlot(context.Background(), 1, 1, "observed")
	require.NoError(t, err)
	require.True(t, owned)
	ticks := make(chan time.Time, 1)
	var stopped atomic.Int64
	lease := newObservedAccountLease(context.Background(), cache, cache, 1, "observed", nil, ticks, func() { stopped.Add(1) })
	t.Cleanup(lease.Release)
	for i := 0; i < 60; i++ {
		cache.mu.Lock()
		cache.now = cache.now.Add(observedAccountLeaseRefreshInterval)
		now := cache.now
		cache.mu.Unlock()
		ticks <- now
		<-cache.refreshed
		allowed, acquireErr := cache.AcquireAccountSlot(context.Background(), 1, 1, "other-business")
		require.NoError(t, acquireErr)
		require.False(t, allowed, "a competing ordinary request remains blocked beyond the original fifteen-minute TTL")
	}
	allowed, err := cache.AcquireAccountSlot(context.Background(), 2, 1, "ordinary-after-original-ttl")
	require.NoError(t, err)
	require.True(t, allowed, "ordinary slots must keep the existing expiry contract")
	lease.Release()
	lease.Release()
	require.Equal(t, int64(1), stopped.Load())
	require.Equal(t, int64(60), cache.refreshes.Load())
	ticks <- cache.now
	cache.mu.Lock()
	releases, remaining := cache.releases[1], len(cache.slots[1])
	cache.mu.Unlock()
	require.Equal(t, 1, releases)
	require.Zero(t, remaining)
	require.ErrorIs(t, lease.Context().Err(), context.Canceled)
}

func TestObservedAccountLeaseCancellationAndLossStopWorkerAndOperation(t *testing.T) {
	for _, mode := range []string{"cancel", "missing", "redis_error"} {
		t.Run(mode, func(t *testing.T) {
			cache := newObservedAccountLeaseCacheForTest()
			owned, err := cache.AcquireAccountSlot(context.Background(), 1, 1, "observed")
			require.NoError(t, err)
			require.True(t, owned)
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			state := newPelicanExecutionState(parent, newPelicanExecutionCoordinator(1), 30*time.Minute, time.Now)
			operation := context.WithValue(state.parent, pelicanExecutionContextKey{}, state)
			defer state.finish()
			ticks := make(chan time.Time, 1)
			var stopped, losses atomic.Int64
			lease := newObservedAccountLease(operation, cache, cache, 1, "observed", func(cause error) {
				losses.Add(1)
				CancelPelicanExecution(operation, cause)
			}, ticks, func() { stopped.Add(1) })
			t.Cleanup(lease.Release)
			if mode == "cancel" {
				cancel()
			} else {
				cache.mu.Lock()
				if mode == "missing" {
					delete(cache.slots[1], "observed")
				} else {
					cache.refreshErr = errors.New("synthetic redis unavailable")
				}
				cache.mu.Unlock()
				ticks <- cache.now
			}
			select {
			case <-lease.done:
			case <-time.After(2 * time.Second):
				t.Fatal("cancelled or lost account lease left its renewal worker running")
			}
			require.Equal(t, int64(1), stopped.Load())
			cache.mu.Lock()
			releases, remaining := cache.releases[1], len(cache.slots[1])
			cache.mu.Unlock()
			require.Equal(t, 1, releases)
			require.Zero(t, remaining)
			if mode == "cancel" {
				require.Zero(t, losses.Load())
				require.Zero(t, cache.refreshes.Load())
			} else {
				require.Equal(t, int64(1), losses.Load())
				require.ErrorIs(t, context.Cause(operation), ErrObservedAccountLeaseLost)
				require.ErrorIs(t, context.Cause(lease.Context()), ErrObservedAccountLeaseLost)
				_, _, acquireErr := AcquirePelicanExecution(operation, 2)
				require.ErrorIs(t, acquireErr, context.Canceled, "lost account ownership cannot continue with a later model request")
			}
			lease.Release()
			require.Equal(t, int64(1), stopped.Load())
		})
	}
}

func TestObservedAccountLeaseAcquisitionUsesNormalCapacityAndRequiresRenewal(t *testing.T) {
	cache := newObservedAccountLeaseCacheForTest()
	svc := NewConcurrencyService(cache)
	lease, acquired, err := svc.AcquireObservedAccountLease(nil, 1, 1, nil)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, lease)
	second, acquired, err := svc.AcquireObservedAccountLease(context.Background(), 1, 1, nil)
	require.NoError(t, err)
	require.False(t, acquired)
	require.Nil(t, second)
	lease.Release()
	cache.mu.Lock()
	releases := cache.releases[1]
	cache.mu.Unlock()
	require.Equal(t, 1, releases)
	require.Zero(t, cache.refreshes.Load(), "acquiring/releasing a short observation need not wait for a timer")
	unsupported, acquired, err := NewConcurrencyService(&stubConcurrencyCacheForTest{}).AcquireObservedAccountLease(context.Background(), 1, 1, nil)
	require.ErrorContains(t, err, "renewal is unsupported")
	require.False(t, acquired)
	require.Nil(t, unsupported)
	var empty *ConcurrencyService
	unlimited, acquired, err := empty.AcquireObservedAccountLease(nil, 1, 0, nil)
	require.NoError(t, err)
	require.True(t, acquired)
	require.Nil(t, unlimited)
}
