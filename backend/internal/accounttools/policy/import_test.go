package policy

import (
	"context"
	"encoding/json"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func TestImportPolicyPreservesRejectionPriorityAndExplicitGroup(t *testing.T) {
	valid := extensionv1.AccountImportItemFacts{CindyCandidate: true, APIKeyValid: true, PayloadValid: true, DeviceValid: true, DeviceSourceValid: true}
	request := extensionv1.AccountImportPlanningRequest{TargetGroupID: 12, TargetCanonical: true, Items: []extensionv1.AccountImportItemFacts{valid}}
	plan := planImport(request)[0]
	if plan.Action != "create" || len(plan.GroupIDs) != 1 || plan.GroupIDs[0] != 12 {
		t.Fatalf("empty canonical group cannot bootstrap: %+v", plan)
	}
	request.Items[0].APIKeyValid = false
	request.Items[0].PayloadValid = false
	request.TargetGroupID = 0
	if planImport(request)[0].Code != extensionv1.AccountImportCodeCindyAPIKeyInvalid {
		t.Fatal("credential failure priority changed")
	}
	request.Items[0].APIKeyValid = true
	if planImport(request)[0].Code != extensionv1.AccountImportCodeCindyTargetRequired {
		t.Fatal("explicit group requirement was bypassed")
	}
	request.Items = []extensionv1.AccountImportItemFacts{{PayloadValid: true, Matches: []int64{7, 8}}}
	if planImport(request)[0].Code != extensionv1.AccountImportCodeIdentityConflict {
		t.Fatal("ambiguous ordinary identity was accepted")
	}
	request.Phase = "finalize"
	raw, _ := json.Marshal(request)
	if _, err := New().Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityAdmin, Operation: "import.plan", Payload: raw}); err == nil {
		t.Fatal("incomplete prepared phase was accepted")
	}
}
