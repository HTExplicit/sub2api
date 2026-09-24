package catalog

import (
	"context"
	"encoding/json"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

func TestAlphaSearchPlanRequiresSearchEnabled(t *testing.T) {
	registry := Registry{Config: extensionv1.CindyProviderConfig{SearchEnabled: false}}
	plan := registry.AlphaSearchPlan("gpt-5.6-luna")
	if plan.Allowed || plan.Reason != "search_disabled" {
		t.Fatalf("unexpected disabled search plan: %+v", plan)
	}
}

func TestAlphaSearchPlanUsesExactPublicModelAndResponsesFirst(t *testing.T) {
	registry := Registry{Config: extensionv1.CindyProviderConfig{SearchEnabled: true}}
	plan := registry.AlphaSearchPlan("gpt-5.6-luna")
	if !plan.Allowed {
		t.Fatalf("public model was denied: %+v", plan)
	}
	if plan.RequestedModel != "gpt-5.6-luna" || plan.UpstreamModel != "openai/gpt-5.6-luna" {
		t.Fatalf("unexpected model mapping: %+v", plan)
	}
	if plan.PrimaryProtocol != "responses" || plan.ResponsesToolType != "web_search" {
		t.Fatalf("unexpected Responses plan: %+v", plan)
	}
	if plan.MaxSearchUses != 1 || plan.FallbackProtocol != "messages" || !plan.FallbackOnCapabilityMiss || !plan.FallbackOnMissingSearchEvidence || plan.NativeMessagesModel != CindyWebSearchModel {
		t.Fatalf("unexpected bounded fallback plan: %+v", plan)
	}
}

func TestAlphaSearchPlanRejectsHiddenLiveAndCompatibilityAliasModels(t *testing.T) {
	registry := Registry{Config: extensionv1.CindyProviderConfig{SearchEnabled: true}}
	for _, model := range []string{"openai/gpt-5.6-luna", CindyWebSearchModel, "gpt-5.4-mini"} {
		plan := registry.AlphaSearchPlan(model)
		if plan.Allowed {
			t.Fatalf("non-public model was admitted: model=%q plan=%+v", model, plan)
		}
	}
}

func TestAlphaSearchPlanUsesHelperQualificationInsteadOfPublicMessagesEndpoint(t *testing.T) {
	const model = "search-no-messages-fixture"
	previous, existed := cindyCapabilityByPublicID[model]
	cindyCapabilityByPublicID[model] = &CindyCapability{
		PublicID:          model,
		LiveUpstreamID:    "fixture/search-no-messages",
		Kind:              CindyModelKindText,
		VerifiedEndpoints: []CindyEndpoint{CindyEndpointResponses, CindyEndpointAlphaSearch},
		PublicModel:       true,
	}
	t.Cleanup(func() {
		if existed {
			cindyCapabilityByPublicID[model] = previous
		} else {
			delete(cindyCapabilityByPublicID, model)
		}
	})

	plan := (Registry{Config: extensionv1.CindyProviderConfig{SearchEnabled: true}}).AlphaSearchPlan(model)
	if !plan.Allowed || !plan.FallbackOnCapabilityMiss || plan.FallbackProtocol != "messages" || plan.NativeMessagesModel != CindyWebSearchModel {
		t.Fatalf("public model Messages endpoint incorrectly controlled fallback: %+v", plan)
	}
}

func TestAlphaSearchPlanWithoutQualifiedHelperDoesNotFallback(t *testing.T) {
	const model = "search-helper-disabled-fixture"
	previous, existed := cindyCapabilityByPublicID[CindyWebSearchModel]
	delete(cindyCapabilityByPublicID, CindyWebSearchModel)
	cindyCapabilityByPublicID[model] = &CindyCapability{
		PublicID:          model,
		LiveUpstreamID:    "fixture/search-helper-disabled",
		Kind:              CindyModelKindText,
		VerifiedEndpoints: []CindyEndpoint{CindyEndpointResponses, CindyEndpointAlphaSearch, CindyEndpointMessages},
		PublicModel:       true,
	}
	t.Cleanup(func() {
		if existed {
			cindyCapabilityByPublicID[CindyWebSearchModel] = previous
		}
		delete(cindyCapabilityByPublicID, model)
	})

	plan := (Registry{Config: extensionv1.CindyProviderConfig{SearchEnabled: true}}).AlphaSearchPlan(model)
	if !plan.Allowed || plan.FallbackOnCapabilityMiss || plan.FallbackProtocol != "" || plan.NativeMessagesModel != "" {
		t.Fatalf("unqualified helper still enabled fallback: %+v", plan)
	}
}

func TestAlphaSearchPlanProviderOperationCarriesOnlyModel(t *testing.T) {
	module := New()
	if err := module.ApplyConfig(context.Background(), []byte(`{"search_enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(struct {
		Model string `json:"model"`
	}{Model: "gpt-5.6-luna"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := module.Invoke(context.Background(), extensionv1.Invocation{
		Capability: extensionv1.CapabilityProvider,
		Operation:  "cindy.search.plan",
		Payload:    payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	var plan AlphaSearchPlan
	if err := json.Unmarshal(result.Payload, &plan); err != nil {
		t.Fatal(err)
	}
	if !plan.Allowed || plan.RequestedModel != "gpt-5.6-luna" {
		t.Fatalf("unexpected provider operation result: %+v", plan)
	}
	_, err = module.Invoke(context.Background(), extensionv1.Invocation{
		Capability: extensionv1.CapabilityProvider,
		Operation:  "cindy.search.plan",
		Payload:    []byte(`{"model":"gpt-5.6-luna","prompt":"must-not-cross"}`),
	})
	if err == nil {
		t.Fatal("provider operation accepted a non-model RPC field")
	}
}
