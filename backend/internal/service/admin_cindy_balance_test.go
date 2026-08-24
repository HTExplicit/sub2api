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
