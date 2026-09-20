package service

import (
	"context"
	"strings"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	hcplugin "github.com/hashicorp/go-plugin"
	"github.com/stretchr/testify/require"
)

func TestPluginResourcesBindDeclaredScopeAndRejectPersistedDisable(t *testing.T) {
	grant := extensionv1.ResourceGrant{Name: "prompts.list", Capability: extensionv1.CapabilityRequest, Permission: "admin"}
	installation := &PluginInstallation{ID: 7, PluginKey: "codexrip.prompt-skills", Version: "0.2.7", RuntimeGeneration: 1, PackageSHA256: strings.Repeat("a", 64), State: PluginStateEnabled,
		Manifest: PluginManifest{Resources: []extensionv1.ResourceGrant{grant}}, Bindings: []PluginBinding{{Capability: grant.Capability, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true, RolloutPercent: 100}}}
	repo := &pluginTokenRepository{installation: installation}
	manager := NewPluginManager(repo, pluginTokenEncryptor{}, nil, PluginHostInfo{}, nil)
	runtime := &pluginRuntime{installation: installation, client: hcplugin.NewClient(&hcplugin.ClientConfig{}), done: make(chan struct{})}
	manager.extensions.Store(&pluginExtensionRegistry{installations: map[int64]*PluginInstallation{7: installation}, runtimes: map[int64]*pluginRuntime{7: runtime}})
	ctx, release, err := manager.BindResourceContext(context.Background(), 0, "", grant)
	require.NoError(t, err, "a global management resource may use an OAuth-only binding")
	execution, ok := PluginExecutionFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, installation.ID, execution.ID)
	release()
	require.Zero(t, runtime.inFlight.Load())
	_, _, err = manager.BindResourceContext(context.Background(), 7, strings.Repeat("b", 64), grant)
	require.ErrorIs(t, err, ErrPluginUISessionChanged)
	_, _, err = manager.BindResourceContext(context.Background(), 7, "", extensionv1.ResourceGrant{Name: "prompts.delete", Capability: grant.Capability, Permission: "admin"})
	require.ErrorIs(t, err, ErrExtensionOperationDisabled)
	items, err := manager.ResourceDescriptors(context.Background(), 7, "user", []extensionv1.ResourceDescriptor{{ResourceGrant: grant, Method: "GET", Path: "/api/v1/admin/system-prompts"}})
	require.NoError(t, err)
	require.Empty(t, items, "user metadata cannot expose an administrator resource")
	updated := *installation
	updated.Bindings = append([]PluginBinding(nil), installation.Bindings...)
	updated.Bindings[0].Enabled = false
	repo.installation = &updated
	_, _, err = manager.BindResourceContext(context.Background(), 7, "", grant)
	require.ErrorIs(t, err, ErrExtensionOperationDisabled, "a stale process registry cannot authorize a database-disabled binding")
}
