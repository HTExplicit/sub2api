package tickets

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/codexruntime/profile"
	"github.com/Wei-Shaw/sub2api/internal/codexruntime/recovery"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	proxytransport "github.com/Wei-Shaw/sub2api/internal/proxytransport"
)

type Config struct {
	RoutingSchema int      `json:"routing_schema"`
	Enabled       bool     `json:"enabled"`
	FailClosed    bool     `json:"fail_closed"`
	ProxyURL      string   `json:"proxy_url"`
	ProxyProtocol string   `json:"proxy_protocol,omitempty"`
	Models        []string `json:"models"`
	RequestZstd   bool     `json:"request_zstd"`
}

type HostCaller interface {
	Call(context.Context, extensionv1.HostInvocation) (extensionv1.Result, error)
}

type Module struct {
	lifecycleMu  sync.Mutex
	root         context.Context
	started      bool
	accepting    bool
	work         *sync.WaitGroup
	mu           sync.RWMutex
	config       Config
	host         HostCaller
	epoch        context.Context
	cancel       context.CancelFunc
	slots        chan struct{}
	prepareProxy func(context.Context, HostCaller, string, bool) (*http.Client, *ProxyResult, error)
}

func NewModule() *Module {
	ctx, cancel := context.WithCancel(context.Background())
	return &Module{accepting: true, work: &sync.WaitGroup{}, config: Config{RoutingSchema: 2, FailClosed: true, Models: []string{"gpt-6-astra", "gpt-6-sol", "gpt-6-luna"}, RequestZstd: true}, epoch: ctx, cancel: cancel, slots: make(chan struct{}, 5), prepareProxy: prepareProxy}
}

func (m *Module) SetHost(host HostCaller) { m.mu.Lock(); defer m.mu.Unlock(); m.host = host }

func (m *Module) ValidateConfig(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	cfg := Config{RoutingSchema: 2, FailClosed: true, Models: []string{"gpt-6-astra", "gpt-6-sol", "gpt-6-luna"}, RequestZstd: true}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil || json.Unmarshal(raw, &cfg) != nil {
		return nil, errors.New("invalid codex runtime configuration")
	}
	for key, value := range fields {
		switch key {
		case "enabled", "fail_closed", "proxy_url", "proxy_protocol", "models", "request_zstd", "routing_schema":
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return nil, errors.New("null codex runtime setting")
			}
		default:
			return nil, errors.New("unknown codex runtime setting")
		}
	}
	if cfg.RoutingSchema < 0 || cfg.RoutingSchema > 2 {
		return nil, errors.New("unsupported routing schema")
	}
	_, schemaPresent := fields["routing_schema"]
	if (!schemaPresent || cfg.RoutingSchema < 2) && len(cfg.Models) == 2 && slices.Contains(cfg.Models, "gpt-6-astra") && slices.Contains(cfg.Models, "gpt-5.6-sol") {
		cfg.Models = append(cfg.Models, "gpt-6-sol", "gpt-6-luna")
	}
	cfg.RoutingSchema = 2
	if len(cfg.Models) == 0 || len(cfg.Models) > 32 {
		return nil, errors.New("ticket model list required")
	}
	seen := make(map[string]bool, len(cfg.Models))
	for _, model := range cfg.Models {
		if strings.TrimSpace(model) != model || model == "" || len(model) > 256 {
			return nil, errors.New("invalid ticket model")
		}
		if seen[model] {
			return nil, errors.New("duplicate ticket model")
		}
		seen[model] = true
	}
	if cfg.ProxyURL != "" {
		normal, err := normalizeProxyForm(cfg.ProxyURL, cfg.ProxyProtocol)
		if err != nil {
			return nil, errors.New("invalid ticket proxy")
		}
		cfg.ProxyURL = normal
	}
	cfg.ProxyProtocol = ""
	return json.Marshal(cfg)
}

// Start owns the renewal loop; construction and validation never start network work.
func (m *Module) Start(ctx context.Context) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.RLock()
	started := m.started
	m.mu.RUnlock()
	if started {
		return nil
	}
	if err := m.stopEpoch(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	m.root, m.started = ctx, true
	m.startEpochLocked()
	m.mu.Unlock()
	return nil
}

func (m *Module) Stop(ctx context.Context) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.Lock()
	m.started = false
	m.mu.Unlock()
	return m.stopEpoch(ctx)
}

func (m *Module) stopEpoch(ctx context.Context) error {
	m.mu.Lock()
	m.accepting = false
	m.cancel()
	work := m.work
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { work.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Module) startEpochLocked() {
	root := m.root
	if root == nil {
		root = context.Background()
	}
	m.epoch, m.cancel = context.WithCancel(root)
	m.accepting = true
	if m.started && m.host != nil {
		epoch, host, cfg, work := m.epoch, m.host, m.config, m.work
		work.Add(2)
		go func() { defer work.Done(); m.migrateRoutingStates(epoch, host, cfg) }()
		go func() { defer work.Done(); m.renew(epoch, host, cfg) }()
	}
}

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
	m.cancel()
	m.config = cfg
	m.startEpochLocked()
	m.mu.Unlock()
	return nil
}

func (m *Module) renew(ctx context.Context, host HostCaller, cfg Config) {
	ticker := time.NewTicker(6 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		m.renewOnce(ctx, host, cfg)
	}
}

func (m *Module) renewOnce(ctx context.Context, host HostCaller, cfg Config) {
	var redacted map[string]int
	_ = hostCall(ctx, host, extensionv1.HostCodexRoutingCleanup, struct{}{}, &redacted)
	if !cfg.Enabled {
		return
	}
	var due []extensionv1.DueState
	if hostCall(ctx, host, extensionv1.HostStateDue, extensionv1.DueStateRequest{Namespace: "tickets", Limit: 100}, &due) != nil {
		return
	}
	queue := make(chan Operation, len(due))
	for _, record := range due {
		var state State
		if json.Unmarshal(record.Value, &state) != nil {
			continue
		}
		running := state.Phase == "manual_running" || state.Phase == "pre_running" || state.Phase == "post_running"
		state.migrateRouting()
		if !running && (!state.Enrolled || state.Phase == "stopped") {
			continue
		}
		// The namespace key remains available when a first attempt crashed before
		// producing any ticket. Only configured model keys may be recovered.
		prefix, _, ok := strings.Cut(record.Key, ".")
		id, err := strconv.ParseInt(prefix, 10, 64)
		if !ok || err != nil || id <= 0 {
			continue
		}
		for _, model := range cfg.Models {
			if record.Key == stateKey(id, model) {
				if !running && !hasRoutingDemand(ctx, host, id, model) {
					// Remove dormant rows from the due index; otherwise the first
					// hundred idle accounts can starve a later demanded account.
					state.NextAt = nil
					_, _ = writeState(ctx, host, id, model, state, record.Revision)
					continue
				}
				queue <- Operation{AccountID: id, Model: model}
				break
			}
		}
	}
	close(queue)
	var workers sync.WaitGroup
	for range min(5, len(due)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for operation := range queue {
				if ctx.Err() != nil {
					return
				}
				_, _ = m.harvest(ctx, ctx, host, cfg, operation, false)
			}
		}()
	}
	workers.Wait()
}

func (m *Module) Status(context.Context) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return json.Marshal(map[string]any{"enabled": m.config.Enabled, "proxy_configured": m.config.ProxyURL != "", "models": m.config.Models, "request_zstd": m.config.RequestZstd, "routing_schema": extensionv1.CodexRoutingSchema, "cookie_max_age_seconds": 120, "refresh_lead_seconds": 20})
}

type Operation struct {
	AccountID   int64  `json:"account_id"`
	Model       string `json:"model"`
	OperationID string `json:"operation_id"`
	Force       bool   `json:"force"`
}

type Outcome struct {
	Success        bool                                 `json:"success"`
	Code           string                               `json:"code"`
	HTTPStatus     int                                  `json:"http_status,omitempty"`
	ObservedLength int                                  `json:"observed_length,omitempty"`
	ExpiresAt      *time.Time                           `json:"expires_at,omitempty"`
	Observation    *extensionv1.CodexRoutingObservation `json:"observation,omitempty"`
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
		return extensionv1.Result{}, context.Canceled
	}
	host, cfg, epoch, work := m.host, m.config, m.epoch, m.work
	work.Add(1)
	m.mu.Unlock()
	defer work.Done()
	if in.Capability == extensionv1.CapabilityRequest && in.Operation == "codex.transport.plan" {
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
	if host == nil {
		return extensionv1.Result{}, errors.New("host services unavailable")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(epoch, cancel)
	defer stop()
	var value any
	var err error
	switch {
	case in.Capability == extensionv1.CapabilityRequest && in.Operation == "codex.routing.demand":
		var req extensionv1.CodexRoutingDemand
		if json.Unmarshal(in.Payload, &req) != nil || req.AccountID <= 0 || !slices.Contains(cfg.Models, req.Model) {
			return extensionv1.Result{}, errors.New("invalid routing demand")
		}
		value, err = m.recordRoutingDemand(ctx, host, cfg, req)
	case in.Capability == extensionv1.CapabilityRequest && in.Operation == "codex.routing.observe":
		var req extensionv1.CodexRoutingResponse
		if json.Unmarshal(in.Payload, &req) != nil {
			return extensionv1.Result{}, errors.New("invalid routing observation")
		}
		value, err = observeRoutingResponse(ctx, host, req)
	case in.Capability == extensionv1.CapabilityScheduling && in.Operation == "describe":
		rules := []extensionv1.SchedulingRule{}
		if cfg.Enabled && cfg.FailClosed {
			rules = append(rules, extensionv1.SchedulingRule{Models: cfg.Models, Default: "deny", Reason: "ticket_missing", ExcludeShadows: true})
		}
		value = rules
	case in.Capability == extensionv1.CapabilityAdmin && in.Operation == "proxy.test":
		var req struct {
			ProxyURL string `json:"proxy_url"`
			Protocol string `json:"protocol"`
		}
		if json.Unmarshal(in.Payload, &req) != nil {
			return extensionv1.Result{}, errors.New("invalid proxy test")
		}
		testCtx, testCancel := context.WithTimeout(ctx, 25*time.Second)
		defer testCancel()
		normal, normalizeErr := normalizeProxyForm(req.ProxyURL, req.Protocol)
		if normalizeErr != nil {
			return extensionv1.Result{}, errors.New("invalid proxy input")
		}
		client, result, testErr := m.prepareProxy(testCtx, host, normal, true)
		if client != nil {
			client.CloseIdleConnections()
		}
		if result == nil {
			return extensionv1.Result{}, testErr
		}
		value = result
	case in.Capability == extensionv1.CapabilityScheduling && in.Operation == "admit":
		var req extensionv1.SchedulingRequest
		if json.Unmarshal(in.Payload, &req) != nil {
			return extensionv1.Result{}, errors.New("invalid admission request")
		}
		decision := extensionv1.SchedulingDecision{Allowed: true}
		if cfg.Enabled && cfg.FailClosed && slices.Contains(cfg.Models, req.Model) && Eligible(req.Account.Platform, req.Account.Type, req.Account.Shadow) {
			state, _, readErr := readState(ctx, host, req.Account.ID, req.Model)
			if readErr != nil {
				return extensionv1.Result{}, readErr
			}
			if !state.Qualification.Valid(req.Now, req.Account.ID, req.Account.Identity, req.Model) {
				decision = extensionv1.SchedulingDecision{Allowed: false, Reason: "ticket_missing", Scope: req.Model}
			}
		}
		value = decision
	case in.Capability == extensionv1.CapabilityRequest && in.Operation == "inject":
		var req extensionv1.SchedulingRequest
		if json.Unmarshal(in.Payload, &req) != nil {
			return extensionv1.Result{}, errors.New("invalid ticket injection request")
		}
		injection := extensionv1.CodexRoutingInjection{Headers: map[string]string{}}
		if cfg.Enabled && slices.Contains(cfg.Models, req.Model) && Eligible(req.Account.Platform, req.Account.Type, req.Account.Shadow) {
			state, revision, readErr := readState(ctx, host, req.Account.ID, req.Model)
			if readErr != nil {
				return extensionv1.Result{}, readErr
			}
			if state.Qualification.Valid(req.Now, req.Account.ID, req.Account.Identity, req.Model) {
				var checked extensionv1.CodexRoutingProbeResult
				q := state.Qualification
				if checkErr := hostCall(ctx, host, extensionv1.HostCodexRoutingCheck, extensionv1.CodexRoutingQuery{AccountID: req.Account.ID, Model: req.Model, Transport: q.Scope.Transport, Bundle: &q.Bundle, Scope: &q.Scope}, &checked); checkErr != nil || !checked.Valid {
					if state.Phase != "manual_running" && state.Phase != "pre_running" && state.Phase != "post_running" {
						state.Qualification, state.ExpiresAt = nil, nil
						state.LastCode = "routing_stale"
						if state.Phase != "stopped" && state.Enrolled {
							state.Phase = "needs_cookie_verification"
							now := time.Now().UTC()
							state.NextAt = &now
						}
						_, _ = writeState(ctx, host, req.Account.ID, req.Model, state, revision)
					}
					return extensionv1.Result{Code: "routing_stale"}, nil
				}
				injection.Qualification = q
			} else if cfg.FailClosed {
				return extensionv1.Result{Code: "ticket_missing"}, nil
			}
		}
		value = injection
	case in.Capability == extensionv1.CapabilityAdmin && in.Operation == "harvest", in.Capability == extensionv1.CapabilityJobs && in.Operation == "renew":
		var req Operation
		if json.Unmarshal(in.Payload, &req) != nil || req.AccountID <= 0 {
			return extensionv1.Result{}, errors.New("invalid ticket operation")
		}
		value, err = m.harvest(ctx, epoch, host, cfg, req, in.Operation == "harvest")
	case in.Capability == extensionv1.CapabilityAdmin && in.Operation == "stop":
		var req Operation
		if json.Unmarshal(in.Payload, &req) != nil || req.AccountID <= 0 || req.Model == "" || len(req.Model) > 256 {
			return extensionv1.Result{}, errors.New("invalid ticket operation")
		}
		state, revision, readErr := readState(ctx, host, req.AccountID, req.Model)
		if readErr != nil {
			return extensionv1.Result{}, readErr
		}
		state.Stop()
		_, err = writeState(ctx, host, req.AccountID, req.Model, state, revision)
		value = Outcome{Success: err == nil, Code: "ticket_stopped"}
	default:
		return extensionv1.Result{}, errors.New("unsupported codex runtime operation")
	}
	if err != nil {
		return extensionv1.Result{}, err
	}
	raw, err := json.Marshal(value)
	return extensionv1.Result{Payload: raw}, err
}

func normalizeProxyForm(raw, protocol string) (string, error) {
	normal, err := proxytransport.Normalize(raw)
	if err != nil || normal == "" {
		return normal, err
	}
	if protocol != "" {
		if !slices.Contains([]string{"http", "https", "socks5", "socks5h"}, protocol) {
			return "", errors.New("invalid proxy protocol")
		}
		address, _ := url.Parse(normal)
		address.Scheme = protocol
		normal = address.String()
	}
	return normal, nil
}

func stateKey(accountID int64, model string) string {
	sum := sha256.Sum256([]byte(model))
	return strconv.FormatInt(accountID, 10) + "." + hex.EncodeToString(sum[:])
}

func hostCall(ctx context.Context, host HostCaller, operation extensionv1.HostOperation, value, into any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	result, err := host.Call(ctx, extensionv1.HostInvocation{Operation: operation, Payload: raw})
	if err != nil {
		return err
	}
	if result.Code != "" {
		return fmt.Errorf("host operation failed: %s", result.Code)
	}
	return json.Unmarshal(result.Payload, into)
}

func readState(ctx context.Context, host HostCaller, id int64, model string) (State, int64, error) {
	var result extensionv1.StateResult
	err := hostCall(ctx, host, extensionv1.HostStateRead, extensionv1.StateRequest{Namespace: "tickets", Key: stateKey(id, model)}, &result)
	var state State
	if err == nil && result.Found {
		err = json.Unmarshal(result.Value, &state)
	}
	state.migrateRouting()
	return state, result.Revision, err
}

func writeState(ctx context.Context, host HostCaller, id int64, model string, state State, revision int64) (int64, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return 0, err
	}
	var result extensionv1.StateResult
	request := extensionv1.StateRequest{Namespace: "tickets", Key: stateKey(id, model), ExpectedRevision: revision, Value: raw, NextAt: state.NextAt}
	if state.LastAttemptAt != nil && (state.Phase == "manual_running" || state.Phase == "pre_running" || state.Phase == "post_running") {
		// Index an abandoned attempt for recovery after its execution lease, but
		// keep NextAt in the domain state empty: this is never a retry permission.
		recoverAt := state.LastAttemptAt.Add(75 * time.Second)
		request.NextAt = &recoverAt
	}
	if state.Identity != "" {
		constraint := extensionv1.SchedulingConstraint{Model: model, Effect: "deny", Reason: "ticket_missing"}
		if state.Qualification.Valid(time.Now().UTC(), id, state.Identity, model) {
			constraint.Effect = "allow"
			constraint.Until = &state.Qualification.ExpiresAt
			constraint.Reason = "routing_verified"
		}
		observation := extensionv1.AccountObservation{Key: model, Kind: "codex_routing", State: state.Phase, ExpiresAt: state.ExpiresAt, NextAt: state.NextAt, CheckedAt: state.LastAttemptAt, Code: state.LastCode}
		if state.Observation != nil {
			observation.Count = state.Observation.StateLength
		}
		request.Projection = &extensionv1.AccountProjection{AccountID: id, Identity: state.Identity, Scheduling: []extensionv1.SchedulingConstraint{constraint}, Observations: []extensionv1.AccountObservation{observation}}
	}
	err = hostCall(ctx, host, extensionv1.HostStateCompareSwap, request, &result)
	if err == nil && !result.Applied {
		err = errors.New("ticket_state_changed")
	}
	return result.Revision, err
}

func newSessionID() string {
	var value [16]byte
	_, _ = rand.Read(value[:])
	value[6] = (value[6] & 15) | 64
	value[8] = (value[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[:4], value[4:6], value[6:8], value[8:10], value[10:])
}
