package extensionv1

type ImageToolsConfig struct {
	StudioEnabled         bool `json:"studio_enabled"`
	ResponsesImageEnabled bool `json:"responses_image_enabled"`
}

const (
	ImageStudioModelGPTImage2      = "gpt-image-2"
	ImageStudioModelGeminiProImage = "gemini-3-pro-image"
	ImageStudioMaxOutputCount      = 4
)

// Image bytes, prompt text and API key material stay in the host. Only bounded
// request facts and provider capabilities are needed to choose an image plan.
type ImageStudioPlanRequest struct {
	APIKeySelected bool   `json:"api_key_selected"`
	Mode           string `json:"mode"`
	Model          string `json:"model"`
	PromptBytes    int    `json:"prompt_bytes"`
	Count          int    `json:"count"`
	Size           string `json:"size"`
	Quality        string `json:"quality"`
	HasReference   bool   `json:"has_reference"`
	HasMask        bool   `json:"has_mask"`
}

type ImageStudioPlan struct {
	Model            string `json:"model"`
	Mode             string `json:"mode"`
	Size             string `json:"size"`
	Quality          string `json:"quality"`
	Endpoint         string `json:"endpoint"`
	OutputPerRequest int    `json:"output_per_request"`
	ResponseFormat   string `json:"response_format"`
}

type ImageStudioCatalogRequest struct {
	Capabilities []CindyModelCapability `json:"capabilities"`
}
