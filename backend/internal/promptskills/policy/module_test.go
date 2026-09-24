package policy

import (
	"context"
	"encoding/json"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
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

func TestPromptPublicationPlanCarriesActionAndPreservesRollbackScope(t *testing.T) {
	base := extensionv1.PromptPublicationPolicyRequest{
		Action:           extensionv1.PublicationActionPublish,
		CurrentVersionID: 4,
		Target: extensionv1.PromptPublicationVersionSummary{
			ID: 5, Version: 2, CompositionMode: "inline",
		},
	}
	raw, err := json.Marshal(base)
	require.NoError(t, err)
	out, err := New().Invoke(context.Background(), extensionv1.Invocation{
		Capability: extensionv1.CapabilityRequest, Operation: "prompt.publication.plan", Payload: raw,
	})
	require.NoError(t, err)
	require.Empty(t, out.Code)
	var plan extensionv1.PromptPublicationPolicyPlan
	require.NoError(t, json.Unmarshal(out.Payload, &plan))
	require.Equal(t, extensionv1.PublicationActionPublish, plan.Action)
	require.True(t, plan.Allowed)

	base.Action = extensionv1.PublicationActionRollback
	base.Target.ID = base.CurrentVersionID
	raw, err = json.Marshal(base)
	require.NoError(t, err)
	out, err = New().Invoke(context.Background(), extensionv1.Invocation{
		Capability: extensionv1.CapabilityRequest, Operation: "prompt.publication.plan", Payload: raw,
	})
	require.NoError(t, err)
	require.Equal(t, "prompt_invalid", out.Code)
}
