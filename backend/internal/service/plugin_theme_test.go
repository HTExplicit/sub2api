package service

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	hcplugin "github.com/hashicorp/go-plugin"
	"github.com/stretchr/testify/require"
)

func TestPublicThemeAssetsRequireCurrentActivationDeclarationAndDigest(t *testing.T) {
	files := map[string][]byte{"bin/plugin": []byte("binary"), "ui/index.html": []byte("<html></html>"), "ui/assets/theme.css": []byte("binary"), "ui/assets/font.woff2": []byte("binary"), "ui/assets/private.js": []byte("binary")}
	manifest := testPluginManifest(files)
	manifest.Requires.ExtensionAPI = 1
	manifest.Capabilities = []PluginCapability{{ID: extensionv1.CapabilityUI, Platform: "*", AccountType: "*"}}
	manifest.Contributions = []extensionv1.Contribution{{ID: "theme", Slot: "theme", Permission: "public", ConfigFlag: "theme_enabled", Entrypoint: "ui/assets/theme.css", Assets: []string{"ui/assets/font.woff2"}, Label: map[string]string{"en": "Theme"}}}
	cfg := testPluginConfig(t.TempDir(), true)
	info := PluginHostInfo{Version: "0.1.179"}
	installer := NewPluginPackageInstaller(cfg, info)
	installation, err := installer.Install(context.Background(), bytes.NewReader(buildPluginArchive(t, manifest, nil, "", nil)), nil)
	require.NoError(t, err)
	installation.ID, installation.State = 4, PluginStateEnabled
	installation.Bindings = []PluginBinding{{Capability: extensionv1.CapabilityUI, Platform: "*", AccountType: "*", Enabled: true}}
	repo := &pluginTokenRepository{installation: installation}
	manager := NewPluginManager(repo, pluginTokenEncryptor{}, cfg, info, nil)
	runtime := &pluginRuntime{client: &hcplugin.Client{}}
	configuration := json.RawMessage(`{"theme_enabled":true}`)
	runtime.configSnapshot.Store(&configuration)
	manager.runtimes[4] = runtime
	manager.publishExtensionRegistryLocked([]*PluginInstallation{installation}, "")
	revision := pluginResourceRevision(installation)
	public := manager.PublicContributions()
	require.Len(t, public, 1)
	require.Contains(t, public[0].StylesheetURL, revision)
	require.Empty(t, public[0].Assets)
	for _, asset := range []string{"assets/theme.css", "assets/font.woff2"} {
		data, _, err := manager.ReadPublicThemeAsset(context.Background(), 4, revision, asset)
		require.NoError(t, err)
		require.Equal(t, "binary", string(data))
	}
	for _, asset := range []string{"index.html", "assets/private.js", "../bin/plugin"} {
		_, _, err := manager.ReadPublicThemeAsset(context.Background(), 4, revision, asset)
		require.Error(t, err)
	}
	_, _, err = manager.ReadPublicThemeAsset(context.Background(), 4, "old-revision", "assets/theme.css")
	require.Error(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(installation.InstallPath, "ui", "assets", "theme.css"), []byte("changed"), 0600))
	_, _, err = manager.ReadPublicThemeAsset(context.Background(), 4, revision, "assets/theme.css")
	require.Error(t, err)
	installation.Bindings[0].Enabled = false
	manager.publishExtensionRegistryLocked([]*PluginInstallation{installation}, "")
	require.Empty(t, manager.PublicContributions())
	_, _, err = manager.ReadPublicThemeAsset(context.Background(), 4, revision, "assets/font.woff2")
	require.Error(t, err)
}
