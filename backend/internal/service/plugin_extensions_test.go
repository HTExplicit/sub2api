package service

import (
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

func TestPluginExtensionDesiredStateAndContributions(t *testing.T) {
	m := NewPluginManager(nil, nil, nil, PluginHostInfo{}, nil)
	installation := &PluginInstallation{ID: 1, State: PluginStateError,
		Manifest: PluginManifest{Contributions: []extensionv1.Contribution{{ID: "tickets", Slot: "account.actions", Permission: "admin"}}},
		Bindings: []PluginBinding{{Capability: extensionv1.CapabilityAdmin, Platform: "openai", AccountType: "oauth", Enabled: true}},
	}
	m.publishExtensionRegistryLocked([]*PluginInstallation{installation}, "")
	items := m.Contributions()
	require.Len(t, items, 1, "an enabled unhealthy plugin must retain its unavailable UI contribution")
	require.False(t, items[0].Available)
	require.Equal(t, "plugin_unavailable", items[0].Reason)
	require.True(t, pluginHasCapability(installation, extensionv1.CapabilityAdmin, "openai", "oauth"))
	require.False(t, pluginHasCapability(installation, extensionv1.CapabilityAdmin, "cindy", "apikey"))
	m.removeRuntimeLocked(installation.ID)
	require.Empty(t, m.Contributions(), "disabling must remove contributions immediately")
}

func TestPluginExtensionDependenciesRespectEnabledBindings(t *testing.T) {
	provider := &PluginInstallation{ID: 1, Bindings: []PluginBinding{{Capability: extensionv1.CapabilityProvider, Platform: "cindy", AccountType: "apikey", Enabled: true}}}
	consumer := &PluginInstallation{ID: 2, Manifest: PluginManifest{Dependencies: []extensionv1.Dependency{{Capability: extensionv1.CapabilityProvider, Platform: "cindy", AccountType: "apikey"}}}}
	require.NoError(t, pluginDependenciesReady(consumer, []*PluginInstallation{provider, consumer}))
	provider.Bindings[0].Enabled = false
	require.Error(t, pluginDependenciesReady(consumer, []*PluginInstallation{provider, consumer}))
}
