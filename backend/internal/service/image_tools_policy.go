package service

import (
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

// Image Studio request policy formerly served by the image-tools plugin
// process: a pure check over host facts.

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
