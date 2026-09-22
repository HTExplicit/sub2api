package tickets

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func TestCanceledHarvestCannotPublishLateTicket(t *testing.T) {
	for _, cancelByConfig := range []bool{false, true} {
		name := "request"
		if cancelByConfig {
			name = "configuration"
		}
		t.Run(name, func(t *testing.T) {
			host := &memoryHost{state: make(map[string]extensionv1.StateResult), lease: make(map[string]int64), account: extensionv1.Account{ID: 7, Platform: "openai", Type: "oauth", Identity: "principal"}}
			module := NewModule()
			module.SetHost(testHostClient(t, host))
			t.Cleanup(func() { module.mu.Lock(); module.cancel(); module.mu.Unlock() })
			if err := module.ApplyConfig(context.Background(), json.RawMessage(`{"enabled":true,"proxy_url":"http://proxy.test:8080"}`)); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			module.prepareProxy = func(context.Context, *extensionv1.Client, string, bool) (*http.Client, *ProxyResult, error) {
				return &http.Client{}, &ProxyResult{}, nil
			}
			module.requestTicket = func(_ context.Context, _ *http.Client, identity extensionv1.OutboundIdentity, model string) (*Ticket, Outcome) {
				if cancelByConfig {
					if err := module.ApplyConfig(context.Background(), json.RawMessage(`{"enabled":false}`)); err != nil {
						t.Fatal(err)
					}
					// The callback deliberately ignores cancellation, as a transport
					// can deliver a response concurrently with cancellation.
				} else {
					cancel()
				}
				now := time.Now().UTC()
				return &Ticket{AccountID: identity.AccountID, Identity: identity.Identity, Model: model, State: "gAAAAA" + strings.Repeat("a", 286), CapturedAt: now, ExpiresAt: now.Add(TTL)}, Outcome{Success: true, Code: "ticket_ready"}
			}
			raw, _ := json.Marshal(Operation{AccountID: 7, Model: "gpt-6-astra", OperationID: "cancel-me"})
			result, err := module.Invoke(ctx, extensionv1.Invocation{Capability: extensionv1.CapabilityAdmin, Operation: "harvest", Payload: raw})
			if err != nil {
				t.Fatal(err)
			}
			var outcome Outcome
			if json.Unmarshal(result.Payload, &outcome) != nil || outcome.Success || outcome.Code != "ticket_canceled" {
				t.Fatalf("late response accepted: %s", result.Payload)
			}
			host.mu.Lock()
			stored := host.state[stateKey(7, "gpt-6-astra")]
			host.mu.Unlock()
			var state State
			if json.Unmarshal(stored.Value, &state) != nil || state.Ticket != nil || state.NextAt != nil || state.Phase != "stopped" {
				t.Fatalf("canceled attempt enrolled: %s", stored.Value)
			}
		})
	}
}

func TestTicketAdmissionAndInjectionShareIdentityValidityAndScope(t *testing.T) {
	now := time.Now().UTC()
	account := extensionv1.Account{ID: 7, Platform: "openai", Type: "setup-token", Identity: "principal", Status: "disabled"}
	for _, tc := range []struct {
		name          string
		change        func(*Ticket, *extensionv1.SchedulingRequest)
		allow, inject bool
	}{
		{"valid", func(*Ticket, *extensionv1.SchedulingRequest) {}, true, true},
		{"expired", func(t *Ticket, _ *extensionv1.SchedulingRequest) { t.ExpiresAt = now }, false, false},
		{"wrong_length", func(t *Ticket, _ *extensionv1.SchedulingRequest) { t.State += "a" }, false, false},
		{"wrong_prefix", func(t *Ticket, _ *extensionv1.SchedulingRequest) { t.State = strings.Repeat("x", Length) }, false, false},
		{"wrong_owner", func(t *Ticket, _ *extensionv1.SchedulingRequest) { t.Identity = "other" }, false, false},
		{"wrong_account", func(t *Ticket, _ *extensionv1.SchedulingRequest) { t.AccountID++ }, false, false},
		{"wrong_model", func(t *Ticket, _ *extensionv1.SchedulingRequest) { t.Model = "gpt-5.6-sol" }, false, false},
		{"future_capture", func(t *Ticket, _ *extensionv1.SchedulingRequest) { t.CapturedAt = now.Add(time.Minute) }, false, false},
		{"excessive_ttl", func(t *Ticket, _ *extensionv1.SchedulingRequest) { t.ExpiresAt = now.Add(2 * time.Hour) }, false, false},
		{"ungated_model", func(_ *Ticket, r *extensionv1.SchedulingRequest) { r.Model = "gpt-5.5" }, true, false},
		{"shadow", func(_ *Ticket, r *extensionv1.SchedulingRequest) { r.Account.Shadow = true }, true, false},
		{"apikey", func(_ *Ticket, r *extensionv1.SchedulingRequest) { r.Account.Type = "apikey" }, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ticket := &Ticket{AccountID: account.ID, Identity: account.Identity, Model: "gpt-6-astra", State: "gAAAAA" + strings.Repeat("a", 286), CapturedAt: now, ExpiresAt: now.Add(TTL)}
			request := extensionv1.SchedulingRequest{Account: account, Model: ticket.Model, Now: now}
			tc.change(ticket, &request)
			rawState, _ := json.Marshal(State{Identity: account.Identity, Ticket: ticket})
			host := &memoryHost{account: account, state: map[string]extensionv1.StateResult{stateKey(account.ID, "gpt-6-astra"): {Found: true, Value: rawState}}}
			module := NewModule()
			module.SetHost(testHostClient(t, host))
			t.Cleanup(func() { module.mu.Lock(); module.cancel(); module.mu.Unlock() })
			if err := module.ApplyConfig(context.Background(), json.RawMessage(`{"enabled":true}`)); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(request)
			admission, err := module.Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityScheduling, Operation: "admit", Payload: raw})
			if err != nil {
				t.Fatal(err)
			}
			var decision extensionv1.SchedulingDecision
			if json.Unmarshal(admission.Payload, &decision) != nil || decision.Allowed != tc.allow {
				t.Fatalf("admission mismatch: %s", admission.Payload)
			}
			injection, err := module.Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "inject", Payload: raw})
			if err != nil {
				t.Fatal(err)
			}
			if (injection.Code == "") != tc.allow {
				t.Fatalf("injection disagrees with admission: %+v", injection)
			}
			if strings.Contains(string(injection.Payload), ticket.State) != tc.inject {
				t.Fatal("unexpected ticket injection")
			}
		})
	}
}

func TestRestartRecoversAbandonedFirstAttemptWithoutSendingAnotherRequest(t *testing.T) {
	attempt := time.Now().Add(-time.Minute)
	key := stateKey(7, "gpt-6-astra")
	raw, _ := json.Marshal(State{Identity: "principal", Phase: "manual_running", OperationID: "spent", LastAttemptAt: &attempt})
	host := &memoryHost{state: map[string]extensionv1.StateResult{key: {Found: true, Revision: 1, Value: raw}}, lease: make(map[string]int64), account: extensionv1.Account{ID: 7, Platform: "openai", Type: "oauth", Identity: "principal"}, due: []extensionv1.DueState{{Key: key, Revision: 1, Value: raw}}}
	client := testHostClient(t, host)
	module := NewModule()
	t.Cleanup(module.cancel)
	module.prepareProxy = func(context.Context, *extensionv1.Client, string, bool) (*http.Client, *ProxyResult, error) {
		t.Error("an abandoned attempt must never reconnect")
		return nil, nil, nil
	}
	module.renewOnce(context.Background(), client, Config{Enabled: true, ProxyURL: "http://unused.example:8080", Models: []string{"gpt-6-astra"}})
	host.mu.Lock()
	stored := host.state[key]
	host.mu.Unlock()
	var state State
	if json.Unmarshal(stored.Value, &state) != nil || state.Phase != "stopped" || state.NextAt != nil || state.LastCode != "ticket_interrupted" || state.Ticket != nil {
		t.Fatalf("abandoned state not recovered: %s", stored.Value)
	}
}
