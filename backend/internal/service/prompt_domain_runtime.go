package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

// Core prompt serving starts after its atomic content migration completes.
type PromptDomainRuntime struct {
	prompts      *BusinessSystemPromptService
	mu           sync.Mutex
	reconcileMu  sync.Mutex
	cancel       context.CancelFunc
	done         chan struct{}
	ready        bool
	promptsReady bool
	nextRetry    time.Time
}

func NewPromptDomainRuntime(prompts *BusinessSystemPromptService) *PromptDomainRuntime {
	return &PromptDomainRuntime{prompts: prompts}
}

func promptPolicyAvailability(ctx context.Context) error {
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result, err := invokePromptSkills(call, extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "prompt.availability", Payload: []byte(`{}`)})
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
	r.mu.Lock()
	ready, nextRetry := r.ready, r.nextRetry
	r.mu.Unlock()
	if ready || now.Before(nextRetry) {
		return
	}
	if r.prompts == nil {
		r.mu.Lock()
		r.nextRetry = now.Add(10 * time.Second)
		r.mu.Unlock()
		return
	}
	if !r.promptsReady {
		if err := r.prompts.Start(ctx); err != nil {
			slog.Warn("prompt_domain_initialization_unavailable", "component", "prompts")
		} else {
			r.promptsReady = true
		}
	}
	r.mu.Lock()
	r.ready = r.promptsReady
	if r.ready {
		r.nextRetry = time.Time{}
	} else {
		r.nextRetry = now.Add(10 * time.Second)
	}
	r.mu.Unlock()
}

func (r *PromptDomainRuntime) stopDomainServices() {
	if r == nil {
		return
	}
	if r.prompts != nil {
		r.prompts.Stop()
	}
	r.promptsReady = false
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
