package registry

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

func TestPublicationPlanRejectsCurrentRollbackButAllowsOtherVersion(t *testing.T) {
	controller := New()
	request := extensionv1.SkillPublicationPolicyRequest{
		Action:                 extensionv1.PublicationActionRollback,
		CurrentBundleVersionID: 4,
		Target: extensionv1.SkillPublicationBundleSummary{
			BundleVersionID: 5, PromptVersionID: 6, UpstreamSourceID: RemoteSkillUpstreamSourceID,
		},
	}
	raw, err := json.Marshal(request)
	require.NoError(t, err)
	out, err := controller.Invoke(context.Background(), extensionv1.Invocation{
		Capability: extensionv1.CapabilityRequest, Operation: "skills.publication.plan", Payload: raw,
	})
	require.NoError(t, err)
	require.Empty(t, out.Code)
	var plan extensionv1.SkillPublicationPolicyPlan
	require.NoError(t, json.Unmarshal(out.Payload, &plan))
	require.Equal(t, extensionv1.PublicationActionRollback, plan.Action)
	require.True(t, plan.Allowed)

	request.Target.BundleVersionID = request.CurrentBundleVersionID
	raw, err = json.Marshal(request)
	require.NoError(t, err)
	out, err = controller.Invoke(context.Background(), extensionv1.Invocation{
		Capability: extensionv1.CapabilityRequest, Operation: "skills.publication.plan", Payload: raw,
	})
	require.NoError(t, err)
	require.Equal(t, "bundle_invalid", out.Code)
}

func TestStoragePlanUsesBoundedHashDerivedLayout(t *testing.T) {
	controller := New()
	request := extensionv1.SkillStorageLayoutRequest{
		EffectiveTreeSHA256:   strings.Repeat("a", 64),
		EffectivePromptSHA256: strings.Repeat("b", 64),
	}
	raw, err := json.Marshal(request)
	require.NoError(t, err)
	out, err := controller.Invoke(context.Background(), extensionv1.Invocation{
		Capability: extensionv1.CapabilityRequest, Operation: "skills.storage.plan", Payload: raw,
	})
	require.NoError(t, err)
	require.Empty(t, out.Code)
	var plan extensionv1.SkillStorageLayoutPlan
	require.NoError(t, json.Unmarshal(out.Payload, &plan))
	require.Equal(t, "paired", plan.Namespace)
	require.Equal(t, strings.Repeat("a", 64)+"-"+strings.Repeat("b", 64), plan.CandidateKey)
	require.Equal(t, []string{"metadata", "raw_tree", "effective_tree", "raw_prompt", "effective_prompt", "prompt_diff"}, plan.Slots)
}
