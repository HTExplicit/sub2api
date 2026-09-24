package tickets

import (
	"context"
	"encoding/json"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"testing"
	"time"
)

func TestRoutingConfigMigratesOnlyUnversionedExactDefaults(t *testing.T) {
	module := NewModule()
	defer module.cancel()
	raw, err := module.ValidateConfig(context.Background(), []byte(`{"models":["gpt-6-astra","gpt-5.6-sol"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if json.Unmarshal(raw, &cfg) != nil || len(cfg.Models) != 4 || cfg.RoutingSchema != 2 {
		t.Fatal("old exact defaults were not migrated")
	}
	for _, input := range []string{`{"models":["custom-model"]}`, `{"routing_schema":2,"models":["gpt-6-astra","gpt-5.6-sol"]}`} {
		raw, err := module.ValidateConfig(context.Background(), []byte(input))
		if err != nil {
			t.Fatal(err)
		}
		var updated Config
		_ = json.Unmarshal(raw, &updated)
		if len(updated.Models) > 2 {
			t.Fatal("custom/explicitly edited model list was expanded")
		}
	}
}

func TestRoutingMigrationAndTwoFailuresPreserveStop(t *testing.T) {
	for _, phase := range []string{"ready", "retry", "stopped", "manual_running"} {
		state := State{Phase: phase, Ticket: json.RawMessage(`{"state":"retired-state"}`)}
		state.migrateRouting()
		if state.Ticket != nil || state.Qualification != nil {
			t.Fatal("legacy state became qualification")
		}
		if phase == "ready" || phase == "retry" {
			if !state.Enrolled || state.Phase != "needs_cookie_verification" {
				t.Fatal("enrollment intent lost")
			}
		} else if state.Phase != phase {
			t.Fatal("spent state revived")
		}
	}
	now := time.Now()
	state := State{Schema: 2, Enrolled: true, Phase: "pre_running"}
	state.completeRouting(now, nil, extensionv1.CodexRoutingObservation{Code: "routing_model_mismatch"})
	if state.Failures != 1 || state.Phase != "retry" {
		t.Fatal("first failure should be bounded retry")
	}
	state.Phase = "pre_running"
	state.completeRouting(now, nil, extensionv1.CodexRoutingObservation{Code: "routing_model_mismatch"})
	if state.Phase != "stopped" || state.NextAt != nil || state.Failures != 2 {
		t.Fatal("second failure did not stop")
	}
	state.migrateRouting()
	if state.beginRouting(now.Add(time.Hour), false) == nil {
		t.Fatal("restart revived stopped renewal")
	}
}

func TestRoutingStaleHostReferenceWithdrawsOnlyItsQualification(t *testing.T) {
	account := extensionv1.Account{ID: 7, Identity: "owner", Platform: "openai", Type: "oauth"}
	now := time.Now().UTC()
	q := &extensionv1.CodexRoutingQualification{Scope: testRoutingScope(account), Model: "gpt-6-astra", VerifiedAt: now, ExpiresAt: now.Add(time.Minute), Bundle: extensionv1.CodexRoutingBundleRef{Key: "bundle.stale", Revision: 1, ExpiresAt: now.Add(time.Minute)}}
	raw, _ := json.Marshal(State{Schema: 2, Identity: "owner", Phase: "ready", Enrolled: true, Qualification: q})
	host := &memoryHost{account: account, checkInvalid: true, state: map[string]extensionv1.StateResult{stateKey(7, "gpt-6-astra"): {Found: true, Revision: 1, Value: raw}}}
	module := NewModule()
	defer module.cancel()
	module.SetHost(testHostClient(t, host))
	module.config.Enabled = true
	raw, _ = json.Marshal(extensionv1.SchedulingRequest{Account: account, Model: "gpt-6-astra", Now: now})
	result, err := module.Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "inject", Payload: raw})
	if err != nil || result.Code != "routing_stale" {
		t.Fatal("stale bundle was accepted")
	}
	var state State
	if json.Unmarshal(host.state[stateKey(7, "gpt-6-astra")].Value, &state) != nil || state.Qualification != nil || state.Phase != "needs_cookie_verification" || !state.Enrolled || state.Failures != 0 {
		t.Fatal("stale reference did not withdraw exactly the old qualification")
	}
}

func TestRoutingDemandRearmsAnEnrolledDormantState(t *testing.T) {
	account := extensionv1.Account{ID: 7, Identity: "owner", Platform: "openai", Type: "oauth"}
	state := State{Schema: 2, Identity: "owner", Phase: "ready", Enrolled: true}
	raw, _ := json.Marshal(state)
	key := stateKey(7, "gpt-6-astra")
	host := &memoryHost{account: account, state: map[string]extensionv1.StateResult{key: {Found: true, Revision: 1, Value: raw}}}
	module := NewModule()
	defer module.cancel()
	client := testHostClient(t, host)
	_, err := module.recordRoutingDemand(context.Background(), client, Config{Enabled: true}, extensionv1.CodexRoutingDemand{AccountID: 7, Model: "gpt-6-astra"})
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(host.state[key].Value, &state) != nil || state.NextAt == nil || !state.Enrolled || state.Failures != 0 {
		t.Fatal("demand did not rearm dormant due index")
	}
	if !hasRoutingDemand(context.Background(), client, 7, "gpt-6-astra") {
		t.Fatal("demand was not separately persisted")
	}
}
