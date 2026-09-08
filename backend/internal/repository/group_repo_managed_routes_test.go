package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func managedRoutesRepositoryTestConfig() service.ManagedModelRoutesConfig {
	return service.ManagedModelRoutesConfig{
		Version: 1, Enabled: true,
		Routes: []service.ManagedModelRoute{{
			PublicModel: "gpt-public", Aliases: []string{"gpt-public-alias"},
			Selector: "s2pub-g23-m0123456789abcdef", TargetPlatform: service.PlatformOpenAI,
			Endpoints: []string{"responses", "messages"},
			Accounts: []service.ManagedModelRouteAccount{{
				AccountID: 42, UpstreamModel: "namespace/upstream-vip",
				AccountFingerprint: "internal-account-fingerprint", Endpoints: []string{"responses"},
			}},
		}},
	}
}

func TestGroupRepositoryManagedModelRoutesPersistAndSurviveOrdinaryUpdate(t *testing.T) {
	_, client := newAPIKeyRepoSQLite(t)
	repo := newGroupRepositoryWithSQL(client, nil)
	ctx := context.Background()
	group := &service.Group{
		Name: "managed-routes-repository", Platform: service.PlatformOpenAI,
		Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard,
		RateMultiplier: 1, ManagedModelRoutes: managedRoutesRepositoryTestConfig(),
	}
	require.NoError(t, createGroupRecord(ctx, client, group))
	loaded, err := repo.GetByIDLite(ctx, group.ID)
	require.NoError(t, err)
	require.Equal(t, group.ManagedModelRoutes, loaded.ManagedModelRoutes)

	// Simulate a publication after an ordinary editor loaded the group. Even a
	// stale nonzero managed configuration must never overwrite the published one.
	published := managedRoutesRepositoryTestConfig()
	published.Routes[0].Accounts[0].AccountFingerprint = "republished-fingerprint"
	_, err = client.Group.UpdateOneID(group.ID).SetManagedModelRoutes(published).Save(ctx)
	require.NoError(t, err)
	loaded.Name = "managed-routes-repository-renamed"
	require.NoError(t, repo.Update(ctx, loaded))
	require.Equal(t, published, loaded.ManagedModelRoutes, "return the persisted configuration rather than the editor's stale copy")

	saved, err := repo.GetByIDLite(ctx, group.ID)
	require.NoError(t, err)
	require.Equal(t, loaded.Name, saved.Name)
	require.Equal(t, published, saved.ManagedModelRoutes)
}

func TestAPIKeyRepositoryAuthProjectionCarriesManagedModelRoutes(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "managed-routes-projection@test.invalid")
	config := managedRoutesRepositoryTestConfig()
	group, err := client.Group.Create().
		SetName("managed-routes-auth-projection").
		SetPlatform(service.PlatformOpenAI).
		SetManagedModelRoutes(config).
		Save(ctx)
	require.NoError(t, err)
	key := &service.APIKey{
		UserID: user.ID, Key: "k-managed-routes-projection", Name: "Managed routes projection",
		GroupID: &group.ID, Status: service.StatusActive,
	}
	require.NoError(t, repo.Create(ctx, key))

	got, err := repo.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.NotNil(t, got.Group)
	require.Equal(t, config, got.Group.ManagedModelRoutes,
		"the explicit authentication SELECT must include the complete managed routes column")
}
