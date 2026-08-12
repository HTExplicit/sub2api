//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type cindyBalanceAdminRepoStub struct {
	accountRepoStubForClearAccountError
	clearCalls int
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
	return &CindyInsufficientDeleteResult{}, nil
}

func TestAdminServiceClearCindyBalanceInsufficientPreservesManualState(t *testing.T) {
	markedAt := time.Now().UTC()
	repo := &cindyBalanceAdminRepoStub{
		accountRepoStubForClearAccountError: accountRepoStubForClearAccountError{
			account: &Account{
				ID:                         8401,
				Platform:                   PlatformOpenAI,
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
