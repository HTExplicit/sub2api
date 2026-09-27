package tickets

import (
	"encoding/json"
	"strings"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
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
	// Unknown codes keep their name; the original error and upstream facts are shown.
	observation := &extensionv1.CodexRoutingObservation{Code: "routing_new_case", Error: "dial tcp 192.0.2.1:8080: connect: connection refused", UpstreamErrorCode: "rate_limit_exceeded", UpstreamErrorMessage: "Rate limit reached", RequestID: "req_fixture", CFRay: "ray-fixture", UpstreamBody: `{"error":{"code":"rate_limit_exceeded"}}`}
	raw, err := json.Marshal(Outcome{Code: "routing_new_case", HTTPStatus: 429, Observation: observation})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"未归类的结果代码：routing_new_case", "connection refused", "rate_limit_exceeded", "Rate limit reached", "req_fixture", "ray-fixture", `\"code\":\"rate_limit_exceeded\"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("outcome lost %q: %s", want, raw)
		}
	}
}
