package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"sync/atomic"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

var ErrExtensionOperationDisabled = errors.New("extension operation is not enabled")
var ErrExtensionOperationUnavailable = errors.New("enabled extension operation is unavailable")

type extensionOperationProvider struct{ invoker extensionv1.OperationInvoker }

var processExtensionOperations atomic.Pointer[extensionOperationProvider]

func invokeProcessExtension(ctx context.Context, platform, accountType string, in extensionv1.Invocation) (extensionv1.Result, error) {
	provider := processExtensionOperations.Load()
	if provider == nil {
		return extensionv1.Result{}, ErrExtensionOperationDisabled
	}
	return provider.invoker.InvokeOperation(ctx, platform, accountType, in)
}

func invokeProcessExtensionCached(ctx context.Context, platform, accountType string, in extensionv1.Invocation) (extensionv1.Result, error) {
	provider := processExtensionOperations.Load()
	if provider == nil {
		return extensionv1.Result{}, ErrExtensionOperationDisabled
	}
	if cached, ok := provider.invoker.(extensionv1.CachedOperationInvoker); ok {
		return cached.InvokeCachedOperation(ctx, platform, accountType, in)
	}
	return provider.invoker.InvokeOperation(ctx, platform, accountType, in)
}

// Only callers of immutable configuration-derived policies use this entrypoint.
// Health and activation are checked before consulting the bounded cache.
func (m *PluginManager) InvokeCachedOperation(ctx context.Context, platform, accountType string, in extensionv1.Invocation) (extensionv1.Result, error) {
	if err := ctx.Err(); err != nil {
		return extensionv1.Result{}, err
	}
	id, registry, err := m.operationOwner(platform, accountType, in)
	if err != nil {
		return extensionv1.Result{}, err
	}
	admission, err := m.admitPluginInvocation(ctx, registry, id, platform, accountType, in)
	if err != nil {
		return extensionv1.Result{}, err
	}
	defer admission.release()
	runtime := admission.runtime
	key := in.Capability + "\x00" + in.Operation + "\x00" + strconv.FormatInt(in.AccountID, 10) + "\x00" + strconv.FormatUint(admission.revision, 10) + "\x00" + string(in.Payload)
	if cached, ok := runtime.catalogCache.Load(key); ok {
		var result extensionv1.Result
		if json.Unmarshal(cached.(json.RawMessage), &result) == nil {
			if err := admission.validate(); err != nil {
				return extensionv1.Result{}, err
			}
			result.PluginID = id
			return result, nil
		}
	}
	result, err := admission.invoke(in)
	if err != nil {
		return result, err
	}
	if result.Code == "" && runtime.catalogCacheSize.Load() < 2048 {
		if raw, encodeErr := json.Marshal(result); encodeErr == nil {
			if _, loaded := runtime.catalogCache.LoadOrStore(key, json.RawMessage(raw)); !loaded {
				runtime.catalogCacheSize.Add(1)
			}
		}
	}
	return result, nil
}

// Explicit operation declarations keep independent request extensions from
// being called for another domain's hook. Conflicting owners fail closed.
func (m *PluginManager) InvokeOperation(ctx context.Context, platform, accountType string, in extensionv1.Invocation) (extensionv1.Result, error) {
	selected, _, err := m.operationOwner(platform, accountType, in)
	if err != nil {
		return extensionv1.Result{}, err
	}
	result, err := m.InvokeExtension(ctx, selected, platform, accountType, in)
	if err != nil {
		return extensionv1.Result{}, ErrExtensionOperationUnavailable
	}
	return result, nil
}

func (m *PluginManager) operationOwner(platform, accountType string, in extensionv1.Invocation) (int64, *pluginExtensionRegistry, error) {
	registry := m.extensions.Load()
	if registry == nil || registry.unavailable != "" {
		return 0, nil, ErrExtensionOperationUnavailable
	}
	var selected int64
	for id, installation := range registry.installations {
		if !pluginHasInvocationCapability(installation, in, platform, accountType) || !slices.Contains(installation.Manifest.Operations[in.Capability], in.Operation) {
			continue
		}
		if selected != 0 {
			return 0, nil, ErrExtensionOperationUnavailable
		}
		selected = id
	}
	if selected == 0 {
		return 0, nil, ErrExtensionOperationDisabled
	}
	return selected, registry, nil
}

func pluginHasInvocationCapability(installation *PluginInstallation, in extensionv1.Invocation, platform, accountType string) bool {
	if !pluginHasCapability(installation, in.Capability, platform, accountType) {
		return false
	}
	if in.AccountID <= 0 {
		return true
	}
	for _, binding := range installation.Bindings {
		if binding.Enabled && binding.Capability == in.Capability && pluginScopeMatches(binding.Platform, platform) && pluginScopeMatches(binding.AccountType, accountType) && int(stablePluginBucket(in.AccountID)) < binding.RolloutPercent {
			return true
		}
	}
	return false
}
