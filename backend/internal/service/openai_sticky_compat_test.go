package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type stickyDeleteFaultCache struct {
	*stubGatewayCache
	deleteErrors map[string]error
	deleteCalls  []string
}

func (c *stickyDeleteFaultCache) DeleteSessionAccountIDIfMatches(
	ctx context.Context,
	groupID int64,
	sessionHash string,
	expectedAccountID int64,
) (bool, error) {
	c.deleteCalls = append(c.deleteCalls, sessionHash)
	if err := c.deleteErrors[sessionHash]; err != nil {
		return false, err
	}
	return c.stubGatewayCache.DeleteSessionAccountIDIfMatches(ctx, groupID, sessionHash, expectedAccountID)
}

func TestGetStickySessionAccountID_FallbackToLegacyKey(t *testing.T) {
	beforeFallbackTotal, beforeFallbackHit, _ := openAIStickyCompatStats()

	cache := &stubGatewayCache{
		sessionBindings: map[string]int64{
			"openai:legacy-hash": 42,
		},
	}
	svc := &OpenAIGatewayService{
		cache: cache,
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				OpenAIWS: config.GatewayOpenAIWSConfig{
					SessionHashReadOldFallback: true,
				},
			},
		},
	}

	ctx := withOpenAILegacySessionHash(context.Background(), "legacy-hash")
	accountID, err := svc.getStickySessionAccountID(ctx, nil, "new-hash")
	require.NoError(t, err)
	require.Equal(t, int64(42), accountID)

	afterFallbackTotal, afterFallbackHit, _ := openAIStickyCompatStats()
	require.Equal(t, beforeFallbackTotal+1, afterFallbackTotal)
	require.Equal(t, beforeFallbackHit+1, afterFallbackHit)
}

func TestSetStickySessionAccountID_DualWriteOldEnabled(t *testing.T) {
	_, _, beforeDualWriteTotal := openAIStickyCompatStats()

	cache := &stubGatewayCache{sessionBindings: map[string]int64{}}
	svc := &OpenAIGatewayService{
		cache: cache,
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				OpenAIWS: config.GatewayOpenAIWSConfig{
					SessionHashDualWriteOld: true,
				},
			},
		},
	}

	ctx := withOpenAILegacySessionHash(context.Background(), "legacy-hash")
	err := svc.setStickySessionAccountID(ctx, nil, "new-hash", 9, openaiStickySessionTTL)
	require.NoError(t, err)
	require.Equal(t, int64(9), cache.sessionBindings["openai:new-hash"])
	require.Equal(t, int64(9), cache.sessionBindings["openai:legacy-hash"])

	_, _, afterDualWriteTotal := openAIStickyCompatStats()
	require.Equal(t, beforeDualWriteTotal+1, afterDualWriteTotal)
}

func TestSetStickySessionAccountID_DualWriteOldDisabled(t *testing.T) {
	cache := &stubGatewayCache{sessionBindings: map[string]int64{}}
	svc := &OpenAIGatewayService{
		cache: cache,
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				OpenAIWS: config.GatewayOpenAIWSConfig{
					SessionHashDualWriteOld: false,
				},
			},
		},
	}

	ctx := withOpenAILegacySessionHash(context.Background(), "legacy-hash")
	err := svc.setStickySessionAccountID(ctx, nil, "new-hash", 9, openaiStickySessionTTL)
	require.NoError(t, err)
	require.Equal(t, int64(9), cache.sessionBindings["openai:new-hash"])
	_, exists := cache.sessionBindings["openai:legacy-hash"]
	require.False(t, exists)
}

func TestDeleteStickySessionAccountIDIfMatches_PreservesConcurrentRebind(t *testing.T) {
	cache := &stickyDeleteFaultCache{stubGatewayCache: &stubGatewayCache{sessionBindings: map[string]int64{
		"openai:new-hash":    22,
		"openai:legacy-hash": 22,
	}}}
	svc := &OpenAIGatewayService{cache: cache}
	ctx := withOpenAILegacySessionHash(context.Background(), "legacy-hash")

	err := svc.ClearOpenAIStickySessionAccountIDIfMatches(ctx, nil, "new-hash", 11)

	require.NoError(t, err)
	require.Equal(t, int64(22), cache.sessionBindings["openai:new-hash"])
	require.Equal(t, int64(22), cache.sessionBindings["openai:legacy-hash"])
	require.Equal(t, []string{"openai:new-hash", "openai:legacy-hash"}, cache.deleteCalls)
}

func TestDeleteStickySessionAccountIDIfMatches_DeletesExpectedDualBindings(t *testing.T) {
	cache := &stickyDeleteFaultCache{stubGatewayCache: &stubGatewayCache{sessionBindings: map[string]int64{
		"openai:new-hash":    11,
		"openai:legacy-hash": 11,
	}}}
	svc := &OpenAIGatewayService{cache: cache}
	ctx := withOpenAILegacySessionHash(context.Background(), "legacy-hash")

	err := svc.ClearOpenAIStickySessionAccountIDIfMatches(ctx, nil, "new-hash", 11)

	require.NoError(t, err)
	require.NotContains(t, cache.sessionBindings, "openai:new-hash")
	require.NotContains(t, cache.sessionBindings, "openai:legacy-hash")
	require.Equal(t, []string{"openai:new-hash", "openai:legacy-hash"}, cache.deleteCalls)
}

func TestDeleteStickySessionAccountIDIfMatches_LegacyFailureIsObservable(t *testing.T) {
	legacyErr := errors.New("legacy delete failed")
	cache := &stickyDeleteFaultCache{
		stubGatewayCache: &stubGatewayCache{sessionBindings: map[string]int64{
			"openai:new-hash":    11,
			"openai:legacy-hash": 11,
		}},
		deleteErrors: map[string]error{"openai:legacy-hash": legacyErr},
	}
	svc := &OpenAIGatewayService{cache: cache}
	ctx := withOpenAILegacySessionHash(context.Background(), "legacy-hash")

	err := svc.ClearOpenAIStickySessionAccountIDIfMatches(ctx, nil, "new-hash", 11)

	require.ErrorIs(t, err, legacyErr)
	require.NotContains(t, cache.sessionBindings, "openai:new-hash")
	require.Equal(t, int64(11), cache.sessionBindings["openai:legacy-hash"])
	require.Equal(t, []string{"openai:new-hash", "openai:legacy-hash"}, cache.deleteCalls)
}

func TestDeleteStickySessionAccountIDIfMatches_JoinsPrimaryAndLegacyErrors(t *testing.T) {
	primaryErr := errors.New("primary delete failed")
	legacyErr := errors.New("legacy delete failed")
	cache := &stickyDeleteFaultCache{
		stubGatewayCache: &stubGatewayCache{sessionBindings: map[string]int64{
			"openai:new-hash":    11,
			"openai:legacy-hash": 11,
		}},
		deleteErrors: map[string]error{
			"openai:new-hash":    primaryErr,
			"openai:legacy-hash": legacyErr,
		},
	}
	svc := &OpenAIGatewayService{cache: cache}
	ctx := withOpenAILegacySessionHash(context.Background(), "legacy-hash")

	err := svc.ClearOpenAIStickySessionAccountIDIfMatches(ctx, nil, "new-hash", 11)

	require.ErrorIs(t, err, primaryErr)
	require.ErrorIs(t, err, legacyErr)
	require.Equal(t, []string{"openai:new-hash", "openai:legacy-hash"}, cache.deleteCalls)
}

func TestSnapshotOpenAICompatibilityFallbackMetrics(t *testing.T) {
	before := SnapshotOpenAICompatibilityFallbackMetrics()

	ctx := context.WithValue(context.Background(), ctxkey.ThinkingEnabled, true)
	_, _ = ThinkingEnabledFromContext(ctx)

	after := SnapshotOpenAICompatibilityFallbackMetrics()
	require.GreaterOrEqual(t, after.MetadataLegacyFallbackTotal, before.MetadataLegacyFallbackTotal+1)
	require.GreaterOrEqual(t, after.MetadataLegacyFallbackThinkingEnabledTotal, before.MetadataLegacyFallbackThinkingEnabledTotal+1)
}
