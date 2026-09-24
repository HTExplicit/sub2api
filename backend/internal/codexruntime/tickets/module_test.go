package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

type memoryHost struct {
	mu           sync.Mutex
	state        map[string]extensionv1.StateResult
	lease        map[string]int64
	sequence     int64
	account      extensionv1.Account
	due          []extensionv1.DueState
	probe        func(extensionv1.CodexRoutingQuery) extensionv1.CodexRoutingProbeResult
	checkInvalid bool
}

func TestInvalidConfigurationCannotStopCurrentTicketEpoch(t *testing.T) {
	module := NewModule()
	defer module.cancel()
	if err := module.ApplyConfig(context.Background(), []byte(`{"enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	epoch := module.epoch
	for _, raw := range []string{`null`, `{} {}`, `{"enabled":null}`, `{"fail_closed":null}`, `{"proxy_url":null}`, `{"models":null}`, `{"extra":true}`, `{"models":["one","one"]}`} {
		if err := module.ApplyConfig(context.Background(), []byte(raw)); err == nil {
			t.Fatalf("accepted invalid configuration: %s", raw)
		}
		if !module.config.Enabled || module.epoch != epoch || epoch.Err() != nil {
			t.Fatalf("invalid configuration changed ticket execution: %s", raw)
		}
	}
}

func TestTransportSettingDoesNotDependOnTicketAcquisitionSwitch(t *testing.T) {
	module := NewModule()
	defer module.cancel()
	request := extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "codex.transport.plan",
		Payload: []byte(`{"method":"POST","path":"/backend-api/codex/responses","body_present":true}`)}
	for _, raw := range []string{`{"enabled":false,"request_zstd":true}`, `{"enabled":true,"request_zstd":false}`} {
		if err := module.ApplyConfig(context.Background(), []byte(raw)); err != nil {
			t.Fatal(err)
		}
		result, err := module.Invoke(context.Background(), request)
		var plan extensionv1.CodexTransportPlan
		if err != nil || json.Unmarshal(result.Payload, &plan) != nil || plan.Compress != module.config.RequestZstd {
			t.Fatalf("transport plan unavailable: %v", err)
		}
	}
}

func (h *memoryHost) Call(_ context.Context, in extensionv1.HostInvocation) (extensionv1.Result, error) {
	if in.Operation == extensionv1.HostCodexRoutingProbe {
		var query extensionv1.CodexRoutingQuery
		if json.Unmarshal(in.Payload, &query) != nil {
			return extensionv1.Result{}, errors.New("invalid probe")
		}
		value := h.probe(query)
		raw, err := json.Marshal(value)
		return extensionv1.Result{Payload: raw}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var value any
	switch in.Operation {
	case extensionv1.HostAccountList:
		value = []extensionv1.Account{}
	case extensionv1.HostCodexRoutingScope:
		value = testRoutingScope(h.account)
	case extensionv1.HostCodexRoutingCheck:
		value = extensionv1.CodexRoutingProbeResult{Valid: !h.checkInvalid}
	case extensionv1.HostAccountRead:
		value = h.account
	case extensionv1.HostResolveIdentity:
		value = extensionv1.OutboundIdentity{AccountID: h.account.ID, Identity: h.account.Identity, Token: "test-only"}
	case extensionv1.HostStateRead, extensionv1.HostStateCompareSwap:
		var req extensionv1.StateRequest
		if err := json.Unmarshal(in.Payload, &req); err != nil {
			return extensionv1.Result{}, err
		}
		key := req.Key
		if req.Namespace != "tickets" {
			key = req.Namespace + ":" + key
		}
		current := h.state[key]
		current.Applied = false
		if in.Operation == extensionv1.HostStateCompareSwap && current.Revision == req.ExpectedRevision {
			current = extensionv1.StateResult{Found: true, Applied: true, Revision: current.Revision + 1, Value: append([]byte(nil), req.Value...)}
			h.state[key] = current
		}
		value = current
	case extensionv1.HostStateDue:
		value = h.due
	case extensionv1.HostLeaseAcquire, extensionv1.HostLeaseRelease:
		var req extensionv1.LeaseRequest
		if err := json.Unmarshal(in.Payload, &req); err != nil {
			return extensionv1.Result{}, err
		}
		result := extensionv1.LeaseResult{}
		if in.Operation == extensionv1.HostLeaseAcquire && h.lease[req.Key] == 0 {
			h.sequence++
			h.lease[req.Key] = h.sequence
			result.Acquired = true
			result.Generation = h.sequence
		}
		if in.Operation == extensionv1.HostLeaseRelease && h.lease[req.Key] == req.Generation {
			delete(h.lease, req.Key)
		}
		value = result
	default:
		return extensionv1.Result{}, errors.New("unexpected host call")
	}
	raw, err := json.Marshal(value)
	return extensionv1.Result{Payload: raw}, err
}

func testHostClient(t *testing.T, host *memoryHost) HostCaller {
	t.Helper()
	return host
}

func TestModulePersistsManualLifecycleAndNeverReturnsTicketToAdmin(t *testing.T) {
	host := &memoryHost{state: make(map[string]extensionv1.StateResult), lease: make(map[string]int64), account: extensionv1.Account{ID: 7, Platform: "openai", Type: "setup-token", Status: "error", Identity: "principal"}}
	client := testHostClient(t, host)
	module := NewModule()
	t.Cleanup(module.cancel)
	module.SetHost(client)
	if err := module.ApplyConfig(context.Background(), json.RawMessage(`{"enabled":true,"proxy_url":"http://proxy.test:8080"}`)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { module.mu.Lock(); module.cancel(); module.mu.Unlock() })
	var requests atomic.Int32
	material := "secret-cookie-material"
	host.probe = func(query extensionv1.CodexRoutingQuery) extensionv1.CodexRoutingProbeResult {
		requests.Add(1)
		return testRoutingProbe(host.account, query)
	}
	invoke := func(capability, operation string, payload any) extensionv1.Result {
		t.Helper()
		raw, _ := json.Marshal(payload)
		result, err := module.Invoke(context.Background(), extensionv1.Invocation{Capability: capability, Operation: operation, Payload: raw})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	request := Operation{AccountID: 7, Model: "gpt-6-astra", OperationID: "manual-1"}
	result := invoke(extensionv1.CapabilityAdmin, "harvest", request)
	if strings.Contains(string(result.Payload), material) || !strings.Contains(string(result.Payload), `"success":true`) {
		t.Fatalf("unsafe/failed admin outcome: %s", result.Payload)
	}
	if requests.Load() != 2 {
		t.Fatal("one acquisition and one verification required")
	}
	request.OperationID = "manual-2"
	invoke(extensionv1.CapabilityAdmin, "harvest", request)
	if requests.Load() != 2 {
		t.Fatal("valid ticket caused another upstream request")
	}
	admission := extensionv1.SchedulingRequest{Account: host.account, Model: request.Model, Now: time.Now().UTC()}
	result = invoke(extensionv1.CapabilityScheduling, "admit", admission)
	var decision extensionv1.SchedulingDecision
	if json.Unmarshal(result.Payload, &decision) != nil || !decision.Allowed {
		t.Fatal("prepared inactive credential was not available for ticket admission")
	}
	result = invoke(extensionv1.CapabilityRequest, "inject", admission)
	if strings.Contains(string(result.Payload), material) || !strings.Contains(string(result.Payload), "routing_qualification") || strings.Contains(string(result.Payload), "x-codex-turn-state") {
		t.Fatal("internal request hook must return only qualification reference")
	}
	invoke(extensionv1.CapabilityAdmin, "stop", request)
	host.mu.Lock()
	stored := host.state[stateKey(7, request.Model)]
	host.mu.Unlock()
	var persisted State
	if json.Unmarshal(stored.Value, &persisted) != nil || persisted.Phase != "stopped" || persisted.NextAt != nil {
		t.Fatal("stop did not persist")
	}
	if err := module.ApplyConfig(context.Background(), json.RawMessage(`{"enabled":false}`)); err != nil {
		t.Fatal(err)
	}
	if err := module.ApplyConfig(context.Background(), json.RawMessage(`{"enabled":true,"proxy_url":"http://proxy.test:8080"}`)); err != nil {
		t.Fatal(err)
	}
	host.mu.Lock()
	after := host.state[stateKey(7, request.Model)]
	host.mu.Unlock()
	if after.Revision != stored.Revision || requests.Load() != 2 {
		t.Fatal("configuration changes resurrected stopped renewal")
	}
}

func testRoutingScope(account extensionv1.Account) extensionv1.CodexRoutingScope {
	return extensionv1.CodexRoutingScope{AccountID: account.ID, Identity: account.Identity, ProfileHash: "profile", RouteHash: "route", Transport: "http"}
}

func testRoutingProbe(account extensionv1.Account, query extensionv1.CodexRoutingQuery) extensionv1.CodexRoutingProbeResult {
	scope := testRoutingScope(account)
	scope.ConnectionLeaseID, scope.RouteEvidence = "physical-connection", "connection"
	return extensionv1.CodexRoutingProbeResult{Scope: scope, Valid: true, Bundle: &extensionv1.CodexRoutingBundleRef{Key: "bundle.reference", Revision: 1, ExpiresAt: time.Now().Add(100 * time.Second), ConnectionLeaseID: scope.ConnectionLeaseID}, Observation: extensionv1.CodexRoutingObservation{Stage: query.Stage, Code: "routing_verified", RequestedModel: query.Model, ResponseModel: query.Model, Completed: true, ModelMatched: true, StateLength: 312, HTTPStatus: 200}}
}
