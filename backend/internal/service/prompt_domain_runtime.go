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
	registry  *RemoteSkillRegistryService
	prompts   *BusinessSystemPromptService
	mu        sync.Mutex
	cancel    context.CancelFunc
	done      chan struct{}
	ready     bool
	nextRetry time.Time
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
	// Read persisted intent even if policy initialization fails, so disabled
	// business prompts do not make ordinary gateway requests unavailable.
	_ = r.prompts.Reload(ctx)
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
	if promptPolicyAvailability(ctx) != nil {
		r.ready = false
		return
	}
	if r.ready || now.Before(r.nextRetry) {
		return
	}
	if err := r.registry.Start(ctx); err != nil {
		r.nextRetry = now.Add(10 * time.Second)
		slog.Warn("prompt_domain_initialization_unavailable", "component", "registry")
		return
	}
	if err := r.prompts.Start(ctx); err != nil {
		r.nextRetry = now.Add(10 * time.Second)
		slog.Warn("prompt_domain_initialization_unavailable", "component", "prompts")
		return
	}
	r.ready = true
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
		<-done
	}
}
