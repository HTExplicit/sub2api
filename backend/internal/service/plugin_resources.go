package service

import (
	"context"
	"errors"

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
	out := make([]extensionv1.ResourceDescriptor, 0)
	availability := map[string]bool{}
	for _, descriptor := range registered {
		if descriptor.Permission != permission || !pluginDeclaresResource(installation, descriptor.ResourceGrant) {
			continue
		}
		cacheKey := descriptor.Capability
		if descriptor.Retained {
			cacheKey += "/retained"
		}
		if descriptor.AllAccounts {
			cacheKey += "/all-accounts"
		}
		ready, known := availability[cacheKey]
		if !known {
			bound, release, err := m.BindResourceContext(ctx, id, installation.PackageSHA256, descriptor.ResourceGrant, descriptor.Retained)
			if err == nil && descriptor.AllAccounts {
				err = m.ValidateResourceAccounts(bound, descriptor.Capability, nil, true)
			}
			ready = err == nil
			if release != nil {
				release()
			}
			availability[cacheKey] = ready
		}
		descriptor.Available = ready
		out = append(out, descriptor)
	}
	return out, nil
}
