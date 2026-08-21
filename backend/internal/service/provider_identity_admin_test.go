//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminCreateAccountPersistsCanonicalCindyIdentity(t *testing.T) {
	repo := &accountRepoStubForBulkUpdate{createID: 71}
	svc := &adminServiceImpl{accountRepo: repo}

	account, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name:                 "cindy",
		Platform:             PlatformCindy,
		Type:                 AccountTypeAPIKey,
		Credentials:          map[string]any{"api_key": "secret", "base_url": "https://API.LAXAROUTER.AI/"},
		SkipDefaultGroupBind: true,
	})

	require.NoError(t, err)
	require.Same(t, account, repo.createAccount)
	require.Equal(t, PlatformCindy, account.Platform)
	require.Equal(t, WirePlatformOpenAI, account.WirePlatform)
	require.Equal(t, ProviderProfileCindyLaxaV1, account.ProviderProfile)
}

func TestAdminCreateAccountRejectsCindyCompositeBindingBeforeWrite(t *testing.T) {
	accountRepo := &accountRepoStubForBulkUpdate{createID: 72}
	groupRepo := &groupRepoStubForAdmin{getByIDByID: map[int64]*Group{
		9: {ID: 9, Platform: PlatformComposite},
	}}
	svc := &adminServiceImpl{accountRepo: accountRepo, groupRepo: groupRepo}

	account, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name:                  "cindy",
		Platform:              PlatformCindy,
		Type:                  AccountTypeAPIKey,
		Credentials:           map[string]any{"api_key": "secret", "base_url": "https://api.laxarouter.ai"},
		GroupIDs:              []int64{9},
		SkipDefaultGroupBind:  true,
		SkipMixedChannelCheck: true,
	})

	require.ErrorContains(t, err, "cannot bind to composite")
	require.Nil(t, account)
	require.Nil(t, accountRepo.createAccount)
}

func TestAdminUpdateAccountRejectsMalformedFinalCindyIdentityBeforeWrite(t *testing.T) {
	accountRepo := &accountRepoStubForBulkUpdate{getByIDAccounts: map[int64]*Account{
		73: {
			ID:              73,
			Platform:        PlatformCindy,
			WirePlatform:    WirePlatformOpenAI,
			ProviderProfile: ProviderProfileCindyLaxaV1,
			Type:            AccountTypeAPIKey,
			Status:          StatusActive,
			Credentials:     map[string]any{"api_key": "old", "base_url": "https://api.laxarouter.ai"},
			Extra:           map[string]any{},
		},
	}}
	svc := &adminServiceImpl{accountRepo: accountRepo}

	account, err := svc.UpdateAccount(context.Background(), 73, &UpdateAccountInput{
		Credentials: map[string]any{"base_url": "https://api.laxarouter.ai/v1"},
	})

	require.ErrorContains(t, err, "exact https://api.laxarouter.ai")
	require.Nil(t, account)
	require.Empty(t, accountRepo.updatedAccounts)
}

func TestAdminCreateGroupPersistsCanonicalCindyIdentity(t *testing.T) {
	repo := &groupRepoStubForAdmin{createID: 81}
	svc := &adminServiceImpl{groupRepo: repo}

	group, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name:           "cindy",
		Platform:       PlatformCindy,
		RateMultiplier: 1,
	})

	require.NoError(t, err)
	require.Same(t, group, repo.created)
	require.Equal(t, PlatformCindy, group.Platform)
	require.Equal(t, WirePlatformOpenAI, group.WirePlatform)
	require.Equal(t, ProviderProfileCindyLaxaV1, group.ProviderProfile)
}

func TestAdminUpdateGroupRejectsFinalIdentityMismatchBeforeWrite(t *testing.T) {
	existing := &Group{
		ID:           82,
		Name:         "openai",
		Platform:     PlatformOpenAI,
		WirePlatform: PlatformOpenAI,
		Status:       StatusActive,
	}
	groupRepo := &groupRepoStubForAdmin{
		getByID: existing,
		getAccountIDsByGroupIDsFn: func(groupIDs []int64) ([]int64, error) {
			require.Equal(t, []int64{82}, groupIDs)
			return []int64{91}, nil
		},
	}
	accountRepo := &accountRepoStubForBulkUpdate{getByIDsAccounts: []*Account{{
		ID:           91,
		Platform:     PlatformOpenAI,
		WirePlatform: PlatformOpenAI,
	}}}
	svc := &adminServiceImpl{groupRepo: groupRepo, accountRepo: accountRepo}

	group, err := svc.UpdateGroup(context.Background(), 82, &UpdateGroupInput{Platform: PlatformCindy})

	require.ErrorContains(t, err, "provider identity")
	require.Nil(t, group)
	require.Nil(t, groupRepo.updated)
}
