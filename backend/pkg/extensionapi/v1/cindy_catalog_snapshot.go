package extensionv1

// CindyCatalogSnapshotMethodV1 identifies an additive, atomic catalog query.
// Its schema version is a protocol value, not a provider catalog revision.
const CindyCatalogSnapshotMethodV1 = "CindyCatalogSnapshotV1"

type CindyCatalogMetadata struct {
	CatalogVersion             string `json:"catalog_version"`
	ModelMetadataRevision      string `json:"model_metadata_revision"`
	CompatibilityAliasRevision string `json:"compatibility_alias_revision"`
	InventoryRevision          string `json:"inventory_revision"`
	InventorySHA256            string `json:"inventory_sha256"`
}

type CindyModelReference struct {
	PublicID       string `json:"public_id"`
	LiveUpstreamID string `json:"live_upstream_id"`
}

// CindyCatalogSnapshotV1 is produced from one provider configuration and
// catalog read. Projections and narrow legacy mappings remain provider policy;
// hosts validate their identity and format without maintaining a model table.
// Default references are descriptive even when catalog publication is off.
type CindyCatalogSnapshotV1 struct {
	SchemaVersion            int                    `json:"schema_version"`
	Metadata                 CindyCatalogMetadata   `json:"metadata"`
	Config                   CindyProviderConfig    `json:"config"`
	Images                   ImageToolsConfig       `json:"images"`
	DefaultTestModel         CindyModelReference    `json:"default_test_model"`
	ResponsesImageController CindyModelReference    `json:"responses_image_controller"`
	Capabilities             []CindyCapability      `json:"capabilities"`
	CatalogModels            []CindyCatalogModel    `json:"catalog_models"`
	PublicModelIDs           []string               `json:"public_model_ids"`
	CodexCapabilities        []CindyCapability      `json:"codex_capabilities"`
	ModelCapabilities        []CindyModelCapability `json:"model_capabilities"`
	CompatibilityAliases     map[string]string      `json:"compatibility_aliases"`
	AvailableMappings        map[string]string      `json:"available_mappings"`
	CompatibilityMappings    map[string]string      `json:"compatibility_mappings"`
	LegacyLiveMappings       map[string]string      `json:"legacy_live_mappings"`
}
