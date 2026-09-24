package tickets

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
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
			module.prepareProxy = func(context.Context, HostCaller, string, bool) (*http.Client, *ProxyResult, error) {
				return &http.Client{}, &ProxyResult{}, nil
			}
			host.probe = func(query extensionv1.CodexRoutingQuery) extensionv1.CodexRoutingProbeResult {
				if query.Stage != "verify" {
					return testRoutingProbe(host.account, query)
				}
				if cancelByConfig {
					if err := module.ApplyConfig(context.Background(), json.RawMessage(`{"enabled":false}`)); err != nil {
						t.Fatal(err)
					}
					// The callback deliberately ignores cancellation, as a transport
					// can deliver a response concurrently with cancellation.
				} else {
					cancel()
				}
				return testRoutingProbe(host.account, query)
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
			if json.Unmarshal(stored.Value, &state) != nil || state.Qualification != nil || state.NextAt != nil || state.Phase != "stopped" {
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
		change        func(*extensionv1.CodexRoutingQualification, *extensionv1.SchedulingRequest)
		allow, inject bool
	}{
		{"valid", func(*extensionv1.CodexRoutingQualification, *extensionv1.SchedulingRequest) {}, true, true},
		{"expired", func(q *extensionv1.CodexRoutingQualification, _ *extensionv1.SchedulingRequest) { q.ExpiresAt = now }, false, false},
		{"missing_bundle", func(q *extensionv1.CodexRoutingQualification, _ *extensionv1.SchedulingRequest) { q.Bundle.Key = "" }, false, false},
		{"missing_revision", func(q *extensionv1.CodexRoutingQualification, _ *extensionv1.SchedulingRequest) {
			q.Bundle.Revision = 0
		}, false, false},
		{"wrong_owner", func(q *extensionv1.CodexRoutingQualification, _ *extensionv1.SchedulingRequest) {
			q.Scope.Identity = "other"
		}, false, false},
		{"wrong_account", func(q *extensionv1.CodexRoutingQualification, _ *extensionv1.SchedulingRequest) { q.Scope.AccountID++ }, false, false},
		{"wrong_model", func(q *extensionv1.CodexRoutingQualification, _ *extensionv1.SchedulingRequest) {
			q.Model = "gpt-5.6-sol"
		}, false, false},
		{"future_verification", func(q *extensionv1.CodexRoutingQualification, _ *extensionv1.SchedulingRequest) {
			q.VerifiedAt = now.Add(time.Minute)
		}, false, false},
		{"excessive_ttl", func(q *extensionv1.CodexRoutingQualification, _ *extensionv1.SchedulingRequest) {
			q.ExpiresAt = now.Add(2 * time.Hour)
		}, false, false},
		{"ungated_model", func(_ *extensionv1.CodexRoutingQualification, r *extensionv1.SchedulingRequest) { r.Model = "gpt-5.5" }, true, false},
		{"shadow", func(_ *extensionv1.CodexRoutingQualification, r *extensionv1.SchedulingRequest) {
			r.Account.Shadow = true
		}, true, false},
		{"apikey", func(_ *extensionv1.CodexRoutingQualification, r *extensionv1.SchedulingRequest) {
			r.Account.Type = "apikey"
		}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			qualification := &extensionv1.CodexRoutingQualification{Scope: testRoutingScope(account), Model: "gpt-6-astra", VerifiedAt: now, ExpiresAt: now.Add(time.Minute), Bundle: extensionv1.CodexRoutingBundleRef{Key: "bundle.test", Revision: 1, ExpiresAt: now.Add(time.Minute)}}
			request := extensionv1.SchedulingRequest{Account: account, Model: qualification.Model, Now: now}
			tc.change(qualification, &request)
			rawState, _ := json.Marshal(State{Schema: 2, Identity: account.Identity, Qualification: qualification})
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
			if strings.Contains(string(injection.Payload), "routing_qualification") != tc.inject || strings.Contains(string(injection.Payload), "x-codex-turn-state") {
				t.Fatal("unexpected opaque-state injection or missing qualification reference")
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
	module.prepareProxy = func(context.Context, HostCaller, string, bool) (*http.Client, *ProxyResult, error) {
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
