package policy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

type Module struct {
	mu     sync.RWMutex
	config extensionv1.ImageToolsConfig
}

func New() *Module { return &Module{} }
func (m *Module) ValidateConfig(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	var cfg extensionv1.ImageToolsConfig
	if json.Unmarshal(raw, &fields) != nil || fields == nil || json.Unmarshal(raw, &cfg) != nil {
		return nil, errors.New("invalid image tools configuration")
	}
	for key, value := range fields {
		if (key != "studio_enabled" && key != "responses_image_enabled") || (string(value) != "true" && string(value) != "false") {
			return nil, errors.New("unknown or invalid image tools setting")
		}
	}
	return json.Marshal(cfg)
}
func (m *Module) ApplyConfig(ctx context.Context, raw json.RawMessage) error {
	normalized, err := m.ValidateConfig(ctx, raw)
	if err != nil {
		return err
	}
	var cfg extensionv1.ImageToolsConfig
	if err := json.Unmarshal(normalized, &cfg); err != nil {
		return err
	}
	m.mu.Lock()
	m.config = cfg
	m.mu.Unlock()
	return nil
}
func (m *Module) Status(context.Context) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return json.Marshal(m.config)
}
func (m *Module) Invoke(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
	if err := ctx.Err(); err != nil {
		return extensionv1.Result{}, err
	}
	if in.Capability == extensionv1.CapabilityAdmin && in.Operation == "image.describe" {
		raw, err := m.Status(ctx)
		return extensionv1.Result{Payload: raw}, err
	}
	if in.Capability != extensionv1.CapabilityRequest {
		return extensionv1.Result{}, errors.New("unsupported image capability")
	}
	m.mu.RLock()
	config := m.config
	m.mu.RUnlock()
	var output any
	switch in.Operation {
	case "image.features":
		output = config
	case "image.native.validate":
		var request extensionv1.ImageNativeRequest
		if json.Unmarshal(in.Payload, &request) != nil {
			return extensionv1.Result{}, errors.New("invalid native image facts")
		}
		if err := validateNativeImage(request); err != nil {
			return extensionv1.Result{Code: "invalid_image_request", Message: err.Error(), HTTPStatus: 400}, nil
		}
		output = true
	case "image.responses.plan":
		var request extensionv1.ImageBridgeRequest
		if json.Unmarshal(in.Payload, &request) != nil {
			return extensionv1.Result{}, errors.New("invalid image bridge request")
		}
		plan, code, message := planResponsesBridge(request, config)
		if code != "" {
			return extensionv1.Result{Code: code, Message: message, HTTPStatus: 400}, nil
		}
		output = plan
	case "image.studio.plan", "image.studio.validate":
		var request extensionv1.ImageStudioPlanRequest
		if json.Unmarshal(in.Payload, &request) != nil {
			return extensionv1.Result{}, errors.New("invalid image plan request")
		}
		plan, code, message := planStudio(request)
		if code != "" {
			return extensionv1.Result{Code: code, Message: message, HTTPStatus: 400}, nil
		}
		if in.Operation == "image.studio.plan" && !config.StudioEnabled {
			return extensionv1.Result{Code: "studio_disabled", Message: "Image Studio is not enabled", HTTPStatus: 404}, nil
		}
		output = plan
	case "image.studio.models":
		var request extensionv1.ImageStudioCatalogRequest
		if json.Unmarshal(in.Payload, &request) != nil {
			return extensionv1.Result{}, errors.New("invalid image capability request")
		}
		output = studioModels(request.Capabilities)
	default:
		return extensionv1.Result{}, errors.New("unsupported image operation")
	}
	raw, err := json.Marshal(output)
	return extensionv1.Result{Payload: raw}, err
}

func planStudio(in extensionv1.ImageStudioPlanRequest) (extensionv1.ImageStudioPlan, string, string) {
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

func studioModels(capabilities []extensionv1.CindyModelCapability) []extensionv1.CindyModelCapability {
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
