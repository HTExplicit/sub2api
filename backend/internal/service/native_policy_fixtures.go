package service

import (
	"context"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

// ConfigureNativePolicyOperations is the in-process composition seam used by
// cross-package contract fixtures.
// It has no plugin registry, process transport, capabilities or lifecycle side effects.
func ConfigureNativePolicyOperations(invoker extensionv1.OperationInvoker) {
	invokeAccountTools = func(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
		if invoker == nil {
			return extensionv1.Result{}, ErrExtensionOperationDisabled
		}
		return invoker.InvokeOperation(ctx, in)
	}
}
