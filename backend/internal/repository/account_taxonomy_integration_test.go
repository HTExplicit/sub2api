//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	dbaccountgroup "github.com/Wei-Shaw/sub2api/ent/accountgroup"
	dbaccounttagbinding "github.com/Wei-Shaw/sub2api/ent/accounttagbinding"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type accountTaxonomyIntegrationAdmin interface {
	service.AdminService
	ListAccountFolders(context.Context) ([]service.AccountManagementFolder, error)
	CreateAccountFolder(context.Context, service.AccountTaxonomyInput) (*service.AccountManagementFolder, error)
	UpdateAccountFolder(context.Context, int64, service.AccountTaxonomyInput) (*service.AccountManagementFolder, error)
	DeleteAccountFolder(context.Context, int64, bool) error
	ReorderAccountFolders(context.Context, []int64) ([]service.AccountManagementFolder, error)
	ListAccountTags(context.Context) ([]service.AccountManagementTag, error)
	CreateAccountTag(context.Context, service.AccountTaxonomyInput) (*service.AccountManagementTag, error)
	UpdateAccountTag(context.Context, int64, service.AccountTaxonomyInput) (*service.AccountManagementTag, error)
	DeleteAccountTag(context.Context, int64) error
	ReorderAccountTags(context.Context, []int64) ([]service.AccountManagementTag, error)
	SetAccountTaxonomy(context.Context, int64, service.AccountTaxonomyAssignment) (*service.Account, error)
	ListAccountsConsole(context.Context, int, int, service.AccountConsoleFilters) ([]service.Account, int64, error)
	GetAccountConsoleFacets(context.Context, service.AccountConsoleFilters) (*service.AccountConsoleFacets, error)
	BulkUpdateAccountTaxonomy(context.Context, service.BulkAccountTaxonomyInput) (*service.BulkAccountTaxonomyResult, error)
}

func newAccountTaxonomyIntegrationAdmin(t *testing.T, prefix string) (*dbent.Client, accountTaxonomyIntegrationAdmin) {
	t.Helper()
	client := testEntClient(t)
	repo := newAccountRepositoryWithSQL(client, integrationDB, nil)
	base := service.NewAdminService(
		nil, nil, repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		client, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	admin, ok := base.(accountTaxonomyIntegrationAdmin)
	require.True(t, ok, "admin service must expose account console operations")

	t.Cleanup(func() {
		ctx := context.Background()
		for _, statement := range []string{
			`DELETE FROM accounts WHERE name LIKE $1`,
			`DELETE FROM proxies WHERE name LIKE $1`,
			`DELETE FROM account_tags WHERE name LIKE $1`,
			`DELETE FROM account_folders WHERE name LIKE $1`,
			`DELETE FROM groups WHERE name LIKE $1`,
		} {
			_, err := integrationDB.ExecContext(ctx, statement, prefix+"%")
			require.NoError(t, err)
		}
	})
	return client, admin
}

func taxonomyIntegrationPrefix(label string) string {
	return fmt.Sprintf("taxonomy-it-%s-%d-", label, time.Now().UnixNano())
}

func TestAccountTaxonomyMigrationAndRelationsIntegration(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()

	folder, err := client.AccountFolder.Create().
		SetName("Production").SetNormalizedName("production").Save(ctx)
	require.NoError(t, err)
	tag, err := client.AccountTag.Create().
		SetName("Paid").SetNormalizedName("paid").Save(ctx)
	require.NoError(t, err)
	account, err := client.Account.Create().
		SetName("taxonomy-test").
		SetPlatform(service.PlatformOpenAI).
		SetType(service.AccountTypeOAuth).
		SetManagementFolderID(folder.ID).
		AddTagIDs(tag.ID).
		Save(ctx)
	require.NoError(t, err)

	rowWithTaxonomy, err := client.Account.Query().
		Where(dbaccount.IDEQ(account.ID)).
		WithManagementFolder().
		WithTags().
		Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, rowWithTaxonomy.Edges.ManagementFolder)
	require.Equal(t, folder.ID, rowWithTaxonomy.Edges.ManagementFolder.ID)
	require.Len(t, rowWithTaxonomy.Edges.Tags, 1)
	require.Equal(t, tag.ID, rowWithTaxonomy.Edges.Tags[0].ID)
	require.Equal(t, "Paid", rowWithTaxonomy.Edges.Tags[0].Name)

	repo := newAccountRepositoryWithSQL(client, nil, nil)
	loaded, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, &folder.ID, loaded.ManagementFolderID)
	require.Nil(t, loaded.ManagementFolder, "scheduler/shared repository paths must not hydrate management folders")
	require.Empty(t, loaded.Tags, "scheduler/shared repository paths must not hydrate management tags")

	// General account updates must not mutate management-only taxonomy. Some
	// background refresh paths carry a partial Account value and cannot express
	// whether a nil folder means "clear" or "not loaded".
	loaded.Name = "taxonomy-test-updated"
	loaded.ManagementFolderID = nil
	require.NoError(t, repo.Update(ctx, loaded))
	row, err := client.Account.Get(ctx, account.ID)
	require.NoError(t, err)
	require.NotNil(t, row.ManagementFolderID)
	require.Equal(t, folder.ID, *row.ManagementFolderID)

	_, err = client.Account.UpdateOneID(account.ID).ClearManagementFolderID().Save(ctx)
	require.NoError(t, err)
	require.NoError(t, client.AccountFolder.DeleteOneID(folder.ID).Exec(ctx))
	row, err = client.Account.Get(ctx, account.ID)
	require.NoError(t, err)
	require.Nil(t, row.ManagementFolderID)

	require.NoError(t, client.AccountTag.DeleteOneID(tag.ID).Exec(ctx))
	bindings, err := client.AccountTagBinding.Query().
		Where(dbaccounttagbinding.AccountIDEQ(account.ID)).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, bindings)
}

func TestAccountTaxonomyCRUDAndAdminHydrationIntegration(t *testing.T) {
	ctx := context.Background()
	prefix := taxonomyIntegrationPrefix("crud")
	client, admin := newAccountTaxonomyIntegrationAdmin(t, prefix)

	folder, err := admin.CreateAccountFolder(ctx, service.AccountTaxonomyInput{Name: "  " + prefix + "Production  ", SortOrder: 20})
	require.NoError(t, err)
	require.Equal(t, prefix+"Production", folder.Name)
	_, err = admin.CreateAccountFolder(ctx, service.AccountTaxonomyInput{Name: prefix + "PRODUCTION"})
	require.ErrorIs(t, err, service.ErrAccountFolderExists)

	tag, err := admin.CreateAccountTag(ctx, service.AccountTaxonomyInput{Name: "  " + prefix + "Paid  ", SortOrder: 30})
	require.NoError(t, err)
	_, err = admin.CreateAccountTag(ctx, service.AccountTaxonomyInput{Name: prefix + "PAID"})
	require.ErrorIs(t, err, service.ErrAccountTagExists)

	row, err := client.Account.Create().
		SetName(prefix + "account").
		SetPlatform(service.PlatformOpenAI).
		SetType(service.AccountTypeOAuth).
		SetCredentials(map[string]any{"plan_type": "team"}).
		Save(ctx)
	require.NoError(t, err)

	assigned, err := admin.SetAccountTaxonomy(ctx, row.ID, service.AccountTaxonomyAssignment{
		FolderID: &folder.ID,
		TagIDs:   []int64{tag.ID, tag.ID},
	})
	require.NoError(t, err)
	require.NotNil(t, assigned.ManagementFolder)
	require.Equal(t, folder.ID, assigned.ManagementFolder.ID)
	require.Len(t, assigned.Tags, 1)
	require.Equal(t, tag.ID, assigned.Tags[0].ID)

	folder, err = admin.UpdateAccountFolder(ctx, folder.ID, service.AccountTaxonomyInput{Name: prefix + "Critical", SortOrder: 5})
	require.NoError(t, err)
	require.Equal(t, 1, folder.AccountCount)
	tag, err = admin.UpdateAccountTag(ctx, tag.ID, service.AccountTaxonomyInput{Name: prefix + "Priority", SortOrder: 4})
	require.NoError(t, err)
	require.Equal(t, 1, tag.AccountCount)

	loaded, err := admin.GetAccount(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, prefix+"Critical", loaded.ManagementFolder.Name)
	require.Equal(t, prefix+"Priority", loaded.Tags[0].Name)

	loadedMany, err := admin.GetAccountsByIDs(ctx, []int64{row.ID})
	require.NoError(t, err)
	require.Len(t, loadedMany, 1)
	require.NotNil(t, loadedMany[0].ManagementFolder)
	require.Len(t, loadedMany[0].Tags, 1)

	legacyList, total, err := admin.ListAccounts(ctx, 1, 20, "", "", "", prefix+"account", 0, "", "name", "asc")
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, legacyList, 1)
	require.NotNil(t, legacyList[0].ManagementFolder)
	require.Len(t, legacyList[0].Tags, 1)

	folders, err := admin.ListAccountFolders(ctx)
	require.NoError(t, err)
	require.Contains(t, folders, *folder)
	tags, err := admin.ListAccountTags(ctx)
	require.NoError(t, err)
	require.Contains(t, tags, *tag)

	err = admin.DeleteAccountFolder(ctx, folder.ID, false)
	require.Error(t, err, "a non-empty folder must require explicit confirmation")
	require.NoError(t, admin.DeleteAccountFolder(ctx, folder.ID, true))
	loaded, err = admin.GetAccount(ctx, row.ID)
	require.NoError(t, err)
	require.Nil(t, loaded.ManagementFolder)
	require.Len(t, loaded.Tags, 1)

	require.NoError(t, admin.DeleteAccountTag(ctx, tag.ID))
	loaded, err = admin.GetAccount(ctx, row.ID)
	require.NoError(t, err)
	require.Empty(t, loaded.Tags)
}

func TestAccountTaxonomyCombinedFiltersAndFacetsIntegration(t *testing.T) {
	ctx := context.Background()
	prefix := taxonomyIntegrationPrefix("facets")
	client, admin := newAccountTaxonomyIntegrationAdmin(t, prefix)

	folderA, err := admin.CreateAccountFolder(ctx, service.AccountTaxonomyInput{Name: prefix + "Folder A"})
	require.NoError(t, err)
	folderB, err := admin.CreateAccountFolder(ctx, service.AccountTaxonomyInput{Name: prefix + "Folder B"})
	require.NoError(t, err)
	tagRed, err := admin.CreateAccountTag(ctx, service.AccountTaxonomyInput{Name: prefix + "Red"})
	require.NoError(t, err)
	tagBlue, err := admin.CreateAccountTag(ctx, service.AccountTaxonomyInput{Name: prefix + "Blue"})
	require.NoError(t, err)
	proxy, err := client.Proxy.Create().
		SetName(prefix + "Proxy").
		SetProtocol("socks5h").
		SetHost("127.0.0.1").
		SetPort(1080).
		Save(ctx)
	require.NoError(t, err)

	type accountFixture struct {
		label      string
		platform   string
		accountTyp string
		status     string
		plan       string
		proxyID    *int64
		folderID   *int64
		tagIDs     []int64
	}
	proxyID := proxy.ID
	fixtures := []accountFixture{
		{label: "match", platform: service.PlatformOpenAI, accountTyp: service.AccountTypeOAuth, status: service.StatusActive, plan: "team", proxyID: &proxyID, folderID: &folderA.ID, tagIDs: []int64{tagRed.ID}},
		{label: "platform", platform: service.PlatformGrok, accountTyp: service.AccountTypeOAuth, status: service.StatusActive, plan: "team", proxyID: &proxyID, folderID: &folderA.ID, tagIDs: []int64{tagRed.ID}},
		{label: "type", platform: service.PlatformOpenAI, accountTyp: service.AccountTypeAPIKey, status: service.StatusActive, plan: "team", proxyID: &proxyID, folderID: &folderA.ID, tagIDs: []int64{tagRed.ID}},
		{label: "status", platform: service.PlatformOpenAI, accountTyp: service.AccountTypeOAuth, status: service.StatusDisabled, plan: "team", proxyID: &proxyID, folderID: &folderA.ID, tagIDs: []int64{tagRed.ID}},
		{label: "plan", platform: service.PlatformOpenAI, accountTyp: service.AccountTypeOAuth, status: service.StatusActive, plan: "pro", proxyID: &proxyID, folderID: &folderA.ID, tagIDs: []int64{tagRed.ID}},
		{label: "proxy", platform: service.PlatformOpenAI, accountTyp: service.AccountTypeOAuth, status: service.StatusActive, plan: "team", folderID: &folderA.ID, tagIDs: []int64{tagRed.ID}},
		{label: "folder", platform: service.PlatformOpenAI, accountTyp: service.AccountTypeOAuth, status: service.StatusActive, plan: "team", proxyID: &proxyID, folderID: &folderB.ID, tagIDs: []int64{tagRed.ID}},
		{label: "tag", platform: service.PlatformOpenAI, accountTyp: service.AccountTypeOAuth, status: service.StatusActive, plan: "team", proxyID: &proxyID, folderID: &folderA.ID, tagIDs: []int64{tagBlue.ID}},
		{label: "uncategorized", platform: service.PlatformOpenAI, accountTyp: service.AccountTypeOAuth, status: service.StatusActive, plan: "team", proxyID: &proxyID, tagIDs: []int64{tagRed.ID}},
	}
	accountIDs := make(map[string]int64, len(fixtures))
	for _, fixture := range fixtures {
		builder := client.Account.Create().
			SetName(prefix + fixture.label).
			SetPlatform(fixture.platform).
			SetType(fixture.accountTyp).
			SetStatus(fixture.status).
			SetCredentials(map[string]any{"plan_type": fixture.plan})
		if fixture.proxyID != nil {
			builder.SetProxyID(*fixture.proxyID)
		}
		row, createErr := builder.Save(ctx)
		require.NoError(t, createErr)
		accountIDs[fixture.label] = row.ID
		_, assignErr := admin.SetAccountTaxonomy(ctx, row.ID, service.AccountTaxonomyAssignment{FolderID: fixture.folderID, TagIDs: fixture.tagIDs})
		require.NoError(t, assignErr)
	}

	filters := service.AccountConsoleFilters{
		Search: prefix, Platforms: []string{service.PlatformOpenAI}, Types: []string{service.AccountTypeOAuth},
		Statuses: []string{service.StatusActive}, Plans: []string{"TEAM"}, ProxyIDs: []int64{proxy.ID},
		FolderIDs: []int64{folderA.ID}, TagIDs: []int64{tagRed.ID}, SortBy: "name", SortOrder: "asc",
	}
	accounts, total, err := admin.ListAccountsConsole(ctx, 1, 20, filters)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, accounts, 1)
	require.Equal(t, accountIDs["match"], accounts[0].ID)
	require.Equal(t, folderA.ID, accounts[0].ManagementFolder.ID)
	require.Equal(t, tagRed.ID, accounts[0].Tags[0].ID)
	require.Equal(t, proxy.ID, accounts[0].Proxy.ID)

	facets, err := admin.GetAccountConsoleFacets(ctx, filters)
	require.NoError(t, err)
	require.Equal(t, 3, facets.Total)
	require.Equal(t, 1, facets.UncategorizedCount)
	require.Equal(t, 1, integrationFacetCount(facets.Platforms, service.PlatformOpenAI))
	require.Equal(t, 1, integrationFacetCount(facets.Platforms, service.PlatformGrok))
	require.Equal(t, 1, integrationFacetCount(facets.Types, service.AccountTypeOAuth))
	require.Equal(t, 1, integrationFacetCount(facets.Types, service.AccountTypeAPIKey))
	require.Equal(t, 1, integrationFacetCount(facets.Statuses, service.StatusActive))
	require.Equal(t, 1, integrationFacetCount(facets.Statuses, service.StatusDisabled))
	require.Equal(t, 1, integrationFacetCount(facets.Plans, "team"))
	require.Equal(t, 1, integrationFacetCount(facets.Plans, "pro"))
	require.Equal(t, 1, integrationFacetCount(facets.Proxies, fmt.Sprint(proxy.ID)))
	require.Equal(t, 1, integrationFacetCount(facets.Proxies, "direct"))
	require.Equal(t, 1, integrationTaxonomyCount(facets.Folders, folderA.ID))
	require.Equal(t, 1, integrationTaxonomyCount(facets.Folders, folderB.ID))
	require.Equal(t, facets.Total, facets.UncategorizedCount+
		integrationTaxonomyCount(facets.Folders, folderA.ID)+
		integrationTaxonomyCount(facets.Folders, folderB.ID))
	require.Equal(t, 1, integrationTagCount(facets.Tags, tagRed.ID))
	require.Equal(t, 1, integrationTagCount(facets.Tags, tagBlue.ID))
}

func TestAccountTaxonomyBulkUpdateAndOrderingIntegration(t *testing.T) {
	ctx := context.Background()
	prefix := taxonomyIntegrationPrefix("bulk")
	client, admin := newAccountTaxonomyIntegrationAdmin(t, prefix)

	folderA, err := admin.CreateAccountFolder(ctx, service.AccountTaxonomyInput{Name: prefix + "Folder A"})
	require.NoError(t, err)
	folderB, err := admin.CreateAccountFolder(ctx, service.AccountTaxonomyInput{Name: prefix + "Folder B"})
	require.NoError(t, err)
	tagA, err := admin.CreateAccountTag(ctx, service.AccountTaxonomyInput{Name: prefix + "Tag A"})
	require.NoError(t, err)
	tagB, err := admin.CreateAccountTag(ctx, service.AccountTaxonomyInput{Name: prefix + "Tag B"})
	require.NoError(t, err)
	tagC, err := admin.CreateAccountTag(ctx, service.AccountTaxonomyInput{Name: prefix + "Tag C"})
	require.NoError(t, err)
	routeGroup, err := client.Group.Create().
		SetName(prefix + "Route Group").
		SetPlatform(service.PlatformOpenAI).
		Save(ctx)
	require.NoError(t, err)

	accountIDs := make([]int64, 0, 2)
	for _, suffix := range []string{"one", "two"} {
		row, createErr := client.Account.Create().
			SetName(prefix + suffix).
			SetPlatform(service.PlatformOpenAI).
			SetType(service.AccountTypeOAuth).
			SetStatus(service.StatusActive).
			SetSchedulable(true).
			SetCredentials(map[string]any{"marker": suffix}).
			Save(ctx)
		require.NoError(t, createErr)
		accountIDs = append(accountIDs, row.ID)
		_, bindErr := client.AccountGroup.Create().
			SetAccountID(row.ID).
			SetGroupID(routeGroup.ID).
			SetPriority(17).
			Save(ctx)
		require.NoError(t, bindErr)
		_, assignErr := admin.SetAccountTaxonomy(ctx, row.ID, service.AccountTaxonomyAssignment{FolderID: &folderA.ID, TagIDs: []int64{tagA.ID}})
		require.NoError(t, assignErr)
	}

	folders, err := admin.ReorderAccountFolders(ctx, []int64{folderB.ID, folderA.ID})
	require.NoError(t, err)
	require.Equal(t, []int64{folderB.ID, folderA.ID}, []int64{folders[0].ID, folders[1].ID})
	tags, err := admin.ReorderAccountTags(ctx, []int64{tagC.ID, tagB.ID, tagA.ID})
	require.NoError(t, err)
	require.Equal(t, []int64{tagC.ID, tagB.ID, tagA.ID}, []int64{tags[0].ID, tags[1].ID, tags[2].ID})
	_, err = admin.ReorderAccountFolders(ctx, []int64{folderA.ID})
	require.Error(t, err)
	require.Equal(t, 409, infraerrors.Code(err))

	_, err = admin.BulkUpdateAccountTaxonomy(ctx, service.BulkAccountTaxonomyInput{
		AccountIDs: []int64{accountIDs[0]}, TagAddIDs: []int64{tagB.ID}, TagRemoveIDs: []int64{tagB.ID},
	})
	require.Error(t, err)
	require.Equal(t, 400, infraerrors.Code(err))

	result, err := admin.BulkUpdateAccountTaxonomy(ctx, service.BulkAccountTaxonomyInput{
		AccountIDs: []int64{accountIDs[0]}, FolderAction: "set", FolderID: &folderB.ID,
		TagAddIDs: []int64{tagB.ID}, TagRemoveIDs: []int64{tagA.ID},
	})
	require.NoError(t, err)
	require.Equal(t, &service.BulkAccountTaxonomyResult{MatchedCount: 1, UpdatedCount: 1}, result)
	first, err := admin.GetAccount(ctx, accountIDs[0])
	require.NoError(t, err)
	require.Equal(t, folderB.ID, first.ManagementFolder.ID)
	require.Equal(t, []int64{tagB.ID}, []int64{first.Tags[0].ID})
	require.True(t, first.Schedulable)
	require.Equal(t, "one", first.Credentials["marker"])
	routeBindingCount, err := client.AccountGroup.Query().Where(
		dbaccountgroup.AccountIDEQ(accountIDs[0]),
		dbaccountgroup.GroupIDEQ(routeGroup.ID),
		dbaccountgroup.PriorityEQ(17),
	).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, routeBindingCount, "management taxonomy must not alter request routing groups")
	second, err := admin.GetAccount(ctx, accountIDs[1])
	require.NoError(t, err)
	require.Equal(t, folderA.ID, second.ManagementFolder.ID)
	require.Equal(t, tagA.ID, second.Tags[0].ID)

	expected := 2
	result, err = admin.BulkUpdateAccountTaxonomy(ctx, service.BulkAccountTaxonomyInput{
		Filters:            &service.BulkUpdateAccountFilters{Console: &service.AccountConsoleFilters{Search: prefix, SortBy: "id", SortOrder: "asc"}},
		ExpectedMatchCount: &expected, FolderAction: "clear", TagAddIDs: []int64{tagC.ID},
	})
	require.NoError(t, err)
	require.Equal(t, 2, result.MatchedCount)
	for _, accountID := range accountIDs {
		loaded, loadErr := admin.GetAccount(ctx, accountID)
		require.NoError(t, loadErr)
		require.Nil(t, loaded.ManagementFolder)
		require.Contains(t, []int64{loaded.Tags[0].ID, loaded.Tags[len(loaded.Tags)-1].ID}, tagC.ID)
		bindingCount, countErr := client.AccountGroup.Query().Where(
			dbaccountgroup.AccountIDEQ(accountID),
			dbaccountgroup.GroupIDEQ(routeGroup.ID),
			dbaccountgroup.PriorityEQ(17),
		).Count(ctx)
		require.NoError(t, countErr)
		require.Equal(t, 1, bindingCount)
	}

	wrongExpected := 3
	_, err = admin.BulkUpdateAccountTaxonomy(ctx, service.BulkAccountTaxonomyInput{
		Filters:            &service.BulkUpdateAccountFilters{Console: &service.AccountConsoleFilters{Search: prefix, SortBy: "id", SortOrder: "asc"}},
		ExpectedMatchCount: &wrongExpected, FolderAction: "set", FolderID: &folderA.ID,
	})
	require.Error(t, err)
	require.Equal(t, 409, infraerrors.Code(err))
	for _, accountID := range accountIDs {
		loaded, loadErr := admin.GetAccount(ctx, accountID)
		require.NoError(t, loadErr)
		require.Nil(t, loaded.ManagementFolder)
	}

	missingTagID := int64(9223372036854770000)
	_, err = admin.BulkUpdateAccountTaxonomy(ctx, service.BulkAccountTaxonomyInput{
		AccountIDs: []int64{accountIDs[0]}, FolderAction: "set", FolderID: &folderA.ID, TagAddIDs: []int64{missingTagID},
	})
	require.ErrorIs(t, err, service.ErrAccountTagNotFound)
	first, err = admin.GetAccount(ctx, accountIDs[0])
	require.NoError(t, err)
	require.Nil(t, first.ManagementFolder, "invalid tag must roll back the folder move")
}

func integrationFacetCount(options []service.AccountFacetOption, value string) int {
	for _, option := range options {
		if option.Value == value {
			return option.Count
		}
	}
	return 0
}

func integrationTaxonomyCount(folders []service.AccountManagementFolder, id int64) int {
	for _, folder := range folders {
		if folder.ID == id {
			return folder.AccountCount
		}
	}
	return 0
}

func integrationTagCount(tags []service.AccountManagementTag, id int64) int {
	for _, tag := range tags {
		if tag.ID == id {
			return tag.AccountCount
		}
	}
	return 0
}
