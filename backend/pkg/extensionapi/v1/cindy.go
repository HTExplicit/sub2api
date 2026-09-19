package extensionv1

import "encoding/json"

// CindyCapabilityCatalogVersion is bumped whenever the fixed Cindy data-plane
// catalogue or one of its verified endpoint decisions changes.
const CindyCapabilityCatalogVersion = "2026-09-11.1"

// CindyModelMetadataSourceRevision pins the shipped Cindy registry used for
// display, context-window, output-limit, and reasoning metadata.
const CindyModelMetadataSourceRevision = "makecindy/cindy@2128cd45e08e3419a36ad474d8c01e42a34ea328"

// CindyFreeModelCatalogSourceRevision and SHA256 pin the authenticated model
// inventory returned by the newest eligible production free-trial key.
const (
	CindyFreeModelCatalogSourceRevision = "laxarouter-free-key@2026-09-11"
	CindyFreeModelCatalogSHA256         = "38045af0a5a90c360ba44013d90a1ccbff65c903db7f9b99701c30ce15ca8821"
)

// CindyCompatibilityAliasSourceRevision identifies downstream aliases managed
// by Sub2API. These aliases are not part of Cindy's upstream model registry.
const CindyCompatibilityAliasSourceRevision = "sub2api-cindy-compat@2026-08-17.1"

// CindyDefaultTestModel is the stable public model used by Cindy connectivity
// and capability probes when the caller did not choose a model explicitly.
const CindyDefaultTestModel = "gpt-5.6-luna"

// CindyWebSearchModel is the exact native Messages model verified with the
// web_search_20250305 server tool.
const CindyWebSearchModel = "cindy/web-search"

// CindyAutoReviewModel remains visible only in the management inventory. It
// has no public schema or handler and must fail closed on every routing path.
const CindyAutoReviewModel = "cindy/auto-review"

type CindyModelKind string

const (
	CindyModelKindText    CindyModelKind = "text"
	CindyModelKindImage   CindyModelKind = "image"
	CindyModelKindSpecial CindyModelKind = "special"
)

type CindyCapacitySource string

const (
	CindyCapacityPinnedRegistry CindyCapacitySource = "pinned_registry"
	CindyCapacityApprovedManual CindyCapacitySource = "approved_manual"
	CindyCapacityUnknown        CindyCapacitySource = "unknown"
)

type CindyEndpoint string

const (
	CindyEndpointResponses       CindyEndpoint = "responses"
	CindyEndpointChatCompletions CindyEndpoint = "chat_completions"
	CindyEndpointMessages        CindyEndpoint = "messages"
	// CountTokens is evidence-gated independently from Messages. No current
	// catalog entry advertises it until its own A/B/C canary matrix passes.
	CindyEndpointCountTokens    CindyEndpoint = "messages.count_tokens"
	CindyEndpointImagesGenerate CindyEndpoint = "images.generations"
	CindyEndpointImagesEdit     CindyEndpoint = "images.edits"
	CindyEndpointAlphaSearch    CindyEndpoint = "alpha.search"
	CindyEndpointReview         CindyEndpoint = "cindy.reviews"
)

const (
	CindyClientSurfaceCodex     = "codex"
	CindyClientSurfacePi        = "pi"
	CindyClientSurfaceOpenAI    = "openai_compatible"
	CindyClientSurfaceClaude    = "claude_code"
	CindyClientSurfaceAnthropic = "anthropic_sdk"
	CindyClientSurfaceImage     = "image_studio"
)

// CindyImagePricing contains authoritative per-unit USD prices for Cindy
// image capabilities. Zero means the upstream does not publish or use that
// billing unit; it is not an implicit zero-price model decision.
type CindyImagePricing struct {
	InputCostPerToken            float64
	OutputCostPerToken           float64
	CacheReadInputTokenCost      float64
	InputCostPerImage            float64
	OutputCostPerImage           float64
	OutputCostPerImage1KOr2K     float64
	OutputCostPerImage4K         float64
	InputCostPerImageToken       float64
	OutputCostPerImageToken      float64
	CacheReadInputImageTokenCost float64
}

// CindyTextPricing contains standard, non-batch USD token prices published by
// the exact model's provider. Long-context fields apply to every token in a
// request after the provider-specific threshold comparison is satisfied.
type CindyTextPricing struct {
	InputCostPerToken                          float64
	OutputCostPerToken                         float64
	InputCostPerTokenPriority                  float64
	OutputCostPerTokenPriority                 float64
	CacheReadInputTokenCost                    float64
	CacheReadInputTokenCostPriority            float64
	CacheCreationInputTokenCost                float64
	CacheCreationInputTokenCostPriority        float64
	CacheCreationInputTokenCostPresent         bool
	CacheCreationInputTokenCostAbove1hr        float64
	InputCostPerAudioToken                     float64
	LongContextInputTokenThreshold             int
	LongContextThresholdInclusive              bool
	LongContextInputCostPerToken               float64
	LongContextOutputCostPerToken              float64
	LongContextCacheReadInputTokenCost         float64
	LongContextCacheCreationTokenCost          float64
	LongContextInputCostPerTokenPriority       float64
	LongContextOutputCostPerTokenPriority      float64
	LongContextCacheReadInputTokenCostPriority float64
}

// CindyImageRequestControls describes only request controls verified on one
// Cindy image endpoint. Omitted fields must be hidden and omitted by clients.
type CindyImageRequestControls struct {
	Sizes                  []string `json:"sizes,omitempty"`
	Qualities              []string `json:"qualities,omitempty"`
	MaxOutputCount         int      `json:"max_output_count,omitempty"`
	SupportsReferenceImage bool     `json:"supports_reference_image,omitempty"`
	SupportsMask           bool     `json:"supports_mask,omitempty"`
}

// CindyCapabilityControls keeps generation and edit controls independent so a
// parameter verified on one endpoint is never inferred for the other.
type CindyCapabilityControls struct {
	Generation *CindyImageRequestControls `json:"generation,omitempty"`
	Edit       *CindyImageRequestControls `json:"edit,omitempty"`
}

// CindyCapability is the internal, versioned source of truth for Cindy model
// names. PublicID, LiveUpstreamID and RegistryID are intentionally independent:
// neither provider-prefix stripping nor registry presence is treated as proof
// that the live Cindy data plane accepts a model.
type CindyCapability struct {
	PublicID                   string
	LiveUpstreamID             string
	RegistryID                 string
	DisplayName                string
	Description                string
	Kind                       CindyModelKind
	InputModalities            []string
	OutputModalities           []string
	VerifiedEndpoints          []CindyEndpoint
	ClientSurfaces             []string
	AgentWireProtocols         map[string]string
	MaxInputTokens             int
	CodexContextWindow         int
	MaxOutputTokens            int
	ReasoningEfforts           []string
	CodexReasoningEffortLevels []string
	DefaultReasoningEffort     string
	MetadataSourceRevision     string
	PricingSource              string
	CostDiscount               float64
	TextPricing                *CindyTextPricing
	ImagePricing               *CindyImagePricing
	Controls                   *CindyCapabilityControls
	ExplicitZeroPrice          bool
	PublicModel                bool
}

// CindyCatalogModel is the read-only management projection of one observed
// Cindy model or one exact compatibility alias. ContextWindow is the Codex
// effective value; BaseContextWindow remains available for non-Codex clients.
// SourceRevision covers model metadata only; endpoint evidence is expressed
// independently by Verified and Endpoints.
type CindyCatalogModel struct {
	ID                     string              `json:"id"`
	LiveUpstreamID         string              `json:"live_upstream_id"`
	DisplayName            string              `json:"display_name"`
	Description            string              `json:"description,omitempty"`
	BaseContextWindow      int                 `json:"base_context_window,omitempty"`
	CodexContextWindow     int                 `json:"codex_context_window,omitempty"`
	ContextWindow          int                 `json:"context_window,omitempty"`
	MaxOutputTokens        int                 `json:"max_output_tokens,omitempty"`
	ReasoningEfforts       []string            `json:"reasoning_efforts,omitempty"`
	DefaultReasoningEffort string              `json:"default_reasoning_effort,omitempty"`
	SourceRevision         string              `json:"source_revision"`
	CapacitySource         CindyCapacitySource `json:"capacity_source"`
	Verified               bool                `json:"verified"`
	Endpoints              []CindyEndpoint     `json:"endpoints"`
	AliasTarget            string              `json:"alias_target,omitempty"`
	Managed                bool                `json:"managed"`
	PublicModel            bool                `json:"public_model"`
}

// CindyModelCapability is the client-safe projection of one verified Cindy
// capability. It intentionally excludes live upstream IDs, registry IDs, and
// account identity.
type CindyModelCapability struct {
	Object             string                   `json:"object"`
	ID                 string                   `json:"id"`
	Kind               CindyModelKind           `json:"kind"`
	InputModalities    []string                 `json:"input_modalities"`
	OutputModalities   []string                 `json:"output_modalities"`
	Endpoints          []CindyEndpoint          `json:"endpoints"`
	ClientSurfaces     []string                 `json:"client_surfaces"`
	AgentWireProtocols map[string]string        `json:"agent_wire_protocols,omitempty"`
	MaxInputTokens     int                      `json:"max_input_tokens,omitempty"`
	MaxOutputTokens    int                      `json:"max_output_tokens,omitempty"`
	PricingSource      string                   `json:"pricing_source,omitempty"`
	ExplicitZeroPrice  bool                     `json:"explicit_zero_price,omitempty"`
	Controls           *CindyCapabilityControls `json:"controls,omitempty"`
}

// EffectiveCodexContextWindow returns the Cindy per-agent Codex override when
// present and otherwise falls back to the model's base context window.
func (c CindyCapability) EffectiveCodexContextWindow() int {
	if c.CodexContextWindow > 0 {
		return c.CodexContextWindow
	}
	return c.MaxInputTokens
}

// CodexReasoningEfforts returns the Cindy per-agent Codex effort set when
// present and otherwise returns the model's base effort set.
func (c CindyCapability) CodexReasoningEfforts() []string {
	if len(c.CodexReasoningEffortLevels) > 0 {
		return append([]string(nil), c.CodexReasoningEffortLevels...)
	}
	return append([]string(nil), c.ReasoningEfforts...)
}

type CindyProviderConfig struct {
	BalanceDetection bool `json:"balance_detection"`
	CatalogEnabled   bool `json:"catalog_enabled"`
	SearchEnabled    bool `json:"search_enabled"`
}
type CindyCatalogQuery struct {
	Images ImageToolsConfig  `json:"images"`
	Method string            `json:"method"`
	Args   []json.RawMessage `json:"args"`
}
type CindyPricingSnapshot struct {
	Config  CindyProviderConfig        `json:"config"`
	Results map[string]json.RawMessage `json:"results"`
}
