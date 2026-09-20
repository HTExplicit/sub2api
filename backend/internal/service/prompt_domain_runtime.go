package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// Initialize domain storage only after the signed policy process is ready.
// Existing state stays available for history while the plugin is disabled.
type PromptDomainRuntime struct {
	registry    *RemoteSkillRegistryService
	prompts     *BusinessSystemPromptService
	mu          sync.Mutex
	reconcileMu sync.Mutex
	cancel      context.CancelFunc
	done        chan struct{}
	ready       bool
	nextRetry   time.Time
}

func NewPromptDomainRuntime(registry *RemoteSkillRegistryService, prompts *BusinessSystemPromptService) *PromptDomainRuntime {
	return &PromptDomainRuntime{registry: registry, prompts: prompts}
}

func promptPolicyAvailability(ctx context.Context) error {
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result, err := invokeProcessDomainExtension(call, extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "prompt.availability", Payload: []byte(`{}`)}, true)
	if err != nil {
		return err
	}
	if result.Code != "" {
		return ErrExtensionOperationUnavailable
	}
	var state struct {
		Ready bool `json:"ready"`
	}
	if json.Unmarshal(result.Payload, &state) != nil || !state.Ready {
		return ErrExtensionOperationUnavailable
	}
	return nil
}

func (r *PromptDomainRuntime) Start(parent context.Context) {
	r.mu.Lock()
	if r.cancel != nil {
		r.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	r.cancel, r.done = cancel, make(chan struct{})
	r.mu.Unlock()
	// Read persisted intent only when the policy process is unavailable. On the
	// healthy path, BusinessSystemPromptService.Start initializes and reloads it
	// exactly once after the registry is ready.
	if promptPolicyAvailability(ctx) != nil && r.prompts != nil {
		_ = r.prompts.Reload(ctx)
	}
	r.reconcile(ctx, time.Now())
	go func() {
		defer close(r.done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				r.reconcile(ctx, now)
			}
		}
	}()
}

func (r *PromptDomainRuntime) reconcile(ctx context.Context, now time.Time) {
	if r == nil {
		return
	}
	r.reconcileMu.Lock()
	defer r.reconcileMu.Unlock()
	if promptPolicyAvailability(ctx) != nil {
		r.stopDomainServices()
		r.mu.Lock()
		r.ready = false
		r.nextRetry = time.Time{}
		r.mu.Unlock()
		return
	}
	r.mu.Lock()
	ready, nextRetry := r.ready, r.nextRetry
	r.mu.Unlock()
	if ready || now.Before(nextRetry) {
		return
	}
	if r.registry == nil || r.prompts == nil {
		r.mu.Lock()
		r.nextRetry = now.Add(10 * time.Second)
		r.mu.Unlock()
		return
	}
	if err := r.registry.Start(ctx); err != nil {
		// Keep the persisted prompt snapshot available if registry startup fails,
		// but do not start any new plugin-backed work.
		_ = r.prompts.Reload(ctx)
		r.mu.Lock()
		r.nextRetry = now.Add(10 * time.Second)
		r.mu.Unlock()
		slog.Warn("prompt_domain_initialization_unavailable", "component", "registry")
		return
	}
	if err := r.prompts.Start(ctx); err != nil {
		r.registry.Stop()
		r.mu.Lock()
		r.nextRetry = now.Add(10 * time.Second)
		r.mu.Unlock()
		slog.Warn("prompt_domain_initialization_unavailable", "component", "prompts")
		return
	}
	r.mu.Lock()
	r.ready = true
	r.nextRetry = time.Time{}
	r.mu.Unlock()
}

func (r *PromptDomainRuntime) stopDomainServices() {
	if r == nil {
		return
	}
	// Stop the prompt subscriber before the registry so no late prompt reload
	// races with registry shutdown. Both services retain their snapshots and
	// durable records.
	if r.prompts != nil {
		r.prompts.Stop()
	}
	if r.registry != nil {
		r.registry.Stop()
	}
}

func (r *PromptDomainRuntime) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	r.mu.Unlock()
	if cancel != nil {
		cancel()
		if done != nil {
			<-done
		}
	}
	r.reconcileMu.Lock()
	r.stopDomainServices()
	r.reconcileMu.Unlock()
	r.mu.Lock()
	r.cancel = nil
	r.done = nil
	r.ready = false
	r.nextRetry = time.Time{}
	r.mu.Unlock()
}
