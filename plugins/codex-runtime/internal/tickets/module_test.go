package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type memoryHost struct {
	mu       sync.Mutex
	state    map[string]extensionv1.StateResult
	lease    map[string]int64
	sequence int64
	account  extensionv1.Account
	due      []extensionv1.DueState
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
	h.mu.Lock()
	defer h.mu.Unlock()
	var value any
	switch in.Operation {
	case extensionv1.HostAccountRead:
		value = h.account
	case extensionv1.HostResolveIdentity:
		value = extensionv1.OutboundIdentity{AccountID: h.account.ID, Identity: h.account.Identity, Token: "test-only"}
	case extensionv1.HostStateRead, extensionv1.HostStateCompareSwap:
		var req extensionv1.StateRequest
		if err := json.Unmarshal(in.Payload, &req); err != nil {
			return extensionv1.Result{}, err
		}
		current := h.state[req.Key]
		current.Applied = false
		if in.Operation == extensionv1.HostStateCompareSwap && current.Revision == req.ExpectedRevision {
			current = extensionv1.StateResult{Found: true, Applied: true, Revision: current.Revision + 1, Value: append([]byte(nil), req.Value...)}
			h.state[req.Key] = current
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

func testHostClient(t *testing.T, host *memoryHost) *extensionv1.Client {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	extensionv1.RegisterHost(server, host)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	connection, err := grpc.NewClient("passthrough:///host", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return extensionv1.NewClient(connection)
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
	material := "gAAAAA" + strings.Repeat("a", 286)
	module.prepareProxy = func(context.Context, *extensionv1.Client, string, bool) (*http.Client, *ProxyResult, error) {
		return &http.Client{}, &ProxyResult{NetworkReachable: true}, nil
	}
	module.requestTicket = func(_ context.Context, _ *http.Client, identity extensionv1.OutboundIdentity, model string) (*Ticket, Outcome) {
		requests.Add(1)
		if identity.Token != "test-only" || identity.Identity != "principal" {
			t.Error("wrong prepared credential")
		}
		now := time.Now().UTC()
		ticket := &Ticket{State: material, AccountID: identity.AccountID, Identity: identity.Identity, Model: model, CapturedAt: now, ExpiresAt: now.Add(TTL)}
		return ticket, Outcome{Success: true, Code: "ticket_ready", ObservedLength: 292, HTTPStatus: 200, ExpiresAt: &ticket.ExpiresAt}
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
	if requests.Load() != 1 {
		t.Fatal("first manual attempt not singular")
	}
	request.OperationID = "manual-2"
	invoke(extensionv1.CapabilityAdmin, "harvest", request)
	if requests.Load() != 1 {
		t.Fatal("valid ticket caused another upstream request")
	}
	admission := extensionv1.SchedulingRequest{Account: host.account, Model: request.Model, Now: time.Now().UTC()}
	result = invoke(extensionv1.CapabilityScheduling, "admit", admission)
	var decision extensionv1.SchedulingDecision
	if json.Unmarshal(result.Payload, &decision) != nil || !decision.Allowed {
		t.Fatal("prepared inactive credential was not available for ticket admission")
	}
	result = invoke(extensionv1.CapabilityRequest, "inject", admission)
	if !strings.Contains(string(result.Payload), material) {
		t.Fatal("internal request hook did not supply the stored ticket")
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
	if after.Revision != stored.Revision || requests.Load() != 1 {
		t.Fatal("configuration changes resurrected stopped renewal")
	}
}
