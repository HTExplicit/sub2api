package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestLiveLeaseReplacesRegularSlotsAndCountsTowardLimits(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	regular := NewConcurrencyCache(client, 15, 900)
	live, ok := regular.(service.LiveConcurrencyCache)
	require.True(t, ok)
	ctx := context.Background()

	accountAcquired, err := regular.AcquireAccountSlot(ctx, 10, 1, "regular-account")
	require.NoError(t, err)
	require.True(t, accountAcquired)
	userAcquired, err := regular.AcquireUserSlot(ctx, 20, 1, "regular-user")
	require.NoError(t, err)
	require.True(t, userAcquired)

	acquired, err := live.AcquireLiveLease(ctx, 10, 1, 20, 1, 30, "live-lease", true)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, regular.ReleaseAccountSlot(ctx, 10, "regular-account"))
	require.NoError(t, regular.ReleaseUserSlot(ctx, 20, "regular-user"))

	accountCount, err := regular.GetAccountConcurrency(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, accountCount)
	userCount, err := regular.GetUserConcurrency(ctx, 20)
	require.NoError(t, err)
	require.Equal(t, 1, userCount)
	accountAcquired, err = regular.AcquireAccountSlot(ctx, 10, 1, "ordinary-blocked")
	require.NoError(t, err)
	require.False(t, accountAcquired)

	refreshed, err := live.RefreshLiveLease(ctx, 10, 20, 30, "live-lease")
	require.NoError(t, err)
	require.True(t, refreshed)
	require.NoError(t, live.ReleaseLiveLease(ctx, 10, 20, 30, "live-lease"))
	accountAcquired, err = regular.AcquireAccountSlot(ctx, 10, 1, "ordinary-allowed")
	require.NoError(t, err)
	require.True(t, accountAcquired)
}

func TestLiveLeaseExpiresWithoutRefresh(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	regular := NewConcurrencyCache(client, 15, 900)
	live, ok := regular.(service.LiveConcurrencyCache)
	require.True(t, ok)
	ctx := context.Background()

	acquired, err := live.AcquireLiveLease(ctx, 10, 1, 20, 1, 30, "expired-live", false)
	require.NoError(t, err)
	require.True(t, acquired)

	redisServer.FastForward(61 * time.Second)
	acquired, err = regular.AcquireAccountSlot(ctx, 10, 1, "ordinary-after-expiry")
	require.NoError(t, err)
	require.True(t, acquired)
	refreshed, err := live.RefreshLiveLease(ctx, 10, 20, 30, "expired-live")
	require.NoError(t, err)
	require.False(t, refreshed)
}

func TestAccountTrafficObserveBeginReadsSlotsAndRollingMinute(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	regular := NewConcurrencyCache(client, 15, 900)
	observe := NewAccountTrafficObserveCache(client, 15)
	ctx := context.Background()

	for _, requestID := range []string{"slot-a", "slot-b"} {
		acquired, err := regular.AcquireAccountSlot(ctx, 10, 5, requestID)
		require.NoError(t, err)
		require.True(t, acquired)
	}
	require.NoError(t, observe.Begin(ctx, 10, service.AccountTrafficProtocolHTTP))
	require.NoError(t, observe.Begin(ctx, 10, service.AccountTrafficProtocolHTTP))
	// Both the Lua script and Snapshot read Redis TIME: moving it past the window
	// drops the two earlier members from the rolling count while the 24h counter
	// keeps them.
	redisServer.SetTime(time.Now().Add(61 * time.Second))
	require.NoError(t, observe.Begin(ctx, 10, service.AccountTrafficProtocolHTTP))

	snapshot, err := observe.Snapshot(ctx, 10)
	require.NoError(t, err)
	httpState := snapshot[service.AccountTrafficProtocolHTTP]
	require.EqualValues(t, 3, httpState.Started)
	require.Equal(t, 2, httpState.PeakInFlight)
	require.Equal(t, 1, httpState.RequestsLast60s)
	require.Zero(t, snapshot[service.AccountTrafficProtocolWS].Started)
	// Sampling must leave the concurrency ZSET untouched.
	count, err := regular.GetAccountConcurrency(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 2, count)

	turn := service.NewAccountTrafficObserver(observe, nil).Begin(ctx, &service.Account{ID: 10, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth}, service.AccountTrafficProtocolHTTP)
	require.NotNil(t, turn)
	turn.Finish(nil, &service.UpstreamFailoverError{StatusCode: 429}, false)
	turn.Finish(nil, &service.UpstreamFailoverError{StatusCode: 429}, false)
	snapshot, err = observe.Snapshot(ctx, 10)
	require.NoError(t, err)
	require.EqualValues(t, 4, snapshot[service.AccountTrafficProtocolHTTP].Started)
	require.EqualValues(t, 1, snapshot[service.AccountTrafficProtocolHTTP].Upstream429)
}
