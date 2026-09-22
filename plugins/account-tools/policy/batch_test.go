package policy

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func TestPlanBatchRejectsNonPositiveLegacyIDsWithoutShrinkingSelection(t *testing.T) {
	for _, ids := range [][]int64{{0, 5}, {-1, 5}, {5, 0}} {
		request := extensionv1.BatchTestPlanningRequest{HasLegacy: true, AccountIDs: ids, ModelID: "model"}
		if _, err := planBatch(request); err == nil {
			t.Fatalf("invalid legacy IDs were accepted: %v", ids)
		}
		raw, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		result, err := New().Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityAdmin, Operation: "test.batch", Payload: raw})
		if err != nil || result.HTTPStatus != 400 || result.Code != "ACCOUNT_TEST_SELECTION_INVALID" || len(result.Payload) != 0 {
			t.Fatalf("invalid legacy IDs must return a typed error without a partial selection: ids=%v result=%+v err=%v", ids, result, err)
		}
	}
}

func TestPlanBatchKeepsLegalLegacyDeduplicationAndSorting(t *testing.T) {
	plan, err := planBatch(extensionv1.BatchTestPlanningRequest{HasLegacy: true, AccountIDs: []int64{9, 3, 9}, ModelID: " model "})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual([]int64{3, 9}, plan.AccountIDs) || !reflect.DeepEqual(map[int64]string{3: "model", 9: "model"}, plan.Models) || plan.ModelID != "model" {
		t.Fatalf("legal legacy IDs must remain sorted, deduplicated, and account-keyed: %+v", plan)
	}
}
