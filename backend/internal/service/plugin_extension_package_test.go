package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFirstPartyExtensionSignedPackageContainsIndependentRuntimeAndUI(t *testing.T) {
	path := os.Getenv("SUB2API_EXTENSION_TEST_PACKAGE")
	public := os.Getenv("SUB2API_EXTENSION_TEST_PUBLIC_KEY")
	keyID := os.Getenv("SUB2API_EXTENSION_TEST_KEY_ID")
	if path == "" || public == "" {
		t.Skip("provide the locally signed first-party extension package and public test key")
	}
	if keyID == "" {
		keyID = "codexrip-plugins-test"
	}
	file, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	cfg := testPluginConfig(t.TempDir(), false)
	cfg.Plugins.TrustedPublishers = map[string]string{keyID: public}
	installer := NewPluginPackageInstaller(cfg, PluginHostInfo{Version: "0.2.7-codexrip.2", BuildType: "release"})
	installed, err := installer.Install(context.Background(), file, nil)
	require.NoError(t, err)
	require.Equal(t, "codexrip.codex-runtime", installed.PluginKey)
	require.Equal(t, PluginSignatureTrusted, installed.SignatureStatus)
	require.Equal(t, 1, installed.Manifest.Requires.ExtensionAPI)
	require.FileExists(t, installed.BinaryPath)
	for _, name := range []string{"index.html", "assets/app.js", "assets/styles.css", "assets/bridge.js"} {
		require.FileExists(t, filepath.Join(installed.InstallPath, "ui", name))
	}
	for name := range installed.Manifest.Files {
		require.NotContains(t, name, "publisher.key")
		require.NotContains(t, name, "internal/service")
	}
}

func TestFirstPartyBundleContainsMatchingSignedDomainPackages(t *testing.T) {
	path := os.Getenv("SUB2API_EXTENSION_TEST_BUNDLE")
	if path == "" {
		t.Skip("provide the freshly built first-party bundle lock")
	}
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var bundle PluginBundle
	require.NoError(t, json.Unmarshal(raw, &bundle))
	sourceRaw, err := os.ReadFile(filepath.Join("..", "..", "..", "plugins", "bundle.source.json"))
	require.NoError(t, err)
	var source struct {
		Plugins []struct {
			Directory string `json:"directory"`
		} `json:"plugins"`
	}
	require.NoError(t, json.Unmarshal(sourceRaw, &source))
	require.Len(t, bundle.Plugins, len(source.Plugins))
	expectedFiles := map[string]bool{}
	for _, entry := range source.Plugins {
		expectedFiles[entry.Directory+".s2plugin"] = true
	}
	cfg := testPluginConfig(t.TempDir(), false)
	cfg.Plugins.TrustedPublishers = map[string]string{bundle.PublisherKeyID: bundle.PublisherPublicKey}
	installer := NewPluginPackageInstaller(cfg, PluginHostInfo{Version: bundle.HostVersion, BuildType: "release"})
	for _, entry := range bundle.Plugins {
		require.True(t, expectedFiles[entry.File], "bundle contains an undeclared or duplicate domain")
		delete(expectedFiles, entry.File)
		packagePath := filepath.Join(filepath.Dir(path), entry.File)
		content, err := os.ReadFile(packagePath)
		require.NoError(t, err)
		sum := sha256.Sum256(content)
		require.Equal(t, entry.SHA256, hex.EncodeToString(sum[:]))
		file, err := os.Open(packagePath)
		require.NoError(t, err)
		installed, err := installer.Install(context.Background(), file, nil)
		_ = file.Close()
		require.NoError(t, err)
		require.Equal(t, entry.ID, installed.PluginKey)
		require.Equal(t, PluginSignatureTrusted, installed.SignatureStatus)
		for _, asset := range []string{"index.html", "assets/app.js", "assets/bridge.js", "assets/styles.css"} {
			require.FileExists(t, filepath.Join(installed.InstallPath, "ui", asset))
		}
		for name := range installed.Manifest.Files {
			require.NotContains(t, name, "publisher.key")
			require.NotContains(t, name, "internal/service")
		}
	}
}
