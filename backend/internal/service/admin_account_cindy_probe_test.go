package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type adminAccountCindyProbeRepositoryStub struct {
	CindyBalanceProbeRepository
	calls  int
	ids    []int64
	latest map[int64]CindyBalanceProbeLatest
	err    error
}

func (s *adminAccountCindyProbeRepositoryStub) LatestByAccountIDs(_ context.Context, accountIDs []int64) (map[int64]CindyBalanceProbeLatest, error) {
	s.calls++
	s.ids = append([]int64(nil), accountIDs...)
	return s.latest, s.err
}

func TestAdminServiceHydrateCindyBalanceProbeLatest(t *testing.T) {
	checkedAt := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	repo := &adminAccountCindyProbeRepositoryStub{latest: map[int64]CindyBalanceProbeLatest{
		7: {AccountID: 7, JobID: 91, Outcome: "recovered", CheckedAt: checkedAt},
	}}
	staleJobID := int64(1)
	staleOutcome := "stale"
	accounts := []Account{
		{
			ID: 7, Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI, ProviderProfile: ProviderProfileCindyLaxaV1, Type: AccountTypeAPIKey,
			Credentials: map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": "not-returned"},
		},
		{
			ID: 8, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Credentials:            map[string]any{"base_url": "https://ordinary.example.invalid", "api_key": "not-returned"},
			CindyBalanceProbeJobID: &staleJobID, CindyBalanceProbeOutcome: &staleOutcome,
		},
	}

	err := (&adminServiceImpl{cindyBalanceProbeRepo: repo}).hydrateCindyBalanceProbeLatestValues(context.Background(), accounts)
	require.NoError(t, err)
	require.Equal(t, 1, repo.calls)
	require.Equal(t, []int64{7}, repo.ids)
	require.Equal(t, int64(91), *accounts[0].CindyBalanceProbeJobID)
	require.Equal(t, "recovered", *accounts[0].CindyBalanceProbeOutcome)
	require.Equal(t, checkedAt, *accounts[0].CindyBalanceProbeCheckedAt)
	require.Nil(t, accounts[1].CindyBalanceProbeJobID)
	require.Nil(t, accounts[1].CindyBalanceProbeOutcome)
	require.Nil(t, accounts[1].CindyBalanceProbeCheckedAt)
}

func TestAdminServiceHydrateCindyBalanceProbeLatestPropagatesRepositoryError(t *testing.T) {
	wantErr := errors.New("probe query failed")
	repo := &adminAccountCindyProbeRepositoryStub{err: wantErr}
	accounts := []Account{{
		ID: 7, Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI, ProviderProfile: ProviderProfileCindyLaxaV1, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": "not-returned"},
	}}

	err := (&adminServiceImpl{cindyBalanceProbeRepo: repo}).hydrateCindyBalanceProbeLatestValues(context.Background(), accounts)
	require.ErrorIs(t, err, wantErr)
	require.Equal(t, 1, repo.calls)
}
