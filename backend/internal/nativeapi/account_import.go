package nativeapi

const (
	AccountImportCodeCreate              = "account_import_create"
	AccountImportCodeUpdate              = "account_import_update"
	AccountImportCodePayloadInvalid      = "account_import_payload_invalid"
	AccountImportCodeIdentityConflict    = "account_import_identity_conflict"
	AccountImportCodeCindyTargetRequired = "cindy_import_target_group_required"
	AccountImportCodeCindyTargetInvalid  = "cindy_import_target_group_invalid"
	AccountImportCodeCindyAPIKeyInvalid  = "cindy_import_api_key_invalid"
	AccountImportCodeCredentialConflict  = "cindy_import_credential_conflict"
	AccountImportCodeDeviceConflict      = "cindy_import_device_conflict"
	AccountImportCodeDeviceInvalid       = "cindy_import_device_invalid"
	AccountImportCodeExecutionFailed     = "account_import_execution_failed"
)

// Import policy sees validation facts and opaque identity digests. The host
// keeps the imported credentials, device identifiers, names and proxy secrets.
type AccountImportItemFacts struct {
	CindyCandidate     bool    `json:"cindy_candidate"`
	LegacyCindy        bool    `json:"legacy_cindy"`
	APIKeyValid        bool    `json:"api_key_valid"`
	PayloadValid       bool    `json:"payload_valid"`
	DeviceValid        bool    `json:"device_valid"`
	DeviceSourceValid  bool    `json:"device_source_valid"`
	CredentialIdentity string  `json:"credential_identity,omitempty"`
	DeviceIdentity     string  `json:"device_identity,omitempty"`
	DeviceOwners       []int64 `json:"device_owners,omitempty"`
	Matches            []int64 `json:"matches,omitempty"`
	CredentialCopies   int     `json:"credential_copies,omitempty"`
	DeviceCopies       int     `json:"device_copies,omitempty"`
}

type AccountImportPlanningRequest struct {
	Phase            string                   `json:"phase,omitempty"`
	Prepared         []AccountImportItemPlan  `json:"prepared,omitempty"`
	TargetGroupID    int64                    `json:"target_group_id"`
	TargetCanonical  bool                     `json:"target_canonical"`
	TargetHasMembers bool                     `json:"target_has_members"`
	TargetStrict     bool                     `json:"target_strict"`
	Items            []AccountImportItemFacts `json:"items"`
}

type AccountImportItemPlan struct {
	Index           int     `json:"index"`
	Action          string  `json:"action"`
	Code            string  `json:"code"`
	AccountID       int64   `json:"account_id,omitempty"`
	CanonicalCindy  bool    `json:"canonical_cindy"`
	GroupIDs        []int64 `json:"group_ids,omitempty"`
	TrackCredential bool    `json:"track_credential,omitempty"`
	TrackDevice     bool    `json:"track_device,omitempty"`
}
