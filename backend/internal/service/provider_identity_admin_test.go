//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type recordingAdminCindyMutationRunner struct {
	accountIDs []int64
}

func (r *recordingAdminCindyMutationRunner) Run(
	ctx context.Context,
	accountID int64,
	mutate func(context.Context) (*Account, error),
) (*Account, error) {
	r.accountIDs = append(r.accountIDs, accountID)
	return mutate(ctx)
}

func TestAdminCreateAccountPersistsCanonicalCindyIdentity(t *testing.T) {
	repo := &accountRepoStubForBulkUpdate{createID: 71}
	runner := &recordingAdminCindyMutationRunner{}
	svc := &adminServiceImpl{accountRepo: repo, cindyAccountMutations: runner}

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
	require.Equal(t, []int64{0}, runner.accountIDs)
}

func TestAdminUpdateAccountUsesCanonicalCindyMutationRunner(t *testing.T) {
	accountID := int64(74)
	account := &Account{
		ID: accountID, Name: "cindy", Platform: PlatformCindy,
		WirePlatform: WirePlatformOpenAI, ProviderProfile: ProviderProfileCindyLaxaV1,
		Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_key": "old", "base_url": "https://api.laxarouter.ai"},
		Extra:       map[string]any{},
	}
	repo := &accountRepoStubForBulkUpdate{getByIDAccounts: map[int64]*Account{accountID: account}}
	runner := &recordingAdminCindyMutationRunner{}
	svc := &adminServiceImpl{accountRepo: repo, cindyAccountMutations: runner}

	updated, err := svc.UpdateAccount(context.Background(), accountID, &UpdateAccountInput{
		Credentials: map[string]any{"api_key": "new", "base_url": "https://api.laxarouter.ai"},
	})

	require.NoError(t, err)
	require.NotNil(t, updated)
	require.Equal(t, []int64{accountID}, runner.accountIDs)
}

func TestAdminCreateAccountRejectsCindyCompositeBindingBeforeWrite(t *testing.T) {
	accountRepo := &accountRepoStubForBulkUpdate{createID: 72}
	groupRepo := &groupRepoStubForAdmin{getByIDByID: map[int64]*Group{
		9: {ID: 9, Platform: PlatformComposite},
	}}
	svc := &adminServiceImpl{
		accountRepo: accountRepo, groupRepo: groupRepo,
		cindyAccountMutations: &recordingAdminCindyMutationRunner{},
	}

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
	svc := &adminServiceImpl{accountRepo: accountRepo, cindyAccountMutations: &recordingAdminCindyMutationRunner{}}

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

func TestAdminCreateGroupRejectsCindyFallbackVariants(t *testing.T) {
	t.Parallel()

	fallbackID := int64(92)
	tests := []struct {
		name  string
		input *CreateGroupInput
	}{
		{
			name: "ordinary fallback",
			input: &CreateGroupInput{
				Name: "cindy", Platform: PlatformCindy, RateMultiplier: 1,
				FallbackGroupID: &fallbackID,
			},
		},
		{
			name: "invalid request fallback",
			input: &CreateGroupInput{
				Name: "cindy", Platform: PlatformCindy, RateMultiplier: 1,
				FallbackGroupIDOnInvalidRequest: &fallbackID,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo := &groupRepoStubForAdmin{getByIDByID: map[int64]*Group{
				fallbackID: {ID: fallbackID, Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI, ProviderProfile: ProviderProfileCindyLaxaV1},
			}}
			svc := &adminServiceImpl{groupRepo: repo}

			group, err := svc.CreateGroup(context.Background(), test.input)

			require.ErrorContains(t, err, "cindy groups cannot configure fallback groups")
			require.Nil(t, group)
			require.Nil(t, repo.created)
		})
	}
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
