package nativeapi

type TaxonomyName struct {
	Name       string `json:"name"`
	Normalized string `json:"normalized,omitempty"`
}
type TaxonomyOrder struct {
	Actual  []int64 `json:"actual"`
	Ordered []int64 `json:"ordered"`
}
type TaxonomyBulkPlan struct {
	AccountIDs         []int64 `json:"account_ids"`
	HasFilters         bool    `json:"has_filters"`
	ExpectedMatchCount *int    `json:"expected_match_count,omitempty"`
	FolderAction       string  `json:"folder_action"`
	FolderID           *int64  `json:"folder_id,omitempty"`
	TagAddIDs          []int64 `json:"tag_add_ids"`
	TagRemoveIDs       []int64 `json:"tag_remove_ids"`
}
type TaxonomyAssignmentPlan struct {
	FolderID *int64  `json:"folder_id"`
	TagIDs   []int64 `json:"tag_ids"`
}
type ReasoningSelection struct {
	Mode   string   `json:"mode"`
	Effort string   `json:"effort"`
	Levels []string `json:"levels"`
}

// Only bounded facts about a user prompt cross into the policy process.
type TextPromptSelection struct {
	Characters int  `json:"characters"`
	ValidUTF8  bool `json:"valid_utf8"`
}
