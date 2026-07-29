//go:build integration

package repository

import (
	"context"
	"testing"

	dbaccounttagbinding "github.com/Wei-Shaw/sub2api/ent/accounttagbinding"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

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

	repo := newAccountRepositoryWithSQL(client, nil, nil)
	loaded, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded.ManagementFolder)
	require.Equal(t, folder.ID, loaded.ManagementFolder.ID)
	require.Len(t, loaded.Tags, 1)
	require.Equal(t, tag.ID, loaded.Tags[0].ID)
	require.Equal(t, "Paid", loaded.Tags[0].Name)

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

	require.Error(t, client.AccountFolder.DeleteOneID(folder.ID).Exec(ctx), "non-empty folders require the explicit move-to-uncategorized path")
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
