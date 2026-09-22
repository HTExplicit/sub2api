//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func unfinishedBootstrapUpdateFixture(t *testing.T) (*pluginRepository, *service.PluginInstallation, *service.PluginInstallation, *service.PluginInstallation) {
	t.Helper()
	ctx := context.Background()
	repo := &pluginRepository{db: integrationDB}
	artifact := bundleFixture(t, repo)
	artifact.Manifest.Version = artifact.Version
	prepared, err := repo.PrepareBundledPlugin(ctx, artifact, "unfinished-bundle", "", true, "saved-bootstrap-config")
	require.NoError(t, err)
	candidate := *artifact
	candidate.Version = "1.0.1"
	candidate.Manifest.Version = candidate.Version
	candidate.ArtifactData = []byte("independent pending replacement")
	sum := sha256.Sum256(candidate.ArtifactData)
	candidate.PackageSHA256 = hex.EncodeToString(sum[:])
	return repo, artifact, prepared, &candidate
}

func bootstrapJournalSnapshot(t *testing.T, repo *pluginRepository, key string) string {
	t.Helper()
	var snapshot string
	require.NoError(t, repo.db.QueryRowContext(context.Background(), `SELECT to_jsonb(b)::text FROM sub2api_plugin_bootstrap b WHERE plugin_key=$1`, key).Scan(&snapshot))
	return snapshot
}

// Recreate a pending row accepted by the pre-fix Stage path. New admission now
// rejects this state, but a restart must still preserve already-persisted work.
func seedLegacyUnfinishedBootstrapUpdate(t *testing.T, repo *pluginRepository, previous, candidate *service.PluginInstallation) {
	t.Helper()
	ctx := context.Background()
	tx, err := repo.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	var generation int64
	require.NoError(t, tx.QueryRowContext(ctx, `UPDATE sub2api_plugin_installations SET state='updating' WHERE id=$1 RETURNING runtime_generation`, previous.ID).Scan(&generation))
	_, err = tx.ExecContext(ctx, `INSERT INTO sub2api_plugin_updates(plugin_id,artifact_data,package_sha256,source_generation,update_policy) VALUES($1,$2,$3,$4,'pinned')`, previous.ID, candidate.ArtifactData, candidate.PackageSHA256, generation)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
}

func TestBootstrapPrepareDoesNotOverwritePendingIndependentUpdate(t *testing.T) {
	ctx := context.Background()
	repo, artifact, prepared, candidate := unfinishedBootstrapUpdateFixture(t)
	seedLegacyUnfinishedBootstrapUpdate(t, repo, prepared, candidate)
	before, err := repo.GetByID(ctx, prepared.ID)
	require.NoError(t, err)
	journal := bootstrapJournalSnapshot(t, repo, prepared.PluginKey)
	resumed, err := repo.PrepareBundledPlugin(ctx, artifact, "unfinished-bundle", "", true, "new-default-config")
	require.NoError(t, err)
	after, err := repo.GetByID(ctx, prepared.ID)
	require.NoError(t, err)
	pending, pendingErr := repo.PendingPluginArtifact(ctx, prepared.ID)
	t.Logf("before=%s/generation:%d after=%s/generation:%d pending_readable=%t", before.State, before.RuntimeGeneration, after.State, after.RuntimeGeneration, pendingErr == nil)
	require.Nil(t, resumed, "a pending independent update owns the installed generation")
	require.Equal(t, before, after)
	require.NoError(t, pendingErr)
	require.Equal(t, candidate.ArtifactData, pending)
	require.Equal(t, journal, bootstrapJournalSnapshot(t, repo, prepared.PluginKey))
}

func TestBootstrapCompleteDoesNotOverwritePendingIndependentUpdate(t *testing.T) {
	ctx := context.Background()
	repo, _, prepared, candidate := unfinishedBootstrapUpdateFixture(t)
	seedLegacyUnfinishedBootstrapUpdate(t, repo, prepared, candidate)
	before, err := repo.GetByID(ctx, prepared.ID)
	require.NoError(t, err)
	journal := bootstrapJournalSnapshot(t, repo, prepared.PluginKey)
	completeErr := repo.CompleteBundledPlugin(ctx, prepared.ID, "unfinished-bundle")
	after, err := repo.GetByID(ctx, prepared.ID)
	require.NoError(t, err)
	pending, pendingErr := repo.PendingPluginArtifact(ctx, prepared.ID)
	t.Logf("before=%s/generation:%d after=%s/generation:%d pending_readable=%t", before.State, before.RuntimeGeneration, after.State, after.RuntimeGeneration, pendingErr == nil)
	require.ErrorIs(t, completeErr, service.ErrPluginStateChanged)
	require.Equal(t, before, after)
	require.NoError(t, pendingErr)
	require.Equal(t, candidate.ArtifactData, pending)
	require.Equal(t, journal, bootstrapJournalSnapshot(t, repo, prepared.PluginKey))
}

func TestPluginUpdateWaitsForUnfinishedBundledIntent(t *testing.T) {
	ctx := context.Background()
	repo, _, prepared, candidate := unfinishedBootstrapUpdateFixture(t)
	journal := bootstrapJournalSnapshot(t, repo, prepared.PluginKey)
	require.ErrorIs(t, repo.StagePluginUpdate(ctx, prepared, candidate, service.PluginUpdatePinned), service.ErrPluginStateChanged)
	unchanged, err := repo.GetByID(ctx, prepared.ID)
	require.NoError(t, err)
	require.Equal(t, prepared, unchanged)
	require.Equal(t, journal, bootstrapJournalSnapshot(t, repo, prepared.PluginKey))
	var pendingCount int
	require.NoError(t, repo.db.QueryRowContext(ctx, `SELECT count(*) FROM sub2api_plugin_updates WHERE plugin_id=$1`, prepared.ID).Scan(&pendingCount))
	require.Zero(t, pendingCount)
	require.NoError(t, repo.ImportLegacyPluginState(ctx, prepared, "", nil))
	require.NoError(t, repo.CompleteBundledPlugin(ctx, prepared.ID, "unfinished-bundle"))
	current, err := repo.GetByID(ctx, prepared.ID)
	require.NoError(t, err)
	require.True(t, current.Bindings[0].Enabled, "normal first bootstrap retains its desired activation")
	require.Equal(t, "saved-bootstrap-config", current.ConfigEncrypted)
	require.NoError(t, repo.StagePluginUpdate(ctx, current, candidate, service.PluginUpdatePinned))
	pending, err := repo.PendingPluginArtifact(ctx, prepared.ID)
	require.NoError(t, err)
	require.Equal(t, candidate.ArtifactData, pending)
}

func TestBootstrapCompletionPreservesOperatorIntent(t *testing.T) {
	for _, choice := range []string{"disable_before_complete", "disable_after_complete", "remove", "pin"} {
		t.Run(choice, func(t *testing.T) {
			ctx := context.Background()
			repo, artifact, prepared, candidate := unfinishedBootstrapUpdateFixture(t)
			if choice == "remove" {
				require.NoError(t, repo.Delete(ctx, prepared.ID, prepared.BinarySHA256))
				journal := bootstrapJournalSnapshot(t, repo, prepared.PluginKey)
				resumed, err := repo.PrepareBundledPlugin(ctx, artifact, "unfinished-bundle", "", true, "new-default")
				require.NoError(t, err)
				require.Nil(t, resumed)
				require.ErrorIs(t, repo.CompleteBundledPlugin(ctx, prepared.ID, "unfinished-bundle"), service.ErrPluginStateChanged)
				require.Equal(t, journal, bootstrapJournalSnapshot(t, repo, prepared.PluginKey))
				return
			}
			if choice == "pin" {
				require.NoError(t, repo.SetPluginUpdatePolicy(ctx, prepared.ID, prepared.Revision, service.PluginUpdatePinned))
				before, err := repo.GetByID(ctx, prepared.ID)
				require.NoError(t, err)
				journal := bootstrapJournalSnapshot(t, repo, prepared.PluginKey)
				resumed, err := repo.PrepareBundledPlugin(ctx, artifact, "unfinished-bundle", "", true, "new-default")
				require.NoError(t, err)
				require.Nil(t, resumed)
				require.ErrorIs(t, repo.CompleteBundledPlugin(ctx, prepared.ID, "unfinished-bundle"), service.ErrPluginStateChanged)
				after, err := repo.GetByID(ctx, prepared.ID)
				require.NoError(t, err)
				require.Equal(t, before, after)
				require.Equal(t, journal, bootstrapJournalSnapshot(t, repo, prepared.PluginKey))
				require.NoError(t, repo.StagePluginUpdate(ctx, before, candidate, service.PluginUpdatePinned), "an explicitly pinned installation is not owned by the stale bundle journal")
				return
			}
			if choice == "disable_after_complete" {
				require.NoError(t, repo.CompleteBundledPlugin(ctx, prepared.ID, "unfinished-bundle"))
			}
			current, err := repo.GetByID(ctx, prepared.ID)
			require.NoError(t, err)
			for index := range current.Bindings {
				current.Bindings[index].Enabled = false
				current.Bindings[index].RolloutPercent = 37
			}
			require.NoError(t, repo.UpdateBindingsAndState(ctx, current.ID, current.Bindings, service.PluginStateDisabled, "operator disabled", nil, current.State, current.BinarySHA256))
			before, err := repo.GetByID(ctx, current.ID)
			require.NoError(t, err)
			if choice == "disable_before_complete" {
				restarted := &pluginRepository{db: integrationDB}
				resumed, err := restarted.PrepareBundledPlugin(ctx, artifact, "unfinished-bundle", "", true, "new-default")
				require.NoError(t, err)
				require.NotNil(t, resumed)
				require.Equal(t, "saved-bootstrap-config", resumed.ConfigEncrypted)
			}
			require.NoError(t, repo.CompleteBundledPlugin(ctx, prepared.ID, "unfinished-bundle"))
			after, err := repo.GetByID(ctx, prepared.ID)
			require.NoError(t, err)
			require.Equal(t, service.PluginStateDisabled, after.State)
			require.False(t, after.Bindings[0].Enabled)
			require.Equal(t, 37, after.Bindings[0].RolloutPercent)
			if choice == "disable_after_complete" {
				require.Equal(t, before, after, "duplicate completion is a no-op, not a replay of old intent")
			}
		})
	}
}
