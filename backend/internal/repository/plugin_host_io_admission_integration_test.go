//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPluginHostIOLeaseRejectsStaleAdmissionSnapshot(t *testing.T) {
	for _, change := range []string{"disabled", "config", "bindings", "config-current-revision"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			repo, current, _ := installedUpdateFixture(t)
			business := service.WithPluginBusinessIOLease(ctx)
			release, err := repo.HoldPluginRuntime(business, current)
			require.NoError(t, err, "positive business control must acquire a real PostgreSQL lease")
			release()
			switch change {
			case "disabled":
				bindings := append([]service.PluginBinding(nil), current.Bindings...)
				for i := range bindings {
					bindings[i].Enabled = false
				}
				require.NoError(t, repo.UpdateBindingsAndState(ctx, current.ID, bindings, service.PluginStateDisabled, "", nil, current.State, current.BinarySHA256))
			case "config", "config-current-revision":
				require.NoError(t, repo.UpdateConfig(ctx, current.ID, "new-config", current.BinarySHA256))
			case "bindings":
				bindings := append([]service.PluginBinding(nil), current.Bindings...)
				bindings[0].RolloutPercent = 0
				require.NoError(t, repo.UpdateBindingsAndState(ctx, current.ID, bindings, service.PluginStateEnabled, "", nil, current.State, current.BinarySHA256))
			}
			fresh, err := repo.GetByID(ctx, current.ID)
			require.NoError(t, err)
			require.Greater(t, fresh.Revision, current.Revision)
			if change == "config-current-revision" {
				current.Revision = fresh.Revision
				// A fresh revision number alone cannot authorize a stale config.
				require.NotEqual(t, fresh.ConfigEncrypted, current.ConfigEncrypted)
			}
			release, err = repo.HoldPluginRuntime(business, current)
			if release != nil {
				t.Cleanup(release)
			}
			require.ErrorIs(t, err, service.ErrPluginStateChanged, "stale business admission must be denied, even while package/generation still match")
			require.Nil(t, release)
		})
	}
}

func TestPluginProcessDiagnosticsLeaseAndBusinessPromotionBarrier(t *testing.T) {
	ctx := context.Background()
	repo, current, candidate := installedUpdateFixture(t)
	disabledBindings := append([]service.PluginBinding(nil), current.Bindings...)
	for i := range disabledBindings {
		disabledBindings[i].Enabled = false
	}
	require.NoError(t, repo.UpdateBindingsAndState(ctx, current.ID, disabledBindings, service.PluginStateDisabled, "", nil, current.State, current.BinarySHA256))
	disabled, err := repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	processRelease, err := repo.HoldPluginRuntime(ctx, disabled)
	require.NoError(t, err, "a disabled plugin must still support a temporary Validate/Test process lease")
	t.Cleanup(processRelease)
	businessRelease, err := repo.HoldPluginRuntime(service.WithPluginBusinessIOLease(ctx), disabled)
	if businessRelease != nil {
		t.Cleanup(businessRelease)
	}
	require.ErrorIs(t, err, service.ErrPluginStateChanged)
	processRelease()
	require.NoError(t, repo.UpdateBindingsAndState(ctx, current.ID, current.Bindings, service.PluginStateEnabled, "", nil, service.PluginStateDisabled, current.BinarySHA256))
	enabled, err := repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	businessRelease, err = repo.HoldPluginRuntime(service.WithPluginBusinessIOLease(ctx), enabled)
	require.NoError(t, err)
	t.Cleanup(businessRelease)
	require.NoError(t, repo.StagePluginUpdate(ctx, enabled, candidate, service.PluginUpdatePinned))
	pending, err := repo.GetByID(ctx, enabled.ID)
	require.NoError(t, err)
	require.ErrorIs(t, repo.CommitPluginUpdate(ctx, pending, candidate), service.ErrPluginUpdateWaiting, "business IO must hold the shared promotion barrier")
	businessRelease()
	require.NoError(t, repo.CommitPluginUpdate(ctx, pending, candidate))
	updated, err := repo.GetByID(ctx, current.ID)
	require.NoError(t, err)
	require.Equal(t, enabled.RuntimeGeneration+1, updated.RuntimeGeneration)
}
