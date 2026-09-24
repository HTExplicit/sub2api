package catalog

import extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"

// These are provider release data, intentionally independent of deprecated
// SDK constants. Updating this package updates its complete runtime snapshot.
const (
	CindyCapabilityCatalogVersion         = "2026-09-11.1"
	CindyModelMetadataSourceRevision      = "makecindy/cindy@2128cd45e08e3419a36ad474d8c01e42a34ea328"
	CindyCompatibilityAliasSourceRevision = "sub2api-cindy-compat@2026-08-17.1"
	CindyDefaultTestModel                 = "gpt-5.6-luna"
	CindyAutoReviewModel                  = "cindy/auto-review"
	CindyFreeModelCatalogSourceRevision   = "laxarouter-free-key@2026-09-11"
	CindyFreeModelCatalogSHA256           = "38045af0a5a90c360ba44013d90a1ccbff65c903db7f9b99701c30ce15ca8821"
	cindyResponsesImageController         = "gpt-5.6-luna"
)

// Catalog rollback retains only these deliberate direct wire spellings plus
// compatibility aliases. A different test default must not expand this list.
var cindyLegacyDirectMappings = map[string]string{
	"gpt-5.6-luna": "openai/gpt-5.6-luna",
}

func (r Registry) CatalogSnapshotV1() extensionv1.CindyCatalogSnapshotV1 {
	ref := func(id string) extensionv1.CindyModelReference {
		capability, ok := r.resolveKnownCindyCapability(id)
		if !ok {
			return extensionv1.CindyModelReference{}
		}
		return extensionv1.CindyModelReference{PublicID: capability.PublicID, LiveUpstreamID: capability.LiveUpstreamID}
	}
	out := extensionv1.CindyCatalogSnapshotV1{
		SchemaVersion: 1,
		Metadata: extensionv1.CindyCatalogMetadata{
			CatalogVersion: CindyCapabilityCatalogVersion, ModelMetadataRevision: CindyModelMetadataSourceRevision,
			CompatibilityAliasRevision: CindyCompatibilityAliasSourceRevision,
			InventoryRevision:          CindyFreeModelCatalogSourceRevision, InventorySHA256: CindyFreeModelCatalogSHA256,
		},
		Config: r.Config, Images: r.Images,
		DefaultTestModel: ref(CindyDefaultTestModel), ResponsesImageController: ref(cindyResponsesImageController),
		Capabilities: r.CindyCapabilities(), CatalogModels: r.CindyCatalogModels(),
		PublicModelIDs: r.CindyPublicModelIDs(), ModelCapabilities: r.CindyVerifiedModelCapabilities(),
		CompatibilityAliases: r.CindyManagedCompatibilityAliases(),
		AvailableMappings:    make(map[string]string), CompatibilityMappings: make(map[string]string),
		LegacyLiveMappings: make(map[string]string),
	}
	for _, id := range r.CindyCodexPublicModelIDs() {
		if capability, ok := r.resolveKnownCindyCapability(id); ok {
			out.CodexCapabilities = append(out.CodexCapabilities, capability)
		}
	}
	ids := make(map[string]bool)
	for _, capability := range out.Capabilities {
		ids[capability.PublicID], ids[capability.LiveUpstreamID] = true, true
	}
	for alias := range out.CompatibilityAliases {
		ids[alias] = true
	}
	for id := range ids {
		if model, ok := r.CindyMappedUpstreamModel(id); ok {
			out.AvailableMappings[id], out.LegacyLiveMappings[id] = model, model
		}
		if model, ok := r.CindyCompatibilityMappedUpstreamModel(id); ok {
			out.CompatibilityMappings[id], out.LegacyLiveMappings[id] = model, model
		}
	}
	for id, model := range cindyLegacyDirectMappings {
		out.LegacyLiveMappings[id] = model
	}
	return out
}
