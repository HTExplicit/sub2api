package service

import (
	"encoding/json"
	"testing"
)

func TestCapabilityPublicationPricingIdentifiesExistingExactFallback(t *testing.T) {
	dynamic := &PricingService{pricingData: map[string]*LiteLLMModelPricing{}}
	billing := NewBillingService(nil, dynamic)
	publisher := NewAccountCapabilityPublicationService(nil, billing, nil, nil, nil, nil)
	snapshot := &CapabilityPublicationGroupSnapshot{Group: &Group{}}
	for _, model := range []string{"kimi-k3", "glm-5.3", "glm-5.3-flash"} {
		t.Run(model, func(t *testing.T) {
			if dynamic.GetIdentifiedModelPricing(model) != nil {
				t.Fatal("fixture must exercise existing billing fallback, not dynamic pricing")
			}
			if !CapabilityHasIdentifiedPricing(billing, nil, nil, model) || !publisher.hasPrice(snapshot, model) {
				t.Fatal("an existing exact fallback price was incorrectly rejected")
			}
		})
	}
}

func TestCapabilityPublicationPricingRejectsGuessesAndTokenAbsent(t *testing.T) {
	dynamic := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"capability-image-only-fixture": {TokenPricingAbsent: true, OutputCostPerImage: 0.04},
		"capability-text-fixture":       {InputCostPerToken: 1e-6, OutputCostPerToken: 2e-6},
	}}
	billing := NewBillingService(nil, dynamic)
	if dynamic.GetIdentifiedModelPricing("capability-image-only-fixture") == nil {
		t.Fatal("fixture must have an identified entry with no token prices")
	}
	for _, model := range []string{"totally-made-up-haiku-v9", "capability-image-only-fixture", "qwen3-coder-plus", "gemini-3.8-flash"} {
		t.Run(model, func(t *testing.T) {
			if CapabilityHasIdentifiedPricing(billing, nil, nil, model) {
				t.Fatal("guessed, token-absent or unpriced model was admitted")
			}
		})
	}
	if !CapabilityHasIdentifiedPricing(billing, nil, nil, "capability-text-fixture") {
		t.Fatal("identified dynamic token pricing was rejected")
	}
	if CapabilityHasIdentifiedPricing(nil, nil, nil, "unpriced") {
		t.Fatal("missing pricing sources must fail closed")
	}
}

func TestCapabilityPublicationPricingPreservesExplicitOverrides(t *testing.T) {
	zero, perRequest, imagePrice := 0.0, 0.004, 0.03
	group := &Group{ModelPricing: []ChannelModelPricing{
		{Models: []string{"explicit-group-model"}, InputPrice: &zero, OutputPrice: &zero},
		{Models: []string{"image-only-override"}, ImageOutputPrice: &imagePrice},
	}}
	channel := &Channel{ModelPricing: []ChannelModelPricing{
		{Models: []string{"explicit-channel-model"}, InputPrice: &perRequest, OutputPrice: &perRequest},
		{Models: []string{"explicit-per-request"}, BillingMode: BillingModePerRequest, PerRequestPrice: &perRequest},
	}}
	before, err := json.Marshal([]any{group.ModelPricing, channel.ModelPricing})
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"EXPLICIT-GROUP-MODEL", "explicit-channel-model", "explicit-per-request"} {
		if !CapabilityHasIdentifiedPricing(nil, group, channel, model) {
			t.Fatalf("existing exact override was rejected: %s", model)
		}
	}
	for _, model := range []string{"image-only-override", "prefix-explicit-group-model", "unpriced"} {
		if CapabilityHasIdentifiedPricing(nil, group, channel, model) {
			t.Fatalf("non-token or non-exact override was admitted: %s", model)
		}
	}
	after, err := json.Marshal([]any{group.ModelPricing, channel.ModelPricing})
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("pricing admission changed an existing override amount or configuration")
	}
}

func TestCapabilityPublicationPricingDoesNotAdmitInactiveChannelOverride(t *testing.T) {
	price := 0.000002
	channel := &Channel{Status: "inactive", ModelPricing: []ChannelModelPricing{{Models: []string{"fixture-inactive-only"}, InputPrice: &price, OutputPrice: &price}}}
	if CapabilityHasIdentifiedPricing(nil, nil, channel, "fixture-inactive-only") {
		t.Fatal("an inactive channel's unused price authorized a new public model")
	}
	group := &Group{ModelPricing: []ChannelModelPricing{{Models: []string{"fixture-inactive-only"}, InputPrice: &price, OutputPrice: &price}}}
	if !CapabilityHasIdentifiedPricing(nil, group, channel, "fixture-inactive-only") || channel.Status != "inactive" {
		t.Fatal("an independent group override must not require enabling the inactive channel")
	}
}
