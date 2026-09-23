package tickets

import (
	"encoding/json"
	"strings"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func TestRoutingOutcomeSeparatesModelAndQualityEvidence(t *testing.T) {
	for _, length := range []int{292, 332, 780} {
		outcome := Outcome{Success: true, Code: "routing_verified", ObservedLength: length, Observation: &extensionv1.CodexRoutingObservation{Completed: true, ModelMatched: true, RequestedModel: "gpt-6-astra", ResponseModel: "gpt-6-astra"}}
		raw, err := json.Marshal(outcome)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Facts []extensionv1.DisplayFact `json:"facts"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		facts := map[string]string{}
		for _, fact := range result.Facts {
			facts[fact.Label["en"]] = fact.Value
		}
		if facts["STATE length (observation only)"] == "" || facts["Response completion"] == "" || facts["Model declaration match"] == "" || !strings.Contains(facts["Quality validation"], "Not tested") {
			t.Fatalf("length %d confused routing evidence with quality: %s", length, raw)
		}
	}
}

func TestRoutingOutcomeKeepsFailureCategories(t *testing.T) {
	for _, code := range []string{"routing_capacity", "routing_policy", "routing_cancelled", "routing_legacy_retired"} {
		raw, err := json.Marshal(Outcome{Code: code})
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Code    string `json:"code"`
			Success bool   `json:"success"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &result) != nil || result.Code != code || result.Success || result.Message == "" || strings.Contains(result.Message, "操作未完成，请检查") {
			t.Fatalf("failure category was lost: %s", raw)
		}
	}
}
