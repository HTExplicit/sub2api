package service

import (
	"context"
	"errors"
)

const (
	PluginStateUpdating = "updating"
	PluginUpdateBundled = "bundled"
	PluginUpdatePinned  = "pinned"
)

var ErrPluginUpdateWaiting = errors.New("previous plugin processes are still draining")

// Separate from the original repository port so upstream implementations and
// contract fixtures do not accidentally claim support for independent updates.
type PluginUpdateRepository interface {
	StagePluginUpdate(context.Context, *PluginInstallation, *PluginInstallation, string) error
	PendingPluginArtifact(context.Context, int64) ([]byte, error)
	CommitPluginUpdate(context.Context, *PluginInstallation, *PluginInstallation) error
	SetPluginUpdatePolicy(context.Context, int64, int64, string) error
}

type PluginRuntimeLocker interface {
	HoldPluginRuntime(context.Context, *PluginInstallation) (func(), error)
}

type pluginExecutionKey struct{}
type PluginExecution struct{ ID, Generation int64 }

// Only the authenticated host broker adds this fence. It is never decoded from
// a plugin payload, including when plugins use public storage operations.
func WithPluginExecution(ctx context.Context, installation *PluginInstallation) context.Context {
	return context.WithValue(ctx, pluginExecutionKey{}, PluginExecution{installation.ID, installation.RuntimeGeneration})
}
func PluginExecutionFromContext(ctx context.Context) (PluginExecution, bool) {
	execution, ok := ctx.Value(pluginExecutionKey{}).(PluginExecution)
	return execution, ok
}

type pluginRevisionKey struct{}

func WithPluginExpectedRevision(ctx context.Context, revision int64) context.Context {
	return context.WithValue(ctx, pluginRevisionKey{}, revision)
}
func PluginExpectedRevision(ctx context.Context) int64 {
	revision, _ := ctx.Value(pluginRevisionKey{}).(int64)
	return revision
}

// Preserve exact existing bindings; newly declared capabilities start disabled.
func PluginReplacementBindings(previous []PluginBinding, manifest PluginManifest) []PluginBinding {
	bindings := make([]PluginBinding, 0, len(manifest.Capabilities))
	for _, capability := range manifest.SortedCapabilities() {
		binding := PluginBinding{Capability: capability.ID, Platform: capability.Platform, AccountType: capability.AccountType, RolloutPercent: 100}
		for _, old := range previous {
			if old.Capability == binding.Capability && old.Platform == binding.Platform && old.AccountType == binding.AccountType {
				binding.Enabled, binding.RolloutPercent = old.Enabled, old.RolloutPercent
				break
			}
		}
		bindings = append(bindings, binding)
	}
	return bindings
}
