package policy

import (
	"context"
	"encoding/json"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func TestPromptPlanProtocolVisibilityAndIntegrity(t *testing.T) {
	for _, tc := range []struct {
		name, protocol                             string
		compact, enabled, hasInstructions, applied bool
		carrier                                    string
	}{
		{"responses", "responses", false, true, false, true, "instructions"},
		{"chat", "chat", false, true, false, true, "system_message"},
		{"chat instructions", "chat", false, true, true, true, "instructions"},
		{"compact disabled", "responses", true, true, false, false, ""},
		{"disabled", "responses", false, false, false, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := BusinessSystemPromptSnapshot{Enabled: tc.enabled, Body: "server prompt", ExposeServerPrompt: false}
			raw, _ := json.Marshal(extensionv1.PromptPlanRequest{Snapshot: snapshot, Target: BusinessSystemPromptTarget{Platform: "openai", Protocol: tc.protocol, Compact: tc.compact}, HasInstructions: tc.hasInstructions})
			out, err := New().Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "prompt.plan", Payload: raw})
			if err != nil || out.Code != "" {
				t.Fatalf("plan failed: %v %s", err, out.Code)
			}
			var result BusinessSystemPromptApplication
			if json.Unmarshal(out.Payload, &result) != nil || result.Applied != tc.applied || result.Carrier != tc.carrier {
				t.Fatalf("wrong plan: %s", out.Payload)
			}
		})
	}
	if _, err := Plan(BusinessSystemPromptSnapshot{Enabled: true, Body: "server", SHA256: "wrong"}, BusinessSystemPromptTarget{Platform: "openai", Protocol: "responses"}, true); err == nil {
		t.Fatal("a changed prompt bypassed its stored digest")
	}
}
