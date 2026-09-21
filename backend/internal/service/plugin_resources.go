package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// Resource calls retain the original authenticated HTTP handler and middleware.
// This gate additionally binds the call to a declared capability, package and
// execution lifetime; compatibility routes cannot bypass a disabled plugin.
func (m *PluginManager) BindResourceContext(ctx context.Context, id int64, expectedPackage string, resource extensionv1.ResourceGrant, retained ...bool) (context.Context, func(), error) {
	if len(retained) > 0 && retained[0] && id > 0 {
		installation, err := m.repo.GetByID(ctx, id)
		if err != nil || !pluginDeclaresResource(installation, resource) {
			return nil, nil, ErrExtensionOperationDisabled
		}
		if expectedPackage != "" && installation.PackageSHA256 != expectedPackage {
			return nil, nil, ErrPluginUISessionChanged
		}
		return WithPluginExecution(ctx, installation), func() {}, nil
	}
	registry := m.extensions.Load()
	if registry == nil || registry.unavailable != "" {
		return nil, nil, ErrExtensionOperationUnavailable
	}
	var selected *PluginInstallation
	for candidateID, installation := range registry.installations {
		if id > 0 && candidateID != id {
			continue
		}
		if !pluginDeclaresResource(installation, resource) {
			continue
		}
		enabled := false
		for _, binding := range installation.Bindings {
			enabled = enabled || (binding.Enabled && binding.Capability == resource.Capability)
		}
		if !enabled {
			continue
		}
		if selected != nil {
			return nil, nil, ErrExtensionOperationUnavailable
		}
		selected = installation
	}
	if selected == nil {
		return nil, nil, ErrExtensionOperationDisabled
	}
	current, err := m.repo.GetByID(ctx, selected.ID)
	if err != nil {
		return nil, nil, ErrExtensionOperationUnavailable
	}
	if expectedPackage != "" && current.PackageSHA256 != expectedPackage {
		return nil, nil, ErrPluginUISessionChanged
	}
	if current.State == PluginStateUpdating || !samePluginRuntime(current, selected) {
		return nil, nil, ErrExtensionOperationUnavailable
	}
	// Recheck persisted binding intent before a UI mutation, not just the
	// process-local registry refreshed by the background reconciler.
	enabled := false
	for _, binding := range current.Bindings {
		enabled = enabled || (binding.Enabled && binding.Capability == resource.Capability)
	}
	if !enabled {
		return nil, nil, ErrExtensionOperationDisabled
	}
	runtime := registry.runtimes[selected.ID]
	if runtime == nil || runtime.client == nil || runtime.client.Exited() || !pluginDependenciesHealthy(current, registry, map[int64]bool{}) {
		return nil, nil, ErrExtensionOperationUnavailable
	}
	bound, release, err := m.bindHostPolicyContext(ctx, current, runtime)
	if err != nil {
		return nil, nil, err
	}
	return WithPluginExecution(bound, current), release, nil
}

func pluginDeclaresResource(installation *PluginInstallation, resource extensionv1.ResourceGrant) bool {
	if installation == nil {
		return false
	}
	for _, grant := range installation.Manifest.Resources {
		if grant.Name == resource.Name && grant.Capability == resource.Capability && grant.Permission == resource.Permission {
			return true
		}
	}
	return false
}

func (m *PluginManager) ValidateResourceAccounts(ctx context.Context, capability string, ids []int64, filtered bool, fixedScope ...string) error {
	execution, ok := PluginExecutionFromContext(ctx)
	if !ok {
		return ErrExtensionOperationUnavailable
	}
	installation, err := m.repo.GetByID(ctx, execution.ID)
	if err != nil || installation.RuntimeGeneration != execution.Generation || installation.State == PluginStateUpdating {
		return ErrExtensionOperationUnavailable
	}
	for _, id := range ids {
		if id <= 0 {
			return errors.New("invalid resource account identifier")
		}
	}
	if filtered {
		platform, kind := "*", "*"
		if len(fixedScope) == 2 && fixedScope[0] != "" && fixedScope[1] != "" {
			platform, kind = fixedScope[0], fixedScope[1]
		}
		for _, binding := range installation.Bindings {
			if binding.Enabled && binding.Capability == capability && pluginScopeMatches(binding.Platform, platform) && pluginScopeMatches(binding.AccountType, kind) && binding.RolloutPercent == 100 {
				return nil
			}
		}
		return errors.New("filtered account operations require a binding covering all accounts; select explicit accounts instead")
	}
	if len(ids) == 0 {
		return nil
	}
	directory, ok := m.accountDirectory.(PluginExtensionAccountDirectory)
	if !ok {
		return ErrExtensionOperationUnavailable
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 {
			return errors.New("invalid resource account identifier")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		account, err := directory.ReadExtensionAccount(ctx, id)
		if err != nil || account == nil || account.ID != id {
			return errors.New("resource account unavailable")
		}
		allowed := false
		for _, binding := range installation.Bindings {
			if binding.Enabled && binding.Capability == capability && pluginScopeMatches(binding.Platform, account.Platform) && pluginScopeMatches(binding.AccountType, account.Type) && int(stablePluginBucket(id)) < binding.RolloutPercent {
				allowed = true
				break
			}
		}
		if !allowed {
			return errors.New("account is outside the enabled plugin scope")
		}
	}
	return nil
}

func (m *PluginManager) ResourceDescriptors(ctx context.Context, id int64, permission string, registered []extensionv1.ResourceDescriptor) ([]extensionv1.ResourceDescriptor, error) {
	installation, err := m.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if permission != "admin" && permission != "user" {
		return nil, errors.New("invalid resource permission")
	}
	originAvailable := true
	if identity, retained := RetainedAccountViewIdentity(ctx); retained {
		_, release, err := m.BindAccountViewRequest(ctx, extensionv1.AccountViewContextV1{AccountViewIdentityV1: *identity})
		originAvailable = err == nil
		if release != nil {
			release()
		}
	}
	out := make([]extensionv1.ResourceDescriptor, 0)
	availability := map[string]bool{}
	for _, descriptor := range registered {
		if descriptor.Permission != permission || !pluginDeclaresResource(installation, descriptor.ResourceGrant) {
			continue
		}
		policy, _ := json.Marshal(struct {
			Capabilities                 []string
			Retained, AllAccounts        bool
			Platform, AccountType, Owner string
		}{resourceRequiredCapabilities(descriptor), descriptor.Retained, descriptor.AllAccounts, descriptor.FilterPlatform, descriptor.FilterAccountType, descriptor.OwnerPluginKey})
		cacheKey := string(policy)
		ready, known := availability[cacheKey]
		if !known {
			bound, release, err := m.BindResourceContext(ctx, id, installation.PackageSHA256, descriptor.ResourceGrant, descriptor.Retained)
			if err == nil && !descriptor.Retained {
				err = m.ValidateResourcePolicy(bound, descriptor, nil, descriptor.AllAccounts)
			}
			ready = err == nil
			if release != nil {
				release()
			}
			availability[cacheKey] = ready
		}
		descriptor.Available = ready && (descriptor.Retained || originAvailable)
		out = append(out, descriptor)
	}
	return out, nil
}

func resourceRequiredCapabilities(descriptor extensionv1.ResourceDescriptor) []string {
	capabilities := append([]string{descriptor.Capability}, descriptor.RequiredCapabilities...)
	slices.Sort(capabilities)
	return slices.Compact(capabilities)
}

func (m *PluginManager) ValidateResourcePolicy(ctx context.Context, descriptor extensionv1.ResourceDescriptor, ids []int64, filtered bool) error {
	execution, bound := PluginExecutionFromContext(ctx)
	if !bound {
		return ErrExtensionOperationUnavailable
	}
	installation, err := m.repo.GetByID(ctx, execution.ID)
	if err != nil || installation == nil || installation.RuntimeGeneration != execution.Generation || installation.State != PluginStateEnabled || (descriptor.OwnerPluginKey != "" && installation.PluginKey != descriptor.OwnerPluginKey) {
		return ErrExtensionOperationUnavailable
	}
	registry := m.extensions.Load()
	if registry == nil || registry.unavailable != "" || !samePluginRuntime(installation, registry.installations[execution.ID]) {
		return ErrExtensionOperationUnavailable
	}
	if _, applied := m.contributionAppliedRevision(installation, registry.runtimes[execution.ID]); !applied {
		return ErrExtensionOperationUnavailable
	}
	for _, capability := range resourceRequiredCapabilities(descriptor) {
		enabled := false
		for _, binding := range installation.Bindings {
			enabled = enabled || (binding.Enabled && binding.Capability == capability)
		}
		if !enabled {
			return ErrExtensionOperationDisabled
		}
		if err := m.ValidateResourceAccounts(ctx, capability, ids, filtered, descriptor.FilterPlatform, descriptor.FilterAccountType); err != nil {
			return err
		}
	}
	for _, contribution := range installation.Manifest.Contributions {
		if contribution.ResourceAction == nil || contribution.ResourceAction.Resource != descriptor.Name {
			continue
		}
		directory, ok := m.accountDirectory.(accountViewAccountDirectory)
		if !ok && len(ids) > 0 {
			return ErrExtensionOperationUnavailable
		}
		for _, id := range ids {
			account, err := directory.ReadAccountViewAccount(ctx, id)
			if err != nil || account == nil || account.ID != id || !contributionAccountAllowed(installation, &contribution, *extensionAccount(account)) || (contribution.ResourceAction.RowPredicate != nil && !accountMatchesViewPredicate(account, *contribution.ResourceAction.RowPredicate)) {
				return ErrAccountViewScope
			}
		}
	}
	return nil
}

type boundResourcePolicyKey struct{}
type boundResourcePolicy struct {
	manager    *PluginManager
	descriptor extensionv1.ResourceDescriptor
}

func PluginResourceBound(ctx context.Context) bool {
	_, bound := ctx.Value(boundResourcePolicyKey{}).(boundResourcePolicy)
	return bound
}

func (m *PluginManager) WithResourcePolicy(ctx context.Context, descriptor extensionv1.ResourceDescriptor) context.Context {
	return context.WithValue(ctx, boundResourcePolicyKey{}, boundResourcePolicy{m, descriptor})
}

func ValidateBoundResourceTargets(ctx context.Context, ids []int64) error {
	policy, bound := ctx.Value(boundResourcePolicyKey{}).(boundResourcePolicy)
	if !bound {
		return nil
	}
	return policy.manager.ValidateResourcePolicy(ctx, policy.descriptor, ids, policy.descriptor.AllAccounts)
}

func (m *PluginManager) PinnedPluginOwner(ctx context.Context, key string) (int64, error) {
	registry := m.extensions.Load()
	if registry == nil || registry.unavailable != "" {
		return 0, ErrExtensionOperationUnavailable
	}
	var owner int64
	for id, installation := range registry.installations {
		if installation.PluginKey != key {
			continue
		}
		if owner != 0 {
			return 0, ErrExtensionOperationUnavailable
		}
		owner = id
	}
	if owner == 0 {
		return 0, ErrExtensionOperationDisabled
	}
	current, err := m.repo.GetByID(ctx, owner)
	if err != nil || current == nil || current.PluginKey != key || current.State != PluginStateEnabled || !samePluginRuntime(current, registry.installations[owner]) {
		return 0, ErrExtensionOperationUnavailable
	}
	return owner, nil
}

func CindyCleanupResourcePolicy() extensionv1.ResourceDescriptor {
	return extensionv1.ResourceDescriptor{ResourceGrant: extensionv1.ResourceGrant{Capability: extensionv1.CapabilityProvider, Permission: "admin"}, RequiredCapabilities: []string{extensionv1.CapabilityAdmin}, OwnerPluginKey: CindyAccountViewPluginKey, AllAccounts: true, FilterPlatform: PlatformCindy, FilterAccountType: AccountTypeAPIKey, WholeAccountViewDomain: true}
}
