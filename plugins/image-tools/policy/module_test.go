package policy

import (
	"context"
	"encoding/json"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func TestImageExecutionRequiresExplicitEnablementAndKeepsNativeOne(t *testing.T) {
	module := New()
	request := extensionv1.ImageStudioPlanRequest{APIKeySelected: true, Mode: "generate", Model: "gpt-image-2", PromptBytes: 12, Count: 4}
	raw, _ := json.Marshal(request)
	invocation := extensionv1.Invocation{Capability: extensionv1.CapabilityRequest, Operation: "image.studio.plan", Payload: raw}
	out, err := module.Invoke(context.Background(), invocation)
	if err != nil || out.Code != "studio_disabled" {
		t.Fatalf("default must remain disabled: %+v %v", out, err)
	}
	if err := module.ApplyConfig(context.Background(), []byte(`{"studio_enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	out, err = module.Invoke(context.Background(), invocation)
	if err != nil || out.Code != "" {
		t.Fatalf("valid plan rejected: %+v %v", out, err)
	}
	var plan extensionv1.ImageStudioPlan
	if json.Unmarshal(out.Payload, &plan) != nil || plan.OutputPerRequest != 1 || plan.Size != "1024x1024" || plan.Endpoint != "/v1/images/generations" {
		t.Fatalf("unexpected plan: %s", out.Payload)
	}
	request.Mode, request.HasReference = "edit", true
	invocation.Payload, _ = json.Marshal(request)
	out, err = module.Invoke(context.Background(), invocation)
	if err != nil || out.Code != "unsupported_mode" {
		t.Fatalf("unsupported editing must be rejected: %+v %v", out, err)
	}
	request.Model = "gemini-3-pro-image"
	invocation.Payload, _ = json.Marshal(request)
	out, err = module.Invoke(context.Background(), invocation)
	if err != nil || out.Code != "" || json.Unmarshal(out.Payload, &plan) != nil || plan.Endpoint != "/v1/images/edits" {
		t.Fatalf("supported editing plan unavailable: %+v %v", out, err)
	}
}
