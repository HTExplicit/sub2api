package policy

import (
	"context"
	"encoding/json"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/stretchr/testify/require"
)

func TestPromptPlanProtocolVisibilityAndIntegrity(t *testing.T) {
	for _, protocol := range []string{"responses", "chat"} {
		snapshot := ruleSnapshot("auto", "control_append")
		raw, err := json.Marshal(extensionv1.PromptPlanRequest{Snapshot: snapshot, Target: BusinessSystemPromptTarget{Platform: "openai", Protocol: protocol}, HasInstructions: true})
		require.NoError(t, err)
		out, err := New().Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "prompt.plan", Payload: raw})
		require.NoError(t, err)
		require.Empty(t, out.Code)
		var result BusinessSystemPromptApplication
		require.NoError(t, json.Unmarshal(out.Payload, &result))
		require.True(t, result.Applied)
		require.Equal(t, "rule_plan", result.Carrier)
		expectedCarrier := "instructions"
		if protocol == "chat" {
			expectedCarrier = "messages"
		}
		require.Equal(t, expectedCarrier, result.RulesPlan.Placements[0].Carrier)
	}
	snapshot := ruleSnapshot("auto", "control_append")
	snapshot.ResolvedRules[0].SHA256 = "wrong"
	_, err := Plan(snapshot, BusinessSystemPromptTarget{Platform: "openai", Protocol: "responses"}, true)
	require.Error(t, err, "changed prompt must not bypass its stored digest")
	_, err = Plan(BusinessSystemPromptSnapshot{Enabled: true, Body: "legacy"}, BusinessSystemPromptTarget{Platform: "openai", Protocol: "responses"}, true)
	require.Error(t, err, "single-prompt legacy snapshots must be migrated before execution")
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
