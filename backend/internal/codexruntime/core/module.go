// Package core hosts the native Codex runtime module: reasoning-replay
// recovery, Codex client identity and request transport planning.
package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/codexruntime/profile"
	"github.com/Wei-Shaw/sub2api/internal/codexruntime/recovery"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

// Config is the complete runtime configuration.
type Config struct {
	RequestZstd bool `json:"request_zstd"`
}

// retiredSettings belonged to the removed Codex route-qualification feature.
// Stored configurations and older admin pages may still carry them; they are
// dropped without being read, so such a configuration still loads.
var retiredSettings = map[string]bool{
	"enabled": true, "fail_closed": true, "proxy_url": true, "proxy_protocol": true,
	"proxy_selection_id": true, "models": true, "routing_schema": true,
}

// ErrStopping is returned while the module stops. It wraps context.Canceled
// for callers that treat it so.
var ErrStopping = fmt.Errorf("codex runtime is stopping or reloading: %w", context.Canceled)

type Module struct {
	lifecycleMu sync.Mutex
	mu          sync.RWMutex
	started     bool
	accepting   bool
	work        sync.WaitGroup
	config      Config
}

func NewModule() *Module {
	return &Module{accepting: true, config: defaultConfig()}
}

func defaultConfig() Config { return Config{RequestZstd: true} }

// ValidateConfig returns the normalized configuration. Retired settings are
// dropped; any other unknown or null setting is rejected.
func (m *Module) ValidateConfig(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	cfg := defaultConfig()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("invalid codex runtime configuration: %w", err)
	}
	if fields == nil {
		return nil, errors.New("invalid codex runtime configuration: expected a JSON object")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("invalid codex runtime configuration: %w", err)
	}
	var nullKeys, unknownKeys []string
	for key, value := range fields {
		switch {
		case retiredSettings[key]:
		case key == "request_zstd":
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				nullKeys = append(nullKeys, key)
			}
		default:
			unknownKeys = append(unknownKeys, key)
		}
	}
	if len(unknownKeys) > 0 {
		slices.Sort(unknownKeys)
		return nil, fmt.Errorf("unknown codex runtime setting: %s", strings.Join(unknownKeys, ", "))
	}
	if len(nullKeys) > 0 {
		return nil, fmt.Errorf("null codex runtime setting: %s", strings.Join(nullKeys, ", "))
	}
	return json.Marshal(cfg)
}

// Start opens the module for transport planning. It has no background work.
func (m *Module) Start(ctx context.Context) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.RLock()
	started := m.started
	m.mu.RUnlock()
	if started {
		return nil
	}
	if err := m.drain(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	m.started, m.accepting = true, true
	m.mu.Unlock()
	return nil
}

// Stop refuses new transport plans and waits for the ones in progress.
func (m *Module) Stop(ctx context.Context) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.Lock()
	m.started = false
	m.mu.Unlock()
	return m.drain(ctx)
}

func (m *Module) drain(ctx context.Context) error {
	m.mu.Lock()
	m.accepting = false
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { m.work.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ApplyConfig replaces the configuration. An invalid configuration leaves the
// current one in effect.
func (m *Module) ApplyConfig(ctx context.Context, raw json.RawMessage) error {
	normalized, err := m.ValidateConfig(ctx, raw)
	if err != nil {
		return err
	}
	var cfg Config
	if err = json.Unmarshal(normalized, &cfg); err != nil {
		return err
	}
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.Lock()
	m.config, m.accepting = cfg, true
	m.mu.Unlock()
	return nil
}

func (m *Module) Invoke(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
	if in.Capability == extensionv1.CapabilityRecovery {
		return recovery.Invoke(ctx, in)
	}
	if in.Capability == extensionv1.CapabilityRequest && strings.HasPrefix(in.Operation, "codex.identity.") {
		return profile.Invoke(ctx, in)
	}
	m.mu.Lock()
	if !m.accepting {
		m.mu.Unlock()
		return extensionv1.Result{}, ErrStopping
	}
	cfg := m.config
	m.work.Add(1)
	m.mu.Unlock()
	defer m.work.Done()
	if in.Capability != extensionv1.CapabilityRequest || in.Operation != "codex.transport.plan" {
		return extensionv1.Result{}, errors.New("unsupported codex runtime operation")
	}
	if err := ctx.Err(); err != nil {
		return extensionv1.Result{}, err
	}
	var query extensionv1.CodexTransportQuery
	if len(in.Payload) > 16384 || json.Unmarshal(in.Payload, &query) != nil {
		return extensionv1.Result{}, errors.New("invalid transport metadata")
	}
	raw, err := json.Marshal(profile.TransportPlan(query, cfg.RequestZstd))
	return extensionv1.Result{Payload: raw}, err
}
