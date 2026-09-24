package nativeapi

import "time"

// Credential identities are computed by the host; management policy receives
// only their digest and lifecycle facts, never credential or request content.
type CindyDuplicateCandidate struct {
	ID           int64     `json:"id"`
	IdentityHash string    `json:"identity_hash"`
	Status       string    `json:"status"`
	Banned       bool      `json:"banned"`
	Exhausted    bool      `json:"exhausted"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type CindyDuplicateIdentityGroup struct {
	IdentityHash    string  `json:"identity_hash"`
	ProposedOwnerID int64   `json:"proposed_owner_id"`
	OtherAccountIDs []int64 `json:"other_account_ids"`
}

type CindyGroupSplitInput struct {
	SourceKeeps       string  `json:"source_keeps"`
	TargetName        string  `json:"target_name"`
	APIKeyIDs         []int64 `json:"api_key_ids"`
	MemberFingerprint string  `json:"member_fingerprint,omitempty"`
}

type CindyGroupInputRequest struct {
	Input              CindyGroupSplitInput `json:"input"`
	RequireFingerprint bool                 `json:"require_fingerprint"`
}

type CindyGroupPartitionRequest struct {
	CindyCount    int64  `json:"cindy_count"`
	OrdinaryCount int64  `json:"ordinary_count"`
	SourceKeeps   string `json:"source_keeps,omitempty"`
}

type CindyGroupPartitionPlan struct {
	Classification       string `json:"classification"`
	TargetClassification string `json:"target_classification,omitempty"`
	AccountsToMove       int64  `json:"accounts_to_move"`
	MoveCindy            bool   `json:"move_cindy"`
	SourceCindy          bool   `json:"source_cindy"`
	TargetCindy          bool   `json:"target_cindy"`
}
