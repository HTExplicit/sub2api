package extensionv1

// A separate query keeps the strict CatalogSnapshotV1 and pricing companion
// contracts unchanged for hosts that do not request this presentation view.
const CindyAccountTestPlanMethodV1 = "CindyAccountTestPlanV1"

// CindyAccountTestPlanV1 is data produced by one provider Registry capture.
// Eligibility, ordering and defaults belong to the provider, not the SDK.
type CindyAccountTestPlanV1 struct {
	SchemaVersion   int                    `json:"schema_version"`
	CatalogSnapshot CindyCatalogSnapshotV1 `json:"catalog_snapshot"`
	Models          []CindyCatalogModel    `json:"models"`
	DefaultModelID  string                 `json:"default_model_id"`
}
