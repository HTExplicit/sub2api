package service

import (
	"context"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func imageBridgeFixture() extensionv1.ImageBridgeRequest {
	return extensionv1.ImageBridgeRequest{Stage: "validate", Model: "luna", CatalogEnabled: true,
		Capabilities: []extensionv1.CindyCapability{{PublicID: "gpt-image-2", LiveUpstreamID: "openai/gpt-image-2", PublicModel: true,
			Kind: extensionv1.CindyModelKindImage, VerifiedEndpoints: []extensionv1.CindyEndpoint{extensionv1.CindyEndpointImagesGenerate},
			Controls: &extensionv1.CindyCapabilityControls{Generation: &extensionv1.CindyImageRequestControls{Sizes: []string{"1024x1024"}, Qualities: []string{"low"}, MaxOutputCount: 1}}}},
		Tools: []extensionv1.ImageBridgeTool{{Index: 2, Controls: extensionv1.ImageBridgeControls{Count: extensionv1.ImageBridgeValue{Present: true, Valid: true, Number: 1}}}},
	}
}

func TestImageToolsResponsesBridgePlansOnlyVerifiedModelsAndRequiresEnablement(t *testing.T) {
	in := imageBridgeFixture()
	enabled := extensionv1.ImageToolsConfig{ResponsesImageEnabled: true}
	plan, code, message := planResponsesImageBridge(in, enabled)
	if code != "" || len(plan.Tools) != 1 || plan.Tools[0].Index != 2 || plan.Tools[0].Model != "openai/gpt-image-2" || !plan.Tools[0].StripCount {
		t.Fatalf("default tool plan: %+v %s %s", plan, code, message)
	}
	for _, disabled := range []extensionv1.ImageToolsConfig{{}, {StudioEnabled: true}} {
		if _, code, _ := planResponsesImageBridge(in, disabled); code != "image_model_not_found" {
			t.Fatalf("disabled bridge executed with config %+v: %s", disabled, code)
		}
	}
	in.Model = "openai/gpt-image-2"
	in.Tools = nil
	plan, code, _ = planResponsesImageBridge(in, enabled)
	if code != "" || !plan.Supported || plan.Model != "gpt-image-2" {
		t.Fatalf("top-level live ID: %+v %s", plan, code)
	}
	in.Capabilities[0].PublicModel = false
	if _, code, _ := planResponsesImageBridge(in, enabled); code != "image_model_not_found" {
		t.Fatalf("hidden model admitted: %s", code)
	}
}

func TestImageToolsResponsesBridgeRejectsUnverifiedControlsBeforeIssuingPlan(t *testing.T) {
	for _, count := range []float64{0, 2, 1.5} {
		in := imageBridgeFixture()
		in.Tools[0].Controls.Count.Number = count
		if _, code, _ := planResponsesImageBridge(in, extensionv1.ImageToolsConfig{ResponsesImageEnabled: true}); code != "invalid_image_control" {
			t.Fatalf("invalid count %v: %s", count, code)
		}
	}
	for _, field := range []string{"quality", "size", "mask", "model"} {
		in := imageBridgeFixture()
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
		if _, code, _ := planResponsesImageBridge(in, extensionv1.ImageToolsConfig{ResponsesImageEnabled: true}); code != "invalid_image_control" {
			t.Fatalf("invalid %s: %s", field, code)
		}
	}
}

func TestImageToolsNativeImageControlsKeepGenerationAndEditingIndependent(t *testing.T) {
	request := extensionv1.ImageNativeRequest{Model: "fixture-image", Verified: true, Count: 1, Size: "1024x1024", Quality: "low", ResponseFormat: "b64_json", Capability: extensionv1.CindyCapability{PublicID: "fixture-image", PublicModel: true, Kind: extensionv1.CindyModelKindImage, Controls: &extensionv1.CindyCapabilityControls{Generation: &extensionv1.CindyImageRequestControls{Sizes: []string{"1024x1024"}, Qualities: []string{"low"}, MaxOutputCount: 1}}}}
	if err := validateNativeImageRequest(request); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*extensionv1.ImageNativeRequest){
		func(r *extensionv1.ImageNativeRequest) { r.Editing = true; r.HasReference = true },
		func(r *extensionv1.ImageNativeRequest) { r.Count = 2 },
		func(r *extensionv1.ImageNativeRequest) { r.Stream = true },
		func(r *extensionv1.ImageNativeRequest) { r.UnverifiedControls = true },
		func(r *extensionv1.ImageNativeRequest) { r.Verified = false },
	} {
		copy := request
		change(&copy)
		if err := validateNativeImageRequest(copy); err == nil {
			t.Fatalf("unverified request admitted: %+v", copy)
		}
	}
	request.Editing = true
	request.Capability.Controls.Edit = &extensionv1.CindyImageRequestControls{Sizes: []string{"1024x1024"}, Qualities: []string{"low"}, MaxOutputCount: 1, SupportsReferenceImage: true}
	if err := validateNativeImageRequest(request); err == nil {
		t.Fatal("missing reference admitted")
	}
	request.HasReference = true
	if err := validateNativeImageRequest(request); err != nil {
		t.Fatal(err)
	}
	request.HasMask = true
	if err := validateNativeImageRequest(request); err == nil {
		t.Fatal("unverified mask admitted")
	}
}

func TestImageToolsStudioPlanRequiresExplicitEnablementAndKeepsNativeOne(t *testing.T) {
	t.Cleanup(func() { ConfigureImageTools(nil) })
	input := ImageStudioCreateInput{APIKeyID: 1, Mode: "generate", Model: "gpt-image-2", Prompt: "fixture text", Count: 4}
	ConfigureImageTools(&extensionv1.ImageToolsConfig{})
	if _, err := planImageStudio(context.Background(), input, false, false, true); err == nil {
		t.Fatal("default must remain disabled")
	}
	if _, err := planImageStudio(context.Background(), input, false, false, false); err != nil {
		t.Fatalf("validation must not require enablement: %v", err)
	}
	ConfigureImageTools(&extensionv1.ImageToolsConfig{StudioEnabled: true})
	plan, err := planImageStudio(context.Background(), input, false, false, true)
	if err != nil || plan.OutputPerRequest != 1 || plan.Size != "1024x1024" || plan.Endpoint != "/v1/images/generations" {
		t.Fatalf("unexpected plan: %+v %v", plan, err)
	}
	input.Mode = "edit"
	if _, err := planImageStudio(context.Background(), input, true, false, true); err == nil {
		t.Fatal("unsupported editing must be rejected")
	}
	input.Model = "gemini-3-pro-image"
	plan, err = planImageStudio(context.Background(), input, true, false, true)
	if err != nil || plan.Endpoint != "/v1/images/edits" {
		t.Fatalf("supported editing plan unavailable: %+v %v", plan, err)
	}
}

func TestSwitchingImageStudioOnStartsTheRuntime(t *testing.T) {
	previousConfig, previousStarter := imageToolsConfigOverride.Load(), imageStudioStarter.Load()
	t.Cleanup(func() {
		imageToolsConfigOverride.Store(previousConfig)
		imageStudioStarter.Store(previousStarter)
	})
	starts := 0
	SetImageStudioStarter(func() { starts++ })
	ConfigureImageTools(&extensionv1.ImageToolsConfig{})
	if starts != 0 {
		t.Fatalf("switching Image Studio off must not start the runtime, starts=%d", starts)
	}
	ConfigureImageTools(&extensionv1.ImageToolsConfig{StudioEnabled: true})
	if starts != 1 {
		t.Fatalf("switching Image Studio on must start the runtime once, starts=%d", starts)
	}
}

func TestSwitchingImageStudioOffStopsDetachedUpstreamIO(t *testing.T) {
	previous := imageToolsConfigOverride.Load()
	t.Cleanup(func() { imageToolsConfigOverride.Store(previous) })
	ConfigureImageTools(&extensionv1.ImageToolsConfig{StudioEnabled: true})
	bound, release := bindImageStudioEnabled(context.Background())
	defer release()
	upstream, releaseUpstream := detachUpstreamContext(bound)
	defer releaseUpstream()
	ConfigureImageTools(&extensionv1.ImageToolsConfig{})
	select {
	case <-upstream.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("switching Image Studio off must cancel detached upstream IO")
	}
}
