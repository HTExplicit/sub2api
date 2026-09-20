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

	"github.com/HTExplicit/sub2api-plugins/codexruntime/profile"
	proxytransport "github.com/Wei-Shaw/sub2api/pkg/extensionapi/proxy"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

type Config struct {
	Enabled       bool     `json:"enabled"`
	FailClosed    bool     `json:"fail_closed"`
	ProxyURL      string   `json:"proxy_url"`
	ProxyProtocol string   `json:"proxy_protocol,omitempty"`
	Models        []string `json:"models"`
}

type Module struct {
	mu            sync.RWMutex
	config        Config
	host          *extensionv1.Client
	epoch         context.Context
	cancel        context.CancelFunc
	slots         chan struct{}
	requestTicket func(context.Context, *http.Client, extensionv1.OutboundIdentity, string) (*Ticket, Outcome)
	prepareProxy  func(context.Context, *extensionv1.Client, string, bool) (*http.Client, *ProxyResult, error)
}

func NewModule() *Module {
	ctx, cancel := context.WithCancel(context.Background())
	return &Module{config: Config{FailClosed: true, Models: []string{"gpt-6-astra", "gpt-5.6-sol"}}, epoch: ctx, cancel: cancel, slots: make(chan struct{}, 5), requestTicket: probe, prepareProxy: prepareProxy}
}

func (m *Module) SetHost(host *extensionv1.Client) { m.mu.Lock(); defer m.mu.Unlock(); m.host = host }

func (m *Module) ValidateConfig(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	cfg := Config{FailClosed: true, Models: []string{"gpt-6-astra", "gpt-5.6-sol"}}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil || json.Unmarshal(raw, &cfg) != nil {
		return nil, errors.New("invalid codex runtime configuration")
	}
	for key, value := range fields {
		switch key {
		case "enabled", "fail_closed", "proxy_url", "proxy_protocol", "models":
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return nil, errors.New("null codex runtime setting")
			}
		default:
			return nil, errors.New("unknown codex runtime setting")
		}
	}
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

func (m *Module) ApplyConfig(ctx context.Context, raw json.RawMessage) error {
	normalized, err := m.ValidateConfig(ctx, raw)
	if err != nil {
		return err
	}
	var cfg Config
	if err = json.Unmarshal(normalized, &cfg); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancel()
	m.epoch, m.cancel = context.WithCancel(context.Background())
	m.config = cfg
	if cfg.Enabled && m.host != nil {
		go m.renew(m.epoch, m.host, cfg)
	}
	return nil
}

func (m *Module) renew(ctx context.Context, host *extensionv1.Client, cfg Config) {
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

func (m *Module) renewOnce(ctx context.Context, host *extensionv1.Client, cfg Config) {
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
		if !running && (state.Ticket == nil || (state.Phase != "ready" && state.Phase != "retry")) {
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
	return json.Marshal(map[string]any{"enabled": m.config.Enabled, "proxy_configured": m.config.ProxyURL != "", "models": m.config.Models})
}

type Operation struct {
	AccountID   int64  `json:"account_id"`
	Model       string `json:"model"`
	OperationID string `json:"operation_id"`
	Force       bool   `json:"force"`
}

type Outcome struct {
	Success        bool       `json:"success"`
	Code           string     `json:"code"`
	HTTPStatus     int        `json:"http_status,omitempty"`
	ObservedLength int        `json:"observed_length,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
}

func (m *Module) Invoke(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
	if in.Capability == extensionv1.CapabilityRequest && strings.HasPrefix(in.Operation, "codex.identity.") {
		return profile.Invoke(ctx, in)
	}
	m.mu.RLock()
	host, cfg, epoch := m.host, m.config, m.epoch
	m.mu.RUnlock()
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
			if !state.Ticket.Valid(req.Now, req.Account.ID, req.Account.Identity, req.Model) {
				decision = extensionv1.SchedulingDecision{Allowed: false, Reason: "ticket_missing", Scope: req.Model}
			}
		}
		value = decision
	case in.Capability == extensionv1.CapabilityRequest && in.Operation == "inject":
		var req extensionv1.SchedulingRequest
		if json.Unmarshal(in.Payload, &req) != nil {
			return extensionv1.Result{}, errors.New("invalid ticket injection request")
		}
		headers := map[string]string{}
		if cfg.Enabled && slices.Contains(cfg.Models, req.Model) && Eligible(req.Account.Platform, req.Account.Type, req.Account.Shadow) {
			state, _, readErr := readState(ctx, host, req.Account.ID, req.Model)
			if readErr != nil {
				return extensionv1.Result{}, readErr
			}
			if state.Ticket.Valid(req.Now, req.Account.ID, req.Account.Identity, req.Model) {
				headers["x-codex-turn-state"] = state.Ticket.State
			} else if cfg.FailClosed {
				return extensionv1.Result{Code: "ticket_missing"}, nil
			}
		}
		value = map[string]any{"headers": headers}
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

func hostCall(ctx context.Context, host *extensionv1.Client, operation extensionv1.HostOperation, value, into any) error {
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

func readState(ctx context.Context, host *extensionv1.Client, id int64, model string) (State, int64, error) {
	var result extensionv1.StateResult
	err := hostCall(ctx, host, extensionv1.HostStateRead, extensionv1.StateRequest{Namespace: "tickets", Key: stateKey(id, model)}, &result)
	var state State
	if err == nil && result.Found {
		err = json.Unmarshal(result.Value, &state)
	}
	return state, result.Revision, err
}

func writeState(ctx context.Context, host *extensionv1.Client, id int64, model string, state State, revision int64) (int64, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return 0, err
	}
	var result extensionv1.StateResult
	request := extensionv1.StateRequest{Namespace: "tickets", Key: stateKey(id, model), ExpectedRevision: revision, Value: raw, NextAt: state.NextAt}
	if state.LastAttemptAt != nil && (state.Phase == "manual_running" || state.Phase == "pre_running" || state.Phase == "post_running") {
		// Index an abandoned attempt for recovery after its execution lease, but
		// keep NextAt in the domain state empty: this is never a retry permission.
		recoverAt := state.LastAttemptAt.Add(30 * time.Second)
		request.NextAt = &recoverAt
	}
	if state.Identity != "" {
		constraint := extensionv1.SchedulingConstraint{Model: model, Effect: "deny", Reason: "ticket_missing"}
		if state.Ticket.Valid(time.Now().UTC(), id, state.Identity, model) {
			constraint.Effect = "allow"
			constraint.Until = &state.Ticket.ExpiresAt
			constraint.Reason = "ticket_ready"
		}
		observation := extensionv1.AccountObservation{Key: model, Kind: "codex_ticket", State: state.Phase, ExpiresAt: state.ExpiresAt, NextAt: state.NextAt, CheckedAt: state.LastAttemptAt, Code: state.LastCode}
		if state.Ticket != nil {
			observation.Count = len(state.Ticket.State)
		}
		request.Projection = &extensionv1.AccountProjection{AccountID: id, Identity: state.Identity, Scheduling: []extensionv1.SchedulingConstraint{constraint}, Observations: []extensionv1.AccountObservation{observation}}
	}
	err = hostCall(ctx, host, extensionv1.HostStateCompareSwap, request, &result)
	if err == nil && !result.Applied {
		err = errors.New("ticket_state_changed")
	}
	return result.Revision, err
}

func (m *Module) harvest(ctx, epoch context.Context, host *extensionv1.Client, cfg Config, req Operation, manual bool) (Outcome, error) {
	if !cfg.Enabled {
		return Outcome{Code: "ticket_disabled"}, nil
	}
	if !slices.Contains(cfg.Models, req.Model) {
		return Outcome{Code: "ticket_model_invalid"}, nil
	}
	if cfg.ProxyURL == "" {
		return Outcome{Code: "ticket_proxy_missing"}, nil
	}
	if manual && (req.OperationID == "" || len(req.OperationID) > 256) {
		return Outcome{Code: "ticket_operation_required"}, nil
	}
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	case <-ctx.Done():
		return Outcome{Code: "ticket_canceled"}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	var account extensionv1.Account
	if err := hostCall(ctx, host, extensionv1.HostAccountRead, extensionv1.AccountQuery{AccountID: req.AccountID}, &account); err != nil {
		return Outcome{}, err
	}
	if !Eligible(account.Platform, account.Type, account.Shadow) {
		return Outcome{Code: "ticket_ineligible"}, nil
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Outcome{}, err
	}
	lease := extensionv1.LeaseRequest{Namespace: "tickets", Key: stateKey(req.AccountID, req.Model), Owner: hex.EncodeToString(nonce[:]), TTLSeconds: 30}
	var acquired extensionv1.LeaseResult
	if err := hostCall(ctx, host, extensionv1.HostLeaseAcquire, lease, &acquired); err != nil {
		return Outcome{}, err
	}
	if !acquired.Acquired {
		return Outcome{Code: "ticket_busy"}, nil
	}
	lease.Generation = acquired.Generation
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer releaseCancel()
		var result extensionv1.LeaseResult
		_ = hostCall(releaseCtx, host, extensionv1.HostLeaseRelease, lease, &result)
	}()
	state, revision, err := readState(ctx, host, req.AccountID, req.Model)
	if err != nil {
		return Outcome{}, err
	}
	now := time.Now().UTC()
	if manual && !req.Force && state.Ticket.Valid(now, account.ID, account.Identity, req.Model) {
		return Outcome{Success: true, Code: "ticket_skipped", ExpiresAt: &state.Ticket.ExpiresAt}, nil
	}
	if req.OperationID != "" && state.OperationID == req.OperationID {
		return Outcome{Code: "ticket_interrupted"}, nil
	}
	if (state.Identity != "" && state.Identity != account.Identity) || (state.Ticket != nil && state.Ticket.Identity != account.Identity) {
		state = State{Phase: "stopped", Identity: account.Identity}
		revision, err = writeState(ctx, host, req.AccountID, req.Model, state, revision)
		if err != nil {
			return Outcome{}, err
		}
		if !manual {
			return Outcome{Code: "ticket_stale"}, nil
		}
	}
	// Owning a fresh lease proves no previous process still owns this attempt.
	// Never replay its automatic stage; only a new explicit manual operation may
	// enroll it again.
	if state.Phase == "manual_running" || state.Phase == "pre_running" || state.Phase == "post_running" {
		state.Stop()
		state.LastCode = "ticket_interrupted"
		revision, err = writeState(ctx, host, req.AccountID, req.Model, state, revision)
		if err != nil {
			return Outcome{}, err
		}
		if !manual {
			return Outcome{Code: "ticket_interrupted"}, nil
		}
	}
	state.Identity = account.Identity
	if err = state.Begin(now, manual); err != nil {
		if errors.Is(err, ErrNotDue) && state.Phase == "retry" {
			if _, writeErr := writeState(ctx, host, req.AccountID, req.Model, state, revision); writeErr != nil {
				return Outcome{}, writeErr
			}
		}
		return Outcome{Code: err.Error()}, nil
	}
	state.OperationID = req.OperationID
	revision, err = writeState(ctx, host, req.AccountID, req.Model, state, revision)
	if err != nil {
		return Outcome{}, err
	}
	var identity extensionv1.OutboundIdentity
	outcome := Outcome{Code: "ticket_token"}
	var ticket *Ticket
	client, proxyResult, proxyErr := m.prepareProxy(ctx, host, cfg.ProxyURL, false)
	if client != nil {
		defer client.CloseIdleConnections()
	}
	if proxyErr != nil {
		outcome.Code = "ticket_proxy_connect"
		if proxyResult != nil && proxyResult.Code != "" {
			outcome.Code = proxyResult.Code
		}
	} else if err = hostCall(ctx, host, extensionv1.HostResolveIdentity, extensionv1.AccountQuery{AccountID: account.ID, PrepareCredentials: true}, &identity); err == nil {
		if identity.ObservationID != "" {
			defer func() {
				endCtx, endCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
				defer endCancel()
				var result map[string]bool
				_ = hostCall(endCtx, host, extensionv1.HostFinishObservation, extensionv1.AccountQuery{AccountID: account.ID, ObservationID: identity.ObservationID}, &result)
			}()
		}
		if identity.Identity == account.Identity {
			ticket, outcome = m.requestTicket(ctx, client, identity, req.Model)
		} else {
			outcome.Code = "ticket_stale"
		}
	}
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	var current extensionv1.Account
	if err := hostCall(finishCtx, host, extensionv1.HostAccountRead, extensionv1.AccountQuery{AccountID: account.ID}, &current); err != nil || current.Identity != account.Identity {
		return Outcome{Code: "ticket_stale"}, nil
	}
	// Cancellation revokes this attempt, even when an upstream returned a
	// successful header at the same time. Retain the spent stage durably without
	// publishing the late ticket or enrolling it for renewal.
	if ctx.Err() != nil || epoch.Err() != nil {
		ticket = nil
		outcome = Outcome{Code: "ticket_canceled"}
	}
	state.Complete(time.Now().UTC(), ticket, outcome.Code)
	if _, err = writeState(finishCtx, host, req.AccountID, req.Model, state, revision); err != nil {
		return Outcome{Code: "ticket_persist"}, err
	}
	return outcome, nil
}

func probe(ctx context.Context, client *http.Client, identity extensionv1.OutboundIdentity, model string) (*Ticket, Outcome) {
	body, _ := json.Marshal(map[string]any{"model": model, "store": false, "stream": true, "instructions": "Reply with exactly: pong", "input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "ping"}}}}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", bytes.NewReader(body))
	if err != nil {
		return nil, Outcome{Code: "ticket_transport"}
	}
	req.Header = http.Header(identity.Headers).Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Set("Authorization", "Bearer "+identity.Token)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	applyHarvestIdentity(req.Header, model)
	req.Header.Set("session_id", newSessionID())
	req.Close = true
	response, err := client.Do(req)
	if err != nil {
		return nil, Outcome{Code: "ticket_transport"}
	}
	defer response.Body.Close()
	value := strings.TrimSpace(response.Header.Get("x-codex-turn-state"))
	outcome := Outcome{Code: "ticket_upstream", HTTPStatus: response.StatusCode, ObservedLength: len(value)}
	if response.StatusCode != http.StatusOK {
		return nil, outcome
	}
	if len(value) != Length {
		outcome.Code = "ticket_length"
		return nil, outcome
	}
	if !strings.HasPrefix(value, "gAAAAA") {
		outcome.Code = "ticket_prefix"
		return nil, outcome
	}
	now := time.Now().UTC()
	ticket := &Ticket{State: value, AccountID: identity.AccountID, Identity: identity.Identity, Model: model, CapturedAt: now, ExpiresAt: now.Add(TTL)}
	outcome.Success = true
	outcome.Code = "ticket_ready"
	outcome.ExpiresAt = &ticket.ExpiresAt
	return ticket, outcome
}

func newSessionID() string {
	var value [16]byte
	_, _ = rand.Read(value[:])
	value[6] = (value[6] & 15) | 64
	value[8] = (value[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[:4], value[4:6], value[6:8], value[8:10], value[10:])
}
