package policy

import (
	"context"
	"encoding/json"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

func TestImportPolicyPlansEachItemByIdentityMatches(t *testing.T) {
	request := extensionv1.AccountImportPlanningRequest{Items: []extensionv1.AccountImportItemFacts{
		{PayloadValid: true},
		{PayloadValid: true, Matches: []int64{7}},
		{PayloadValid: true, Matches: []int64{7, 8}},
		{PayloadValid: false, Matches: []int64{7}},
	}}
	plans := planImport(request)
	if plans[0].Action != "create" || plans[0].Code != extensionv1.AccountImportCodeCreate {
		t.Fatalf("unmatched item was not created: %+v", plans[0])
	}
	if plans[1].Action != "update" || plans[1].AccountID != 7 {
		t.Fatalf("single identity match was not updated: %+v", plans[1])
	}
	if plans[2].Code != extensionv1.AccountImportCodeIdentityConflict {
		t.Fatal("ambiguous identity was accepted")
	}
	if plans[3].Code != extensionv1.AccountImportCodePayloadInvalid || plans[3].AccountID != 0 {
		t.Fatal("invalid payload was accepted")
	}
	request.Phase = "finalize"
	raw, _ := json.Marshal(request)
	if _, err := New().Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityAdmin, Operation: "import.plan", Payload: raw}); err == nil {
		t.Fatal("incomplete prepared phase was accepted")
	}
}
