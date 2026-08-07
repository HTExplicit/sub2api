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

func TestGatewayCacheLiveCallIdentityAndController(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	cache, ok := NewGatewayCache(client).(service.LiveCallStore)
	require.True(t, ok)
	otherInstance, ok := NewGatewayCache(client).(service.LiveCallStore)
	require.True(t, ok)
	record := &service.LiveCallRecord{
		CallID:                "call_secret",
		CallHash:              HashLiveCallID("call_secret"),
		AccountID:             11,
		APIKeyID:              22,
		UserID:                33,
		GroupID:               44,
		LeaseID:               "lease",
		Model:                 "gpt-live-test",
		AttestationCiphertext: "encrypted-attestation",
		CreatedAt:             time.Now(),
		ExpiresAt:             time.Now().Add(time.Hour),
		Controller:            service.LiveControllerPending,
	}
	require.NoError(t, cache.SaveLiveCall(context.Background(), record, time.Hour))

	loaded, err := otherInstance.GetLiveCall(context.Background(), record.CallHash)
	require.NoError(t, err)
	require.Equal(t, record.CallID, loaded.CallID)
	require.Equal(t, record.AccountID, loaded.AccountID)
	require.Equal(t, record.AttestationCiphertext, loaded.AttestationCiphertext)

	claimed, err := cache.ClaimLiveController(context.Background(), record.CallHash, service.LiveControllerObserver, "observer-1")
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = cache.ClaimLiveController(context.Background(), record.CallHash, service.LiveControllerProxy, "proxy-1")
	require.NoError(t, err)
	require.True(t, claimed)
	controller, err := cache.GetLiveController(context.Background(), record.CallHash)
	require.NoError(t, err)
	require.Equal(t, service.LiveControllerProxy, controller)

	released, err := cache.ReleaseLiveController(context.Background(), record.CallHash, "proxy-1")
	require.NoError(t, err)
	require.True(t, released)
	closed, err := cache.MarkLiveCallClosed(context.Background(), record.CallHash, time.Hour)
	require.NoError(t, err)
	require.True(t, closed)
	closed, err = cache.MarkLiveCallClosed(context.Background(), record.CallHash, time.Hour)
	require.NoError(t, err)
	require.False(t, closed)
}

func TestGatewayCacheOpenAIRuntimeBreakerPersistsAndClaimsHalfOpen(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	first, ok := NewGatewayCache(client).(service.OpenAIRuntimeBreakerStore)
	require.True(t, ok)
	second, ok := NewGatewayCache(client).(service.OpenAIRuntimeBreakerStore)
	require.True(t, ok)
	ctx := context.Background()

	require.NoError(t, first.BlockOpenAIRuntimeBreaker(ctx, 71, "gpt-5.4", "retry_exhausted_503", 2*time.Second))
	allowed, err := second.AllowOpenAIRuntimeBreakerProbe(ctx, 71, "gpt-5.4", "owner-2", time.Minute)
	require.NoError(t, err)
	require.False(t, allowed)
	reason, err := redisServer.Get(openAIRuntimeBreakerBlockKey(71, "gpt-5.4"))
	require.NoError(t, err)
	require.Equal(t, "retry_exhausted_503", reason)
	require.Greater(t, redisServer.TTL(openAIRuntimeBreakerBlockKey(71, "gpt-5.4")), time.Duration(0))
	require.NoError(t, first.ClearOpenAIRuntimeBreaker(ctx, 71, "gpt-5.4", "owner-2"))
	allowed, err = second.AllowOpenAIRuntimeBreakerProbe(ctx, 71, "gpt-5.4", "owner-2", time.Minute)
	require.NoError(t, err)
	require.False(t, allowed, "a late success must not clear an active breaker")

	redisServer.FastForward(3 * time.Second)
	allowed, err = first.AllowOpenAIRuntimeBreakerProbe(ctx, 71, "gpt-5.4", "owner-1", time.Minute)
	require.NoError(t, err)
	require.True(t, allowed)
	allowed, err = second.AllowOpenAIRuntimeBreakerProbe(ctx, 71, "gpt-5.4", "owner-2", time.Minute)
	require.NoError(t, err)
	require.False(t, allowed)
	allowed, err = first.AllowOpenAIRuntimeBreakerProbe(ctx, 71, "gpt-5.4", "owner-1", time.Minute)
	require.NoError(t, err)
	require.True(t, allowed)

	require.NoError(t, first.ClearOpenAIRuntimeBreaker(ctx, 71, "gpt-5.4", "owner-2"))
	allowed, err = second.AllowOpenAIRuntimeBreakerProbe(ctx, 71, "gpt-5.4", "owner-2", time.Minute)
	require.NoError(t, err)
	require.False(t, allowed, "a non-owner success must not clear another request's half-open claim")
	require.NoError(t, first.ClearOpenAIRuntimeBreaker(ctx, 71, "gpt-5.4", "owner-1"))
	allowed, err = second.AllowOpenAIRuntimeBreakerProbe(ctx, 71, "gpt-5.4", "owner-2", time.Minute)
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestGatewayCacheOpenAIRuntimeBreakerBatchClaimIsAtomicAcrossScopes(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	cache := NewGatewayCache(client)
	batch, ok := cache.(interface {
		AllowOpenAIRuntimeBreakerProbes(context.Context, int64, []string, string, time.Duration) (bool, []string, error)
	})
	require.True(t, ok, "gateway cache must support atomic multi-scope breaker claims")
	store, ok := cache.(service.OpenAIRuntimeBreakerStore)
	require.True(t, ok, "gateway cache must support runtime breaker persistence")
	ctx := context.Background()

	require.NoError(t, store.BlockOpenAIRuntimeBreaker(ctx, 73, "", "account_failure", time.Second))
	require.NoError(t, store.BlockOpenAIRuntimeBreaker(ctx, 73, "gpt-5.4", "model_failure", time.Minute))
	redisServer.FastForward(2 * time.Second)

	allowed, claimedScopes, err := batch.AllowOpenAIRuntimeBreakerProbes(
		ctx,
		73,
		[]string{"", "gpt-5.4"},
		"owner-1",
		time.Minute,
	)
	require.NoError(t, err)
	require.False(t, allowed)
	require.Empty(t, claimedScopes)
	require.False(t, redisServer.Exists(openAIRuntimeBreakerClaimKey(73, "")),
		"a later denied scope must not leave an earlier scope claimed")
}

func TestGatewayCacheOpenAIRuntimeBreakerRenewExtendsLeaseStateForCurrentOwnerOnly(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	cache := NewGatewayCache(client)
	batch, ok := cache.(interface {
		AllowOpenAIRuntimeBreakerProbes(context.Context, int64, []string, string, time.Duration) (bool, []string, error)
	})
	require.True(t, ok, "gateway cache must support atomic multi-scope breaker claims")
	renewer, ok := cache.(interface {
		RenewOpenAIRuntimeBreakerProbes(context.Context, int64, []string, string, time.Duration) (bool, error)
	})
	require.True(t, ok, "gateway cache must support owner-safe breaker lease renewal")
	store, ok := cache.(service.OpenAIRuntimeBreakerStore)
	require.True(t, ok, "gateway cache must support runtime breaker persistence")
	ctx := context.Background()
	models := []string{"", "gpt-5.4"}

	for _, model := range models {
		require.NoError(t, store.BlockOpenAIRuntimeBreaker(ctx, 74, model, "transient", time.Second))
	}
	redisServer.FastForward(openAIRuntimeBreakerHalfOpenRetention)

	allowed, claimedScopes, err := batch.AllowOpenAIRuntimeBreakerProbes(ctx, 74, models, "owner-1", 10*time.Second)
	require.NoError(t, err)
	require.True(t, allowed)
	require.ElementsMatch(t, models, claimedScopes)

	renewed, err := renewer.RenewOpenAIRuntimeBreakerProbes(ctx, 74, claimedScopes, "owner-1", time.Minute)
	require.NoError(t, err)
	require.True(t, renewed)
	for _, model := range models {
		require.Greater(t, redisServer.TTL(openAIRuntimeBreakerClaimKey(74, model)), 50*time.Second)
		require.Greater(t, redisServer.TTL(openAIRuntimeBreakerMarkerKey(74, model)), 50*time.Second)
	}
	require.Greater(t, redisServer.TTL(openAIRuntimeBreakerIndexKey(74)), 50*time.Second)

	redisServer.FastForward(61 * time.Second)
	allowed, claimedScopes, err = batch.AllowOpenAIRuntimeBreakerProbes(ctx, 74, models, "owner-2", time.Minute)
	require.NoError(t, err)
	require.True(t, allowed)
	require.ElementsMatch(t, models, claimedScopes)
	owner2TTL := redisServer.TTL(openAIRuntimeBreakerClaimKey(74, ""))

	renewed, err = renewer.RenewOpenAIRuntimeBreakerProbes(ctx, 74, models, "owner-1", 5*time.Minute)
	require.NoError(t, err)
	require.False(t, renewed)
	owner, err := redisServer.Get(openAIRuntimeBreakerClaimKey(74, ""))
	require.NoError(t, err)
	require.Equal(t, "owner-2", owner)
	require.Equal(t, owner2TTL, redisServer.TTL(openAIRuntimeBreakerClaimKey(74, "")),
		"a stale owner must not extend the current owner's lease")
}

func TestGatewayCacheOpenAIRuntimeBreakerRenewRejectsExpiredLease(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	cache := NewGatewayCache(client)
	store, ok := cache.(service.OpenAIRuntimeBreakerStore)
	require.True(t, ok, "gateway cache must support runtime breaker persistence")
	leaseStore, ok := cache.(service.OpenAIRuntimeBreakerLeaseStore)
	require.True(t, ok, "gateway cache must support owner-safe breaker leases")
	ctx := context.Background()
	models := []string{"", "gpt-5.4"}

	for _, model := range models {
		require.NoError(t, store.BlockOpenAIRuntimeBreaker(ctx, 76, model, "transient", time.Second))
	}
	redisServer.FastForward(2 * time.Second)
	allowed, claimedScopes, err := leaseStore.AllowOpenAIRuntimeBreakerProbes(ctx, 76, models, "owner-1", time.Second)
	require.NoError(t, err)
	require.True(t, allowed)
	require.ElementsMatch(t, models, claimedScopes)

	redisServer.FastForward(openAIRuntimeBreakerHalfOpenRetention + time.Second)
	renewed, err := leaseStore.RenewOpenAIRuntimeBreakerProbes(ctx, 76, claimedScopes, "owner-1", time.Minute)
	require.NoError(t, err)
	require.False(t, renewed, "a lease with no remaining marker or owner claim must not be resurrected")
}

func TestGatewayCacheOpenAIRuntimeBreakerRenewRestoresIndexTTL(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	cache := NewGatewayCache(client)
	store, ok := cache.(service.OpenAIRuntimeBreakerStore)
	require.True(t, ok, "gateway cache must support runtime breaker persistence")
	leaseStore, ok := cache.(service.OpenAIRuntimeBreakerLeaseStore)
	require.True(t, ok, "gateway cache must support owner-safe breaker leases")
	ctx := context.Background()
	models := []string{"", "gpt-5.4"}

	for _, model := range models {
		require.NoError(t, store.BlockOpenAIRuntimeBreaker(ctx, 77, model, "transient", time.Second))
	}
	redisServer.FastForward(2 * time.Second)
	allowed, claimedScopes, err := leaseStore.AllowOpenAIRuntimeBreakerProbes(ctx, 77, models, "owner-1", time.Minute)
	require.NoError(t, err)
	require.True(t, allowed)
	require.ElementsMatch(t, models, claimedScopes)

	indexKey := openAIRuntimeBreakerIndexKey(77)
	redisServer.Del(indexKey)
	renewed, err := leaseStore.RenewOpenAIRuntimeBreakerProbes(ctx, 77, claimedScopes, "owner-1", time.Minute)
	require.NoError(t, err)
	require.True(t, renewed)
	require.Greater(t, redisServer.TTL(indexKey), time.Duration(0), "a recreated breaker index must retain bounded lifetime")
}

func TestGatewayCacheOpenAIRuntimeBreakerReleaseIsOwnerSafeAndKeepsMarker(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	cache := NewGatewayCache(client)
	batch, ok := cache.(interface {
		AllowOpenAIRuntimeBreakerProbes(context.Context, int64, []string, string, time.Duration) (bool, []string, error)
	})
	require.True(t, ok, "gateway cache must support atomic multi-scope breaker claims")
	releaser, ok := cache.(interface {
		ReleaseOpenAIRuntimeBreakerProbes(context.Context, int64, []string, string) (bool, error)
	})
	require.True(t, ok, "gateway cache must support owner-safe breaker lease release")
	store, ok := cache.(service.OpenAIRuntimeBreakerStore)
	require.True(t, ok, "gateway cache must support runtime breaker persistence")
	ctx := context.Background()
	models := []string{"", "gpt-5.4"}
	for _, model := range models {
		require.NoError(t, store.BlockOpenAIRuntimeBreaker(ctx, 75, model, "transient", time.Second))
	}
	redisServer.FastForward(openAIRuntimeBreakerHalfOpenRetention)
	allowed, claimedScopes, err := batch.AllowOpenAIRuntimeBreakerProbes(ctx, 75, models, "owner-1", time.Minute)
	require.NoError(t, err)
	require.True(t, allowed)
	require.ElementsMatch(t, models, claimedScopes)

	released, err := releaser.ReleaseOpenAIRuntimeBreakerProbes(ctx, 75, claimedScopes, "owner-2")
	require.NoError(t, err)
	require.False(t, released)
	owner, err := redisServer.Get(openAIRuntimeBreakerClaimKey(75, ""))
	require.NoError(t, err)
	require.Equal(t, "owner-1", owner)

	released, err = releaser.ReleaseOpenAIRuntimeBreakerProbes(ctx, 75, claimedScopes, "owner-1")
	require.NoError(t, err)
	require.True(t, released)
	for _, model := range models {
		require.False(t, redisServer.Exists(openAIRuntimeBreakerClaimKey(75, model)))
		require.True(t, redisServer.Exists(openAIRuntimeBreakerMarkerKey(75, model)),
			"releasing a probe must preserve half-open marker state")
	}
	require.True(t, redisServer.Exists(openAIRuntimeBreakerIndexKey(75)),
		"the account index remains until the breaker marker expires")
}

func TestGatewayCacheOpenAIRuntimeBreakerDoesNotShortenExistingTTL(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	store, ok := NewGatewayCache(client).(service.OpenAIRuntimeBreakerStore)
	require.True(t, ok, "gateway cache must support runtime breaker persistence")
	ctx := context.Background()

	require.NoError(t, store.BlockOpenAIRuntimeBreaker(ctx, 72, "", "long", time.Minute))
	require.NoError(t, store.BlockOpenAIRuntimeBreaker(ctx, 72, "", "short", time.Second))

	reason, err := redisServer.Get(openAIRuntimeBreakerBlockKey(72, ""))
	require.NoError(t, err)
	require.Equal(t, "long", reason)
	require.Greater(t, redisServer.TTL(openAIRuntimeBreakerBlockKey(72, "")), 50*time.Second)
}

func TestGatewayCacheDeleteSessionAccountIDIfMatches(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	cache := NewGatewayCache(client)
	ctx := context.Background()

	require.NoError(t, cache.SetSessionAccountID(ctx, 9, "session", 22, time.Minute))
	deleted, err := cache.DeleteSessionAccountIDIfMatches(ctx, 9, "session", 11)
	require.NoError(t, err)
	require.False(t, deleted)
	accountID, err := cache.GetSessionAccountID(ctx, 9, "session")
	require.NoError(t, err)
	require.Equal(t, int64(22), accountID)

	deleted, err = cache.DeleteSessionAccountIDIfMatches(ctx, 9, "session", 22)
	require.NoError(t, err)
	require.True(t, deleted)
	_, err = cache.GetSessionAccountID(ctx, 9, "session")
	require.ErrorIs(t, err, service.ErrStickySessionNotFound)
}
