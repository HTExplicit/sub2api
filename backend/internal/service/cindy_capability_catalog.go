package service

import (
	"sort"
	"strings"
)

// CindyCapabilityCatalogVersion is bumped whenever the fixed Cindy data-plane
// catalogue or one of its verified endpoint decisions changes.
const CindyCapabilityCatalogVersion = "2026-08-15.8"

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
	PublicID          string
	LiveUpstreamID    string
	RegistryID        string
	Kind              CindyModelKind
	InputModalities   []string
	OutputModalities  []string
	VerifiedEndpoints []CindyEndpoint
	ClientSurfaces    []string
	MaxInputTokens    int
	MaxOutputTokens   int
	PricingSource     string
	TextPricing       *CindyTextPricing
	ImagePricing      *CindyImagePricing
	Controls          *CindyCapabilityControls
	ExplicitZeroPrice bool
	PublicModel       bool
}

// cindyCapabilityCatalog contains exactly the 23 IDs observed consistently on
// the 2026-08-15 Cindy data plane. Endpoint verification is deliberately
// conservative: unprobed combinations remain in the catalogue for explicit
// connection tests but are neither schedulable nor advertised.
var cindyCapabilityCatalog = []CindyCapability{
	{PublicID: "claude-opus-4-8", LiveUpstreamID: "anthropic/claude-opus-4-8", RegistryID: "anthropic/claude-opus-4-8", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, PricingSource: "https://platform.claude.com/docs/en/about-claude/pricing", TextPricing: &CindyTextPricing{InputCostPerToken: 5e-6, OutputCostPerToken: 25e-6, CacheReadInputTokenCost: 0.5e-6, CacheCreationInputTokenCost: 6.25e-6, CacheCreationInputTokenCostAbove1hr: 10e-6}, PublicModel: true},
	{PublicID: "claude-opus-5", LiveUpstreamID: "anthropic/claude-opus-5", RegistryID: "anthropic/claude-opus-5", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, PricingSource: "https://platform.claude.com/docs/en/about-claude/pricing", TextPricing: &CindyTextPricing{InputCostPerToken: 5e-6, OutputCostPerToken: 25e-6, CacheReadInputTokenCost: 0.5e-6, CacheCreationInputTokenCost: 6.25e-6, CacheCreationInputTokenCostAbove1hr: 10e-6}, PublicModel: true},
	{PublicID: "claude-sonnet-5", LiveUpstreamID: "anthropic/claude-sonnet-5", RegistryID: "anthropic/claude-sonnet-5", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, PricingSource: "https://platform.claude.com/docs/en/about-claude/pricing", TextPricing: &CindyTextPricing{InputCostPerToken: 2e-6, OutputCostPerToken: 10e-6, CacheReadInputTokenCost: 0.2e-6, CacheCreationInputTokenCost: 2.5e-6, CacheCreationInputTokenCostAbove1hr: 4e-6}, PublicModel: true},
	{PublicID: "seed-2.1-pro", LiveUpstreamID: "bytedance-seed/seed-2.1-pro", RegistryID: "bytedance-seed/seed-2.1-pro", Kind: CindyModelKindText, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, MaxInputTokens: 256000, MaxOutputTokens: 256000, PricingSource: "explicit-zero:no-exact-public-usd-price", ExplicitZeroPrice: true, PublicModel: false},
	{PublicID: "cindy/auto-review", LiveUpstreamID: "cindy/auto-review", RegistryID: "", Kind: CindyModelKindSpecial, InputModalities: []string{"text"}, OutputModalities: []string{"json"}, PricingSource: "explicit-zero:no-public-price", ExplicitZeroPrice: true, PublicModel: false},
	{PublicID: CindyWebSearchModel, LiveUpstreamID: CindyWebSearchModel, RegistryID: "", Kind: CindyModelKindSpecial, InputModalities: []string{"text"}, OutputModalities: []string{"text", "citations"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointAlphaSearch}, ClientSurfaces: []string{CindyClientSurfaceCodex, CindyClientSurfaceOpenAI}, PricingSource: "explicit-zero:no-public-price", ExplicitZeroPrice: true, PublicModel: false},
	{PublicID: "deepseek-v4-flash", LiveUpstreamID: "deepseek/deepseek-v4-flash", RegistryID: "deepseek/deepseek-v4-flash", Kind: CindyModelKindText, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, MaxInputTokens: 1000000, MaxOutputTokens: 384000, PricingSource: "explicit-zero:no-stable-exact-price-across-published-rate-boundary", ExplicitZeroPrice: true, PublicModel: false},
	{PublicID: "deepseek-v4-pro", LiveUpstreamID: "deepseek/deepseek-v4-pro", RegistryID: "deepseek/deepseek-v4-pro", Kind: CindyModelKindText, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, MaxInputTokens: 1000000, MaxOutputTokens: 384000, PricingSource: "explicit-zero:no-stable-exact-price-across-published-rate-boundary", ExplicitZeroPrice: true, PublicModel: false},
	{PublicID: "gemini-3-pro-image", LiveUpstreamID: "google/gemini-3-pro-image", RegistryID: "", Kind: CindyModelKindImage, InputModalities: []string{"text", "image"}, OutputModalities: []string{"image"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointImagesGenerate, CindyEndpointImagesEdit}, ClientSurfaces: []string{CindyClientSurfaceImage}, PricingSource: "https://ai.google.dev/gemini-api/docs/pricing#gemini-3-pro-image", ImagePricing: &CindyImagePricing{InputCostPerToken: 2e-6, OutputCostPerToken: 12e-6, InputCostPerImage: 0.0011, OutputCostPerImage1KOr2K: 0.134, OutputCostPerImage4K: 0.24, OutputCostPerImageToken: 120e-6}, Controls: &CindyCapabilityControls{Generation: &CindyImageRequestControls{Sizes: []string{"1024x1024"}, Qualities: []string{"low"}, MaxOutputCount: 1}, Edit: &CindyImageRequestControls{Sizes: []string{"1024x1024"}, Qualities: []string{"low"}, MaxOutputCount: 1, SupportsReferenceImage: true, SupportsMask: true}}, PublicModel: true},
	{PublicID: "gemini-3.5-flash", LiveUpstreamID: "google/gemini-3.5-flash", RegistryID: "google/gemini-3.5-flash", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, PricingSource: "https://ai.google.dev/gemini-api/docs/pricing#gemini-3.5-flash", TextPricing: &CindyTextPricing{InputCostPerToken: 1.5e-6, OutputCostPerToken: 9e-6, CacheReadInputTokenCost: 0.15e-6}, PublicModel: false},
	{PublicID: "gemini-3.6-flash", LiveUpstreamID: "google/gemini-3.6-flash", RegistryID: "", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, PricingSource: "https://ai.google.dev/gemini-api/docs/pricing#gemini-3.6-flash", TextPricing: &CindyTextPricing{InputCostPerToken: 0.75e-6, OutputCostPerToken: 3.75e-6, CacheReadInputTokenCost: 0.075e-6}, PublicModel: true},
	{PublicID: "gemini-3.7-flash", LiveUpstreamID: "google/gemini-3.7-flash", RegistryID: "", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, PricingSource: "https://ai.google.dev/gemini-api/docs/pricing#gemini-3.7-flash", TextPricing: &CindyTextPricing{InputCostPerToken: 0.75e-6, OutputCostPerToken: 3.75e-6, CacheReadInputTokenCost: 0.075e-6}, PublicModel: false},
	{PublicID: "kimi-k3", LiveUpstreamID: "moonshotai/kimi-k3", RegistryID: "moonshotai/kimi-k3", Kind: CindyModelKindText, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, MaxInputTokens: 1048576, MaxOutputTokens: 131072, PricingSource: "explicit-zero:no-exact-public-usd-price", ExplicitZeroPrice: true, PublicModel: false},
	{PublicID: "gpt-5.6-luna", LiveUpstreamID: "openai/gpt-5.6-luna", RegistryID: "openai/gpt-5.6-luna", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointResponses, CindyEndpointChatCompletions, CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceCodex, CindyClientSurfacePi, CindyClientSurfaceOpenAI, CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, MaxInputTokens: 1050000, MaxOutputTokens: 128000, PricingSource: "https://developers.openai.com/api/docs/pricing", TextPricing: &CindyTextPricing{InputCostPerToken: 0.2e-6, OutputCostPerToken: 1.2e-6, CacheReadInputTokenCost: 0.02e-6, CacheCreationInputTokenCost: 0.25e-6, LongContextInputTokenThreshold: 272000, LongContextInputCostPerToken: 0.4e-6, LongContextOutputCostPerToken: 1.8e-6, LongContextCacheReadInputTokenCost: 0.04e-6, LongContextCacheCreationTokenCost: 0.5e-6}, PublicModel: true},
	{PublicID: "gpt-5.6-sol", LiveUpstreamID: "openai/gpt-5.6-sol", RegistryID: "openai/gpt-5.6-sol", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointResponses, CindyEndpointChatCompletions, CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceCodex, CindyClientSurfacePi, CindyClientSurfaceOpenAI, CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, MaxInputTokens: 1050000, MaxOutputTokens: 128000, PricingSource: "https://developers.openai.com/api/docs/pricing", TextPricing: &CindyTextPricing{InputCostPerToken: 5e-6, OutputCostPerToken: 30e-6, CacheReadInputTokenCost: 0.5e-6, CacheCreationInputTokenCost: 6.25e-6, LongContextInputTokenThreshold: 272000, LongContextInputCostPerToken: 10e-6, LongContextOutputCostPerToken: 45e-6, LongContextCacheReadInputTokenCost: 1e-6, LongContextCacheCreationTokenCost: 12.5e-6}, PublicModel: true},
	{PublicID: "gpt-5.6-terra", LiveUpstreamID: "openai/gpt-5.6-terra", RegistryID: "openai/gpt-5.6-terra", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointResponses, CindyEndpointChatCompletions, CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceCodex, CindyClientSurfacePi, CindyClientSurfaceOpenAI, CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, MaxInputTokens: 1050000, MaxOutputTokens: 128000, PricingSource: "https://developers.openai.com/api/docs/pricing", TextPricing: &CindyTextPricing{InputCostPerToken: 2e-6, OutputCostPerToken: 12e-6, CacheReadInputTokenCost: 0.2e-6, CacheCreationInputTokenCost: 2.5e-6, LongContextInputTokenThreshold: 272000, LongContextInputCostPerToken: 4e-6, LongContextOutputCostPerToken: 18e-6, LongContextCacheReadInputTokenCost: 0.4e-6, LongContextCacheCreationTokenCost: 5e-6}, PublicModel: true},
	{PublicID: "gpt-image-2", LiveUpstreamID: "openai/gpt-image-2", RegistryID: "", Kind: CindyModelKindImage, InputModalities: []string{"text", "image"}, OutputModalities: []string{"image"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointImagesGenerate}, ClientSurfaces: []string{CindyClientSurfaceImage}, PricingSource: "https://developers.openai.com/api/docs/pricing", ImagePricing: &CindyImagePricing{InputCostPerToken: 5e-6, OutputCostPerToken: 10e-6, CacheReadInputTokenCost: 1.25e-6, InputCostPerImageToken: 8e-6, OutputCostPerImageToken: 30e-6, CacheReadInputImageTokenCost: 2e-6}, Controls: &CindyCapabilityControls{Generation: &CindyImageRequestControls{Sizes: []string{"1024x1024"}, Qualities: []string{"low"}, MaxOutputCount: 1}}, PublicModel: true},
	{PublicID: "qwen3.8-max", LiveUpstreamID: "qwen/qwen3.8-max", RegistryID: "", Kind: CindyModelKindText, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, PricingSource: "explicit-zero:no-exact-public-usd-price", ExplicitZeroPrice: true, PublicModel: false},
	{PublicID: "hy3", LiveUpstreamID: "tencent/hy3", RegistryID: "", Kind: CindyModelKindText, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, MaxInputTokens: 262144, MaxOutputTokens: 128000, PricingSource: "explicit-zero:no-exact-public-usd-price", ExplicitZeroPrice: true, PublicModel: true},
	{PublicID: "grok-4.5", LiveUpstreamID: "x-ai/grok-4.5", RegistryID: "xai/grok-4.5", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointResponses, CindyEndpointChatCompletions, CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceCodex, CindyClientSurfacePi, CindyClientSurfaceOpenAI, CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, PricingSource: "https://docs.x.ai/developers/pricing", TextPricing: &CindyTextPricing{InputCostPerToken: 2e-6, OutputCostPerToken: 6e-6, CacheReadInputTokenCost: 0.3e-6, LongContextInputTokenThreshold: 200000, LongContextThresholdInclusive: true, LongContextInputCostPerToken: 4e-6, LongContextOutputCostPerToken: 12e-6, LongContextCacheReadInputTokenCost: 0.6e-6}, PublicModel: true},
	{PublicID: "grok-4.6", LiveUpstreamID: "x-ai/grok-4.6", RegistryID: "", Kind: CindyModelKindText, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, PricingSource: "https://docs.x.ai/developers/pricing", TextPricing: &CindyTextPricing{InputCostPerToken: 2e-6, OutputCostPerToken: 6e-6, CacheReadInputTokenCost: 0.5e-6, LongContextInputTokenThreshold: 200000, LongContextThresholdInclusive: true, LongContextInputCostPerToken: 4e-6, LongContextOutputCostPerToken: 12e-6, LongContextCacheReadInputTokenCost: 1e-6}, PublicModel: true},
	{PublicID: "glm-5.2", LiveUpstreamID: "z-ai/glm-5.2", RegistryID: "z-ai/glm-5.2", Kind: CindyModelKindText, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, VerifiedEndpoints: []CindyEndpoint{CindyEndpointResponses, CindyEndpointChatCompletions, CindyEndpointMessages}, ClientSurfaces: []string{CindyClientSurfaceCodex, CindyClientSurfacePi, CindyClientSurfaceOpenAI, CindyClientSurfaceClaude, CindyClientSurfaceAnthropic}, MaxInputTokens: 1000000, MaxOutputTokens: 131072, PricingSource: "https://docs.z.ai/guides/overview/pricing", TextPricing: &CindyTextPricing{InputCostPerToken: 1.4e-6, OutputCostPerToken: 4.4e-6, CacheReadInputTokenCost: 0.26e-6}, PublicModel: true},
	{PublicID: "glm-5.3", LiveUpstreamID: "z-ai/glm-5.3", RegistryID: "", Kind: CindyModelKindText, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, PricingSource: "explicit-zero:no-exact-public-usd-price", ExplicitZeroPrice: true, PublicModel: false},
}

var cindyHiddenAliases = map[string]string{
	"gpt-5.4":                    "gpt-5.6-sol",
	"gpt-5.4-mini":               "gpt-5.6-luna",
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
	if publicID, ok := cindyHiddenAliases[model]; ok {
		if capability := cindyCapabilityByPublicID[publicID]; capability != nil {
			return cloneCindyCapability(*capability), true
		}
	}
	return CindyCapability{}, false
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
