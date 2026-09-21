package service

import (
	"context"
	"slices"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

type pluginInvocationAdmission struct {
	ctx      context.Context
	release  context.CancelFunc
	runtime  *pluginRuntime
	revision uint64
	owner    int64
}

// Admission precedes both a cached policy read and a process call. A local
// registry can lag another host's persisted disable/config/binding changes.
// The exact business lease fences the fresh snapshot against target updates;
// it is released after this operation, not inherited by a later host IO call.
func (m *PluginManager) admitPluginInvocation(ctx context.Context, registry *pluginExtensionRegistry, id int64, platform, accountType string, in extensionv1.Invocation) (*pluginInvocationAdmission, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if in.AccountID < 0 || registry == nil || registry.unavailable != "" {
		return nil, ErrExtensionOperationUnavailable
	}
	installation := registry.installations[id]
	runtime := registry.runtimes[id]
	if installation == nil || runtime == nil || runtime.client == nil || runtime.extension == nil || runtime.client.Exited() {
		return nil, ErrExtensionOperationUnavailable
	}
	if m.repo != nil {
		current, err := m.repo.GetByID(ctx, id)
		if err != nil || !samePluginRuntime(current, installation) {
			return nil, ErrExtensionOperationUnavailable
		}
		if current.State == PluginStateDisabled {
			return nil, ErrExtensionOperationDisabled
		}
		if current.State != PluginStateEnabled {
			return nil, ErrExtensionOperationUnavailable
		}
		// Older direct injection adapters may have no operation declarations.
		// A declared operation may never disappear merely because our registry
		// still remembers the previous declaration.
		if _, declared := installation.Manifest.Operations[in.Capability]; declared && !slices.Contains(current.Manifest.Operations[in.Capability], in.Operation) {
			return nil, ErrExtensionOperationDisabled
		}
		installation = current
	}
	// Repository-free process-contract fixtures retain their explicit local
	// snapshot; production managers always use the persisted port above.
	if !pluginHasInvocationCapability(installation, in, platform, accountType) {
		return nil, ErrExtensionOperationDisabled
	}
	if !pluginDependenciesHealthy(installation, registry, map[int64]bool{}) {
		return nil, ErrExtensionOperationUnavailable
	}
	bound, release, err := m.bindHostPolicyContext(ctx, installation, runtime)
	if err != nil {
		return nil, err
	}
	admission := &pluginInvocationAdmission{ctx: bound, release: release, runtime: runtime, revision: runtime.configRevision.Load(), owner: id}
	if err := admission.validate(); err != nil {
		release()
		return nil, err
	}
	return admission, nil
}

func (a *pluginInvocationAdmission) validate() error {
	if err := a.ctx.Err(); err != nil {
		return err
	}
	if a.runtime.draining.Load() || a.runtime.configuring.Load() || a.runtime.client.Exited() || a.runtime.configRevision.Load() != a.revision {
		return ErrExtensionOperationUnavailable
	}
	return nil
}

func (a *pluginInvocationAdmission) invoke(in extensionv1.Invocation) (extensionv1.Result, error) {
	result, err := a.runtime.extension.Invoke(a.ctx, in)
	if err != nil {
		return extensionv1.Result{}, err
	}
	if err := a.validate(); err != nil {
		return extensionv1.Result{}, err
	}
	result.PluginID = a.owner
	return result, nil
}
