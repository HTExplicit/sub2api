package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/stretchr/testify/require"
)

func replaceAccountTools(t *testing.T, invoke func(context.Context, extensionv1.Invocation) (extensionv1.Result, error)) {
	t.Helper()
	previous := invokeAccountTools
	t.Cleanup(func() { invokeAccountTools = previous })
	invokeAccountTools = invoke
}

func TestSetAccountTaxonomyRejectsChangedAssignmentPlanBeforeTransaction(t *testing.T) {
	folder, otherFolder := int64(7), int64(8)
	for _, tc := range []struct {
		name string
		plan extensionv1.TaxonomyAssignmentPlan
	}{
		{"changed-folder", extensionv1.TaxonomyAssignmentPlan{FolderID: &otherFolder, TagIDs: []int64{5, 6}}},
		{"cleared-folder", extensionv1.TaxonomyAssignmentPlan{TagIDs: []int64{5, 6}}},
		{"added-tag", extensionv1.TaxonomyAssignmentPlan{FolderID: &folder, TagIDs: []int64{5, 6, 9}}},
		{"omitted-tag", extensionv1.TaxonomyAssignmentPlan{FolderID: &folder, TagIDs: []int64{5}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replaceAccountTools(t, func(context.Context, extensionv1.Invocation) (extensionv1.Result, error) {
				raw, err := json.Marshal(tc.plan)
				return extensionv1.Result{Payload: raw}, err
			})
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			svc := &adminServiceImpl{entClient: client}
			account, err := svc.SetAccountTaxonomy(context.Background(), 42, AccountTaxonomyAssignment{FolderID: &folder, TagIDs: []int64{5, 5, 6}})
			require.ErrorIs(t, err, ErrExtensionOperationUnavailable, "a plan cannot replace or expand the user's assignment")
			require.Nil(t, account)
			require.NoError(t, mock.ExpectationsWereMet(), "no transaction or query should be started for a changed assignment")
		})
	}
}

func TestTaxonomyAssignmentPreservesPolicyNormalizationAndFailsClosed(t *testing.T) {
	folder := int64(7)
	input := AccountTaxonomyAssignment{FolderID: &folder, TagIDs: []int64{6, 5, 6}}
	var plan extensionv1.TaxonomyAssignmentPlan
	require.NoError(t, accountToolsOperation(context.Background(), "taxonomy.assignment", extensionv1.TaxonomyAssignmentPlan{FolderID: input.FolderID, TagIDs: input.TagIDs}, &plan))
	require.Equal(t, []int64{6, 5}, plan.TagIDs, "the policy owns stable deduplication")
	require.NoError(t, validateTaxonomyAssignmentIntent(input, plan))
	require.NoError(t, validateTaxonomyAssignmentIntent(AccountTaxonomyAssignment{}, extensionv1.TaxonomyAssignmentPlan{}))
	replaceAccountTools(t, func(context.Context, extensionv1.Invocation) (extensionv1.Result, error) {
		return extensionv1.Result{}, errors.New("policy failed")
	})
	svc := &adminServiceImpl{}
	account, err := svc.SetAccountTaxonomy(context.Background(), 42, input)
	require.Error(t, err)
	require.Nil(t, account, "a failed policy must stop before using the nil database")
	result, err := svc.BulkUpdateAccountTaxonomy(context.Background(), BulkAccountTaxonomyInput{AccountIDs: []int64{42}, FolderAction: "clear"})
	require.Error(t, err)
	require.Nil(t, result)
}

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
