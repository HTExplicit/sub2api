//go:build unit

package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayCacheCindyBalancePendingIsDurableUntilExplicitClear(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &gatewayCache{rdb: client}
	ctx := context.Background()
	const accountID int64 = 77101
	fingerprint, err := service.CindyCredentialsFingerprint(map[string]any{"api_key": "fixture"})
	require.NoError(t, err)

	require.NoError(t, cache.MarkCindyBalancePending(ctx, accountID, fingerprint))
	pendingBatch, err := cache.HasCindyBalancePendingBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.True(t, pendingBatch[accountID])
	ttl, err := client.TTL(ctx, cindyBalancePendingKey(accountID)).Result()
	require.NoError(t, err)
	require.Equal(t, int64(-1), int64(ttl), "pending marker must not have a TTL")

	// A second cache instance models another service/process sharing Redis.
	other := &gatewayCache{rdb: redis.NewClient(&redis.Options{Addr: server.Addr()})}
	t.Cleanup(func() { _ = other.rdb.Close() })
	pendingBatch, err = other.HasCindyBalancePendingBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.True(t, pendingBatch[accountID])
	storedFingerprint, err := other.GetCindyBalancePendingFingerprint(ctx, accountID)
	require.NoError(t, err)
	require.Equal(t, fingerprint, storedFingerprint)

	require.NoError(t, other.ClearCindyBalancePending(ctx, accountID))
	pendingBatch, err = cache.HasCindyBalancePendingBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.False(t, pendingBatch[accountID])
}

func TestGatewayCacheCindyBalancePendingIgnoresLegacyNamespace(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &gatewayCache{rdb: client}
	ctx := context.Background()
	const accountID int64 = 77102
	legacyKey := fmt.Sprintf("cindy_balance_pending:%d", accountID)
	require.Equal(t, fmt.Sprintf("cindy_balance_pending:v2:%d", accountID), cindyBalancePendingKey(accountID))
	fingerprint, err := service.CindyCredentialsFingerprint(map[string]any{"api_key": "fixture"})
	require.NoError(t, err)

	require.NoError(t, client.Set(ctx, legacyKey, "1", 0).Err())
	pendingBatch, err := cache.HasCindyBalancePendingBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.False(t, pendingBatch[accountID], "legacy single-signal marker must not block scheduling")

	require.NoError(t, cache.MarkCindyBalancePending(ctx, accountID, fingerprint))
	pendingBatch, err = cache.HasCindyBalancePendingBatch(ctx, []int64{accountID})
	require.NoError(t, err)
	require.True(t, pendingBatch[accountID])
	require.True(t, server.Exists(cindyBalancePendingKey(accountID)))

	require.NoError(t, cache.ClearCindyBalancePending(ctx, accountID))
	require.True(t, server.Exists(legacyKey), "v2 cleanup must not mutate legacy Redis evidence")
}

func TestGatewayCacheCindyBalancePendingCompareDeletePreservesNewerGeneration(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &gatewayCache{rdb: client}
	ctx := context.Background()
	const accountID int64 = 77103
	oldFingerprint, err := service.CindyCredentialsFingerprint(map[string]any{"api_key": "old"})
	require.NoError(t, err)
	newFingerprint, err := service.CindyCredentialsFingerprint(map[string]any{"api_key": "new"})
	require.NoError(t, err)

	require.NoError(t, cache.MarkCindyBalancePending(ctx, accountID, newFingerprint))
	require.NoError(t, cache.ClearCindyBalancePendingIfFingerprintMatches(ctx, accountID, oldFingerprint))
	stored, err := cache.GetCindyBalancePendingFingerprint(ctx, accountID)
	require.NoError(t, err)
	require.Equal(t, newFingerprint, stored)

	require.NoError(t, cache.ClearCindyBalancePendingIfFingerprintMatches(ctx, accountID, newFingerprint))
	stored, err = cache.GetCindyBalancePendingFingerprint(ctx, accountID)
	require.NoError(t, err)
	require.Empty(t, stored)
}

func TestGatewayCacheCindyHealthEpisodeCASSeparatesGenerationAndEpisode(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &gatewayCache{rdb: client}
	ctx := context.Background()
	oldEpisode := service.CindyHealthEpisode{AccountID: 77201, Generation: 4, EpisodeID: "old-episode"}
	newEpisode := service.CindyHealthEpisode{AccountID: 77201, Generation: 5, EpisodeID: "new-episode"}

	claimed, err := cache.ClaimCindyHealthEpisode(ctx, oldEpisode, 5*time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = cache.ClaimCindyHealthEpisode(ctx, oldEpisode, 5*time.Minute)
	require.NoError(t, err)
	require.False(t, claimed, "the same generation must have only one active episode")
	claimed, err = cache.ClaimCindyHealthEpisode(ctx, newEpisode, 5*time.Minute)
	require.NoError(t, err)
	require.True(t, claimed, "a newer credential generation replaces stale work")

	require.NoError(t, cache.ClearCindyHealthEpisodeIfMatch(ctx, oldEpisode))
	require.True(t, server.Exists(cindyHealthEpisodeKey(oldEpisode.AccountID)), "old episode must not clear newer work")
	require.NoError(t, cache.ClearCindyHealthEpisodeIfMatch(ctx, newEpisode))
	require.False(t, server.Exists(cindyHealthEpisodeKey(oldEpisode.AccountID)))
}

func TestGatewayCacheCindyHealthEpisodeCASDoesNotRoundBigintGenerations(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &gatewayCache{rdb: client}
	ctx := context.Background()
	oldEpisode := service.CindyHealthEpisode{AccountID: 77202, Generation: 9007199254740992, EpisodeID: "old-large"}
	newEpisode := service.CindyHealthEpisode{AccountID: 77202, Generation: 9007199254740993, EpisodeID: "new-large"}

	claimed, err := cache.ClaimCindyHealthEpisode(ctx, oldEpisode, time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = cache.ClaimCindyHealthEpisode(ctx, newEpisode, time.Minute)
	require.NoError(t, err)
	require.True(t, claimed, "adjacent BIGINT generations must remain distinct above 2^53")
}

func TestGatewayCacheClearAllCindyHealthStateRemovesLegacyV2AndV3Keys(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &gatewayCache{rdb: client}
	ctx := context.Background()
	const accountID int64 = 77203
	keys := []string{
		fmt.Sprintf("cindy_balance_pending:%d", accountID),
		cindyBalancePendingKey(accountID),
		cindyHealthEpisodeKey(accountID),
		fmt.Sprintf("cindy_health_episode:v2:%d", accountID),
		cindyHealthPendingKeyV3(accountID),
		cindyHealthPendingKeyV3(accountID, service.CindyHealthStatusBanned),
		cindyHealthPendingKeyV3(accountID, service.CindyHealthStatusBalanceInsufficient),
	}
	for _, key := range keys {
		require.NoError(t, client.Set(ctx, key, "fixture", 0).Err())
	}

	require.NoError(t, cache.ClearAllCindyHealthState(ctx, accountID))
	for _, key := range keys {
		require.False(t, server.Exists(key), key)
	}
}

func TestGatewayCacheCindyTerminalPendingSurvivesRestartAndListsGenerationExactly(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &gatewayCache{rdb: client}
	ctx := context.Background()
	episode := service.CindyHealthEpisode{
		AccountID: 77204, Generation: 9007199254740993, EpisodeID: "terminal-bigint",
		Fingerprint: strings.Repeat("a", 64), Status: service.CindyHealthStatusBanned,
		Evidence: service.CindyHealthEvidenceUnauthorized, ObservedAt: time.Now().UTC().Truncate(time.Microsecond),
	}

	claimed, err := cache.ClaimCindyHealthEpisode(ctx, episode, 0)
	require.NoError(t, err)
	require.True(t, claimed)
	ttl, err := client.TTL(ctx, cindyHealthPendingKeyV3(episode.AccountID, episode.Status)).Result()
	require.NoError(t, err)
	require.Equal(t, int64(-1), int64(ttl))
	balanceEpisode := episode
	balanceEpisode.EpisodeID = "terminal-balance-bigint"
	balanceEpisode.Status = service.CindyHealthStatusBalanceInsufficient
	balanceEpisode.Evidence = service.CindyHealthEvidenceExactBudget
	claimed, err = cache.ClaimCindyHealthEpisode(ctx, balanceEpisode, 0)
	require.NoError(t, err)
	require.True(t, claimed, "independent terminal states must coexist for the same credential generation")

	restarted := &gatewayCache{rdb: redis.NewClient(&redis.Options{Addr: server.Addr()})}
	t.Cleanup(func() { _ = restarted.rdb.Close() })
	episodes, err := restarted.ListCindyHealthEpisodes(ctx, 10)
	require.NoError(t, err)
	require.ElementsMatch(t, []service.CindyHealthEpisode{episode, balanceEpisode}, episodes)
}
