package service

import (
	"context"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

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
