package catalog

import (
	"context"
	"encoding/json"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

func TestProbePolicyOwnsControlsAndHealthDecisions(t *testing.T) {
	plan := balanceProbePlan()
	if plan.Models != [2]string{"tencent/hy3", "z-ai/glm-5.3-flash"} || plan.Input != "Reply OK." || plan.MaxOutputTokens != 1 {
		t.Fatal("probe controls changed")
	}
	for _, test := range []struct {
		stage         string
		marked        bool
		outcome       extensionv1.CindyProbeOutcome
		action, state string
	}{
		{"luna", true, extensionv1.CindyProbeSuccess, "recover", ""},
		{"luna", false, extensionv1.CindyProbeSuccess, "complete", "healthy"},
		{"terra", false, extensionv1.CindyProbeSuccess, "complete", "inconclusive"},
		{"luna", false, extensionv1.CindyProbeExhausted, "complete", "luna_exact"},
		{"luna", true, extensionv1.CindyProbeExhausted, "complete", "still_exhausted"},
		{"terra", false, extensionv1.CindyProbeExhausted, "exhausted", ""},
		{"luna", false, extensionv1.CindyProbeNetworkFailure, "complete", "inconclusive"},
	} {
		decision, err := decideBalanceProbe(extensionv1.CindyProbeResult{Stage: test.stage, WasMarked: test.marked, Outcome: test.outcome})
		if err != nil || decision.Action != test.action || decision.State != test.state {
			t.Fatalf("%+v => %+v, %v", test, decision, err)
		}
	}
	module := New()
	if err := module.ApplyConfig(context.Background(), []byte(`{"balance_detection":false}`)); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"cindy.probe.plan", "cindy.probe.decide"} {
		result, err := module.Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityProvider, Operation: operation, Payload: json.RawMessage(`{}`)})
		if err != nil || result.Code != "disabled" {
			t.Fatalf("disabled probe operation allowed: %s", operation)
		}
	}
}
