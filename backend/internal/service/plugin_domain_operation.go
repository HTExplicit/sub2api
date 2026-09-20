package service

import (
	"context"
	"slices"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// Domain operations manage shared policy material and carry no account input.
// Account request hooks continue to resolve against the actual target scope.
type domainOperationInvoker interface {
	InvokeDomainOperation(context.Context, extensionv1.Invocation, bool) (extensionv1.Result, error)
	BindDomainOperationContext(context.Context, extensionv1.Invocation) (context.Context, context.CancelFunc, error)
}

func invokeProcessDomainExtension(ctx context.Context, in extensionv1.Invocation, cached bool) (extensionv1.Result, error) {
	provider := processExtensionOperations.Load()
	if provider == nil {
		return extensionv1.Result{}, ErrExtensionOperationDisabled
	}
	if invoker, ok := provider.invoker.(domainOperationInvoker); ok {
		return invoker.InvokeDomainOperation(ctx, in, cached)
	}
	// In-process contract fixtures have no installation registry.
	return provider.invoker.InvokeOperation(ctx, "*", "*", in)
}

func bindProcessDomainExtensionContext(ctx context.Context, in extensionv1.Invocation) (context.Context, context.CancelFunc, error) {
	provider := processExtensionOperations.Load()
	if provider == nil {
		return nil, nil, ErrExtensionOperationDisabled
	}
	if invoker, ok := provider.invoker.(domainOperationInvoker); ok {
		return invoker.BindDomainOperationContext(ctx, in)
	}
	if binder, ok := provider.invoker.(extensionOperationContextBinder); ok {
		return binder.BindOperationContext(ctx, "*", "*", in)
	}
	bound, cancel := context.WithCancel(ctx)
	return bound, cancel, nil
}

func (m *PluginManager) domainOperationScope(ctx context.Context, in extensionv1.Invocation) (string, string, error) {
	registry := m.extensions.Load()
	if registry == nil || registry.unavailable != "" {
		return "", "", ErrExtensionOperationUnavailable
	}
	var owner int64
	var contextualOwner int64
	if execution, ok := PluginExecutionFromContext(ctx); ok {
		if candidate := registry.installations[execution.ID]; candidate != nil && slices.Contains(candidate.Manifest.Operations[in.Capability], in.Operation) {
			if candidate.RuntimeGeneration != execution.Generation {
				return "", "", ErrExtensionOperationUnavailable
			}
			contextualOwner = execution.ID
		}
	}
	var platform, accountType string
	for id, installation := range registry.installations {
		if contextualOwner != 0 && contextualOwner != id {
			continue
		}
		if !slices.Contains(installation.Manifest.Operations[in.Capability], in.Operation) {
			continue
		}
		for _, binding := range installation.Bindings {
			if !binding.Enabled || binding.Capability != in.Capability {
				continue
			}
			if owner != 0 && owner != id {
				// Different account scopes may have different request providers;
				// their shared policy material has no unambiguous global owner.
				return "", "", ErrExtensionOperationUnavailable
			}
			if owner == 0 || binding.Platform+"\x00"+binding.AccountType < platform+"\x00"+accountType {
				platform, accountType = binding.Platform, binding.AccountType
			}
			owner = id
		}
	}
	if owner == 0 {
		return "", "", ErrExtensionOperationDisabled
	}
	return platform, accountType, nil
}

func (m *PluginManager) InvokeDomainOperation(ctx context.Context, in extensionv1.Invocation, cached bool) (extensionv1.Result, error) {
	platform, accountType, err := m.domainOperationScope(ctx, in)
	if err != nil {
		return extensionv1.Result{}, err
	}
	if cached {
		return m.InvokeCachedOperation(ctx, platform, accountType, in)
	}
	return m.InvokeOperation(ctx, platform, accountType, in)
}

func (m *PluginManager) BindDomainOperationContext(ctx context.Context, in extensionv1.Invocation) (context.Context, context.CancelFunc, error) {
	platform, accountType, err := m.domainOperationScope(ctx, in)
	if err != nil {
		return nil, nil, err
	}
	return m.BindOperationContext(ctx, platform, accountType, in)
}
