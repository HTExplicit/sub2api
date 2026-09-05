package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNormalizeAccountTaxonomyNameTrimsAndBuildsCaseInsensitiveKey(t *testing.T) {
	display, normalized, err := normalizeAccountTaxonomyName("  Production  ")
	require.NoError(t, err)
	require.Equal(t, "Production", display)
	require.Equal(t, "production", normalized)

	_, _, err = normalizeAccountTaxonomyName("   ")
	require.Error(t, err)
	_, _, err = normalizeAccountTaxonomyName(string(make([]rune, 101)))
	require.Error(t, err)
}

func TestFilterConsoleAccountsCombinesDerivedStatusAndPlanAcrossFullSet(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Hour)
	accounts := []*Account{
		{ID: 1, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"plan_type": "team"}},
		{ID: 2, Status: StatusActive, Schedulable: false, Credentials: map[string]any{"plan_type": "team"}},
		{ID: 3, Status: StatusActive, Schedulable: true, RateLimitResetAt: &future, Credentials: map[string]any{"plan_type": "pro"}},
		{ID: 4, Status: StatusDisabled, Schedulable: false, Credentials: map[string]any{"plan_type": "team"}},
	}

	filtered := filterConsoleAccounts(accounts, AccountConsoleFilters{
		Statuses: []string{"active", "unschedulable"},
		Plans:    []string{"TEAM"},
	})
	require.Equal(t, []int64{1, 2}, []int64{filtered[0].ID, filtered[1].ID})

	filtered = filterConsoleAccounts(accounts, AccountConsoleFilters{Statuses: []string{"rate_limited"}})
	require.Len(t, filtered, 1)
	require.Equal(t, int64(3), filtered[0].ID)
}

func TestFilterConsoleAccountsCindyQuickViewsUseStrictIdentity(t *testing.T) {
	markedAt := time.Now()
	accounts := []*Account{
		{ID: 1, Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI, ProviderProfile: ProviderProfileCindyLaxaV1, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: cindyCredentials()},
		{ID: 2, Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI, ProviderProfile: ProviderProfileCindyLaxaV1, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: cindyCredentials(), CindyBalanceInsufficientAt: &markedAt},
		{ID: 3, Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI, ProviderProfile: ProviderProfileCindyLaxaV1, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"base_url": "https://api.laxarouter.ai/v1"}, CindyBalanceInsufficientAt: &markedAt},
		{ID: 4, Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI, ProviderProfile: ProviderProfileCindyLaxaV1, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Credentials: cindyCredentials(), CindyBalanceInsufficientAt: &markedAt},
	}

	cindy := filterConsoleAccounts(accounts, AccountConsoleFilters{CindyOnly: true})
	require.Equal(t, []int64{1, 2}, accountIDsForFacetTest(cindy))
	insufficient := filterConsoleAccounts(accounts, AccountConsoleFilters{CindyOnly: true, CindyBalanceStatus: "insufficient"})
	require.Equal(t, []int64{2}, accountIDsForFacetTest(insufficient))
	unschedulable := filterConsoleAccounts(accounts, AccountConsoleFilters{Statuses: []string{"unschedulable"}})
	require.Equal(t, []int64{2}, accountIDsForFacetTest(unschedulable), "terminal markers require the same strict identity as the Cindy quick views")
}

func TestFilterConsoleAccountsKeepsCindyBannedAndBalanceIndependent(t *testing.T) {
	markedAt := time.Now().UTC()
	accounts := []*Account{
		{ID: 1, Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI, ProviderProfile: ProviderProfileCindyLaxaV1, Type: AccountTypeAPIKey, Credentials: cindyCredentials(), CindyBannedAt: &markedAt},
		{ID: 2, Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI, ProviderProfile: ProviderProfileCindyLaxaV1, Type: AccountTypeAPIKey, Credentials: cindyCredentials(), CindyBalanceInsufficientAt: &markedAt},
	}

	banned := filterConsoleAccounts(accounts, AccountConsoleFilters{CindyHealthStatus: "banned"})
	insufficient := filterConsoleAccounts(accounts, AccountConsoleFilters{CindyBalanceStatus: "insufficient"})
	require.Len(t, banned, 1)
	require.Len(t, insufficient, 1)
	require.Equal(t, int64(1), banned[0].ID)
	require.Equal(t, int64(2), insufficient[0].ID)
}

func TestCindyFacetDimensionsConstrainEachOtherIndependently(t *testing.T) {
	now := time.Now().UTC()
	accounts := []*Account{
		{ID: 1, Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI, ProviderProfile: ProviderProfileCindyLaxaV1, Type: AccountTypeAPIKey, Credentials: cindyCredentials(), CindyBannedAt: &now},
		{ID: 2, Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI, ProviderProfile: ProviderProfileCindyLaxaV1, Type: AccountTypeAPIKey, Credentials: cindyCredentials(), CindyBalanceInsufficientAt: &now},
		{ID: 3, Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI, ProviderProfile: ProviderProfileCindyLaxaV1, Type: AccountTypeAPIKey, Credentials: cindyCredentials(), CindyBannedAt: &now, CindyBalanceInsufficientAt: &now},
	}

	bannedMatcher := newAccountFacetMatcher(AccountConsoleFilters{CindyHealthStatus: "banned"})
	balanceOptions := filterAccountsForFacet(accounts, bannedMatcher, accountFacetCindyBalance, now)
	require.Equal(t, []int64{1, 3}, accountIDsForFacetTest(balanceOptions))

	balanceMatcher := newAccountFacetMatcher(AccountConsoleFilters{CindyBalanceStatus: "insufficient"})
	healthOptions := filterAccountsForFacet(accounts, balanceMatcher, accountFacetCindyHealth, now)
	require.Equal(t, []int64{2, 3}, accountIDsForFacetTest(healthOptions))
}

func TestAccountConsoleStatusTreatsBannedAsUnschedulable(t *testing.T) {
	now := time.Now().UTC()
	require.Equal(t, "unschedulable", accountConsoleStatus(&Account{
		Platform: PlatformCindy, Type: AccountTypeAPIKey, Credentials: cindyCredentials(),
		Status: StatusActive, Schedulable: true, CindyBannedAt: &now,
	}, now))
}

func TestAccountConsolePlanUsesProviderBillingSnapshotAndFacetCounts(t *testing.T) {
	account := &Account{
		Platform:    PlatformGrok,
		Credentials: map[string]any{"plan_type": "fallback"},
		Extra:       map[string]any{"grok_billing_snapshot": map[string]any{"plan": "SuperGrok"}},
	}
	require.Equal(t, "SuperGrok", accountConsolePlan(account))
	require.Equal(t, []AccountFacetOption{
		{Value: "active", Label: "active", Count: 3},
		{Value: "disabled", Label: "disabled", Count: 1},
	}, facetOptions(map[string]int{"disabled": 1, "active": 3, "": 4}))
}

func TestAccountFacetMatcherKeepsOtherValuesAvailableForMultiSelect(t *testing.T) {
	folderOne := int64(21)
	accounts := []*Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, ManagementFolderID: &folderOne, Tags: []AccountManagementTag{{ID: 31}}, Credentials: map[string]any{"plan_type": "team"}},
		{ID: 2, Platform: PlatformGrok, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, ManagementFolderID: &folderOne, Tags: []AccountManagementTag{{ID: 31}}, Credentials: map[string]any{"plan_type": "team"}},
		{ID: 3, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Status: StatusDisabled, Schedulable: false, ManagementFolderID: &folderOne, Tags: []AccountManagementTag{{ID: 31}}, Credentials: map[string]any{"plan_type": "team"}},
		{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, ManagementFolderID: &folderOne, Tags: []AccountManagementTag{{ID: 32}}, Credentials: map[string]any{"plan_type": "team"}},
	}
	matcher := newAccountFacetMatcher(AccountConsoleFilters{
		Platforms: []string{PlatformOpenAI}, Statuses: []string{StatusActive}, TagIDs: []int64{31},
	})
	now := time.Now()

	require.Equal(t, []int64{1}, accountIDsForFacetTest(filterAccountsForFacet(accounts, matcher, accountFacetNone, now)))
	require.Equal(t, []int64{1, 2}, accountIDsForFacetTest(filterAccountsForFacet(accounts, matcher, accountFacetPlatforms, now)))
	require.Equal(t, []int64{1, 4}, accountIDsForFacetTest(filterAccountsForFacet(accounts, matcher, accountFacetTags, now)))
}

func accountIDsForFacetTest(accounts []*Account) []int64 {
	ids := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		ids = append(ids, account.ID)
	}
	return ids
}
