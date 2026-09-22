package policy

import (
	"fmt"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func validateNativeImage(in extensionv1.ImageNativeRequest) error {
	capability := in.Capability
	if !capability.PublicModel || capability.Kind != extensionv1.CindyModelKindImage || capability.Controls == nil {
		return fmt.Errorf("model %q has no verified Cindy image capability", in.Model)
	}
	endpoint := extensionv1.CindyEndpointImagesGenerate
	controls := capability.Controls.Generation
	if in.Editing {
		endpoint, controls = extensionv1.CindyEndpointImagesEdit, capability.Controls.Edit
	}
	if !in.Verified || controls == nil {
		return fmt.Errorf("model %q is not verified for %s", capability.PublicID, endpoint)
	}
	if in.Stream {
		return fmt.Errorf("stream is not verified for model %q on %s", capability.PublicID, endpoint)
	}
	if controls.MaxOutputCount <= 0 || in.Count <= 0 || in.Count > controls.MaxOutputCount {
		return fmt.Errorf("n must be between 1 and %d for model %q on %s", controls.MaxOutputCount, capability.PublicID, endpoint)
	}
	if !imageControlAllows(controls.Sizes, in.Size) {
		return fmt.Errorf("size %q is not verified for model %q on %s", in.Size, capability.PublicID, endpoint)
	}
	if !imageControlAllows(controls.Qualities, in.Quality) {
		return fmt.Errorf("quality %q is not verified for model %q on %s", in.Quality, capability.PublicID, endpoint)
	}
	if in.ResponseFormat != "" && !strings.EqualFold(in.ResponseFormat, "b64_json") {
		return fmt.Errorf("response_format must be b64_json for model %q", capability.PublicID)
	}
	if in.UnverifiedControls {
		return fmt.Errorf("request contains an unverified image control for model %q on %s", capability.PublicID, endpoint)
	}
	if in.Editing {
		if !controls.SupportsReferenceImage || !in.HasReference {
			return fmt.Errorf("a reference image is required for model %q on %s", capability.PublicID, endpoint)
		}
		if in.HasMask && !controls.SupportsMask {
			return fmt.Errorf("mask is not verified for model %q on %s", capability.PublicID, endpoint)
		}
	}
	return nil
}
