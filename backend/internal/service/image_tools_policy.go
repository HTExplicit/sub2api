package service

import (
	"fmt"
	"math"
	"slices"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// Image request policy formerly served by the image-tools plugin process. These
// are pure checks over host facts; the switches come from currentImageToolsConfig.

func validateNativeImageRequest(in extensionv1.ImageNativeRequest) error {
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

func planResponsesImageBridge(in extensionv1.ImageBridgeRequest, config extensionv1.ImageToolsConfig) (extensionv1.ImageBridgePlan, string, string) {
	plan := extensionv1.ImageBridgePlan{}
	resolve := func(model string) (extensionv1.CindyCapability, bool) {
		if target, ok := in.Aliases[model]; ok {
			model = target
		}
		for _, capability := range in.Capabilities {
			if model == capability.PublicID || model == capability.LiveUpstreamID {
				return capability, true
			}
		}
		return extensionv1.CindyCapability{}, false
	}
	available := func(c extensionv1.CindyCapability) bool {
		return c.PublicModel && (in.CatalogEnabled ||
			(c.Kind == extensionv1.CindyModelKindImage && config.StudioEnabled) ||
			(c.PublicID == "gpt-image-2" && config.ResponsesImageEnabled))
	}
	supports := func(c extensionv1.CindyCapability) bool {
		return config.ResponsesImageEnabled && c.PublicModel && c.PublicID == "gpt-image-2" &&
			c.Kind == extensionv1.CindyModelKindImage && slices.Contains(c.VerifiedEndpoints, extensionv1.CindyEndpointImagesGenerate)
	}
	top, known := resolve(in.Model)
	plan.Supported = known && supports(top)
	if in.Stage == "supports" {
		return plan, "", ""
	}
	if in.Stage == "map" {
		if len(in.Tools) > 0 && !config.ResponsesImageEnabled {
			return plan, "image_model_not_found", "Responses image bridge is disabled"
		}
		if known && available(top) {
			plan.Model = top.LiveUpstreamID
		}
		for _, tool := range in.Tools {
			if capability, ok := resolve(tool.Model); ok && available(capability) && capability.Kind == extensionv1.CindyModelKindImage {
				plan.Tools = append(plan.Tools, extensionv1.ImageBridgeModelPlan{Index: tool.Index, Model: capability.LiveUpstreamID})
			}
		}
		return plan, "", ""
	}
	if in.Stage != "validate" {
		return plan, "invalid_bridge_stage", "Invalid image bridge stage"
	}
	modelError := func(model string) (extensionv1.ImageBridgePlan, string, string) {
		return plan, "image_model_not_found", fmt.Sprintf("cindy Responses image tool model is not verified: %q", model)
	}
	if known && top.Kind == extensionv1.CindyModelKindImage && !plan.Supported {
		return modelError(in.Model)
	}
	if plan.Supported {
		if top.Controls == nil || top.Controls.Generation == nil {
			return modelError(in.Model)
		}
		if err := validateImageBridgeControls("request", top.PublicID, top.Controls.Generation, in.Controls); err != nil {
			return plan, "invalid_image_control", err.Error()
		}
		plan.Model, plan.StripCount = top.PublicID, in.Controls.Count.Present
	}
	for _, tool := range in.Tools {
		if tool.InvalidModel {
			return plan, "invalid_image_control", fmt.Sprintf("tools[%d].model must be a bounded string", tool.Index)
		}
		model := tool.Model
		if model == "" {
			model = "gpt-image-2"
		}
		capability, ok := resolve(model)
		if !ok || !available(capability) || !supports(capability) || capability.Controls == nil || capability.Controls.Generation == nil {
			return modelError(model)
		}
		if err := validateImageBridgeControls(fmt.Sprintf("tools[%d]", tool.Index), capability.PublicID, capability.Controls.Generation, tool.Controls); err != nil {
			return plan, "invalid_image_control", err.Error()
		}
		plan.Tools = append(plan.Tools, extensionv1.ImageBridgeModelPlan{Index: tool.Index, Model: capability.LiveUpstreamID, StripCount: tool.Controls.Count.Present})
	}
	return plan, "", ""
}

func validateImageBridgeControls(location, model string, controls *extensionv1.CindyImageRequestControls, facts extensionv1.ImageBridgeControls) error {
	for _, field := range []struct {
		name    string
		value   extensionv1.ImageBridgeValue
		allowed []string
	}{{"size", facts.Size, controls.Sizes}, {"quality", facts.Quality, controls.Qualities}} {
		if field.value.Present && (!field.value.Valid || !imageControlAllows(field.allowed, field.value.Text)) {
			return fmt.Errorf("%s.%s is not verified for model %q", location, field.name, model)
		}
	}
	count := facts.Count
	if count.Present && (!count.Valid || count.Number != math.Trunc(count.Number) || count.Number < 1 || count.Number > float64(controls.MaxOutputCount)) {
		return fmt.Errorf("%s.n must be between 1 and %d for model %q", location, controls.MaxOutputCount, model)
	}
	if len(facts.Present) > 0 {
		return fmt.Errorf("%s.%s is not verified for model %q", location, facts.Present[0], model)
	}
	return nil
}

func imageControlAllows(allowed []string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, candidate := range allowed {
		if strings.EqualFold(strings.TrimSpace(candidate), value) {
			return true
		}
	}
	return false
}

func planImageStudioRequest(in extensionv1.ImageStudioPlanRequest) (extensionv1.ImageStudioPlan, string, string) {
	plan := extensionv1.ImageStudioPlan{Model: strings.TrimSpace(in.Model), Mode: in.Mode, Size: strings.TrimSpace(in.Size), Quality: strings.TrimSpace(in.Quality), OutputPerRequest: 1, ResponseFormat: "b64_json", Endpoint: "/v1/images/generations"}
	if !in.APIKeySelected {
		return plan, "invalid_api_key", "Select an Image Studio API key"
	}
	if in.PromptBytes <= 0 || in.PromptBytes > 32000 {
		return plan, "invalid_prompt", "Prompt must contain between 1 and 32000 bytes"
	}
	if in.Count < 1 || in.Count > extensionv1.ImageStudioMaxOutputCount {
		return plan, "invalid_count", "Image count must be between 1 and 4"
	}
	if plan.Model != extensionv1.ImageStudioModelGPTImage2 && plan.Model != extensionv1.ImageStudioModelGeminiProImage {
		return plan, "unsupported_model", "Image Studio model is not supported"
	}
	if in.Mode != "generate" && in.Mode != "edit" {
		return plan, "unsupported_mode", "Image Studio mode is not supported"
	}
	if in.Mode == "edit" && plan.Model != extensionv1.ImageStudioModelGeminiProImage {
		return plan, "unsupported_mode", "This model does not support image editing"
	}
	if in.Mode == "edit" && !in.HasReference {
		return plan, "reference_required", "A reference image is required for editing"
	}
	if in.Mode == "generate" && in.HasReference {
		return plan, "reference_not_allowed", "Reference images are only supported for editing"
	}
	if in.HasMask && (in.Mode != "edit" || plan.Model != extensionv1.ImageStudioModelGeminiProImage) {
		return plan, "mask_not_allowed", "Masks are not supported for this request"
	}
	if plan.Size != "" && plan.Size != "1024x1024" {
		return plan, "unsupported_size", "Image size is not supported"
	}
	if plan.Quality != "" && plan.Quality != "low" {
		return plan, "unsupported_quality", "Image quality is not supported"
	}
	if plan.Size == "" {
		plan.Size = "1024x1024"
	}
	if plan.Quality == "" {
		plan.Quality = "low"
	}
	if in.Mode == "edit" {
		plan.Endpoint = "/v1/images/edits"
	}
	return plan, "", ""
}

func imageStudioModelChoices(capabilities []extensionv1.CindyModelCapability) []extensionv1.CindyModelCapability {
	result := make([]extensionv1.CindyModelCapability, 0, 2)
	for _, model := range []string{extensionv1.ImageStudioModelGPTImage2, extensionv1.ImageStudioModelGeminiProImage} {
		for _, capability := range capabilities {
			if capability.ID != model || capability.Kind != extensionv1.CindyModelKindImage {
				continue
			}
			if capability.Controls != nil {
				capability.Controls = &extensionv1.CindyCapabilityControls{Generation: extensionv1.CloneCindyImageRequestControls(capability.Controls.Generation), Edit: extensionv1.CloneCindyImageRequestControls(capability.Controls.Edit)}
				if capability.Controls.Generation != nil {
					capability.Controls.Generation.MaxOutputCount = extensionv1.ImageStudioMaxOutputCount
				}
				if capability.Controls.Edit != nil {
					capability.Controls.Edit.MaxOutputCount = extensionv1.ImageStudioMaxOutputCount
				}
			}
			result = append(result, capability)
			break
		}
	}
	return result
}
