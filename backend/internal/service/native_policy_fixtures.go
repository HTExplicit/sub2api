package service

import (
	"context"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

// ConfigureNativePolicyOperations is the in-process composition seam used by
// cross-package contract fixtures. The server constructs NativeCodexRuntime.
// It has no plugin registry, process transport, capabilities or lifecycle side effects.
func ConfigureNativePolicyOperations(invoker extensionv1.OperationInvoker) {
	// Cross-package fixtures can change replies between phases. Start with an
	// empty Cindy runtime and disable writes to its immutable-answer cache;
	// the production cache is covered separately with the real native module.
	ConfigureCindyProvider(nil)
	cindyProvider.Load().cacheSize.Store(cindyProviderCacheLimit)
	invokeCindyProvider = func(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
		if invoker == nil {
			return extensionv1.Result{}, ErrExtensionOperationDisabled
		}
		return invoker.InvokeOperation(ctx, "", "", in)
	}
	invokeAccountTools = func(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
		if invoker == nil {
			return extensionv1.Result{}, ErrExtensionOperationDisabled
		}
		return invoker.InvokeOperation(ctx, "", "", in)
	}
	invokeNativeCodex = func(ctx context.Context, platform, kind string, in extensionv1.Invocation) (extensionv1.Result, error) {
		if invoker == nil {
			return extensionv1.Result{}, ErrNativeCodexPolicyDisabled
		}
		return invoker.InvokeOperation(ctx, platform, kind, in)
	}
	bindNativeCodexContext = func(ctx context.Context, _ string, _ string, _ extensionv1.Invocation) (context.Context, context.CancelFunc, error) {
		if invoker == nil {
			return nil, nil, ErrNativeCodexPolicyDisabled
		}
		bound, cancel := context.WithCancel(ctx)
		return bound, cancel, nil
	}
}
