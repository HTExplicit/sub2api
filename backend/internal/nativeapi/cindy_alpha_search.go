package nativeapi

// CindyAlphaSearchPlan is the bounded provider decision for the host's
// first-class Cindy alpha-search bridge. It intentionally carries only model
// and protocol policy; credentials, prompts, responses, URLs, and billing
// state remain host-owned.
type CindyAlphaSearchPlan struct {
	Allowed                         bool   `json:"allowed"`
	RequestedModel                  string `json:"requested_model"`
	UpstreamModel                   string `json:"upstream_model,omitempty"`
	PrimaryProtocol                 string `json:"primary_protocol,omitempty"`
	FallbackProtocol                string `json:"fallback_protocol,omitempty"`
	FallbackOnCapabilityMiss        bool   `json:"fallback_on_capability_miss"`
	FallbackOnMissingSearchEvidence bool   `json:"fallback_on_missing_search_evidence"`
	ResponsesToolType               string `json:"responses_tool_type,omitempty"`
	NativeMessagesModel             string `json:"native_messages_model,omitempty"`
	MaxSearchUses                   int    `json:"max_search_uses,omitempty"`
	Reason                          string `json:"reason,omitempty"`
}

// CindyAlphaSearchPlanRequest is the complete payload accepted by the
// cindy.search.plan provider operation. Account identity is carried by the
// invocation envelope, never by this payload.
type CindyAlphaSearchPlanRequest struct {
	Model string `json:"model"`
}
