package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	hcplugin "github.com/hashicorp/go-plugin"
	"github.com/stretchr/testify/require"
)

type taxonomyAssignmentPlanFixture struct {
	promptPolicyFixture
	plan extensionv1.TaxonomyAssignmentPlan
}

func (f taxonomyAssignmentPlanFixture) InvokeOperation(ctx context.Context, platform, kind string, in extensionv1.Invocation) (extensionv1.Result, error) {
	if in.Operation == "taxonomy.assignment" {
		raw, err := json.Marshal(f.plan)
		return extensionv1.Result{Payload: raw}, err
	}
	return f.promptPolicyFixture.InvokeOperation(ctx, platform, kind, in)
}

func TestSetAccountTaxonomyRejectsChangedPluginAssignmentBeforeTransaction(t *testing.T) {
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
			previous := processExtensionOperations.Load()
			t.Cleanup(func() { processExtensionOperations.Store(previous) })
			processExtensionOperations.Store(&extensionOperationProvider{invoker: taxonomyAssignmentPlanFixture{plan: tc.plan}})
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			svc := &adminServiceImpl{entClient: client}
			account, err := svc.SetAccountTaxonomy(context.Background(), 42, AccountTaxonomyAssignment{FolderID: &folder, TagIDs: []int64{5, 5, 6}})
			require.ErrorIs(t, err, ErrExtensionOperationUnavailable, "a plugin cannot replace or expand the user's assignment")
			require.Nil(t, account)
			require.NoError(t, mock.ExpectationsWereMet(), "no transaction or query should be started for a changed assignment")
		})
	}
}

func TestTaxonomyAssignmentPreservesPolicyNormalizationAndFailsClosed(t *testing.T) {
	previous := processExtensionOperations.Load()
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	processExtensionOperations.Store(&extensionOperationProvider{invoker: promptPolicyFixture{}})
	folder := int64(7)
	input := AccountTaxonomyAssignment{FolderID: &folder, TagIDs: []int64{6, 5, 6}}
	var plan extensionv1.TaxonomyAssignmentPlan
	require.NoError(t, accountToolsOperation(context.Background(), "*", "*", "taxonomy.assignment", extensionv1.TaxonomyAssignmentPlan{FolderID: input.FolderID, TagIDs: input.TagIDs}, &plan))
	require.Equal(t, []int64{6, 5}, plan.TagIDs, "plugin remains the owner of stable deduplication")
	require.NoError(t, validateTaxonomyAssignmentIntent(input, plan))
	require.NoError(t, validateTaxonomyAssignmentIntent(AccountTaxonomyAssignment{}, extensionv1.TaxonomyAssignmentPlan{}))
	processExtensionOperations.Store(nil)
	svc := &adminServiceImpl{}
	account, err := svc.SetAccountTaxonomy(context.Background(), 42, input)
	require.Error(t, err)
	require.Nil(t, account, "disabled policy must fail before using the nil database, without a host fallback")
	result, err := svc.BulkUpdateAccountTaxonomy(context.Background(), BulkAccountTaxonomyInput{AccountIDs: []int64{42}, FolderAction: "clear"})
	require.Error(t, err)
	require.Nil(t, result)
}

func TestTaxonomyGlobalDeleteAvailabilityMatchesAccountScopeGate(t *testing.T) {
	for _, name := range []string{"taxonomy.folders.delete", "taxonomy.tags.delete"} {
		grant := extensionv1.ResourceGrant{Name: name, Capability: extensionv1.CapabilityAdmin, Permission: "admin"}
		installation := &PluginInstallation{ID: 7, RuntimeGeneration: 1, State: PluginStateEnabled,
			Manifest: PluginManifest{Resources: []extensionv1.ResourceGrant{grant}},
			Bindings: []PluginBinding{{Capability: grant.Capability, Platform: "*", AccountType: "*", Enabled: true, RolloutPercent: 100}}}
		manager := NewPluginManager(&pluginTokenRepository{installation: installation}, pluginTokenEncryptor{}, nil, PluginHostInfo{}, nil)
		runtime := &pluginRuntime{installation: installation, client: hcplugin.NewClient(&hcplugin.ClientConfig{}), done: make(chan struct{})}
		manager.extensions.Store(&pluginExtensionRegistry{installations: map[int64]*PluginInstallation{7: installation}, runtimes: map[int64]*pluginRuntime{7: runtime}})
		for _, scope := range []struct {
			platform, kind string
			rollout        int
			allowed        bool
		}{{"*", "*", 100, true}, {PlatformOpenAI, "*", 100, false}, {"*", AccountTypeOAuth, 100, false}, {"*", "*", 99, false}} {
			installation.Bindings[0].Platform, installation.Bindings[0].AccountType, installation.Bindings[0].RolloutPercent = scope.platform, scope.kind, scope.rollout
			descriptors, err := manager.ResourceDescriptors(context.Background(), 7, "admin", []extensionv1.ResourceDescriptor{{ResourceGrant: grant, AllAccounts: true, Method: "DELETE", Path: "/fixture"}})
			require.NoError(t, err)
			require.Len(t, descriptors, 1)
			require.Equal(t, scope.allowed, descriptors[0].Available, "%s: %+v", name, scope)
			require.Equal(t, scope.allowed, manager.ValidateResourceAccounts(WithPluginExecution(context.Background(), installation), grant.Capability, nil, true) == nil)
		}
	}
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
