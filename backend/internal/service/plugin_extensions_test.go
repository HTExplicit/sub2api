package service

import (
	"encoding/json"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

type pluginContributionCipher struct{}

func (pluginContributionCipher) Encrypt(value string) (string, error) { return value, nil }
func (pluginContributionCipher) Decrypt(value string) (string, error) { return value, nil }

func TestPublicPluginSurfaceRetainsFailureAndHidesDisabledConfiguration(t *testing.T) {
	m := NewPluginManager(nil, pluginContributionCipher{}, nil, PluginHostInfo{}, nil)
	installation := &PluginInstallation{ID: 2, State: PluginStateError, ConfigEncrypted: `{"studio_enabled":true}`,
		Manifest: PluginManifest{Contributions: []extensionv1.Contribution{
			{ID: "image-studio", Slot: "surface", Permission: "user", ConfigFlag: "studio_enabled", Action: "private.action", Entrypoint: "ui/index.html"},
			{ID: "settings", Slot: "admin.settings", Permission: "admin"},
		}},
		Bindings: []PluginBinding{{Capability: extensionv1.CapabilityRequest, Platform: "*", AccountType: "*", Enabled: true}},
	}
	m.publishExtensionRegistryLocked([]*PluginInstallation{installation}, "")
	items := m.PublicContributions()
	require.Len(t, items, 1)
	require.False(t, items[0].Available)
	raw, err := json.Marshal(items)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private.action")
	require.NotContains(t, string(raw), "ui/index.html")
	require.NotContains(t, string(raw), "studio_enabled")
	installation.ConfigEncrypted = `{"studio_enabled":false}`
	m.publishExtensionRegistryLocked([]*PluginInstallation{installation}, "")
	require.Empty(t, m.PublicContributions())
	installation.ConfigEncrypted = `{"studio_enabled":true}`
	installation.Bindings[0].Enabled = false
	m.publishExtensionRegistryLocked([]*PluginInstallation{installation}, "")
	require.Empty(t, m.PublicContributions())
}

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

func TestContributionFollowsItsOwnCapabilityWhileOtherCapabilitiesRemainEnabled(t *testing.T) {
	m := NewPluginManager(nil, nil, nil, PluginHostInfo{}, nil)
	installation := &PluginInstallation{ID: 1,
		Manifest: PluginManifest{Contributions: []extensionv1.Contribution{{ID: "recovery", Slot: "surface", Capability: extensionv1.CapabilityRecovery, Permission: "admin"}}},
		Bindings: []PluginBinding{
			{Capability: extensionv1.CapabilityAdmin, Platform: "*", AccountType: "*", Enabled: true},
			{Capability: extensionv1.CapabilityRecovery, Platform: "openai", AccountType: "*", Enabled: false},
		},
	}
	m.publishExtensionRegistryLocked([]*PluginInstallation{installation}, "")
	require.Empty(t, m.Contributions())
	installation.Bindings[1].Enabled = true
	m.publishExtensionRegistryLocked([]*PluginInstallation{installation}, "")
	items := m.Contributions()
	require.Len(t, items, 1)
	require.False(t, items[0].Available, "an enabled failed capability stays visible with its reason")
}

func TestPluginExtensionDependenciesRespectEnabledBindings(t *testing.T) {
	provider := &PluginInstallation{ID: 1, Bindings: []PluginBinding{{Capability: extensionv1.CapabilityProvider, Platform: "cindy", AccountType: "apikey", Enabled: true}}}
	consumer := &PluginInstallation{ID: 2, Manifest: PluginManifest{Dependencies: []extensionv1.Dependency{{Capability: extensionv1.CapabilityProvider, Platform: "cindy", AccountType: "apikey"}}}}
	require.NoError(t, pluginDependenciesReady(consumer, []*PluginInstallation{provider, consumer}))
	provider.Bindings[0].Enabled = false
	require.Error(t, pluginDependenciesReady(consumer, []*PluginInstallation{provider, consumer}))
}
