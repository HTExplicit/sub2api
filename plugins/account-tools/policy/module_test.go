package policy

import (
	"context"
	"encoding/json"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func TestTaxonomyPoliciesRejectAmbiguityAndInvalidAssignments(t *testing.T) {
	for _, tc := range []struct{ operation, payload, code string }{
		{"taxonomy.name", `{"name":"  Production  "}`, ""},
		{"taxonomy.name", `{"name":"bad\u0000name"}`, "ACCOUNT_TAXONOMY_NAME_INVALID"},
		{"taxonomy.order", `{"actual":[1,2],"ordered":[2,1]}`, ""},
		{"taxonomy.order", `{"actual":[1,2],"ordered":[1,1]}`, "ACCOUNT_TAXONOMY_ORDER_INVALID"},
		{"taxonomy.order", `{"actual":[1,2,3],"ordered":[2,1]}`, "ACCOUNT_TAXONOMY_ORDER_CHANGED"},
		{"taxonomy.bulk", `{"account_ids":[1],"has_filters":true,"folder_action":"clear"}`, "ACCOUNT_TAXONOMY_TARGET_INVALID"},
		{"taxonomy.bulk", `{"account_ids":[1],"tag_add_ids":[2],"tag_remove_ids":[2]}`, "ACCOUNT_TAXONOMY_TAG_OPERATION_CONFLICT"},
		{"taxonomy.bulk", `{"has_filters":true,"folder_action":"clear"}`, "ACCOUNT_TAXONOMY_EXPECTED_COUNT_REQUIRED"},
		{"taxonomy.assignment", `{"tag_ids":[-1]}`, "ACCOUNT_TAG_ID_INVALID"},
		{"taxonomy.assignment", `{"tag_ids":[1,1,2]}`, ""},
	} {
		out, err := New().Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityAdmin, Operation: tc.operation, Payload: json.RawMessage(tc.payload)})
		if err != nil || out.Code != tc.code {
			t.Fatalf("%s: %v %+v", tc.operation, err, out)
		}
	}
}

func TestReasoningSelectionValidatesTheSuppliedWireCapabilities(t *testing.T) {
	for _, tc := range []struct {
		mode, effort string
		valid        bool
	}{{"default", "ultra", true}, {"text", "ultra", true}, {"compact", "ultra", false}, {"default", " high ", false}, {"default", "high", false}, {"default", "", true}} {
		raw, _ := json.Marshal(extensionv1.ReasoningSelection{Mode: tc.mode, Effort: tc.effort, Levels: []string{"ultra"}})
		out, err := New().Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityAdmin, Operation: "test.reasoning", Payload: raw})
		if err != nil || (out.Code == "") != tc.valid {
			t.Fatalf("%s/%s: %v %+v", tc.mode, tc.effort, err, out)
		}
	}
}
