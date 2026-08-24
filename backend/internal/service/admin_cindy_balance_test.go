//go:build unit

package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type cindyBalanceAdminRepoStub struct {
	accountRepoStubForClearAccountError
	clearCalls   int
	deleteResult *CindyInsufficientDeleteResult
}

type cindyBalanceRecoveryInterleavingCache struct {
	*stubGatewayCache
	terminalPending *CindyHealthEpisode
	afterClear      func()
}

func (c *cindyBalanceRecoveryInterleavingCache) GetCindyHealthTerminalPending(
	_ context.Context,
	_ int64,
	_ string,
) (*CindyHealthEpisode, error) {
	if c.terminalPending == nil {
		return nil, nil
	}
	episode := *c.terminalPending
	return &episode, nil
}

func (c *cindyBalanceRecoveryInterleavingCache) ClearCindyHealthTerminalPendingIfMatch(
	_ context.Context,
	episode CindyHealthEpisode,
) (bool, error) {
	current := c.terminalPending
	if current == nil || current.AccountID != episode.AccountID || current.Generation != episode.Generation ||
		current.EpisodeID != episode.EpisodeID || current.Fingerprint != episode.Fingerprint || current.Status != episode.Status {
		return false, nil
	}
	c.terminalPending = nil
	if c.afterClear != nil {
		c.afterClear()
	}
	return true, nil
}

func (r *cindyBalanceAdminRepoStub) MarkCindyBalanceInsufficient(context.Context, int64, time.Time) (bool, error) {
	return false, nil
}

func (r *cindyBalanceAdminRepoStub) ClearCindyBalanceInsufficient(_ context.Context, _ int64) (bool, error) {
	r.clearCalls++
	r.account.CindyBalanceInsufficientAt = nil
	return true, nil
}

func (r *cindyBalanceAdminRepoStub) PreviewCindyInsufficientDeletion(context.Context) (*CindyInsufficientDeletePreview, error) {
	return &CindyInsufficientDeletePreview{}, nil
}

func (r *cindyBalanceAdminRepoStub) DeleteCindyInsufficient(context.Context, int, string) (*CindyInsufficientDeleteResult, error) {
	if r.deleteResult != nil {
		return r.deleteResult, nil
	}
	return &CindyInsufficientDeleteResult{}, nil
}

func (r *cindyBalanceAdminRepoStub) PreviewCindyBannedDeletion(context.Context) (*CindyInsufficientDeletePreview, error) {
	return &CindyInsufficientDeletePreview{}, nil
}

func (r *cindyBalanceAdminRepoStub) DeleteCindyBanned(context.Context, int, string) (*CindyInsufficientDeleteResult, error) {
	return r.deleteResult, nil
}

func TestAdminServiceClearCindyBalanceInsufficientPreservesManualState(t *testing.T) {
	markedAt := time.Now().UTC()
	repo := &cindyBalanceAdminRepoStub{
		accountRepoStubForClearAccountError: accountRepoStubForClearAccountError{
			account: &Account{
				ID:                         8401,
				Platform:                   PlatformCindy,
				WirePlatform:               WirePlatformOpenAI,
				ProviderProfile:            ProviderProfileCindyLaxaV1,
				Type:                       AccountTypeAPIKey,
				Status:                     StatusDisabled,
				Schedulable:                false,
				Credentials:                cindyCredentials(),
				CindyBalanceInsufficientAt: &markedAt,
			},
		},
	}
	blocker := &runtimeBlockRecorder{}
	svc := &adminServiceImpl{accountRepo: repo, runtimeBlocker: blocker}

	updated, err := svc.ClearCindyBalanceInsufficient(context.Background(), 8401)

	require.NoError(t, err)
	require.Equal(t, 1, repo.clearCalls)
	require.Nil(t, updated.CindyBalanceInsufficientAt)
	require.Equal(t, StatusDisabled, updated.Status)
	require.False(t, updated.Schedulable)
	require.Equal(t, []int64{8401}, blocker.clearedIDs)
	require.False(t, updated.IsSchedulable(), "clearing the Cindy marker must not re-enable a manually disabled account")
}

func TestAdminBalanceRecoveryDoesNotDeleteConcurrentReplacementOrClearItsRuntimeBlock(t *testing.T) {
	oldEpisode := &CindyHealthEpisode{
		AccountID: 8404, Generation: 7, EpisodeID: "old", Fingerprint: strings.Repeat("a", 64),
		Status: CindyHealthStatusBalanceInsufficient, Evidence: CindyHealthEvidenceExactBudget, ObservedAt: time.Now().UTC(),
	}
	newEpisode := *oldEpisode
	newEpisode.EpisodeID = "replacement"
	repo := &cindyBalanceAdminRepoStub{accountRepoStubForClearAccountError: accountRepoStubForClearAccountError{
		account: &Account{ID: 8404, Platform: PlatformCindy, Type: AccountTypeAPIKey, Credentials: cindyCredentials()},
	}}
	blocker := &runtimeBlockRecorder{terminalPending: oldEpisode, replacementOnClear: &newEpisode}
	svc := &adminServiceImpl{accountRepo: repo, runtimeBlocker: blocker}

	_, err := svc.ClearCindyBalanceInsufficient(context.Background(), 8404)

	require.NoError(t, err)
	require.Equal(t, "replacement", blocker.terminalPending.EpisodeID)
	require.Empty(t, blocker.clearedIDs, "replacement pending owns the runtime block")
}

func TestAdminBalanceRecoveryDoesNotClearRuntimeReplacementAfterRedisCAS(t *testing.T) {
	account, identity := newHealthTestAccount(t, 8405, "admin-runtime-race")
	markedAt := time.Now().UTC()
	account.CindyBalanceInsufficientAt = &markedAt
	oldEpisode := CindyHealthEpisode{
		AccountID: account.ID, Generation: identity.Generation, EpisodeID: "admin-old",
		Fingerprint: identity.Fingerprint, Status: CindyHealthStatusBalanceInsufficient,
		Evidence: CindyHealthEvidenceExactBudget, ObservedAt: markedAt,
	}
	newEpisode := oldEpisode
	newEpisode.EpisodeID = "admin-replacement"
	newEpisode.ObservedAt = markedAt.Add(time.Second)
	cache := &cindyBalanceRecoveryInterleavingCache{
		stubGatewayCache: &stubGatewayCache{},
		terminalPending:  &oldEpisode,
	}
	gateway := &OpenAIGatewayService{cache: cache}
	require.True(t, gateway.BlockCindyHealthEpisode(account, oldEpisode, "cindy_balance_insufficient"))
	cache.afterClear = func() {
		require.True(t, gateway.BlockCindyHealthEpisode(account, newEpisode, "cindy_balance_insufficient"))
	}
	repo := &cindyBalanceAdminRepoStub{accountRepoStubForClearAccountError: accountRepoStubForClearAccountError{
		account: account,
	}}
	svc := &adminServiceImpl{accountRepo: repo, runtimeBlocker: gateway}

	_, err := svc.ClearCindyBalanceInsufficient(context.Background(), account.ID)

	require.NoError(t, err)
	require.True(t, gateway.isOpenAIAccountRuntimeBlocked(account), "the replacement episode must retain the runtime block")
	raw, loaded := gateway.cindyHealthRuntimeBlocks.Load(account.ID)
	require.True(t, loaded)
	block, ok := raw.(cindyHealthRuntimeBlock)
	require.True(t, ok)
	require.Equal(t, newEpisode.EpisodeID, block.Episode.EpisodeID)
}

func TestAdminServiceBannedCleanupClearsAllCindyRuntimeStateAfterCommit(t *testing.T) {
	repo := &cindyBalanceAdminRepoStub{deleteResult: &CindyInsufficientDeleteResult{
		DeletedCount: 1, DeletedAccountIDs: []int64{8402, 8403},
	}}
	blocker := &runtimeBlockRecorder{}
	svc := &adminServiceImpl{accountRepo: repo, runtimeBlocker: blocker}

	result, err := svc.DeleteCindyBanned(context.Background(), 1, "fingerprint")

	require.NoError(t, err)
	require.Equal(t, 1, result.DeletedCount)
	require.Equal(t, []int64{8402, 8403}, blocker.clearedCindyHealthIDs)
	require.Equal(t, []int64{8402, 8403}, blocker.clearedIDs)
}
