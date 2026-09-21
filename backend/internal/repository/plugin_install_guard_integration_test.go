//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPluginInstallCannotReplaceDisabledIdentityWithLiveLease(t *testing.T) {
	ctx := context.Background()
	repo, current, candidate := installedUpdateFixture(t)
	release, err := repo.HoldPluginRuntime(ctx, current)
	require.NoError(t, err)
	defer release()
	for index := range current.Bindings {
		current.Bindings[index].Enabled = false
	}
	require.NoError(t, repo.UpdateBindingsAndState(ctx, current.ID, current.Bindings, service.PluginStateDisabled, "operator disabled", nil, current.State, current.BinarySHA256))
	before, err := repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	candidate.State = service.PluginStateDisabled
	candidate.Version = before.Version
	candidate.Manifest.Version = before.Manifest.Version
	// Even a same-version package with different UI bytes must use the update
	// contract; the legacy insert must not advance a still-owned generation.
	require.NotEqual(t, before.PackageSHA256, candidate.PackageSHA256)
	_, err = repo.Install(ctx, candidate, []service.PluginBinding{{Capability: before.Bindings[0].Capability, Platform: before.Bindings[0].Platform, AccountType: before.Bindings[0].AccountType, RolloutPercent: 100}})
	require.ErrorIs(t, err, service.ErrPluginAlreadyInstalled)
	after, err := repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	require.Equal(t, before, after, "conflict must not change package, config, revision, generation, policy, bindings or rollout")
	release()
	_, err = repo.Install(ctx, candidate, nil)
	require.ErrorIs(t, err, service.ErrPluginAlreadyInstalled, "lease release must not reopen legacy replacement")
}

func TestPluginInstallConflictPreservesPendingArtifactAndIntent(t *testing.T) {
	ctx := context.Background()
	repo, current, candidate := installedUpdateFixture(t)
	require.NoError(t, repo.StagePluginUpdate(ctx, current, candidate, service.PluginUpdatePinned))
	before, err := repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	pending, err := repo.PendingPluginArtifact(ctx, current.ID)
	require.NoError(t, err)
	candidate.State = service.PluginStateDisabled
	_, err = repo.Install(ctx, candidate, nil)
	require.ErrorIs(t, err, service.ErrPluginAlreadyInstalled)
	after, err := repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	require.Equal(t, before, after)
	retained, err := repo.PendingPluginArtifact(ctx, current.ID)
	require.NoError(t, err)
	require.Equal(t, pending, retained)
}

func TestPluginInstallAfterExplicitDeleteRetainsPluginLedger(t *testing.T) {
	ctx := context.Background()
	repo, current, candidate := installedUpdateFixture(t)
	_, err := repo.db.ExecContext(ctx, `INSERT INTO sub2api_plugin_state(plugin_key,namespace,state_key,value) VALUES($1,'install-test','diagnostic','{"retained":true}')`, current.PluginKey)
	require.NoError(t, err)
	for index := range current.Bindings {
		current.Bindings[index].Enabled = false
	}
	require.NoError(t, repo.UpdateBindingsAndState(ctx, current.ID, current.Bindings, service.PluginStateDisabled, "", nil, current.State, current.BinarySHA256))
	require.NoError(t, repo.Delete(ctx, current.ID, current.BinarySHA256))
	var removed bool
	require.NoError(t, repo.db.QueryRowContext(ctx, `SELECT user_removed FROM sub2api_plugin_bootstrap WHERE plugin_key=$1`, current.PluginKey).Scan(&removed))
	require.True(t, removed)
	candidate.State = service.PluginStateDisabled
	installed, err := repo.Install(ctx, candidate, []service.PluginBinding{{Capability: current.Bindings[0].Capability, Platform: current.Bindings[0].Platform, AccountType: current.Bindings[0].AccountType, RolloutPercent: 100}})
	require.NoError(t, err)
	require.NotEqual(t, current.ID, installed.ID)
	require.Equal(t, service.PluginStateDisabled, installed.State)
	require.Equal(t, service.PluginUpdatePinned, installed.UpdatePolicy)
	require.False(t, installed.Bindings[0].Enabled)
	var enabled bool
	require.NoError(t, repo.db.QueryRowContext(ctx, `SELECT user_removed,desired_enabled FROM sub2api_plugin_bootstrap WHERE plugin_key=$1`, current.PluginKey).Scan(&removed, &enabled))
	require.False(t, removed)
	require.False(t, enabled)
	var retained bool
	require.NoError(t, repo.db.QueryRowContext(ctx, `SELECT (value->>'retained')::boolean FROM sub2api_plugin_state WHERE plugin_key=$1 AND namespace='install-test' AND state_key='diagnostic'`, current.PluginKey).Scan(&retained))
	require.True(t, retained, "explicit uninstall/reinstall must not erase the plugin diagnostic ledger")
}
