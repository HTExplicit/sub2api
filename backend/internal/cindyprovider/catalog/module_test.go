package catalog

import (
	"context"
	"encoding/json"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

func text(value string) *string { return &value }

func TestInvalidProviderConfigurationCannotChangeActivation(t *testing.T) {
	module := New()
	if err := module.ApplyConfig(context.Background(), []byte(`{"balance_detection":false}`)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`null`, `{"balance_detection":null}`, `{} {}`, `{"unknown":true}`} {
		if err := module.ApplyConfig(context.Background(), []byte(raw)); err == nil {
			t.Fatalf("accepted invalid configuration: %s", raw)
		}
		status, err := module.Status(context.Background())
		if err != nil || string(status) != `{"balance_detection":false,"catalog_enabled":false,"search_enabled":false}` {
			t.Fatalf("invalid configuration changed current state: %s %v", status, err)
		}
	}
}
func TestHealthPolicyUsesOnlyExactStructuredBudgetSignals(t *testing.T) {
	registry := Registry{Config: extensionv1.CindyProviderConfig{BalanceDetection: true}}
	for _, tc := range []struct {
		in              extensionv1.CindyObservedResponse
		balance, health uint8
	}{
		{extensionv1.CindyObservedResponse{Status: 429, ValidJSON: true, ErrorType: text("budget_exceeded"), ErrorCode: text("429")}, 1, 1},
		{extensionv1.CindyObservedResponse{Status: 429, ValidJSON: true, ErrorType: text("budget_exceeded")}, 0, 0},
		{extensionv1.CindyObservedResponse{Status: 200, ValidJSON: true, EventType: text("response.failed"), ResponseErrorType: text("budget_exceeded"), ResponseErrorCode: text("429")}, 2, 1},
		{extensionv1.CindyObservedResponse{Status: 500, ValidJSON: true, EventType: text("response.failed"), ResponseErrorType: text("budget_exceeded"), ResponseErrorCode: text("429")}, 0, 0},
		{extensionv1.CindyObservedResponse{Status: 401}, 0, 3},
		{extensionv1.CindyObservedResponse{Status: 403}, 0, 2},
	} {
		out := registry.ClassifyResponse(tc.in)
		if out.Balance != tc.balance || out.Health != tc.health {
			t.Fatalf("unexpected decision: %+v", out)
		}
	}
}

func TestProviderConfigurationControlsCatalogAndSearchIndependently(t *testing.T) {
	module := New()
	if err := module.ApplyConfig(context.Background(), json.RawMessage(`{"catalog_enabled":false,"search_enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"CindyPublicModelIDs", "CindyManagedCompatibilityModels"} {
		raw, _ := json.Marshal(extensionv1.CindyCatalogQuery{Method: method})
		out, err := module.Invoke(context.Background(), extensionv1.Invocation{Capability: extensionv1.CapabilityProvider, Operation: "cindy.catalog", Payload: raw})
		if err != nil {
			t.Fatal(err)
		}
		var values []json.RawMessage
		if json.Unmarshal(out.Payload, &values) != nil || len(values) != 1 {
			t.Fatal("invalid query result")
		}
		var models []string
		if json.Unmarshal(values[0], &models) != nil {
			t.Fatal("invalid models")
		}
		if method == "CindyPublicModelIDs" && len(models) != 0 {
			t.Fatal("disabled catalog still published models")
		}
		if method == "CindyManagedCompatibilityModels" && len(models) == 0 {
			t.Fatal("search lost its independent managed models")
		}
	}
}
