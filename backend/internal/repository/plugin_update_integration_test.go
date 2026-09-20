//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

func installedUpdateFixture(t *testing.T) (*pluginRepository, *service.PluginInstallation, *service.PluginInstallation) {
	t.Helper()
	ctx := context.Background()
	repo := &pluginRepository{db: integrationDB}
	artifact := bundleFixture(t, repo)
	installed, err := repo.PrepareBundledPlugin(ctx, artifact, "update-fixture", "", true, "saved-config")
	require.NoError(t, err)
	require.NoError(t, repo.CompleteBundledPlugin(ctx, installed.ID, "update-fixture"))
	current, err := repo.GetByID(ctx, installed.ID)
	require.NoError(t, err)
	current.Bindings[0].RolloutPercent = 37
	require.NoError(t, repo.UpdateBindingsAndState(ctx, current.ID, current.Bindings, service.PluginStateEnabled, "", nil, current.State, current.BinarySHA256))
	current, err = repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	candidate := *artifact
	candidate.Version = "1.0.1"
	candidate.ArtifactData = []byte("new program and UI resource set")
	sum := sha256.Sum256(candidate.ArtifactData)
	candidate.PackageSHA256 = hex.EncodeToString(sum[:])
	candidate.Manifest.Capabilities = append(append([]service.PluginCapability(nil), artifact.Manifest.Capabilities...), service.PluginCapability{ID: extensionv1.CapabilityUI, Platform: "*", AccountType: "*"})
	return repo, current, &candidate
}

func TestPluginIndependentUpdateWaitsForAllOldProcessesAndPreservesIntent(t *testing.T) {
	ctx := context.Background()
	repo, current, candidate := installedUpdateFixture(t)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Process locks cannot monopolize a small application's SQL query pool.
	previousMax := integrationDB.Stats().MaxOpenConnections
	integrationDB.SetMaxOpenConns(1)
	defer integrationDB.SetMaxOpenConns(previousMax)
	releaseOne, err := repo.HoldPluginRuntime(ctx, current)
	require.NoError(t, err)
	defer releaseOne()
	releaseTwo, err := repo.HoldPluginRuntime(ctx, current)
	require.NoError(t, err)
	defer releaseTwo()
	require.NoError(t, repo.StagePluginUpdate(ctx, current, candidate, service.PluginUpdatePinned))
	pending, err := repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	require.Equal(t, service.PluginStateUpdating, pending.State)
	require.ErrorIs(t, repo.CommitPluginUpdate(ctx, pending, candidate), service.ErrPluginUpdateWaiting)
	releaseOne()
	require.ErrorIs(t, repo.CommitPluginUpdate(ctx, pending, candidate), service.ErrPluginUpdateWaiting)
	releaseTwo()
	require.NoError(t, repo.CommitPluginUpdate(ctx, pending, candidate))
	updated, err := repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	require.Equal(t, "saved-config", updated.ConfigEncrypted)
	require.Equal(t, current.RuntimeGeneration+1, updated.RuntimeGeneration)
	require.Equal(t, candidate.PackageSHA256, updated.PackageSHA256)
	require.Equal(t, service.PluginUpdatePinned, updated.UpdatePolicy)
	for _, binding := range updated.Bindings {
		if binding.Capability == extensionv1.CapabilityAdmin {
			require.True(t, binding.Enabled)
			require.Equal(t, 37, binding.RolloutPercent)
		} else {
			require.False(t, binding.Enabled)
		}
	}
	// The program bytes intentionally did not change: UI-only releases must
	// still revoke the old process generation and survive a future host bundle.
	require.Equal(t, current.BinarySHA256, updated.BinarySHA256)
	_, err = repo.HoldPluginRuntime(ctx, current)
	require.ErrorIs(t, err, service.ErrPluginStateChanged)
	applied, err := repo.BundleApplied(ctx, current.PluginKey, "new-host-bundle")
	require.NoError(t, err)
	require.True(t, applied)
	require.NoError(t, repo.SetPluginUpdatePolicy(ctx, current.ID, updated.Revision, service.PluginUpdateBundled))
	applied, err = repo.BundleApplied(ctx, current.PluginKey, "new-host-bundle")
	require.NoError(t, err)
	require.False(t, applied)
}

func TestPluginIndependentUpdateRejectsConcurrentConfigurationAndResumesAfterRestart(t *testing.T) {
	ctx := context.Background()
	repo, current, candidate := installedUpdateFixture(t)
	require.NoError(t, repo.UpdateConfig(ctx, current.ID, "edited-config", current.BinarySHA256))
	require.ErrorIs(t, repo.StagePluginUpdate(ctx, current, candidate, service.PluginUpdatePinned), service.ErrPluginStateChanged)
	fresh, err := repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	require.Greater(t, fresh.Revision, current.Revision)
	require.NoError(t, repo.StagePluginUpdate(ctx, fresh, candidate, service.PluginUpdatePinned))
	// A newly constructed repository models recovery without any process memory.
	restarted := &pluginRepository{db: integrationDB}
	artifact, err := restarted.PendingPluginArtifact(ctx, current.ID)
	require.NoError(t, err)
	require.Equal(t, candidate.ArtifactData, artifact)
	pending, err := restarted.GetByID(ctx, current.ID)
	require.NoError(t, err)
	require.ErrorIs(t, restarted.UpdateConfig(ctx, current.ID, "late-config", current.BinarySHA256), service.ErrPluginStateChanged)
	require.NoError(t, restarted.CommitPluginUpdate(ctx, pending, candidate))
	updated, err := restarted.GetByID(ctx, current.ID)
	require.NoError(t, err)
	require.Equal(t, "edited-config", updated.ConfigEncrypted)
}

func TestPluginIndependentUpdatePreservesExplicitDisableAndRejectsOldWrites(t *testing.T) {
	ctx := context.Background()
	repo, current, candidate := installedUpdateFixture(t)
	oldCtx := service.WithPluginExecution(ctx, current)
	request := extensionv1.StateRequest{Namespace: "update-test", Key: "record", Value: []byte(`{"value":"old"}`)}
	state, err := repo.CompareSwapExtensionState(oldCtx, current.PluginKey, request)
	require.NoError(t, err)
	require.True(t, state.Applied)
	request.ExpectedRevision = state.Revision
	require.NoError(t, repo.StagePluginUpdate(ctx, current, candidate, service.PluginUpdatePinned))
	_, err = repo.CompareSwapExtensionState(oldCtx, current.PluginKey, request)
	require.ErrorIs(t, err, service.ErrPluginStateChanged)
	pending, err := repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	for i := range pending.Bindings {
		pending.Bindings[i].Enabled = false
	}
	require.NoError(t, repo.UpdateBindingsAndState(service.WithPluginExpectedRevision(ctx, pending.Revision), pending.ID, pending.Bindings, service.PluginStateDisabled, "", nil, "", pending.BinarySHA256))
	require.ErrorIs(t, repo.CommitPluginUpdate(ctx, pending, candidate), service.ErrPluginStateChanged)
	pending, err = repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	require.Equal(t, service.PluginStateUpdating, pending.State)
	require.NoError(t, repo.CommitPluginUpdate(ctx, pending, candidate))
	updated, err := repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	require.Equal(t, service.PluginStateDisabled, updated.State)
	for _, binding := range updated.Bindings {
		require.False(t, binding.Enabled)
	}
	_, err = repo.CompareSwapExtensionState(oldCtx, current.PluginKey, request)
	require.ErrorIs(t, err, service.ErrPluginStateChanged)
	_, err = repo.AcquireExtensionLease(oldCtx, current.PluginKey, extensionv1.LeaseRequest{Namespace: "update-test", Key: "old-work", Owner: "old-worker", TTLSeconds: 30})
	require.ErrorIs(t, err, service.ErrPluginStateChanged)
	newCtx := service.WithPluginExecution(ctx, updated)
	state, err = repo.CompareSwapExtensionState(newCtx, current.PluginKey, request)
	require.NoError(t, err)
	require.True(t, state.Applied)
}
