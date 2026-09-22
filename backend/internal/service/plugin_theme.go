package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
)

func (m *PluginManager) installedByID(id int64) (*PluginInstallation, *pluginRuntime) {
	registry := m.extensions.Load()
	if registry == nil {
		return nil, nil
	}
	return registry.installations[id], registry.runtimes[id]
}

func pluginResourceRevision(installation *PluginInstallation) string {
	raw, _ := json.Marshal(installation.Manifest.Files)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func publicThemeAssetURL(installation *PluginInstallation, path string) string {
	return fmt.Sprintf("/api/v1/settings/plugins/%d/theme/%s/%s", installation.ID, pluginResourceRevision(installation), strings.TrimPrefix(path, "ui/"))
}

// Only an enabled public theme may publish its declared stylesheet and fonts.
// Configuration pages, scripts, and arbitrary package files stay private.
func (m *PluginManager) ReadPublicThemeAsset(ctx context.Context, id int64, revision, relative string) ([]byte, string, error) {
	installation, _ := m.installedByID(id)
	if installation == nil || revision != pluginResourceRevision(installation) || !safePluginRelativePath(relative) {
		return nil, "", os.ErrNotExist
	}
	path := "ui/" + relative
	allowed := false
	for _, item := range m.Contributions() {
		if item.PluginID != id || item.Permission != "public" || item.Slot != "theme" || !item.Available {
			continue
		}
		if path == item.Entrypoint || slices.Contains(item.Assets, path) {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, "", os.ErrNotExist
	}
	expected := installation.Manifest.Files[path]
	if expected == "" {
		return nil, "", os.ErrNotExist
	}
	data, actualPath, err := m.ReadUIAsset(ctx, id, relative)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(data)
	if actualPath != path || hex.EncodeToString(digest[:]) != expected {
		return nil, "", os.ErrNotExist
	}
	return data, actualPath, nil
}
