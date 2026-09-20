package service

import (
	"context"
	"encoding/json"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
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

func queryCindyCatalog(method string, args, outputs []any) bool {
	var encoded []json.RawMessage
	for _, arg := range args {
		raw, err := json.Marshal(arg)
		if err != nil {
			return false
		}
		encoded = append(encoded, raw)
	}
	images, _ := currentImageToolsConfig()
	raw, err := json.Marshal(extensionv1.CindyCatalogQuery{Method: method, Args: encoded, Images: images})
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := invokeProcessExtensionCached(ctx, PlatformCindy, AccountTypeAPIKey, extensionv1.Invocation{Capability: extensionv1.CapabilityProvider, Operation: "cindy.catalog", Payload: raw})
	if err != nil || result.Code != "" {
		return false
	}
	return decodeCindyCatalogResult(result.Payload, outputs)
}
func decodeCindyCatalogResult(raw json.RawMessage, outputs []any) bool {
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil || len(values) != len(outputs) {
		return false
	}
	for i, out := range outputs {
		if json.Unmarshal(values[i], out) != nil {
			return false
		}
	}
	return true
}
func CindyCapabilities() []CindyCapability {
	var out0 []CindyCapability
	queryCindyCatalog("CindyCapabilities", []any{}, []any{&out0})
	return out0
}
func CindyCatalogModels() []CindyCatalogModel {
	var out0 []CindyCatalogModel
	queryCindyCatalog("CindyCatalogModels", []any{}, []any{&out0})
	return out0
}
func CindyManagedModelMappings() []CindyCatalogModel {
	var out0 []CindyCatalogModel
	queryCindyCatalog("CindyManagedModelMappings", []any{}, []any{&out0})
	return out0
}
func ResolveCindyCapability(model string) (CindyCapability, bool) {
	var out0 CindyCapability
	var out1 bool
	queryCindyCatalog("ResolveCindyCapability", []any{model}, []any{&out0, &out1})
	return out0, out1
}
func resolveKnownCindyCapability(model string) (CindyCapability, bool) {
	var out0 CindyCapability
	var out1 bool
	queryCindyCatalog("resolveKnownCindyCapability", []any{model}, []any{&out0, &out1})
	return out0, out1
}
func CindyCompatibilityMappedUpstreamModel(model string) (string, bool) {
	var out0 string
	var out1 bool
	queryCindyCatalog("CindyCompatibilityMappedUpstreamModel", []any{model}, []any{&out0, &out1})
	return out0, out1
}
func CindyCompatibilityRoutingTarget(model string) bool {
	var out0 bool
	queryCindyCatalog("CindyCompatibilityRoutingTarget", []any{model}, []any{&out0})
	return out0
}
func CindyCompatibilityTextPricingForModel(model string) (CindyTextPricing, bool) {
	var out0 CindyTextPricing
	var out1 bool
	queryCindyCatalog("CindyCompatibilityTextPricingForModel", []any{model}, []any{&out0, &out1})
	return out0, out1
}
func CindyMappedUpstreamModel(model string) (string, bool) {
	var out0 string
	var out1 bool
	queryCindyCatalog("CindyMappedUpstreamModel", []any{model}, []any{&out0, &out1})
	return out0, out1
}
func CindyModelSupportsEndpoint(model string, endpoint CindyEndpoint) bool {
	var out0 bool
	queryCindyCatalog("CindyModelSupportsEndpoint", []any{model, endpoint}, []any{&out0})
	return out0
}
func CindyFreePoolModelSupportsEndpoint(model string, endpoint CindyEndpoint) bool {
	var out0 bool
	queryCindyCatalog("CindyFreePoolModelSupportsEndpoint", []any{model, endpoint}, []any{&out0})
	return out0
}
func CindyFreePoolModelAllowed(model string) bool {
	var out0 bool
	queryCindyCatalog("CindyFreePoolModelAllowed", []any{model}, []any{&out0})
	return out0
}
func CindyAlphaSearchModelAvailable(model string) bool {
	var out0 bool
	queryCindyCatalog("CindyAlphaSearchModelAvailable", []any{model}, []any{&out0})
	return out0
}
func CindyAlphaSearchUpstreamModel(model string) (string, bool) {
	var out0 string
	var out1 bool
	queryCindyCatalog("CindyAlphaSearchUpstreamModel", []any{model}, []any{&out0, &out1})
	return out0, out1
}
func CindyManagedCompatibilityModels() []string {
	var out0 []string
	queryCindyCatalog("CindyManagedCompatibilityModels", []any{}, []any{&out0})
	return out0
}
func CindyManagedCompatibilityAliases() map[string]string {
	var out0 map[string]string
	queryCindyCatalog("CindyManagedCompatibilityAliases", []any{}, []any{&out0})
	return out0
}
func CindyModelHasVerifiedEndpoint(model string) bool {
	var out0 bool
	queryCindyCatalog("CindyModelHasVerifiedEndpoint", []any{model}, []any{&out0})
	return out0
}
func CindyModelUsesExplicitZeroPrice(model string) bool {
	var out0 bool
	queryCindyCatalog("CindyModelUsesExplicitZeroPrice", []any{model}, []any{&out0})
	return out0
}
func CindyImagePricingForModel(model string) (CindyImagePricing, bool) {
	var out0 CindyImagePricing
	var out1 bool
	queryCindyCatalog("CindyImagePricingForModel", []any{model}, []any{&out0, &out1})
	return out0, out1
}
func CindyTextPricingForModel(model string) (CindyTextPricing, bool) {
	var out0 CindyTextPricing
	var out1 bool
	queryCindyCatalog("CindyTextPricingForModel", []any{model}, []any{&out0, &out1})
	return out0, out1
}
func CindyPublicModelIDs() []string {
	var out0 []string
	queryCindyCatalog("CindyPublicModelIDs", []any{}, []any{&out0})
	return out0
}
func CindyCodexPublicModelIDs() []string {
	var out0 []string
	queryCindyCatalog("CindyCodexPublicModelIDs", []any{}, []any{&out0})
	return out0
}
func cindyCapabilitySupportsCodexModels(capability CindyCapability) bool {
	var out0 bool
	queryCindyCatalog("cindyCapabilitySupportsCodexModels", []any{capability}, []any{&out0})
	return out0
}
func CindyVerifiedCapabilities() []CindyCapability {
	var out0 []CindyCapability
	queryCindyCatalog("CindyVerifiedCapabilities", []any{}, []any{&out0})
	return out0
}
func CindyVerifiedModelCapabilities() []CindyModelCapability {
	var out0 []CindyModelCapability
	queryCindyCatalog("CindyVerifiedModelCapabilities", []any{}, []any{&out0})
	return out0
}
func CindyImageModelCapabilities() []CindyModelCapability {
	var out0 []CindyModelCapability
	queryCindyCatalog("CindyImageModelCapabilities", []any{}, []any{&out0})
	return out0
}
func cloneCindyImageRequestControls(in *CindyImageRequestControls) *CindyImageRequestControls {
	return extensionv1.CloneCindyImageRequestControls(in)
}

func cindyModelCapabilityFromCapability(capability CindyCapability) CindyModelCapability {
	var out CindyModelCapability
	queryCindyCatalog("cindyModelCapabilityFromCapability", []any{capability}, []any{&out})
	return out
}
