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
	}}, Bindings: []PluginBinding{{Capability: extensionv1.CapabilityRequest, Platform: "*", AccountType: "*", Enabled: true}}}
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

func TestPluginPageSessionRejectsDisabledContributionButAllowsFaultStaticPage(t *testing.T) {
	installation := &PluginInstallation{ID: 8, State: PluginStateDisabled, Manifest: PluginManifest{UI: PluginUIManifest{Entrypoint: "ui/index.html"}, Contributions: []extensionv1.Contribution{{ID: "page", Permission: "user", Entrypoint: "ui/page/index.html"}}}, Bindings: []PluginBinding{{Capability: extensionv1.CapabilityRequest, Platform: "*", AccountType: "*", Enabled: false}}}
	repo := &pluginTokenRepository{installation: installation}
	manager := NewPluginManager(repo, pluginTokenEncryptor{}, nil, PluginHostInfo{}, nil)
	_, err := manager.CreateUIAssetSession(context.Background(), 8, "page", "user", time.Minute)
	require.ErrorIs(t, err, ErrExtensionOperationDisabled)

	installation.State = PluginStateError
	installation.Bindings[0].Enabled = true
	session, err := manager.CreateUIAssetSession(context.Background(), 8, "page", "user", time.Minute)
	require.NoError(t, err)
	require.NotEmpty(t, session.Token)
}

func TestPluginConfigUISessionRemainsAvailableWhenBusinessBindingsAreDisabled(t *testing.T) {
	installation := &PluginInstallation{ID: 9, State: PluginStateDisabled, Manifest: PluginManifest{UI: PluginUIManifest{Entrypoint: "ui/index.html"}}}
	manager := NewPluginManager(&pluginTokenRepository{installation: installation}, pluginTokenEncryptor{}, nil, PluginHostInfo{}, nil)

	session, err := manager.CreateUIAssetSession(context.Background(), installation.ID, "", "admin", time.Minute)
	require.NoError(t, err, "the administrator must be able to reopen a disabled plugin's configuration page")
	require.Equal(t, "admin", session.Permission)
	claims, err := manager.parseUIAssetToken(session.Token)
	require.NoError(t, err)
	require.Equal(t, "ui/index.html", claims.Entrypoint)
	installation.Bindings = []PluginBinding{{Capability: extensionv1.CapabilityRequest, Platform: "*", AccountType: "*", Enabled: false}}
	_, err = manager.CreateUIAssetSession(context.Background(), installation.ID, "", "admin", time.Minute)
	require.NoError(t, err, "persisted disabled bindings must not prevent repairing the plugin configuration")
	installation.State = PluginStateError
	_, err = manager.CreateUIAssetSession(context.Background(), installation.ID, "", "admin", time.Minute)
	require.NoError(t, err, "runtime health must not gate the static administrator configuration surface")
}

func TestPluginBusinessUISessionRequiresItsCapabilityBinding(t *testing.T) {
	installation := &PluginInstallation{
		ID:    10,
		State: PluginStateEnabled,
		Manifest: PluginManifest{UI: PluginUIManifest{Entrypoint: "ui/index.html"}, Contributions: []extensionv1.Contribution{{
			ID: "page", Permission: "admin", Capability: extensionv1.CapabilityRequest, Entrypoint: "ui/page/index.html",
		}}},
	}
	manager := NewPluginManager(&pluginTokenRepository{installation: installation}, pluginTokenEncryptor{}, nil, PluginHostInfo{}, nil)

	_, err := manager.CreateUIAssetSession(context.Background(), installation.ID, "page", "admin", time.Minute)
	require.ErrorIs(t, err, ErrExtensionOperationDisabled, "an empty binding set must not authorize a new business page")

	installation.Bindings = []PluginBinding{{Capability: extensionv1.CapabilityAdmin, Platform: "*", AccountType: "*", Enabled: true}}
	_, err = manager.CreateUIAssetSession(context.Background(), installation.ID, "page", "admin", time.Minute)
	require.ErrorIs(t, err, ErrExtensionOperationDisabled, "an unrelated enabled capability must not authorize the page")

	installation.Bindings[0] = PluginBinding{Capability: extensionv1.CapabilityRequest, Platform: "openai", AccountType: "oauth", Enabled: true, RolloutPercent: 50}
	session, err := manager.CreateUIAssetSession(context.Background(), installation.ID, "page", "admin", time.Minute)
	require.NoError(t, err, "a scoped enabled binding may retain the static page for its rollout cohort")
	require.NotEmpty(t, session.Token)
}

func TestPluginGlobalBusinessUISessionRequiresFullWildcardBinding(t *testing.T) {
	installation := &PluginInstallation{
		ID:    11,
		State: PluginStateEnabled,
		Manifest: PluginManifest{UI: PluginUIManifest{Entrypoint: "ui/index.html"}, Contributions: []extensionv1.Contribution{{
			ID: "global", Permission: "admin", Capability: extensionv1.CapabilityAdmin, AllAccounts: true, Entrypoint: "ui/global/index.html",
		}}},
		Bindings: []PluginBinding{{Capability: extensionv1.CapabilityAdmin, Platform: "openai", AccountType: "*", Enabled: true, RolloutPercent: 100}},
	}
	manager := NewPluginManager(&pluginTokenRepository{installation: installation}, pluginTokenEncryptor{}, nil, PluginHostInfo{}, nil)

	for _, tc := range []struct {
		name     string
		platform string
		kind     string
		rollout  int
		allowed  bool
	}{
		{name: "scoped platform", platform: "openai", kind: "*", rollout: 100},
		{name: "scoped account type", platform: "*", kind: "oauth", rollout: 100},
		{name: "partial rollout", platform: "*", kind: "*", rollout: 99},
		{name: "full wildcard", platform: "*", kind: "*", rollout: 100, allowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installation.Bindings[0].Platform = tc.platform
			installation.Bindings[0].AccountType = tc.kind
			installation.Bindings[0].RolloutPercent = tc.rollout
			session, err := manager.CreateUIAssetSession(context.Background(), installation.ID, "global", "admin", time.Minute)
			if tc.allowed {
				require.NoError(t, err)
				require.NotEmpty(t, session.Token)
			} else {
				require.ErrorIs(t, err, ErrExtensionOperationDisabled)
			}
		})
	}
}

func TestPluginBusinessUISessionDoesNotInvalidateStaticTokenOnDisable(t *testing.T) {
	installation := &PluginInstallation{ID: 12, State: PluginStateEnabled, PackageSHA256: "package-v1", Manifest: PluginManifest{
		UI:            PluginUIManifest{Entrypoint: "ui/index.html"},
		Contributions: []extensionv1.Contribution{{ID: "page", Permission: "admin", Capability: extensionv1.CapabilityRequest, Entrypoint: "ui/page/index.html"}},
	}, Bindings: []PluginBinding{{Capability: extensionv1.CapabilityRequest, Platform: "*", AccountType: "*", Enabled: true, RolloutPercent: 100}}}
	repo := &pluginTokenRepository{installation: installation}
	manager := NewPluginManager(repo, pluginTokenEncryptor{}, nil, PluginHostInfo{}, nil)

	session, err := manager.CreateUIAssetSession(context.Background(), installation.ID, "page", "admin", time.Minute)
	require.NoError(t, err)
	installation.State = PluginStateDisabled
	installation.ConfigEncrypted = `{"revised":true}`
	installation.Revision++
	installation.RuntimeGeneration++
	require.Equal(t, int64(installation.ID), mustResolvePluginUIToken(t, manager, session.Token))
}

func mustResolvePluginUIToken(t *testing.T, manager *PluginManager, token string) int64 {
	t.Helper()
	id, err := manager.ResolveUIAssetToken(token)
	require.NoError(t, err)
	return id
}

func TestPluginManifestRejectsMissingContributedUI(t *testing.T) {
	manifest := testPluginManifest(nil)
	manifest.Contributions = []extensionv1.Contribution{{ID: "page", Slot: "surface", Permission: "admin", Label: map[string]string{"en": "Page"}, Entrypoint: "ui/compiled/index.html"}}
	require.ErrorContains(t, manifest.Validate(), "未包含在签名资源")
}
