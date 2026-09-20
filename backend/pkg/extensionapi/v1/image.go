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

// ImageBridgeValue exposes only bounded scalar control facts. Invalid values
// never carry their original object, text, image, or prompt to the plugin.
type ImageBridgeValue struct {
	Present bool    `json:"present"`
	Valid   bool    `json:"valid"`
	Text    string  `json:"text,omitempty"`
	Number  float64 `json:"number,omitempty"`
}

type ImageBridgeControls struct {
	Size    ImageBridgeValue `json:"size"`
	Quality ImageBridgeValue `json:"quality"`
	Count   ImageBridgeValue `json:"count"`
	Present []string         `json:"present,omitempty"`
}

type ImageBridgeTool struct {
	Index        int                 `json:"index"`
	Model        string              `json:"model"`
	InvalidModel bool                `json:"invalid_model,omitempty"`
	Controls     ImageBridgeControls `json:"controls"`
}

type ImageBridgeRequest struct {
	Stage          string              `json:"stage"`
	Model          string              `json:"model"`
	Controls       ImageBridgeControls `json:"controls"`
	Tools          []ImageBridgeTool   `json:"tools,omitempty"`
	Capabilities   []CindyCapability   `json:"capabilities"`
	Aliases        map[string]string   `json:"aliases,omitempty"`
	CatalogEnabled bool                `json:"catalog_enabled"`
}

// Plans can only change model identifiers and remove the Images-only count.
// The host validates indices and model identities before applying any change.
type ImageBridgeModelPlan struct {
	Index      int    `json:"index"`
	Model      string `json:"model"`
	StripCount bool   `json:"strip_count,omitempty"`
}

type ImageBridgePlan struct {
	Supported  bool                   `json:"supported"`
	Model      string                 `json:"model,omitempty"`
	StripCount bool                   `json:"strip_count,omitempty"`
	Tools      []ImageBridgeModelPlan `json:"tools,omitempty"`
}
