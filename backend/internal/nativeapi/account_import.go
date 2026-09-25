package nativeapi

const (
	AccountImportCodeCreate           = "account_import_create"
	AccountImportCodeUpdate           = "account_import_update"
	AccountImportCodePayloadInvalid   = "account_import_payload_invalid"
	AccountImportCodeIdentityConflict = "account_import_identity_conflict"
	AccountImportCodeExecutionFailed  = "account_import_execution_failed"
)

// Import policy sees validation facts and identity matches. The host keeps the
// imported credentials, names and proxy secrets.
type AccountImportItemFacts struct {
	PayloadValid bool    `json:"payload_valid"`
	Matches      []int64 `json:"matches,omitempty"`
}

type AccountImportPlanningRequest struct {
	Phase    string                   `json:"phase,omitempty"`
	Prepared []AccountImportItemPlan  `json:"prepared,omitempty"`
	Items    []AccountImportItemFacts `json:"items"`
}

type AccountImportItemPlan struct {
	Index     int    `json:"index"`
	Action    string `json:"action"`
	Code      string `json:"code"`
	AccountID int64  `json:"account_id,omitempty"`
}
