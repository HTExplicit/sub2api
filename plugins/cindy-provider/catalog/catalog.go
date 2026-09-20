package catalog

import (
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"sort"
	"strings"
)

type CindyModelKind = extensionv1.CindyModelKind
type CindyCapacitySource = extensionv1.CindyCapacitySource
type CindyEndpoint = extensionv1.CindyEndpoint
type CindyImagePricing = extensionv1.CindyImagePricing
type CindyTextPricing = extensionv1.CindyTextPricing
type CindyImageRequestControls = extensionv1.CindyImageRequestControls
type CindyCapabilityControls = extensionv1.CindyCapabilityControls
type CindyCapability = extensionv1.CindyCapability
type CindyCatalogModel = extensionv1.CindyCatalogModel
type CindyModelCapability = extensionv1.CindyModelCapability

const (
	CindyCapabilityCatalogVersion         = extensionv1.CindyCapabilityCatalogVersion
	CindyModelMetadataSourceRevision      = extensionv1.CindyModelMetadataSourceRevision
	CindyCompatibilityAliasSourceRevision = extensionv1.CindyCompatibilityAliasSourceRevision
	CindyDefaultTestModel                 = extensionv1.CindyDefaultTestModel
	CindyWebSearchModel                   = extensionv1.CindyWebSearchModel
	CindyAutoReviewModel                  = extensionv1.CindyAutoReviewModel
	CindyFreeModelCatalogSourceRevision   = extensionv1.CindyFreeModelCatalogSourceRevision
	CindyFreeModelCatalogSHA256           = extensionv1.CindyFreeModelCatalogSHA256
	CindyModelKindText                    = extensionv1.CindyModelKindText
	CindyModelKindImage                   = extensionv1.CindyModelKindImage
	CindyModelKindSpecial                 = extensionv1.CindyModelKindSpecial
	CindyCapacityPinnedRegistry           = extensionv1.CindyCapacityPinnedRegistry
	CindyCapacityApprovedManual           = extensionv1.CindyCapacityApprovedManual
	CindyCapacityUnknown                  = extensionv1.CindyCapacityUnknown
	CindyEndpointResponses                = extensionv1.CindyEndpointResponses
	CindyEndpointChatCompletions          = extensionv1.CindyEndpointChatCompletions
	CindyEndpointMessages                 = extensionv1.CindyEndpointMessages
	CindyEndpointCountTokens              = extensionv1.CindyEndpointCountTokens
	CindyEndpointImagesGenerate           = extensionv1.CindyEndpointImagesGenerate
	CindyEndpointImagesEdit               = extensionv1.CindyEndpointImagesEdit
	CindyEndpointAlphaSearch              = extensionv1.CindyEndpointAlphaSearch
	CindyEndpointReview                   = extensionv1.CindyEndpointReview
	CindyClientSurfaceCodex               = extensionv1.CindyClientSurfaceCodex
	CindyClientSurfacePi                  = extensionv1.CindyClientSurfacePi
	CindyClientSurfaceOpenAI              = extensionv1.CindyClientSurfaceOpenAI
	CindyClientSurfaceClaude              = extensionv1.CindyClientSurfaceClaude
	CindyClientSurfaceAnthropic           = extensionv1.CindyClientSurfaceAnthropic
	CindyClientSurfaceImage               = extensionv1.CindyClientSurfaceImage
)

type Registry struct {
	Config extensionv1.CindyProviderConfig
	Images extensionv1.ImageToolsConfig
}

var cindyCompatibilityAliases = map[string]string{
	"gpt-5.4-mini": "gpt-5.6-luna",
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

// CindyCapabilities returns a defensive copy of the complete authenticated
// free inventory.
func (r Registry) CindyCapabilities() []CindyCapability {
	result := make([]CindyCapability, len(cindyCapabilityCatalog))
	for i := range cindyCapabilityCatalog {
		result[i] = r.cloneCindyCapability(cindyCapabilityCatalog[i])
	}
	return result
}

func (r Registry) cloneCindyCapability(in CindyCapability) CindyCapability {
	out := in
	if in.InputModalities != nil {
		out.InputModalities = append([]string{}, in.InputModalities...)
	}
	if in.OutputModalities != nil {
		out.OutputModalities = append([]string{}, in.OutputModalities...)
	}
	out.VerifiedEndpoints = append([]CindyEndpoint(nil), in.VerifiedEndpoints...)
	out.ClientSurfaces = append([]string(nil), in.ClientSurfaces...)
	out.AgentWireProtocols = r.cloneCindyStringMap(in.AgentWireProtocols)
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
		controls.Generation = r.cloneCindyImageRequestControls(in.Controls.Generation)
		controls.Edit = r.cloneCindyImageRequestControls(in.Controls.Edit)
		out.Controls = &controls
	}
	return out
}

// CindyCatalogModels returns the complete authenticated free inventory for
// management views. It is independent of rollout flags so the two special IDs
// remain visible without entering ordinary client-facing model surfaces.
func (r Registry) CindyCatalogModels() []CindyCatalogModel {
	models := make([]CindyCatalogModel, 0, len(cindyCapabilityCatalog))
	for _, capability := range cindyCapabilityCatalog {
		models = append(models, r.cindyCatalogModelFromCapability(capability))
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}

// CindyManagedModelMappings returns only the exact downstream compatibility
// aliases. Catalog identity mappings are available from CindyCatalogModels.
func (r Registry) CindyManagedModelMappings() []CindyCatalogModel {
	aliases := make([]string, 0, len(cindyCompatibilityAliases))
	for alias := range cindyCompatibilityAliases {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)

	mappings := make([]CindyCatalogModel, 0, len(aliases))
	for _, alias := range aliases {
		targetID := cindyCompatibilityAliases[alias]
		capability := cindyCapabilityByPublicID[targetID]
		if capability == nil || !capability.PublicModel {
			continue
		}
		model := r.cindyCatalogModelFromCapability(*capability)
		model.ID = alias
		model.AliasTarget = targetID
		model.SourceRevision = CindyCompatibilityAliasSourceRevision
		mappings = append(mappings, model)
	}
	return mappings
}

func (r Registry) cindyCatalogModelFromCapability(capability CindyCapability) CindyCatalogModel {
	displayName := strings.TrimSpace(capability.DisplayName)
	if displayName == "" {
		displayName = capability.PublicID
	}
	sourceRevision := strings.TrimSpace(capability.MetadataSourceRevision)
	if sourceRevision == "" {
		sourceRevision = CindyModelMetadataSourceRevision
	}
	codexContextWindow := capability.EffectiveCodexContextWindow()
	capacitySource := r.cindyCapacitySourceForCapability(capability)
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

func (r Registry) cindyCapacitySourceForCapability(capability CindyCapability) CindyCapacitySource {
	if capability.MaxInputTokens > 0 {
		return CindyCapacityPinnedRegistry
	}
	return CindyCapacityUnknown
}

func (r Registry) cloneCindyImageRequestControls(in *CindyImageRequestControls) *CindyImageRequestControls {
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
func (r Registry) ResolveCindyCapability(model string) (CindyCapability, bool) {
	capability, ok := r.resolveKnownCindyCapability(model)
	if !ok || !capability.PublicModel {
		return CindyCapability{}, false
	}
	if r.Config.CatalogEnabled {
		return capability, true
	}
	if capability.Kind == CindyModelKindImage && r.Images.StudioEnabled {
		return capability, true
	}
	if capability.PublicID == "gpt-image-2" && r.Images.ResponsesImageEnabled {
		return capability, true
	}
	return CindyCapability{}, false
}

// resolveKnownCindyCapability recognizes every fixed catalog ID independently
// of rollout flags. This lets fail-closed projections drop a known disabled or
// unverified Cindy ID instead of mistaking it for an unrelated provider model.
func (r Registry) resolveKnownCindyCapability(model string) (CindyCapability, bool) {
	model = strings.TrimSpace(model)
	if capability := cindyCapabilityByPublicID[model]; capability != nil {
		return r.cloneCindyCapability(*capability), true
	}
	if capability := cindyCapabilityByUpstreamID[model]; capability != nil {
		return r.cloneCindyCapability(*capability), true
	}
	if publicID, ok := cindyCompatibilityAliases[model]; ok {
		if capability := cindyCapabilityByPublicID[publicID]; capability != nil {
			return r.cloneCindyCapability(*capability), true
		}
	}
	return CindyCapability{}, false
}

// CindyCompatibilityMappedUpstreamModel resolves only the exact OpenAI
// compatibility alias that must remain callable while the broader Cindy
// capability catalog is rolled back. The caller is responsible for enforcing
// canonical Cindy group identity or the exact temporary legacy Laxa runtime
// identity before applying the result.
func (r Registry) CindyCompatibilityMappedUpstreamModel(model string) (string, bool) {
	publicID, ok := cindyCompatibilityAliases[model]
	if !ok {
		return "", false
	}
	capability := cindyCapabilityByPublicID[publicID]
	if capability == nil || !capability.PublicModel {
		return "", false
	}
	return capability.LiveUpstreamID, true
}

// CindyCompatibilityRoutingTarget reports the exact live upstream IDs targeted
// by compatibility aliases. Public IDs must still pass through the account's
// configured model mapping; otherwise a catalog-off rollout would send the
// public slug to Cindy instead of its provider-qualified live ID. The aliases
// themselves remain group-aware and must be resolved before account selection.
func (r Registry) CindyCompatibilityRoutingTarget(model string) bool {
	for _, publicID := range cindyCompatibilityAliases {
		capability := cindyCapabilityByPublicID[publicID]
		if capability != nil && capability.PublicModel && model == capability.LiveUpstreamID {
			return true
		}
	}
	return false
}

// CindyCompatibilityTextPricingForModel resolves only the exact alias
// that remain routable when the broader Cindy capability catalog is disabled.
// Callers must enforce canonical Cindy identity or the exact temporary legacy
// Laxa runtime identity before using this price.
func (r Registry) CindyCompatibilityTextPricingForModel(model string) (CindyTextPricing, bool) {
	if publicID, ok := cindyCompatibilityAliases[model]; ok {
		capability := cindyCapabilityByPublicID[publicID]
		if capability != nil && capability.PublicModel && capability.TextPricing != nil {
			return *capability.TextPricing, true
		}
		return CindyTextPricing{}, false
	}
	for _, publicID := range cindyCompatibilityAliases {
		capability := cindyCapabilityByPublicID[publicID]
		if capability != nil && capability.PublicModel && capability.TextPricing != nil &&
			(model == capability.PublicID || model == capability.LiveUpstreamID) {
			return *capability.TextPricing, true
		}
	}
	return CindyTextPricing{}, false
}

func (r Registry) CindyMappedUpstreamModel(model string) (string, bool) {
	capability, ok := r.ResolveCindyCapability(model)
	if !ok {
		return "", false
	}
	return capability.LiveUpstreamID, true
}

func (r Registry) CindyModelSupportsEndpoint(model string, endpoint CindyEndpoint) bool {
	if !r.Config.CatalogEnabled {
		return false
	}
	return r.CindyFreePoolModelSupportsEndpoint(model, endpoint)
}

// CindyFreePoolModelSupportsEndpoint is the permanent Cindy data-plane
// allowlist. Unlike catalog publication it is not a rollout flag: disabling
// the public catalog must never restore routing to removed paid models.
func (r Registry) CindyFreePoolModelSupportsEndpoint(model string, endpoint CindyEndpoint) bool {
	capability, ok := r.resolveKnownCindyCapability(model)
	if !ok || !r.cindyFreePoolOrdinaryCapability(capability) {
		return false
	}
	if (endpoint == CindyEndpointImagesGenerate || endpoint == CindyEndpointImagesEdit) &&
		!r.Images.StudioEnabled {
		return false
	}
	if endpoint == CindyEndpointChatCompletions {
		return capability.Kind == CindyModelKindText && r.cindyCapabilityHasEndpoint(capability, CindyEndpointResponses)
	}
	for _, verified := range capability.VerifiedEndpoints {
		if verified == endpoint {
			return true
		}
	}
	return false
}

// CindyFreePoolModelAllowed reports whether a model belongs to the verified
// ordinary routing surface even when a scheduler call does not specify an
// endpoint capability.
func (r Registry) CindyFreePoolModelAllowed(model string) bool {
	capability, ok := r.resolveKnownCindyCapability(model)
	return ok && r.cindyFreePoolOrdinaryCapability(capability)
}

func (r Registry) cindyFreePoolOrdinaryCapability(capability CindyCapability) bool {
	return capability.PublicModel && capability.Kind == CindyModelKindText &&
		len(capability.VerifiedEndpoints) > 0
}

// CindyAlphaSearchModelAvailable reports whether a client-visible Cindy model
// can drive native Responses web_search. Search rollout is intentionally
// independent from catalog publication, so this lookup uses only the pinned
// public ID and never accepts a live upstream ID or hidden helper model.
func (r Registry) CindyAlphaSearchModelAvailable(model string) bool {
	if !r.Config.SearchEnabled {
		return false
	}
	capability, ok := r.resolveKnownCindyCapability(model)
	return ok && capability.PublicModel &&
		capability.Kind == CindyModelKindText &&
		r.cindyCapabilityHasEndpoint(capability, CindyEndpointResponses) &&
		r.cindyCapabilityHasEndpoint(capability, CindyEndpointAlphaSearch)
}

// CindyAlphaSearchUpstreamModel resolves the provider-qualified model for a
// client-visible Search model. It deliberately shares the Search gate rather
// than the Catalog gate so the two features can be rolled out independently.
func (r Registry) CindyAlphaSearchUpstreamModel(model string) (string, bool) {
	if !r.CindyAlphaSearchModelAvailable(model) {
		return "", false
	}
	capability, ok := r.resolveKnownCindyCapability(model)
	if !ok || strings.TrimSpace(capability.LiveUpstreamID) == "" {
		return "", false
	}
	return capability.LiveUpstreamID, true
}

// CindyManagedCompatibilityModels returns the verified public text IDs
// used by managed Search. The compatibility alias is projected separately so
// management clients never confuse it with an upstream catalog entry.
func (r Registry) CindyManagedCompatibilityModels() []string {
	models := make([]string, 0, len(cindyCapabilityCatalog))
	for _, capability := range cindyCapabilityCatalog {
		if capability.PublicModel && capability.Kind == CindyModelKindText &&
			r.cindyCapabilityHasEndpoint(capability, CindyEndpointResponses) &&
			r.cindyCapabilityHasEndpoint(capability, CindyEndpointAlphaSearch) {
			models = append(models, capability.PublicID)
		}
	}
	sort.Strings(models)
	return models
}

func (r Registry) CindyManagedCompatibilityAliases() map[string]string {
	aliases := make(map[string]string, len(cindyCompatibilityAliases))
	for alias, target := range cindyCompatibilityAliases {
		aliases[alias] = target
	}
	return aliases
}

func (r Registry) CindyModelHasVerifiedEndpoint(model string) bool {
	capability, ok := r.ResolveCindyCapability(model)
	return ok && len(capability.VerifiedEndpoints) > 0
}

// CindyModelUsesExplicitZeroPrice reports the only allowed fallback for a
// verified Cindy capability without an authoritative public price. Callers
// must never substitute a generic model or image price for strict Cindy traffic.
func (r Registry) CindyModelUsesExplicitZeroPrice(model string) bool {
	capability, ok := r.ResolveCindyCapability(model)
	return ok && capability.ExplicitZeroPrice
}

// CindyImagePricingForModel resolves exact public, live upstream, and hidden
// compatibility IDs to an authoritative image pricing record. Callers must
// fail closed when neither this record nor ExplicitZeroPrice is available.
func (r Registry) CindyImagePricingForModel(model string) (CindyImagePricing, bool) {
	capability, ok := r.ResolveCindyCapability(model)
	if !ok || capability.ImagePricing == nil {
		return CindyImagePricing{}, false
	}
	return *capability.ImagePricing, true
}

// CindyTextPricingForModel resolves an exact model or hidden compatibility
// alias to the current catalog's authoritative standard token prices.
func (r Registry) CindyTextPricingForModel(model string) (CindyTextPricing, bool) {
	capability, ok := r.ResolveCindyCapability(model)
	if !ok || capability.TextPricing == nil {
		return CindyTextPricing{}, false
	}
	return *capability.TextPricing, true
}

func (r Registry) CindyPublicModelIDs() []string {
	if !r.Config.CatalogEnabled {
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
func (r Registry) CindyCodexPublicModelIDs() []string {
	if !r.Config.CatalogEnabled {
		return nil
	}
	models := make([]string, 0, len(cindyCapabilityCatalog))
	for _, capability := range cindyCapabilityCatalog {
		if r.cindyCapabilitySupportsCodexModels(capability) {
			models = append(models, capability.PublicID)
		}
	}
	sort.Strings(models)
	return models
}

func (r Registry) cindyCapabilitySupportsCodexModels(capability CindyCapability) bool {
	if !r.Config.CatalogEnabled || !capability.PublicModel {
		return false
	}
	if capability.Kind == CindyModelKindImage {
		return r.Images.ResponsesImageEnabled &&
			capability.PublicID == "gpt-image-2" &&
			r.cindyCapabilityHasEndpoint(capability, CindyEndpointImagesGenerate)
	}
	return r.cindyCapabilityHasEndpoint(capability, CindyEndpointResponses)
}

func (r Registry) cindyCapabilityHasEndpoint(capability CindyCapability, endpoint CindyEndpoint) bool {
	for _, verified := range capability.VerifiedEndpoints {
		if verified == endpoint {
			return true
		}
	}
	return false
}

func (r Registry) CindyVerifiedCapabilities() []CindyCapability {
	if !r.Config.CatalogEnabled {
		return nil
	}
	result := make([]CindyCapability, 0, len(cindyCapabilityCatalog))
	for _, capability := range cindyCapabilityCatalog {
		if len(capability.VerifiedEndpoints) > 0 {
			result = append(result, r.cloneCindyCapability(capability))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].PublicID < result[j].PublicID })
	return result
}

// CindyVerifiedModelCapabilities returns the client-safe projection of every
// currently enabled, verified Cindy capability.
func (r Registry) CindyVerifiedModelCapabilities() []CindyModelCapability {
	capabilities := r.CindyVerifiedCapabilities()
	result := make([]CindyModelCapability, 0, len(capabilities))
	for i := range capabilities {
		if !capabilities[i].PublicModel {
			continue
		}
		result = append(result, r.cindyModelCapabilityFromCapability(capabilities[i]))
	}
	return result
}

// CindyImageModelCapabilities returns the fixed client-safe image subset used
// by Image Studio eligibility responses.
func (r Registry) CindyImageModelCapabilities() []CindyModelCapability {
	if !r.Images.StudioEnabled {
		return nil
	}
	result := make([]CindyModelCapability, 0, 2)
	for i := range cindyCapabilityCatalog {
		capability := cindyCapabilityCatalog[i]
		if capability.Kind == CindyModelKindImage && capability.PublicModel && len(capability.VerifiedEndpoints) > 0 {
			result = append(result, r.cindyModelCapabilityFromCapability(capability))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (r Registry) cindyModelCapabilityFromCapability(capability CindyCapability) CindyModelCapability {
	return CindyModelCapability{
		Object:             "model_capability",
		ID:                 capability.PublicID,
		Kind:               capability.Kind,
		InputModalities:    append([]string(nil), capability.InputModalities...),
		OutputModalities:   append([]string(nil), capability.OutputModalities...),
		Endpoints:          append([]CindyEndpoint(nil), capability.VerifiedEndpoints...),
		ClientSurfaces:     append([]string(nil), capability.ClientSurfaces...),
		AgentWireProtocols: r.cloneCindyStringMap(capability.AgentWireProtocols),
		MaxInputTokens:     capability.MaxInputTokens,
		MaxOutputTokens:    capability.MaxOutputTokens,
		PricingSource:      capability.PricingSource,
		ExplicitZeroPrice:  capability.ExplicitZeroPrice,
		Controls:           r.cloneCindyCapability(capability).Controls,
	}
}

func (r Registry) cloneCindyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
