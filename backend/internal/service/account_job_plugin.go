package service

import (
	"context"
	"encoding/json"
	"errors"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

var ErrAccountJobPluginUnavailable = errors.New("account job plugin is unavailable or has changed")

// Account jobs are administrative operations. Image and probe job storage has
// its own execution contract and does not pass through this metadata envelope.
func AccountJobPluginExecution(metadata json.RawMessage) (PluginExecution, error) {
	var owner struct {
		ID         int64 `json:"plugin_id"`
		Generation int64 `json:"plugin_generation"`
	}
	if len(metadata) > 0 && json.Unmarshal(metadata, &owner) != nil {
		return PluginExecution{}, ErrAccountJobInvalidMetadata
	}
	if owner.ID < 0 || owner.Generation < 0 || (owner.ID == 0 && owner.Generation != 0) {
		return PluginExecution{}, ErrAccountJobInvalidMetadata
	}
	return PluginExecution{ID: owner.ID, Generation: owner.Generation}, nil
}

func stampAccountJobPlugin(metadata json.RawMessage, execution PluginExecution) (json.RawMessage, error) {
	owner, err := AccountJobPluginExecution(metadata)
	if err != nil {
		return nil, err
	}
	if execution.ID <= 0 || execution.Generation <= 0 || (owner.ID > 0 && owner.ID != execution.ID) {
		return nil, ErrAccountJobPluginUnavailable
	}
	fields := map[string]json.RawMessage{}
	if len(metadata) > 0 && json.Unmarshal(metadata, &fields) != nil {
		return nil, ErrAccountJobInvalidMetadata
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	fields["plugin_id"], _ = json.Marshal(execution.ID)
	fields["plugin_generation"], _ = json.Marshal(execution.Generation)
	return json.Marshal(fields)
}

type accountJobPluginBinder interface {
	BindAccountJobExecution(context.Context, int64, int64, ...string) (context.Context, func(), error)
}

func bindAccountJobPlugin(ctx context.Context, owner PluginExecution, kind string) (context.Context, func(), error) {
	provider := processExtensionOperations.Load()
	if provider == nil {
		return nil, nil, ErrAccountJobPluginUnavailable
	}
	binder, ok := provider.invoker.(accountJobPluginBinder)
	if !ok {
		return nil, nil, ErrAccountJobPluginUnavailable
	}
	return binder.BindAccountJobExecution(ctx, owner.ID, owner.Generation, kind)
}

// The separate shared database lease lasts until the host task actually exits,
// including its transaction cleanup. Killing only the old plugin process must
// not let an update overtake host IO that is still finishing cancellation.
func (m *PluginManager) BindAccountJobExecution(ctx context.Context, id, expectedGeneration int64, kinds ...string) (context.Context, func(), error) {
	if m == nil || m.repo == nil {
		return nil, nil, ErrAccountJobPluginUnavailable
	}
	current, err := m.repo.GetByID(ctx, id)
	if err == nil && current != nil && PluginExpectedPackage(ctx) != "" && PluginExpectedPackage(ctx) != current.PackageSHA256 {
		return nil, nil, ErrPluginStateChanged
	}
	if err != nil || current == nil || current.State != PluginStateEnabled || (expectedGeneration > 0 && current.RuntimeGeneration != expectedGeneration) {
		return nil, nil, ErrAccountJobPluginUnavailable
	}
	registry := m.extensions.Load()
	if registry == nil || registry.unavailable != "" || !samePluginRuntime(current, registry.installations[id]) {
		return nil, nil, ErrAccountJobPluginUnavailable
	}
	admin := false
	for _, binding := range current.Bindings {
		admin = admin || (binding.Enabled && binding.Capability == extensionv1.CapabilityAdmin)
	}
	runtime := registry.runtimes[id]
	if !admin || runtime == nil || runtime.client == nil || runtime.client.Exited() || !pluginDependenciesHealthy(current, registry, map[int64]bool{}) {
		return nil, nil, ErrAccountJobPluginUnavailable
	}
	bound, release, err := m.bindHostPolicyContext(ctx, current, runtime)
	if err != nil {
		return nil, nil, ErrAccountJobPluginUnavailable
	}
	bound = WithPluginExecution(bound, current)
	if len(kinds) > 0 && (kinds[0] == AccountJobKindImportData || kinds[0] == AccountJobKindImportCodex || kinds[0] == AccountJobKindBatchCreate) {
		if err := m.ValidateResourceAccounts(bound, extensionv1.CapabilityAdmin, nil, true); err != nil {
			release()
			return nil, nil, ErrAccountJobPluginUnavailable
		}
	}
	return bound, release, nil
}

func prepareAccountJobSubmission(ctx context.Context, metadata json.RawMessage, kind string) (context.Context, json.RawMessage, func(), error) {
	owner, err := AccountJobPluginExecution(metadata)
	if err != nil {
		return nil, nil, nil, err
	}
	if execution, bound := PluginExecutionFromContext(ctx); bound {
		raw, err := stampAccountJobPlugin(metadata, execution)
		return ctx, raw, func() {}, err
	}
	if owner.ID == 0 {
		return ctx, metadata, func() {}, nil
	}
	bound, release, err := bindAccountJobPlugin(ctx, owner, kind)
	if err != nil {
		return nil, nil, nil, err
	}
	execution, _ := PluginExecutionFromContext(bound)
	raw, err := stampAccountJobPlugin(metadata, execution)
	if err != nil {
		release()
		return nil, nil, nil, err
	}
	return bound, raw, release, nil
}
