package service

import (
	"context"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

func TestPluginPageSessionCannotSelectAnotherRoleOrUndeclaredEntry(t *testing.T) {
	installation := &PluginInstallation{ID: 7, Manifest: PluginManifest{UI: PluginUIManifest{Entrypoint: "ui/index.html"}, Contributions: []extensionv1.Contribution{
		{ID: "management", Permission: "admin", Entrypoint: "ui/compiled/index.html"},
		{ID: "studio", Permission: "user", Entrypoint: "ui/studio/index.html"},
	}}}
	manager := NewPluginManager(&pluginTokenRepository{installation: installation}, pluginTokenEncryptor{}, nil, PluginHostInfo{}, nil)
	ctx := context.Background()
	_, err := manager.CreateUIAssetSession(ctx, 7, "management", "user", time.Minute)
	require.Error(t, err)
	_, err = manager.CreateUIAssetSession(ctx, 7, "", "user", time.Minute)
	require.Error(t, err)
	_, err = manager.CreateUIAssetSession(ctx, 7, "undeclared", "admin", time.Minute)
	require.Error(t, err)
	session, err := manager.CreateUIAssetSession(ctx, 7, "studio", "admin", time.Minute)
	require.NoError(t, err)
	require.Equal(t, "user", session.Permission, "administrator browsing a user page still receives a user bridge")
	claims, err := manager.parseUIAssetToken(session.Token)
	require.NoError(t, err)
	require.Equal(t, "ui/studio/index.html", claims.Entrypoint)
	require.Equal(t, "user", claims.Permission)
}

func TestPluginManifestRejectsMissingContributedUI(t *testing.T) {
	manifest := testPluginManifest(nil)
	manifest.Contributions = []extensionv1.Contribution{{ID: "page", Slot: "surface", Permission: "admin", Label: map[string]string{"en": "Page"}, Entrypoint: "ui/compiled/index.html"}}
	require.ErrorContains(t, manifest.Validate(), "未包含在签名资源")
}
