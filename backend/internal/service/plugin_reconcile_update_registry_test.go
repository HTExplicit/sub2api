package service

import (
	"context"
	"errors"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

type postUpdateRegistryRepository struct {
	PluginRepository
	installations []*PluginInstallation
	err           error
	lists         int
}

func (r *postUpdateRegistryRepository) List(context.Context) ([]*PluginInstallation, error) {
	r.lists++
	return r.installations, r.err
}

func TestPluginRegistryAfterPromotionReplacesPreDrainPeerSnapshot(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*PluginInstallation, *PluginInstallation)
		want   string
	}{
		{
			name: "dependency_disabled",
			change: func(target, peer *PluginInstallation) {
				target.Manifest.Dependencies = []extensionv1.Dependency{{Capability: extensionv1.CapabilityProvider, Platform: "cindy", AccountType: "apikey"}}
				peer.Bindings[0].Enabled = false
			},
			want: "missing enabled plugin dependency",
		},
		{
			name: "operation_owner_added",
			change: func(target, peer *PluginInstallation) {
				target.Manifest.Operations = map[string][]string{extensionv1.CapabilityAdmin: {"admin.promoted"}}
				peer.Manifest.Operations = map[string][]string{extensionv1.CapabilityAdmin: {"admin.promoted"}}
				peer.Bindings = append(peer.Bindings, PluginBinding{Capability: extensionv1.CapabilityAdmin, Platform: "*", AccountType: "*", Enabled: true})
			},
			want: "extension operation has overlapping enabled owners",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := &PluginInstallation{ID: 7, RuntimeGeneration: 2, State: PluginStateEnabled, Bindings: []PluginBinding{{Capability: extensionv1.CapabilityAdmin, Platform: "*", AccountType: "*", Enabled: true}}}
			peer := &PluginInstallation{ID: 8, State: PluginStateEnabled, Bindings: []PluginBinding{{Capability: extensionv1.CapabilityProvider, Platform: "cindy", AccountType: "apikey", Enabled: true}}}
			freshPeer := *peer
			freshPeer.Bindings = append([]PluginBinding(nil), peer.Bindings...)
			test.change(target, &freshPeer)
			// This is the former reconcile state: the promoted target is new,
			// while every other installation still comes from before the drain.
			before := []*PluginInstallation{target, peer}
			require.NoError(t, validatePluginRegistry(before))
			repo := &postUpdateRegistryRepository{installations: []*PluginInstallation{target, &freshPeer}}
			manager := NewPluginManager(repo, nil, nil, PluginHostInfo{}, nil)
			current, err := manager.pluginRegistryAfterUpdates(context.Background(), before, true)
			require.NoError(t, err)
			require.Equal(t, 1, repo.lists)
			require.Same(t, &freshPeer, current[1])
			require.ErrorContains(t, validatePluginRegistry(current), test.want)
			require.NoError(t, validatePluginRegistry(before), "the old peer snapshot alone would still pass")
		})
	}
}

func TestPluginRegistryAfterPromotionReadFailureRemainsUnavailable(t *testing.T) {
	repo := &postUpdateRegistryRepository{err: errors.New("registry read failed after promotion")}
	manager := NewPluginManager(repo, nil, nil, PluginHostInfo{}, nil)
	manager.extensions.Store(&pluginExtensionRegistry{installations: map[int64]*PluginInstallation{7: {ID: 7}}, runtimes: map[int64]*pluginRuntime{}})
	current, err := manager.pluginRegistryAfterUpdates(context.Background(), []*PluginInstallation{{ID: 7}}, true)
	require.Nil(t, current)
	require.ErrorContains(t, err, "registry read failed after promotion")
	require.Equal(t, 1, repo.lists)
	require.NotEmpty(t, manager.extensions.Load().unavailable)
	require.Contains(t, manager.extensions.Load().installations, int64(7))
	require.NotEmpty(t, manager.route.Load().unavailable)
}

func TestPluginRegistryWithoutPromotionDoesNotAddDatabaseRead(t *testing.T) {
	repo := &postUpdateRegistryRepository{err: errors.New("unexpected registry read")}
	manager := NewPluginManager(repo, nil, nil, PluginHostInfo{}, nil)
	before := []*PluginInstallation{{ID: 7}}
	current, err := manager.pluginRegistryAfterUpdates(context.Background(), before, false)
	require.NoError(t, err)
	require.Zero(t, repo.lists)
	require.Same(t, before[0], current[0])
}
