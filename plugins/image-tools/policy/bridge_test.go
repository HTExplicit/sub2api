package policy

import (
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func bridgeFixture() extensionv1.ImageBridgeRequest {
	return extensionv1.ImageBridgeRequest{Stage: "validate", Model: "luna", CatalogEnabled: true,
		Capabilities: []extensionv1.CindyCapability{{PublicID: "gpt-image-2", LiveUpstreamID: "openai/gpt-image-2", PublicModel: true,
			Kind: extensionv1.CindyModelKindImage, VerifiedEndpoints: []extensionv1.CindyEndpoint{extensionv1.CindyEndpointImagesGenerate},
			Controls: &extensionv1.CindyCapabilityControls{Generation: &extensionv1.CindyImageRequestControls{Sizes: []string{"1024x1024"}, Qualities: []string{"low"}, MaxOutputCount: 1}}}},
		Tools: []extensionv1.ImageBridgeTool{{Index: 2, Controls: extensionv1.ImageBridgeControls{Count: extensionv1.ImageBridgeValue{Present: true, Valid: true, Number: 1}}}},
	}
}

func TestResponsesBridgePlansOnlyVerifiedModelsAndRequiresEnablement(t *testing.T) {
	in := bridgeFixture()
	enabled := extensionv1.ImageToolsConfig{ResponsesImageEnabled: true}
	plan, code, message := planResponsesBridge(in, enabled)
	if code != "" || len(plan.Tools) != 1 || plan.Tools[0].Index != 2 || plan.Tools[0].Model != "openai/gpt-image-2" || !plan.Tools[0].StripCount {
		t.Fatalf("default tool plan: %+v %s %s", plan, code, message)
	}
	for _, disabled := range []extensionv1.ImageToolsConfig{{}, {StudioEnabled: true}} {
		if _, code, _ := planResponsesBridge(in, disabled); code != "image_model_not_found" {
			t.Fatalf("disabled bridge executed with config %+v: %s", disabled, code)
		}
	}
	in.Model = "openai/gpt-image-2"
	in.Tools = nil
	plan, code, _ = planResponsesBridge(in, enabled)
	if code != "" || !plan.Supported || plan.Model != "gpt-image-2" {
		t.Fatalf("top-level live ID: %+v %s", plan, code)
	}
	in.Capabilities[0].PublicModel = false
	if _, code, _ := planResponsesBridge(in, enabled); code != "image_model_not_found" {
		t.Fatalf("hidden model admitted: %s", code)
	}
}

func TestResponsesBridgeRejectsUnverifiedControlsBeforeIssuingPlan(t *testing.T) {
	for _, count := range []float64{0, 2, 1.5} {
		in := bridgeFixture()
		in.Tools[0].Controls.Count.Number = count
		if _, code, _ := planResponsesBridge(in, extensionv1.ImageToolsConfig{ResponsesImageEnabled: true}); code != "invalid_image_control" {
			t.Fatalf("invalid count %v: %s", count, code)
		}
	}
	for _, field := range []string{"quality", "size", "mask", "model"} {
		in := bridgeFixture()
		switch field {
		case "quality":
			in.Tools[0].Controls.Quality = extensionv1.ImageBridgeValue{Present: true, Valid: true, Text: "high"}
		case "size":
			in.Tools[0].Controls.Size = extensionv1.ImageBridgeValue{Present: true, Valid: false}
		case "mask":
			in.Tools[0].Controls.Present = []string{"mask"}
		case "model":
			in.Tools[0].InvalidModel = true
		}
		if _, code, _ := planResponsesBridge(in, extensionv1.ImageToolsConfig{ResponsesImageEnabled: true}); code != "invalid_image_control" {
			t.Fatalf("invalid %s: %s", field, code)
		}
	}
}
