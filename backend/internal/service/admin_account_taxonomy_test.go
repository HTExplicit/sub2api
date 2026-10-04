package service

import (
	"context"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestAccountTaxonomyRulesRejectAmbiguityAndInvalidAssignments(t *testing.T) {
	require.NoError(t, validateTaxonomyOrderIDs([]int64{1, 2}, []int64{2, 1}))
	require.Equal(t, "ACCOUNT_TAXONOMY_ORDER_INVALID", infraerrors.Reason(validateTaxonomyOrderIDs([]int64{1, 2}, []int64{1, 1})))
	changed := validateTaxonomyOrderIDs([]int64{1, 2, 3}, []int64{2, 1})
	require.Equal(t, "ACCOUNT_TAXONOMY_ORDER_CHANGED", infraerrors.Reason(changed))
	require.Equal(t, 409, infraerrors.Code(changed))

	for _, tc := range []struct {
		input  BulkAccountTaxonomyInput
		reason string
	}{
		{BulkAccountTaxonomyInput{FolderAction: "clear"}, "ACCOUNT_TAXONOMY_TARGET_INVALID"},
		{BulkAccountTaxonomyInput{AccountIDs: []int64{1}, TagAddIDs: []int64{2}, TagRemoveIDs: []int64{2}}, "ACCOUNT_TAXONOMY_TAG_OPERATION_CONFLICT"},
		{BulkAccountTaxonomyInput{AccountIDs: []int64{1}, TagAddIDs: []int64{2}, TagRemoveIDs: []int64{3}}, ""},
	} {
		err := ValidateBulkAccountTaxonomy(tc.input)
		if tc.reason == "" {
			require.NoError(t, err)
			continue
		}
		require.Equal(t, tc.reason, infraerrors.Reason(err))
		require.Equal(t, 400, infraerrors.Code(err))
	}

	// An invalid assignment or delete target is refused before the database is used.
	svc, nonPositive := &adminServiceImpl{}, int64(0)
	account, err := svc.SetAccountTaxonomy(context.Background(), 42, AccountTaxonomyAssignment{TagIDs: []int64{-1}})
	require.Equal(t, "ACCOUNT_TAG_ID_INVALID", infraerrors.Reason(err))
	require.Nil(t, account)
	account, err = svc.SetAccountTaxonomy(context.Background(), 42, AccountTaxonomyAssignment{FolderID: &nonPositive})
	require.Equal(t, "ACCOUNT_FOLDER_ID_INVALID", infraerrors.Reason(err))
	require.Nil(t, account)
	require.Equal(t, "ACCOUNT_TAXONOMY_ID_INVALID", infraerrors.Reason(svc.DeleteAccountFolder(context.Background(), 0, false)))
	require.Equal(t, "ACCOUNT_TAXONOMY_ID_INVALID", infraerrors.Reason(svc.DeleteAccountTag(context.Background(), -1)))
	require.Equal(t, []int64{6, 5}, uniquePositiveIDs([]int64{6, 5, 6}), "repeated tag IDs keep their first position")
}

func TestNormalizeAccountTaxonomyNameTrimsAndBuildsCaseInsensitiveKey(t *testing.T) {
	display, normalized, err := normalizeAccountTaxonomyName("  Production  ")
	require.NoError(t, err)
	require.Equal(t, "Production", display)
	require.Equal(t, "production", normalized)

	for _, invalid := range []string{"   ", string(make([]rune, 101)), "bad\x00name"} {
		_, _, err = normalizeAccountTaxonomyName(invalid)
		require.Equal(t, "ACCOUNT_TAXONOMY_NAME_INVALID", infraerrors.Reason(err))
	}
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
