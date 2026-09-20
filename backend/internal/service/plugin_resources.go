package service

import (
	"context"
	"errors"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// Resource calls retain the original authenticated HTTP handler and middleware.
// This gate additionally binds the call to a declared capability, package and
// execution lifetime; compatibility routes cannot bypass a disabled plugin.
func (m *PluginManager) BindResourceContext(ctx context.Context, id int64, expectedPackage string, resource extensionv1.ResourceGrant) (context.Context, func(), error) {
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
	if runtime == nil || runtime.client == nil || runtime.client.Exited() || !pluginDependenciesHealthy(current, registry, map[int64]bool{}) || !runtime.beginRequest() {
		return nil, nil, ErrExtensionOperationUnavailable
	}
	bound, cancel, err := runtime.bindPolicyContext(ctx)
	if err != nil {
		runtime.finishRequest()
		return nil, nil, err
	}
	return WithPluginExecution(bound, current), func() { cancel(); runtime.finishRequest() }, nil
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
		ready, known := availability[descriptor.Capability]
		if !known {
			_, release, err := m.BindResourceContext(ctx, id, installation.PackageSHA256, descriptor.ResourceGrant)
			ready = err == nil
			if release != nil {
				release()
			}
			availability[descriptor.Capability] = ready
		}
		descriptor.Available = ready
		out = append(out, descriptor)
	}
	return out, nil
}
