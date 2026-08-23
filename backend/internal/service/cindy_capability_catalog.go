package service

import (
	"sort"
	"strings"
)

// CindyCapabilityCatalogVersion is bumped whenever the fixed Cindy data-plane
// catalogue or one of its verified endpoint decisions changes.
const CindyCapabilityCatalogVersion = "2026-08-24.1"

// CindyModelMetadataSchemaVersion and CindyModelMetadataSourceSHA256 pin the
// exact public international Cindy catalog shipped with this release.
const (
	CindyModelMetadataSchemaVersion = 4
	CindyModelMetadataSourceSHA256  = "b2783df6c272fc9851c85f3ffe871c962a6ab701f7698be1893fbfd03a5f28d3"
)

// CindyModelMetadataSourceRevision pins the shipped Cindy registry used for
// display, context-window, output-limit, and reasoning metadata.
const CindyModelMetadataSourceRevision = "makecindy/cindy@1ff5cbcc9d29aec6fedb8c14889ecd86b72a22de"

// CindyCompatibilityAliasSourceRevision identifies downstream aliases managed
// by Sub2API. These aliases are not part of Cindy's upstream model registry.
const CindyCompatibilityAliasSourceRevision = "sub2api-cindy-compat@2026-08-17.1"

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

// CindyCapabilities returns a defensive copy of the 22 fixed v4 models.
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
	out.AgentWireProtocols = cloneCindyStringMap(in.AgentWireProtocols)
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

// CindyCatalogModels returns all 22 pinned v4 models for management views.
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
		sourceRevision = CindyModelMetadataSourceRevision
	}
	codexContextWindow := capability.EffectiveCodexContextWindow()
	capacitySource := cindyCapacitySourceForCapability(capability)
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
		CapacitySource:         capacitySource,
		Verified:               len(capability.VerifiedEndpoints) > 0,
		Endpoints:              append([]CindyEndpoint(nil), capability.VerifiedEndpoints...),
		Managed:                true,
		PublicModel:            capability.PublicModel,
	}
}

func cindyCapacitySourceForCapability(capability CindyCapability) CindyCapacitySource {
	if capability.MaxInputTokens > 0 {
		return CindyCapacityPinnedRegistry
	}
	return CindyCapacityUnknown
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
	capability, ok := resolveKnownCindyCapability(model)
	if !ok {
		return CindyCapability{}, false
	}
	if CindyCapabilityCatalogFeatureEnabled() {
		return capability, true
	}
	if capability.Kind == CindyModelKindImage && CindyImageStudioFeatureEnabled() {
		return capability, true
	}
	if capability.PublicID == "gpt-image-2" && CindyResponsesImageBridgeFeatureEnabled() {
		return capability, true
	}
	return CindyCapability{}, false
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
// canonical Cindy group identity or the exact temporary legacy Laxa runtime
// identity before applying the result.
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
// Callers must enforce canonical Cindy identity or the exact temporary legacy
// Laxa runtime identity before using this price.
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
	if (endpoint == CindyEndpointImagesGenerate || endpoint == CindyEndpointImagesEdit) &&
		!CindyImageStudioFeatureEnabled() {
		return false
	}
	if endpoint == CindyEndpointChatCompletions {
		return capability.Kind == CindyModelKindText && cindyCapabilityHasEndpoint(capability, CindyEndpointResponses)
	}
	for _, verified := range capability.VerifiedEndpoints {
		if verified == endpoint {
			return true
		}
	}
	return false
}

// CindyAlphaSearchModelAvailable reports whether a client-visible Cindy model
// can drive native Responses web_search. Search rollout is intentionally
// independent from catalog publication, so this lookup uses only the pinned
// public ID and never accepts a live upstream ID or hidden helper model.
func CindyAlphaSearchModelAvailable(model string) bool {
	if !CindySearchFeatureEnabled() {
		return false
	}
	capability := cindyCapabilityByPublicID[strings.TrimSpace(model)]
	return capability != nil && capability.PublicModel &&
		capability.Kind == CindyModelKindText &&
		cindyCapabilityHasEndpoint(*capability, CindyEndpointResponses)
}

// CindyAlphaSearchUpstreamModel resolves the provider-qualified model for a
// client-visible Search model. It deliberately shares the Search gate rather
// than the Catalog gate so the two features can be rolled out independently.
func CindyAlphaSearchUpstreamModel(model string) (string, bool) {
	if !CindyAlphaSearchModelAvailable(model) {
		return "", false
	}
	capability := cindyCapabilityByPublicID[strings.TrimSpace(model)]
	if capability == nil || strings.TrimSpace(capability.LiveUpstreamID) == "" {
		return "", false
	}
	return capability.LiveUpstreamID, true
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
	capability, ok := resolveKnownCindyCapability(model)
	return CindyResponsesImageBridgeFeatureEnabled() && ok &&
		capability.PublicModel &&
		capability.PublicID == "gpt-image-2" &&
		capability.Kind == CindyModelKindImage &&
		cindyCapabilityHasEndpoint(capability, CindyEndpointImagesGenerate)
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
		return CindyResponsesImageBridgeFeatureEnabled() &&
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
		if !capabilities[i].PublicModel {
			continue
		}
		result = append(result, cindyModelCapabilityFromCapability(capabilities[i]))
	}
	return result
}

// CindyImageModelCapabilities returns the fixed client-safe image subset used
// by Image Studio eligibility responses.
func CindyImageModelCapabilities() []CindyModelCapability {
	if !CindyImageStudioFeatureEnabled() {
		return nil
	}
	result := make([]CindyModelCapability, 0, 2)
	for i := range cindyCapabilityCatalog {
		capability := cindyCapabilityCatalog[i]
		if capability.Kind == CindyModelKindImage && capability.PublicModel && len(capability.VerifiedEndpoints) > 0 {
			result = append(result, cindyModelCapabilityFromCapability(capability))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func cindyModelCapabilityFromCapability(capability CindyCapability) CindyModelCapability {
	return CindyModelCapability{
		Object:             "model_capability",
		ID:                 capability.PublicID,
		Kind:               capability.Kind,
		InputModalities:    append([]string(nil), capability.InputModalities...),
		OutputModalities:   append([]string(nil), capability.OutputModalities...),
		Endpoints:          append([]CindyEndpoint(nil), capability.VerifiedEndpoints...),
		ClientSurfaces:     append([]string(nil), capability.ClientSurfaces...),
		AgentWireProtocols: cloneCindyStringMap(capability.AgentWireProtocols),
		MaxInputTokens:     capability.MaxInputTokens,
		MaxOutputTokens:    capability.MaxOutputTokens,
		PricingSource:      capability.PricingSource,
		ExplicitZeroPrice:  capability.ExplicitZeroPrice,
		Controls:           cloneCindyCapability(capability).Controls,
	}
}

func cloneCindyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
