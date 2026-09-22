package policy

import (
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func TestNativeImageControlsKeepGenerationAndEditingIndependent(t *testing.T) {
	request := extensionv1.ImageNativeRequest{Model: "fixture-image", Verified: true, Count: 1, Size: "1024x1024", Quality: "low", ResponseFormat: "b64_json", Capability: extensionv1.CindyCapability{PublicID: "fixture-image", PublicModel: true, Kind: extensionv1.CindyModelKindImage, Controls: &extensionv1.CindyCapabilityControls{Generation: &extensionv1.CindyImageRequestControls{Sizes: []string{"1024x1024"}, Qualities: []string{"low"}, MaxOutputCount: 1}}}}
	if err := validateNativeImage(request); err != nil {
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
		if err := validateNativeImage(copy); err == nil {
			t.Fatalf("unverified request admitted: %+v", copy)
		}
	}
	request.Editing = true
	request.Capability.Controls.Edit = &extensionv1.CindyImageRequestControls{Sizes: []string{"1024x1024"}, Qualities: []string{"low"}, MaxOutputCount: 1, SupportsReferenceImage: true}
	if err := validateNativeImage(request); err == nil {
		t.Fatal("missing reference admitted")
	}
	request.HasReference = true
	if err := validateNativeImage(request); err != nil {
		t.Fatal(err)
	}
	request.HasMask = true
	if err := validateNativeImage(request); err == nil {
		t.Fatal("unverified mask admitted")
	}
}
