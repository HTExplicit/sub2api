package service

import (
	"context"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

type nativeCindyTestSnapshot struct {
	invoker extensionv1.OperationInvoker
	invoke  func(context.Context, extensionv1.Invocation) (extensionv1.Result, error)
	runtime *cindyProviderRuntime
}
type nativeCindyTestInvoker func(context.Context, extensionv1.Invocation) (extensionv1.Result, error)

func (f nativeCindyTestInvoker) InvokeOperation(ctx context.Context, _, _ string, in extensionv1.Invocation) (extensionv1.Result, error) {
	return f(ctx, in)
}
func captureNativeCindyTestInvoker() nativeCindyTestSnapshot {
	return nativeCindyTestSnapshot{invoke: invokeCindyProvider, runtime: cindyProvider.Load(), invoker: nativeCindyTestInvoker(invokeCindyProvider)}
}
func restoreNativeCindyTestInvoker(previous nativeCindyTestSnapshot) {
	invokeCindyProvider = previous.invoke
	cindyProvider.Store(previous.runtime)
}
func setNativeCindyTestInvoker(fixture interface {
	InvokeOperation(context.Context, string, string, extensionv1.Invocation) (extensionv1.Result, error)
}) {
	current := cindyProvider.Load()
	runtime := &cindyProviderRuntime{module: current.module, policySHA256: current.policySHA256}
	// Injected replies deliberately change between calls. Native production
	// modules are immutable per configuration; cache behavior has its own test.
	runtime.cacheSize.Store(cindyProviderCacheLimit)
	cindyProvider.Store(runtime)
	invokeCindyProvider = func(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
		if fixture == nil {
			return extensionv1.Result{}, ErrExtensionOperationDisabled
		}
		return fixture.InvokeOperation(ctx, PlatformCindy, AccountTypeAPIKey, in)
	}
}
