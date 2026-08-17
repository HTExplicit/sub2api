package service

import (
	"sort"
	"strings"
)

// CindyCapabilityCatalogVersion is bumped whenever the fixed Cindy data-plane
// catalogue or one of its verified endpoint decisions changes.
const CindyCapabilityCatalogVersion = "2026-08-17.1"

// CindyModelMetadataSourceRevision pins the shipped Cindy registry used for
// display, context-window, output-limit, and reasoning metadata.
const CindyModelMetadataSourceRevision = "makecindy/cindy@v0.1.52+61dc9e660b744b9ca3284d5313df11634fa4e2fe"

// CindyGatewayModelMetadataSourceRevision identifies values confirmed by the
// Cindy gateway model inventory independently from the pinned source registry.
const CindyGatewayModelMetadataSourceRevision = "cindy-gateway-v1-models@2026-08-17"

// CindyCatalogProjectionSourceRevision identifies model metadata curated by
// Sub2API when Cindy's registry does not contain the observed gateway model.
const CindyCatalogProjectionSourceRevision = "sub2api-cindy-catalog@2026-08-17.1"

// CindyCompatibilityAliasSourceRevision identifies downstream aliases managed
// by Sub2API. These aliases are not part of Cindy's upstream model registry.
const CindyCompatibilityAliasSourceRevision = "sub2api-cindy-compat@2026-08-17.1"

const cindyCompositeModelMetadataSourceRevision = CindyModelMetadataSourceRevision + ";" + CindyGatewayModelMetadataSourceRevision

// cindyObservedModelMetadataSourceRevision identifies candidates that exist in
// the fixed live catalogue but not in the pinned Cindy registry.
const cindyObservedModelMetadataSourceRevision = CindyGatewayModelMetadataSourceRevision

// CindyDefaultTestModel is the stable public model used by Cindy connectivity
// and capability probes when the caller did not choose a model explicitly.
const CindyDefaultTestModel = "gpt-5.6-luna"

// CindyWebSearchModel is the exact native Messages model verified with the
// web_search_20250305 server tool.
const CindyWebSearchModel = "cindy/web-search"

type CindyModelKind string

const (
	CindyModelKindText    CindyModelKind = "text"
	CindyModelKindImage   CindyModelKind = "image"
	CindyModelKindSpecial CindyModelKind = "special"
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
	InputCostPerToken                   float64
	OutputCostPerToken                  float64
	CacheReadInputTokenCost             float64
	CacheCreationInputTokenCost         float64
	CacheCreationInputTokenCostAbove1hr float64
	LongContextInputTokenThreshold      int
	LongContextThresholdInclusive       bool
	LongContextInputCostPerToken        float64
	LongContextOutputCostPerToken       float64
	LongContextCacheReadInputTokenCost  float64
	LongContextCacheCreationTokenCost   float64
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
	MaxInputTokens             int
	CodexContextWindow         int
	MaxOutputTokens            int
	ReasoningEfforts           []string
	CodexReasoningEffortLevels []string
	DefaultReasoningEffort     string
	MetadataSourceRevision     string
	PricingSource              string
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
	ID                     string          `json:"id"`
	LiveUpstreamID         string          `json:"live_upstream_id"`
	DisplayName            string          `json:"display_name"`
	Description            string          `json:"description,omitempty"`
	BaseContextWindow      int             `json:"base_context_window,omitempty"`
	CodexContextWindow     int             `json:"codex_context_window,omitempty"`
	ContextWindow          int             `json:"context_window,omitempty"`
	MaxOutputTokens        int             `json:"max_output_tokens,omitempty"`
	ReasoningEfforts       []string        `json:"reasoning_efforts,omitempty"`
	DefaultReasoningEffort string          `json:"default_reasoning_effort,omitempty"`
	SourceRevision         string          `json:"source_revision"`
	Verified               bool            `json:"verified"`
	Endpoints              []CindyEndpoint `json:"endpoints"`
	AliasTarget            string          `json:"alias_target,omitempty"`
	Managed                bool            `json:"managed"`
	PublicModel            bool            `json:"public_model"`
}

// CindyModelCapability is the client-safe projection of one verified Cindy
// capability. It intentionally excludes live upstream IDs, registry IDs, and
// account identity.
type CindyModelCapability struct {
	Object            string                   `json:"object"`
	ID                string                   `json:"id"`
	Kind              CindyModelKind           `json:"kind"`
	InputModalities   []string                 `json:"input_modalities"`
	OutputModalities  []string                 `json:"output_modalities"`
	Endpoints         []CindyEndpoint          `json:"endpoints"`
	ClientSurfaces    []string                 `json:"client_surfaces"`
	MaxInputTokens    int                      `json:"max_input_tokens,omitempty"`
	MaxOutputTokens   int                      `json:"max_output_tokens,omitempty"`
	PricingSource     string                   `json:"pricing_source,omitempty"`
	ExplicitZeroPrice bool                     `json:"explicit_zero_price,omitempty"`
	Controls          *CindyCapabilityControls `json:"controls,omitempty"`
}

// cindyCapabilityCatalog contains exactly the 23 IDs observed consistently on
// the 2026-08-17 Cindy data plane. Endpoint verification is deliberately
// conservative: unprobed combinations remain available for management
// inspection and future explicit verification, but are not schedulable.
var cindyCapabilityCatalog = []CindyCapability{
	{PublicID: "claude-opus-4-8", LiveUpstreamID: "anthropic/claude-opus-4-8", RegistryID: "anthropic/claude-opus-4-8", DisplayName: "Opus 4.8", Description: "Most capable, supports xhigh effort", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, MaxInputTokens: 1000000, CodexContextWindow: 1000000, MaxOutputTokens: 128000, ReasoningEfforts: []string{"low", "medium", "high", "xhigh", "max"}, DefaultReasoningEffort: "high", MetadataSourceRevision: CindyModelMetadataSourceRevision, PricingSource: "https://platform.claude.com/docs/en/about-claude/pricing", TextPricing: &CindyTextPricing{InputCostPerToken: 5e-6, OutputCostPerToken: 25e-6, CacheReadInputTokenCost: 0.5e-6, CacheCreationInputTokenCost: 6.25e-6, CacheCreationInputTokenCostAbove1hr: 10e-6}, PublicModel: true},
	{PublicID: "claude-opus-5", LiveUpstreamID: "anthropic/claude-opus-5", RegistryID: "anthropic/claude-opus-5", DisplayName: "Opus 5", Description: "Latest Opus for ambitious work", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, MaxInputTokens: 1000000, CodexContextWindow: 1000000, MaxOutputTokens: 128000, ReasoningEfforts: []string{"low", "medium", "high", "xhigh", "max"}, DefaultReasoningEffort: "high", MetadataSourceRevision: CindyModelMetadataSourceRevision, PricingSource: "https://platform.claude.com/docs/en/about-claude/pricing", TextPricing: &CindyTextPricing{InputCostPerToken: 5e-6, OutputCostPerToken: 25e-6, CacheReadInputTokenCost: 0.5e-6, CacheCreationInputTokenCost: 6.25e-6, CacheCreationInputTokenCostAbove1hr: 10e-6}, PublicModel: true},
	{PublicID: "claude-sonnet-5", LiveUpstreamID: "anthropic/claude-sonnet-5", RegistryID: "anthropic/claude-sonnet-5", DisplayName: "Sonnet 5", Description: "Latest Sonnet - fast and highly capable", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, MaxInputTokens: 1000000, CodexContextWindow: 1000000, MaxOutputTokens: 128000, ReasoningEfforts: []string{"low", "medium", "high", "xhigh", "max"}, DefaultReasoningEffort: "high", MetadataSourceRevision: CindyModelMetadataSourceRevision, PricingSource: "https://platform.claude.com/docs/en/about-claude/pricing", TextPricing: &CindyTextPricing{InputCostPerToken: 2e-6, OutputCostPerToken: 10e-6, CacheReadInputTokenCost: 0.2e-6, CacheCreationInputTokenCost: 2.5e-6, CacheCreationInputTokenCostAbove1hr: 4e-6}, PublicModel: true},
	{PublicID: "seed-2.1-pro", LiveUpstreamID: "bytedance-seed/seed-2.1-pro", RegistryID: "bytedance-seed/seed-2.1-pro", DisplayName: "Seed 2.1 Pro", Kind: CindyModelKindText, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, MaxInputTokens: 256000, CodexContextWindow: 256000, ReasoningEfforts: []string{"minimal", "low", "medium", "high"}, DefaultReasoningEffort: "minimal", MetadataSourceRevision: CindyModelMetadataSourceRevision, PricingSource: "explicit-zero:no-exact-public-usd-price", ExplicitZeroPrice: true, PublicModel: false},
	{PublicID: "cindy/auto-review", LiveUpstreamID: "cindy/auto-review", RegistryID: "", DisplayName: "Cindy Auto Review", Kind: CindyModelKindSpecial, InputModalities: []string{"text"}, OutputModalities: []string{"json"}, MetadataSourceRevision: CindyGatewayModelMetadataSourceRevision, PricingSource: "explicit-zero:no-public-price", ExplicitZeroPrice: true, PublicModel: false},
	{PublicID: CindyWebSearchModel, LiveUpstreamID: CindyWebSearchModel, RegistryID: "", DisplayName: "Cindy Web Search", Kind: CindyModelKindSpecial, InputModalities: []string{"text"}, OutputModalities: []string{"text", "citations"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointAlphaSearch}, ClientSurfaces: []string{CindyClientSurfaceCodex, CindyClientSurfaceOpenAI}, MetadataSourceRevision: CindyGatewayModelMetadataSourceRevision, PricingSource: "explicit-zero:no-public-price", ExplicitZeroPrice: true, PublicModel: false},
	{PublicID: "deepseek-v4-flash", LiveUpstreamID: "deepseek/deepseek-v4-flash", RegistryID: "deepseek/deepseek-v4-flash", DisplayName: "DeepSeek V4 Flash", Description: "DeepSeek V4 Flash efficiency-optimized MoE; only high/max effective", Kind: CindyModelKindText, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, MaxInputTokens: 1000000, CodexContextWindow: 1000000, MaxOutputTokens: 384000, ReasoningEfforts: []string{"high", "max"}, DefaultReasoningEffort: "high", MetadataSourceRevision: cindyCompositeModelMetadataSourceRevision, PricingSource: "explicit-zero:no-stable-exact-price-across-published-rate-boundary", ExplicitZeroPrice: true, PublicModel: false},
	{PublicID: "deepseek-v4-pro", LiveUpstreamID: "deepseek/deepseek-v4-pro", RegistryID: "deepseek/deepseek-v4-pro", DisplayName: "DeepSeek V4 Pro", Description: "DeepSeek V4 Pro reasoning model; only high/max effective", Kind: CindyModelKindText, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, MaxInputTokens: 1000000, CodexContextWindow: 1000000, MaxOutputTokens: 384000, ReasoningEfforts: []string{"high", "max"}, DefaultReasoningEffort: "high", MetadataSourceRevision: cindyCompositeModelMetadataSourceRevision, PricingSource: "explicit-zero:no-stable-exact-price-across-published-rate-boundary", ExplicitZeroPrice: true, PublicModel: false},
	{PublicID: "gemini-3-pro-image", LiveUpstreamID: "google/gemini-3-pro-image", RegistryID: "", DisplayName: "Gemini 3 Pro Image", Kind: CindyModelKindImage, InputModalities: []string{"text", "image"}, OutputModalities: []string{"image"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointImagesGenerate, CindyEndpointImagesEdit}, ClientSurfaces: []string{CindyClientSurfaceImage}, MetadataSourceRevision: CindyGatewayModelMetadataSourceRevision, PricingSource: "https://ai.google.dev/gemini-api/docs/pricing#gemini-3-pro-image", ImagePricing: &CindyImagePricing{InputCostPerToken: 2e-6, OutputCostPerToken: 12e-6, InputCostPerImage: 0.0011, OutputCostPerImage1KOr2K: 0.134, OutputCostPerImage4K: 0.24, OutputCostPerImageToken: 120e-6}, Controls: &CindyCapabilityControls{Generation: &CindyImageRequestControls{Sizes: []string{"1024x1024"}, Qualities: []string{"low"}, MaxOutputCount: 1}, Edit: &CindyImageRequestControls{Sizes: []string{"1024x1024"}, Qualities: []string{"low"}, MaxOutputCount: 1, SupportsReferenceImage: true, SupportsMask: true}}, PublicModel: true},
	{PublicID: "gemini-3.5-flash", LiveUpstreamID: "google/gemini-3.5-flash", RegistryID: "google/gemini-3.5-flash", DisplayName: "Gemini 3.5 Flash", Description: "Google Gemini 3.5 Flash with stronger reasoning", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, MaxInputTokens: 1000000, CodexContextWindow: 1000000, ReasoningEfforts: []string{"low", "medium", "high"}, DefaultReasoningEffort: "high", MetadataSourceRevision: CindyModelMetadataSourceRevision, PricingSource: "https://ai.google.dev/gemini-api/docs/pricing#gemini-3.5-flash", TextPricing: &CindyTextPricing{InputCostPerToken: 1.5e-6, OutputCostPerToken: 9e-6, CacheReadInputTokenCost: 0.15e-6}, PublicModel: false},
	{PublicID: "gemini-3.6-flash", LiveUpstreamID: "google/gemini-3.6-flash", RegistryID: "", DisplayName: "Gemini 3.6 Flash", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, MetadataSourceRevision: CindyGatewayModelMetadataSourceRevision, PricingSource: "https://ai.google.dev/gemini-api/docs/pricing#gemini-3.6-flash", TextPricing: &CindyTextPricing{InputCostPerToken: 0.75e-6, OutputCostPerToken: 3.75e-6, CacheReadInputTokenCost: 0.075e-6}, PublicModel: true},
	{PublicID: "gemini-3.7-flash", LiveUpstreamID: "google/gemini-3.7-flash", RegistryID: "", DisplayName: "Gemini 3.7 Flash", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, MetadataSourceRevision: CindyGatewayModelMetadataSourceRevision, PricingSource: "https://ai.google.dev/gemini-api/docs/pricing#gemini-3.7-flash", TextPricing: &CindyTextPricing{InputCostPerToken: 0.75e-6, OutputCostPerToken: 3.75e-6, CacheReadInputTokenCost: 0.075e-6}, PublicModel: false},
	{PublicID: "kimi-k3", LiveUpstreamID: "moonshotai/kimi-k3", RegistryID: "moonshotai/kimi-k3", DisplayName: "Kimi K3", Kind: CindyModelKindText, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, MaxInputTokens: 1048576, CodexContextWindow: 1048576, MaxOutputTokens: 131072, ReasoningEfforts: []string{"low", "high", "max"}, DefaultReasoningEffort: "max", MetadataSourceRevision: cindyCompositeModelMetadataSourceRevision, PricingSource: "explicit-zero:no-exact-public-usd-price", ExplicitZeroPrice: true, PublicModel: false},
	{PublicID: "gpt-5.6-luna", LiveUpstreamID: "openai/gpt-5.6-luna", RegistryID: "openai/gpt-5.6-luna", DisplayName: "GPT-5.6-Luna", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointResponses, CindyEndpointChatCompletions, CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceCodex, CindyClientSurfacePi, CindyClientSurfaceOpenAI, CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, MaxInputTokens: 1050000, CodexContextWindow: 1050000, MaxOutputTokens: 128000, ReasoningEfforts: []string{"low", "medium", "high", "xhigh", "max"}, DefaultReasoningEffort: "high", MetadataSourceRevision: CindyModelMetadataSourceRevision, PricingSource: "https://developers.openai.com/api/docs/pricing", TextPricing: &CindyTextPricing{InputCostPerToken: 0.2e-6, OutputCostPerToken: 1.2e-6, CacheReadInputTokenCost: 0.02e-6, CacheCreationInputTokenCost: 0.25e-6, LongContextInputTokenThreshold: 272000, LongContextInputCostPerToken: 0.4e-6, LongContextOutputCostPerToken: 1.8e-6, LongContextCacheReadInputTokenCost: 0.04e-6, LongContextCacheCreationTokenCost: 0.5e-6}, PublicModel: true},
	{PublicID: "gpt-5.6-sol", LiveUpstreamID: "openai/gpt-5.6-sol", RegistryID: "openai/gpt-5.6-sol", DisplayName: "GPT-5.6-Sol", Description: "GPT-5.6-Sol for coding tasks", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointResponses, CindyEndpointChatCompletions, CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceCodex, CindyClientSurfacePi, CindyClientSurfaceOpenAI, CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, MaxInputTokens: 1050000, CodexContextWindow: 372000, MaxOutputTokens: 128000, ReasoningEfforts: []string{"low", "medium", "high", "xhigh", "max"}, CodexReasoningEffortLevels: []string{"low", "medium", "high", "xhigh", "max", "ultra"}, DefaultReasoningEffort: "high", MetadataSourceRevision: CindyModelMetadataSourceRevision, PricingSource: "https://developers.openai.com/api/docs/pricing", TextPricing: &CindyTextPricing{InputCostPerToken: 5e-6, OutputCostPerToken: 30e-6, CacheReadInputTokenCost: 0.5e-6, CacheCreationInputTokenCost: 6.25e-6, LongContextInputTokenThreshold: 272000, LongContextInputCostPerToken: 10e-6, LongContextOutputCostPerToken: 45e-6, LongContextCacheReadInputTokenCost: 1e-6, LongContextCacheCreationTokenCost: 12.5e-6}, PublicModel: true},
	{PublicID: "gpt-5.6-terra", LiveUpstreamID: "openai/gpt-5.6-terra", RegistryID: "openai/gpt-5.6-terra", DisplayName: "GPT-5.6-Terra", Description: "GPT-5.6-Terra for coding tasks", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointResponses, CindyEndpointChatCompletions, CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceCodex, CindyClientSurfacePi, CindyClientSurfaceOpenAI, CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, MaxInputTokens: 1050000, CodexContextWindow: 372000, MaxOutputTokens: 128000, ReasoningEfforts: []string{"low", "medium", "high", "xhigh", "max"}, CodexReasoningEffortLevels: []string{"low", "medium", "high", "xhigh", "max", "ultra"}, DefaultReasoningEffort: "high", MetadataSourceRevision: CindyModelMetadataSourceRevision, PricingSource: "https://developers.openai.com/api/docs/pricing", TextPricing: &CindyTextPricing{InputCostPerToken: 2e-6, OutputCostPerToken: 12e-6, CacheReadInputTokenCost: 0.2e-6, CacheCreationInputTokenCost: 2.5e-6, LongContextInputTokenThreshold: 272000, LongContextInputCostPerToken: 4e-6, LongContextOutputCostPerToken: 18e-6, LongContextCacheReadInputTokenCost: 0.4e-6, LongContextCacheCreationTokenCost: 5e-6}, PublicModel: true},
	{PublicID: "gpt-image-2", LiveUpstreamID: "openai/gpt-image-2", RegistryID: "", DisplayName: "GPT Image 2", Kind: CindyModelKindImage, InputModalities: []string{"text", "image"}, OutputModalities: []string{"image"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointImagesGenerate}, ClientSurfaces: []string{CindyClientSurfaceImage}, MetadataSourceRevision: CindyGatewayModelMetadataSourceRevision, PricingSource: "https://developers.openai.com/api/docs/pricing", ImagePricing: &CindyImagePricing{InputCostPerToken: 5e-6, OutputCostPerToken: 10e-6, CacheReadInputTokenCost: 1.25e-6, InputCostPerImageToken: 8e-6, OutputCostPerImageToken: 30e-6, CacheReadInputImageTokenCost: 2e-6}, Controls: &CindyCapabilityControls{Generation: &CindyImageRequestControls{Sizes: []string{"1024x1024"}, Qualities: []string{"low"}, MaxOutputCount: 1}}, PublicModel: true},
	{PublicID: "qwen3.8-max", LiveUpstreamID: "qwen/qwen3.8-max", RegistryID: "", Kind: CindyModelKindText, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, MetadataSourceRevision: CindyGatewayModelMetadataSourceRevision, PricingSource: "explicit-zero:no-exact-public-usd-price", ExplicitZeroPrice: true, PublicModel: false},
	{PublicID: "hy3", LiveUpstreamID: "tencent/hy3", RegistryID: "", DisplayName: "Hy3", Kind: CindyModelKindText, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, MaxInputTokens: 262144, CodexContextWindow: 262144, MaxOutputTokens: 128000, MetadataSourceRevision: CindyGatewayModelMetadataSourceRevision, PricingSource: "explicit-zero:no-exact-public-usd-price", ExplicitZeroPrice: true, PublicModel: true},
	{PublicID: "grok-4.5", LiveUpstreamID: "x-ai/grok-4.5", RegistryID: "xai/grok-4.5", DisplayName: "Grok 4.5", Description: "xAI Grok 4.5 flagship coding and agent model with a 500k context window", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointResponses, CindyEndpointChatCompletions, CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceCodex, CindyClientSurfacePi, CindyClientSurfaceOpenAI, CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, MaxInputTokens: 500000, CodexContextWindow: 500000, ReasoningEfforts: []string{"low", "medium", "high"}, DefaultReasoningEffort: "high", MetadataSourceRevision: CindyModelMetadataSourceRevision, PricingSource: "https://docs.x.ai/developers/pricing", TextPricing: &CindyTextPricing{InputCostPerToken: 2e-6, OutputCostPerToken: 6e-6, CacheReadInputTokenCost: 0.3e-6, LongContextInputTokenThreshold: 200000, LongContextThresholdInclusive: true, LongContextInputCostPerToken: 4e-6, LongContextOutputCostPerToken: 12e-6, LongContextCacheReadInputTokenCost: 0.6e-6}, PublicModel: true},
	{PublicID: "grok-4.6", LiveUpstreamID: "x-ai/grok-4.6", RegistryID: "", DisplayName: "Grok 4.6", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, MetadataSourceRevision: CindyGatewayModelMetadataSourceRevision, PricingSource: "https://docs.x.ai/developers/pricing", TextPricing: &CindyTextPricing{InputCostPerToken: 2e-6, OutputCostPerToken: 6e-6, CacheReadInputTokenCost: 0.5e-6, LongContextInputTokenThreshold: 200000, LongContextThresholdInclusive: true, LongContextInputCostPerToken: 4e-6, LongContextOutputCostPerToken: 12e-6, LongContextCacheReadInputTokenCost: 1e-6}, PublicModel: true},
	{PublicID: "glm-5.2", LiveUpstreamID: "z-ai/glm-5.2", RegistryID: "z-ai/glm-5.2", DisplayName: "GLM-5.2", Description: "Zhipu GLM-5.2 agentic coding model; 1M context", Kind: CindyModelKindText, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointResponses, CindyEndpointChatCompletions, CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceCodex, CindyClientSurfacePi, CindyClientSurfaceOpenAI, CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, MaxInputTokens: 1000000, CodexContextWindow: 1000000, MaxOutputTokens: 131072, ReasoningEfforts: []string{"minimal", "high", "max"}, DefaultReasoningEffort: "max", MetadataSourceRevision: cindyCompositeModelMetadataSourceRevision, PricingSource: "https://docs.z.ai/guides/overview/pricing", TextPricing: &CindyTextPricing{InputCostPerToken: 1.4e-6, OutputCostPerToken: 4.4e-6, CacheReadInputTokenCost: 0.26e-6}, PublicModel: true},
	{PublicID: "glm-5.3", LiveUpstreamID: "z-ai/glm-5.3", RegistryID: "", DisplayName: "GLM-5.3", Kind: CindyModelKindText, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, MetadataSourceRevision: CindyGatewayModelMetadataSourceRevision, PricingSource: "explicit-zero:no-exact-public-usd-price", ExplicitZeroPrice: true, PublicModel: false},
}

var cindyCompatibilityAliases = map[string]string{
	"gpt-5.4":      "gpt-5.6-sol",
	"gpt-5.4-mini": "gpt-5.6-luna",
}

var cindyHiddenAliases = map[string]string{
	"claude-opus-4":              "claude-opus-5",
	"claude-opus-4-20250514":     "claude-opus-5",
	"claude-opus-4-1-20250805":   "claude-opus-5",
	"claude-opus-4-5-20251101":   "claude-opus-5",
	"claude-opus-4-6":            "claude-opus-5",
	"claude-opus-4-7":            "claude-opus-5",
	"claude-sonnet-4":            "claude-sonnet-5",
	"claude-sonnet-4-20250514":   "claude-sonnet-5",
	"claude-sonnet-4-5-20250929": "claude-sonnet-5",
	"claude-sonnet-4-6":          "claude-sonnet-5",
	"claude-3-5-haiku-20241022":  "claude-sonnet-5",
	"claude-haiku-4-5-20251001":  "claude-sonnet-5",
	"claude-haiku-4-6":           "claude-sonnet-5",
	"claude-haiku-5":             "claude-sonnet-5",
}

var (
	cindyCapabilityByPublicID   map[string]*CindyCapability
	cindyCapabilityByUpstreamID map[string]*CindyCapability
)

func init() {
	cindyCapabilityByPublicID = make(map[string]*CindyCapability, len(cindyCapabilityCatalog))
	cindyCapabilityByUpstreamID = make(map[string]*CindyCapability, len(cindyCapabilityCatalog))
	for i := range cindyCapabilityCatalog {
		capability := &cindyCapabilityCatalog[i]
		cindyCapabilityByPublicID[capability.PublicID] = capability
		cindyCapabilityByUpstreamID[capability.LiveUpstreamID] = capability
	}
}

// CindyCapabilities returns a defensive copy of all 23 fixed candidates.
func CindyCapabilities() []CindyCapability {
	result := make([]CindyCapability, len(cindyCapabilityCatalog))
	for i := range cindyCapabilityCatalog {
		result[i] = cloneCindyCapability(cindyCapabilityCatalog[i])
	}
	return result
}

func cloneCindyCapability(in CindyCapability) CindyCapability {
	out := in
	out.InputModalities = append([]string(nil), in.InputModalities...)
	out.OutputModalities = append([]string(nil), in.OutputModalities...)
	out.VerifiedEndpoints = append([]CindyEndpoint(nil), in.VerifiedEndpoints...)
	out.ClientSurfaces = append([]string(nil), in.ClientSurfaces...)
	out.ReasoningEfforts = append([]string(nil), in.ReasoningEfforts...)
	out.CodexReasoningEffortLevels = append([]string(nil), in.CodexReasoningEffortLevels...)
	if in.TextPricing != nil {
		pricing := *in.TextPricing
		out.TextPricing = &pricing
	}
	if in.ImagePricing != nil {
		pricing := *in.ImagePricing
		out.ImagePricing = &pricing
	}
	if in.Controls != nil {
		controls := *in.Controls
		controls.Generation = cloneCindyImageRequestControls(in.Controls.Generation)
		controls.Edit = cloneCindyImageRequestControls(in.Controls.Edit)
		out.Controls = &controls
	}
	return out
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

// CindyCatalogModels returns all 23 observed candidates for management views.
// It is intentionally independent of rollout flags and PublicModel so an
// administrator can inspect the complete fixed inventory without advertising
// unverified candidates on client-facing endpoints.
func CindyCatalogModels() []CindyCatalogModel {
	models := make([]CindyCatalogModel, 0, len(cindyCapabilityCatalog))
	for _, capability := range cindyCapabilityCatalog {
		models = append(models, cindyCatalogModelFromCapability(capability))
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}

// CindyManagedModelMappings returns only the exact downstream compatibility
// aliases. Catalog identity mappings are available from CindyCatalogModels.
func CindyManagedModelMappings() []CindyCatalogModel {
	aliases := make([]string, 0, len(cindyCompatibilityAliases))
	for alias := range cindyCompatibilityAliases {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)

	mappings := make([]CindyCatalogModel, 0, len(aliases))
	for _, alias := range aliases {
		targetID := cindyCompatibilityAliases[alias]
		capability := cindyCapabilityByPublicID[targetID]
		if capability == nil {
			continue
		}
		model := cindyCatalogModelFromCapability(*capability)
		model.ID = alias
		model.AliasTarget = targetID
		model.SourceRevision = CindyCompatibilityAliasSourceRevision
		mappings = append(mappings, model)
	}
	return mappings
}

func cindyCatalogModelFromCapability(capability CindyCapability) CindyCatalogModel {
	displayName := strings.TrimSpace(capability.DisplayName)
	if displayName == "" {
		displayName = capability.PublicID
	}
	sourceRevision := strings.TrimSpace(capability.MetadataSourceRevision)
	if sourceRevision == "" {
		if capability.RegistryID != "" {
			sourceRevision = CindyModelMetadataSourceRevision
		} else {
			sourceRevision = cindyObservedModelMetadataSourceRevision
		}
	}
	if capability.RegistryID == "" {
		sourceRevision += ";" + CindyCatalogProjectionSourceRevision
	}
	codexContextWindow := capability.EffectiveCodexContextWindow()
	return CindyCatalogModel{
		ID:                     capability.PublicID,
		LiveUpstreamID:         capability.LiveUpstreamID,
		DisplayName:            displayName,
		Description:            capability.Description,
		BaseContextWindow:      capability.MaxInputTokens,
		CodexContextWindow:     codexContextWindow,
		ContextWindow:          codexContextWindow,
		MaxOutputTokens:        capability.MaxOutputTokens,
		ReasoningEfforts:       capability.CodexReasoningEfforts(),
		DefaultReasoningEffort: capability.DefaultReasoningEffort,
		SourceRevision:         sourceRevision,
		Verified:               len(capability.VerifiedEndpoints) > 0,
		Endpoints:              append([]CindyEndpoint(nil), capability.VerifiedEndpoints...),
		Managed:                true,
		PublicModel:            capability.PublicModel,
	}
}

func cloneCindyImageRequestControls(in *CindyImageRequestControls) *CindyImageRequestControls {
	if in == nil {
		return nil
	}
	out := *in
	out.Sizes = append([]string(nil), in.Sizes...)
	out.Qualities = append([]string(nil), in.Qualities...)
	return &out
}

// ResolveCindyCapability accepts an explicit public ID, exact live upstream ID,
// or one of the deliberately enumerated compatibility aliases. No dynamic
// provider-prefix stripping or family wildcard matching is performed.
func ResolveCindyCapability(model string) (CindyCapability, bool) {
	if !CindyCapabilityCatalogFeatureEnabled() {
		return CindyCapability{}, false
	}
	capability, ok := resolveKnownCindyCapability(model)
	if !ok || (capability.Kind == CindyModelKindImage && !CindyImageStudioFeatureEnabled()) {
		return CindyCapability{}, false
	}
	return capability, true
}

// resolveKnownCindyCapability recognizes every fixed catalog ID independently
// of rollout flags. This lets fail-closed projections drop a known disabled or
// unverified Cindy ID instead of mistaking it for an unrelated provider model.
func resolveKnownCindyCapability(model string) (CindyCapability, bool) {
	model = strings.TrimSpace(model)
	if capability := cindyCapabilityByPublicID[model]; capability != nil {
		return cloneCindyCapability(*capability), true
	}
	if capability := cindyCapabilityByUpstreamID[model]; capability != nil {
		return cloneCindyCapability(*capability), true
	}
	if publicID, ok := cindyCompatibilityAliases[model]; ok {
		if capability := cindyCapabilityByPublicID[publicID]; capability != nil {
			return cloneCindyCapability(*capability), true
		}
	}
	if publicID, ok := cindyHiddenAliases[model]; ok {
		if capability := cindyCapabilityByPublicID[publicID]; capability != nil {
			return cloneCindyCapability(*capability), true
		}
	}
	return CindyCapability{}, false
}

// CindyCompatibilityMappedUpstreamModel resolves only the two exact OpenAI
// compatibility aliases that must remain callable while the broader Cindy
// capability catalog is rolled back. The caller is responsible for enforcing
// exact Cindy account identity before applying the result.
func CindyCompatibilityMappedUpstreamModel(model string) (string, bool) {
	publicID, ok := cindyCompatibilityAliases[model]
	if !ok {
		return "", false
	}
	capability := cindyCapabilityByPublicID[publicID]
	if capability == nil {
		return "", false
	}
	return capability.LiveUpstreamID, true
}

// CindyCompatibilityRoutingTarget reports the exact live upstream IDs targeted
// by compatibility aliases. Public IDs must still pass through the account's
// configured model mapping; otherwise a catalog-off rollout would send the
// public slug to Cindy instead of its provider-qualified live ID. The aliases
// themselves remain group-aware and must be resolved before account selection.
func CindyCompatibilityRoutingTarget(model string) bool {
	for _, publicID := range cindyCompatibilityAliases {
		capability := cindyCapabilityByPublicID[publicID]
		if capability != nil && model == capability.LiveUpstreamID {
			return true
		}
	}
	return false
}

// CindyCompatibilityTextPricingForModel resolves only the two exact aliases
// that remain routable when the broader Cindy capability catalog is disabled.
// Callers must enforce strict Cindy account identity before using this price.
func CindyCompatibilityTextPricingForModel(model string) (CindyTextPricing, bool) {
	if publicID, ok := cindyCompatibilityAliases[model]; ok {
		capability := cindyCapabilityByPublicID[publicID]
		if capability != nil && capability.TextPricing != nil {
			return *capability.TextPricing, true
		}
		return CindyTextPricing{}, false
	}
	for _, publicID := range cindyCompatibilityAliases {
		capability := cindyCapabilityByPublicID[publicID]
		if capability != nil && capability.TextPricing != nil &&
			(model == capability.PublicID || model == capability.LiveUpstreamID) {
			return *capability.TextPricing, true
		}
	}
	return CindyTextPricing{}, false
}

func CindyMappedUpstreamModel(model string) (string, bool) {
	capability, ok := ResolveCindyCapability(model)
	if !ok {
		return "", false
	}
	return capability.LiveUpstreamID, true
}

func CindyModelSupportsEndpoint(model string, endpoint CindyEndpoint) bool {
	capability, ok := ResolveCindyCapability(model)
	if !ok {
		return false
	}
	for _, verified := range capability.VerifiedEndpoints {
		if verified == endpoint {
			return true
		}
	}
	return false
}

func CindyModelHasVerifiedEndpoint(model string) bool {
	capability, ok := ResolveCindyCapability(model)
	return ok && len(capability.VerifiedEndpoints) > 0
}

// CindyModelSupportsResponsesImageBridge identifies the one image-only model
// whose public Responses surface is implemented by the deterministic local
// controller/tool rewrite. Both its public ID and exact live ID resolve here;
// no other image model inherits this bridge.
func CindyModelSupportsResponsesImageBridge(model string) bool {
	capability, ok := ResolveCindyCapability(model)
	return ok &&
		capability.PublicID == "gpt-image-2" &&
		capability.Kind == CindyModelKindImage &&
		CindyModelSupportsEndpoint(model, CindyEndpointImagesGenerate)
}

// CindyModelUsesExplicitZeroPrice reports the only allowed fallback for a
// verified Cindy capability without an authoritative public price. Callers
// must never substitute a generic model or image price for strict Cindy traffic.
func CindyModelUsesExplicitZeroPrice(model string) bool {
	capability, ok := ResolveCindyCapability(model)
	return ok && capability.ExplicitZeroPrice
}

// CindyImagePricingForModel resolves exact public, live upstream, and hidden
// compatibility IDs to an authoritative image pricing record. Callers must
// fail closed when neither this record nor ExplicitZeroPrice is available.
func CindyImagePricingForModel(model string) (CindyImagePricing, bool) {
	capability, ok := ResolveCindyCapability(model)
	if !ok || capability.ImagePricing == nil {
		return CindyImagePricing{}, false
	}
	return *capability.ImagePricing, true
}

// CindyTextPricingForModel resolves an exact model or hidden compatibility
// alias to the current catalog's authoritative standard token prices.
func CindyTextPricingForModel(model string) (CindyTextPricing, bool) {
	capability, ok := ResolveCindyCapability(model)
	if !ok || capability.TextPricing == nil {
		return CindyTextPricing{}, false
	}
	return *capability.TextPricing, true
}

func CindyPublicModelIDs() []string {
	if !CindyCapabilityCatalogFeatureEnabled() {
		return nil
	}
	models := make([]string, 0, len(cindyCapabilityCatalog))
	for _, capability := range cindyCapabilityCatalog {
		if capability.Kind == CindyModelKindImage && !CindyImageStudioFeatureEnabled() {
			continue
		}
		if capability.PublicModel && len(capability.VerifiedEndpoints) > 0 {
			models = append(models, capability.PublicID)
		}
	}
	sort.Strings(models)
	return models
}

// CindyCodexPublicModelIDs returns only the public Cindy surface that Codex
// can invoke through Responses. Image models require the explicit local
// Responses bridge as well as the image rollout flag; Messages-only models are
// deliberately absent.
func CindyCodexPublicModelIDs() []string {
	if !CindyCapabilityCatalogFeatureEnabled() {
		return nil
	}
	models := make([]string, 0, len(cindyCapabilityCatalog))
	for _, capability := range cindyCapabilityCatalog {
		if cindyCapabilitySupportsCodexModels(capability) {
			models = append(models, capability.PublicID)
		}
	}
	sort.Strings(models)
	return models
}

func cindyCapabilitySupportsCodexModels(capability CindyCapability) bool {
	if !CindyCapabilityCatalogFeatureEnabled() || !capability.PublicModel {
		return false
	}
	if capability.Kind == CindyModelKindImage {
		return CindyImageStudioFeatureEnabled() &&
			capability.PublicID == "gpt-image-2" &&
			cindyCapabilityHasEndpoint(capability, CindyEndpointImagesGenerate)
	}
	return cindyCapabilityHasEndpoint(capability, CindyEndpointResponses)
}

func cindyCapabilityHasEndpoint(capability CindyCapability, endpoint CindyEndpoint) bool {
	for _, verified := range capability.VerifiedEndpoints {
		if verified == endpoint {
			return true
		}
	}
	return false
}

func CindyVerifiedCapabilities() []CindyCapability {
	if !CindyCapabilityCatalogFeatureEnabled() {
		return nil
	}
	result := make([]CindyCapability, 0, len(cindyCapabilityCatalog))
	for _, capability := range cindyCapabilityCatalog {
		if capability.Kind == CindyModelKindImage && !CindyImageStudioFeatureEnabled() {
			continue
		}
		if len(capability.VerifiedEndpoints) > 0 {
			result = append(result, cloneCindyCapability(capability))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].PublicID < result[j].PublicID })
	return result
}

// CindyVerifiedModelCapabilities returns the client-safe projection of every
// currently enabled, verified Cindy capability.
func CindyVerifiedModelCapabilities() []CindyModelCapability {
	capabilities := CindyVerifiedCapabilities()
	result := make([]CindyModelCapability, 0, len(capabilities))
	for i := range capabilities {
		result = append(result, cindyModelCapabilityFromCapability(capabilities[i]))
	}
	return result
}

// CindyImageModelCapabilities returns the fixed client-safe image subset used
// by Image Studio eligibility responses.
func CindyImageModelCapabilities() []CindyModelCapability {
	capabilities := CindyVerifiedCapabilities()
	result := make([]CindyModelCapability, 0, len(capabilities))
	for i := range capabilities {
		if capabilities[i].Kind == CindyModelKindImage {
			result = append(result, cindyModelCapabilityFromCapability(capabilities[i]))
		}
	}
	return result
}

func cindyModelCapabilityFromCapability(capability CindyCapability) CindyModelCapability {
	return CindyModelCapability{
		Object:            "model_capability",
		ID:                capability.PublicID,
		Kind:              capability.Kind,
		InputModalities:   append([]string(nil), capability.InputModalities...),
		OutputModalities:  append([]string(nil), capability.OutputModalities...),
		Endpoints:         append([]CindyEndpoint(nil), capability.VerifiedEndpoints...),
		ClientSurfaces:    append([]string(nil), capability.ClientSurfaces...),
		MaxInputTokens:    capability.MaxInputTokens,
		MaxOutputTokens:   capability.MaxOutputTokens,
		PricingSource:     capability.PricingSource,
		ExplicitZeroPrice: capability.ExplicitZeroPrice,
		Controls:          cloneCindyCapability(capability).Controls,
	}
}
