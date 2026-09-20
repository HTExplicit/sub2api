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

type resourceAccountDirectory struct {
	PluginAccountDirectory
	PluginExtensionAccountDirectory
	accounts map[int64]extensionv1.Account
}

func (d *resourceAccountDirectory) ReadExtensionAccount(_ context.Context, id int64) (*extensionv1.Account, error) {
	account := d.accounts[id]
	return &account, nil
}

func TestPluginResourceAccountSelectionsRespectScopeAndRollout(t *testing.T) {
	installation := &PluginInstallation{ID: 7, RuntimeGeneration: 2, State: PluginStateEnabled, Bindings: []PluginBinding{{Capability: extensionv1.CapabilityAdmin, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true, RolloutPercent: 100}}}
	repo := &pluginTokenRepository{installation: installation}
	manager := NewPluginManager(repo, pluginTokenEncryptor{}, nil, PluginHostInfo{}, nil)
	manager.accountDirectory = &resourceAccountDirectory{accounts: map[int64]extensionv1.Account{
		1: {ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, 2: {ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
	}}
	ctx := WithPluginExecution(context.Background(), installation)
	require.NoError(t, manager.ValidateResourceAccounts(ctx, extensionv1.CapabilityAdmin, []int64{1}, false))
	require.Error(t, manager.ValidateResourceAccounts(ctx, extensionv1.CapabilityAdmin, []int64{1, 2}, false))
	require.Error(t, manager.ValidateResourceAccounts(ctx, extensionv1.CapabilityAdmin, nil, true))
	installation.Bindings[0].RolloutPercent = int(stablePluginBucket(1))
	require.Error(t, manager.ValidateResourceAccounts(ctx, extensionv1.CapabilityAdmin, []int64{1}, false))
	installation.Bindings[0].Platform, installation.Bindings[0].AccountType, installation.Bindings[0].RolloutPercent = "*", "*", 100
	require.NoError(t, manager.ValidateResourceAccounts(ctx, extensionv1.CapabilityAdmin, nil, true))
	require.Error(t, manager.ValidateResourceAccounts(ctx, extensionv1.CapabilityAdmin, []int64{-1}, true))
}

func TestRetainedPluginResourcesDoNotRequireRunningBusinessCapability(t *testing.T) {
	grant := extensionv1.ResourceGrant{Name: "image.job", Capability: extensionv1.CapabilityRequest, Permission: "user"}
	installation := &PluginInstallation{ID: 7, PluginKey: "codexrip.image-tools", State: PluginStateDisabled, Manifest: PluginManifest{Resources: []extensionv1.ResourceGrant{grant}}}
	manager := NewPluginManager(&pluginTokenRepository{installation: installation}, pluginTokenEncryptor{}, nil, PluginHostInfo{}, nil)
	_, release, err := manager.BindResourceContext(context.Background(), 7, "", grant, true)
	require.NoError(t, err)
	release()
	_, _, err = manager.BindResourceContext(context.Background(), 7, "", grant)
	require.Error(t, err)
	_, _, err = manager.BindResourceContext(context.Background(), 7, "", extensionv1.ResourceGrant{Name: "image.create", Capability: grant.Capability, Permission: "user"}, true)
	require.Error(t, err)
}

func TestPluginFilteredResourceUsesOnlyHostDeclaredFixedScope(t *testing.T) {
	installation := &PluginInstallation{ID: 7, RuntimeGeneration: 2, State: PluginStateEnabled, Bindings: []PluginBinding{{Capability: extensionv1.CapabilityProvider, Platform: PlatformCindy, AccountType: AccountTypeAPIKey, Enabled: true, RolloutPercent: 100}}}
	manager := NewPluginManager(&pluginTokenRepository{installation: installation}, pluginTokenEncryptor{}, nil, PluginHostInfo{}, nil)
	ctx := WithPluginExecution(context.Background(), installation)
	require.Error(t, manager.ValidateResourceAccounts(ctx, extensionv1.CapabilityProvider, nil, true))
	require.NoError(t, manager.ValidateResourceAccounts(ctx, extensionv1.CapabilityProvider, nil, true, PlatformCindy, AccountTypeAPIKey))
	require.Error(t, manager.ValidateResourceAccounts(ctx, extensionv1.CapabilityProvider, nil, true, PlatformOpenAI, AccountTypeAPIKey))
	installation.Bindings[0].RolloutPercent = 50
	require.Error(t, manager.ValidateResourceAccounts(ctx, extensionv1.CapabilityProvider, nil, true, PlatformCindy, AccountTypeAPIKey))
}
