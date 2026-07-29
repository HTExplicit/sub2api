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
