package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type providerIdentityGroupRepoStub struct {
	GroupRepository
	groups map[int64]*Group
}

func (s providerIdentityGroupRepoStub) GetByID(_ context.Context, id int64) (*Group, error) {
	group, ok := s.groups[id]
	if !ok {
		return nil, ErrGroupNotFound
	}
	return group, nil
}

func TestResolveAccountProviderIdentityRequiresFirstClassCindy(t *testing.T) {
	tests := []struct {
		name         string
		platform     string
		accountType  string
		baseURL      string
		wantPlatform string
		wantWire     string
		wantProfile  string
		wantErr      bool
	}{
		{
			name:         "legacy exact laxa remains ordinary openai at runtime",
			platform:     PlatformOpenAI,
			accountType:  AccountTypeAPIKey,
			baseURL:      "https://api.laxarouter.ai/",
			wantPlatform: PlatformOpenAI,
			wantWire:     PlatformOpenAI,
		},
		{
			name:         "first class cindy",
			platform:     PlatformCindy,
			accountType:  AccountTypeAPIKey,
			baseURL:      "https://api.laxarouter.ai",
			wantPlatform: PlatformCindy,
			wantWire:     WirePlatformOpenAI,
			wantProfile:  ProviderProfileCindyLaxaV1,
		},
		{
			name:         "adaptive provider keeps its own identity",
			platform:     PlatformKimi,
			accountType:  AccountTypeAPIKey,
			baseURL:      DefaultKimiPayGBaseURL,
			wantPlatform: PlatformKimi,
			wantWire:     PlatformKimi,
		},
		{
			name:        "cindy requires exact root",
			platform:    PlatformCindy,
			accountType: AccountTypeAPIKey,
			baseURL:     "https://api.laxarouter.ai/v1",
			wantErr:     true,
		},
		{
			name:        "cindy rejects oauth",
			platform:    PlatformCindy,
			accountType: AccountTypeOAuth,
			baseURL:     "https://api.laxarouter.ai",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			platform, wire, profile, err := ResolveAccountProviderIdentity(tt.platform, tt.accountType, map[string]any{
				"base_url": tt.baseURL,
			})
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantPlatform, platform)
			require.Equal(t, tt.wantWire, wire)
			require.Equal(t, tt.wantProfile, profile)
		})
	}
}

func TestProviderIdentityCompatibleRequiresEveryAxis(t *testing.T) {
	account := &Account{
		Platform:        PlatformCindy,
		Type:            AccountTypeAPIKey,
		Credentials:     map[string]any{"base_url": "https://api.laxarouter.ai"},
		WirePlatform:    WirePlatformOpenAI,
		ProviderProfile: ProviderProfileCindyLaxaV1,
	}
	group := &Group{
		Platform:        PlatformCindy,
		WirePlatform:    WirePlatformOpenAI,
		ProviderProfile: ProviderProfileCindyLaxaV1,
	}
	require.True(t, ProviderIdentityCompatible(account, group))

	ordinaryOpenAI := *group
	ordinaryOpenAI.Platform = PlatformOpenAI
	ordinaryOpenAI.ProviderProfile = ""
	require.False(t, ProviderIdentityCompatible(account, &ordinaryOpenAI))

	otherProfile := *group
	otherProfile.ProviderProfile = "cindy_future_v2"
	require.False(t, ProviderIdentityCompatible(account, &otherProfile))

	otherWire := *group
	otherWire.WirePlatform = PlatformAnthropic
	require.False(t, ProviderIdentityCompatible(account, &otherWire))
}

func TestCanonicalCindyProviderIdentityRejectsLegacyOpenAIRow(t *testing.T) {
	legacy := &Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: cindyCredentials(),
	}
	require.True(t, IsCindyAPIKeyAccount(legacy.Platform, legacy.Type, legacy.Credentials),
		"legacy account-level behavior must remain available")
	require.False(t, hasCanonicalCindyProviderIdentity(legacy),
		"legacy OpenAI rows must not cross the canonical Cindy provider boundary")

	canonical := *legacy
	canonical.Platform = PlatformCindy
	canonical.WirePlatform = WirePlatformOpenAI
	canonical.ProviderProfile = ProviderProfileCindyLaxaV1
	require.True(t, hasCanonicalCindyProviderIdentity(&canonical))
}

func TestCanonicalCindySchedulerRequestRejectsLegacyOpenAIRow(t *testing.T) {
	legacy := &Account{
		ID:          71001,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: cindyCredentials(),
	}

	compatible, reason := (&defaultOpenAIAccountScheduler{}).isAccountRequestCompatibleReason(
		context.Background(), legacy, OpenAIAccountScheduleRequest{Platform: PlatformCindy},
	)
	require.False(t, compatible)
	require.Equal(t, "provider_identity_mismatch", reason)
}

func TestValidateProviderIdentityGroupBindingsExcludesCindyFromComposite(t *testing.T) {
	account := &Account{
		Platform:        PlatformCindy,
		WirePlatform:    WirePlatformOpenAI,
		ProviderProfile: ProviderProfileCindyLaxaV1,
	}
	repo := providerIdentityGroupRepoStub{groups: map[int64]*Group{
		1: {ID: 1, Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI, ProviderProfile: ProviderProfileCindyLaxaV1},
		2: {ID: 2, Platform: PlatformOpenAI, WirePlatform: WirePlatformOpenAI},
		3: {ID: 3, Platform: PlatformComposite},
	}}

	require.NoError(t, validateProviderIdentityGroupBindings(context.Background(), repo, account, []int64{1}))
	require.Error(t, validateProviderIdentityGroupBindings(context.Background(), repo, account, []int64{2}))
	require.Error(t, validateProviderIdentityGroupBindings(context.Background(), repo, account, []int64{3}))
	require.NoError(t, validateProviderIdentityGroupBindings(context.Background(), repo, &Account{
		Platform: PlatformOpenAI,
	}, []int64{3}))
}

func TestCindySchedulingPreservesSemanticPlatformAndRequiresCanonicalAxes(t *testing.T) {
	account := &Account{
		Platform:        PlatformCindy,
		WirePlatform:    WirePlatformOpenAI,
		ProviderProfile: ProviderProfileCindyLaxaV1,
		Type:            AccountTypeAPIKey,
		Status:          StatusActive,
		Schedulable:     true,
		Credentials: map[string]any{
			"api_key":  "test-key",
			"base_url": "https://api.laxarouter.ai",
		},
	}

	require.Equal(t, PlatformCindy, NormalizeOpenAICompatiblePlatform(PlatformCindy))
	require.True(t, isOpenAICompatibleAccountEligibleForRequestBeforeProfit(
		context.Background(), account, PlatformCindy, "", false, "",
	))

	wrongProfile := *account
	wrongProfile.ProviderProfile = "cindy_future_v2"
	require.False(t, isOpenAICompatibleAccountEligibleForRequestBeforeProfit(
		context.Background(), &wrongProfile, PlatformCindy, "", false, "",
	))

	wrongWire := *account
	wrongWire.WirePlatform = PlatformAnthropic
	require.False(t, isOpenAICompatibleAccountEligibleForRequestBeforeProfit(
		context.Background(), &wrongWire, PlatformCindy, "", false, "",
	))

	ordinaryOpenAI := *account
	ordinaryOpenAI.Platform = PlatformOpenAI
	ordinaryOpenAI.ProviderProfile = ""
	require.False(t, isOpenAICompatibleAccountEligibleForRequestBeforeProfit(
		context.Background(), &ordinaryOpenAI, PlatformCindy, "", false, "",
	))
}
