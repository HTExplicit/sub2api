package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type nativeBoundaryFixture struct {
	service.PluginRepository
	rows   map[int64]*service.PluginInstallation
	writes int
}

func (r *nativeBoundaryFixture) List(context.Context) ([]*service.PluginInstallation, error) {
	out := make([]*service.PluginInstallation, 0, len(r.rows))
	for _, p := range r.rows {
		out = append(out, p)
	}
	return out, nil
}
func (r *nativeBoundaryFixture) GetByID(_ context.Context, id int64) (*service.PluginInstallation, error) {
	if p := r.rows[id]; p != nil {
		return p, nil
	}
	return nil, sql.ErrNoRows
}
func (r *nativeBoundaryFixture) GetByKey(_ context.Context, key string) (*service.PluginInstallation, error) {
	for _, p := range r.rows {
		if p.PluginKey == key {
			return p, nil
		}
	}
	return nil, sql.ErrNoRows
}
func (r *nativeBoundaryFixture) Install(_ context.Context, p *service.PluginInstallation, _ []service.PluginBinding) (*service.PluginInstallation, error) {
	r.writes++
	return p, nil
}
func (r *nativeBoundaryFixture) UpdateState(context.Context, int64, string, string, *time.Time, string, string) error {
	r.writes++
	return nil
}

func TestNativePluginBoundaryProtectsRetiredRecordsAndAllowsThirdParties(t *testing.T) {
	ctx := context.Background()
	base := &nativeBoundaryFixture{rows: map[int64]*service.PluginInstallation{}}
	snapshot := &service.NativeRetirementSnapshot{Version: 1, Completed: true, Plugins: map[string]service.NativeRetirementPlugin{}}
	for i, key := range service.FirstPartyNativePluginKeys {
		id := int64(i + 1)
		base.rows[id] = &service.PluginInstallation{ID: id, PluginKey: key, State: "disabled"}
		snapshot.Plugins[key] = service.NativeRetirementPlugin{ID: id, Key: key}
	}
	base.rows[100] = &service.PluginInstallation{ID: 100, PluginKey: "third-party.transport", State: "disabled"}
	r := newNativePluginBoundary(base, &service.NativeFeatureBootstrap{Snapshot: snapshot})
	list, err := r.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.EqualValues(t, 100, list[0].ID)
	for id, p := range base.rows {
		if id == 100 {
			continue
		}
		_, err = r.GetByID(ctx, id)
		require.ErrorIs(t, err, sql.ErrNoRows)
		_, err = r.GetByKey(ctx, p.PluginKey)
		require.ErrorIs(t, err, sql.ErrNoRows)
		_, err = r.GetArtifact(ctx, id)
		require.ErrorIs(t, err, errRetiredNativePlugin)
		require.ErrorIs(t, r.Delete(ctx, id, "fixture"), errRetiredNativePlugin)
		require.ErrorIs(t, r.BeginEnable(ctx, id, "fixture", "disabled"), errRetiredNativePlugin)
		require.ErrorIs(t, r.UpdateState(ctx, id, "incompatible", "", nil, "fixture", "disabled"), errRetiredNativePlugin)
		require.ErrorIs(t, r.UpdateConfig(ctx, id, "cipher", "fixture"), errRetiredNativePlugin)
		require.ErrorIs(t, r.MarkRuntimeHealthy(ctx, id, "fixture", "cipher"), errRetiredNativePlugin)
		require.ErrorIs(t, r.UpdateBindingsAndState(ctx, id, nil, "enabled", "", nil, "disabled", "fixture"), errRetiredNativePlugin)
		_, err = r.Install(ctx, &service.PluginInstallation{PluginKey: p.PluginKey}, nil)
		require.ErrorIs(t, err, errRetiredNativePlugin)
	}
	require.Zero(t, base.writes)
	_, err = r.GetByID(ctx, 100)
	require.NoError(t, err)
	_, err = r.Install(ctx, &service.PluginInstallation{PluginKey: "third-party.new"}, nil)
	require.NoError(t, err)
	require.NoError(t, r.UpdateState(ctx, 100, "disabled", "", nil, "fixture", "disabled"))
	require.Equal(t, 2, base.writes)
}
