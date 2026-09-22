package catalog

import (
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// AlphaSearchPlan is a bounded provider decision. It contains no credentials,
// prompt text, response text, citations, or endpoint URL; the host owns all IO.
type AlphaSearchPlan = extensionv1.CindyAlphaSearchPlan

// AlphaSearchPlan resolves only an exact public Cindy model. Live upstream IDs,
// hidden helper models, and compatibility aliases are intentionally excluded
// from the client Search plan.
func (r Registry) AlphaSearchPlan(model string) AlphaSearchPlan {
	plan := AlphaSearchPlan{RequestedModel: strings.TrimSpace(model)}
	if !r.Config.SearchEnabled {
		plan.Reason = "search_disabled"
		return plan
	}
	if plan.RequestedModel == "" {
		plan.Reason = "model_required"
		return plan
	}
	capability, ok := cindyCapabilityByPublicID[plan.RequestedModel]
	if !ok || capability == nil || !capability.PublicModel || capability.Kind != CindyModelKindText {
		plan.Reason = "model_unavailable"
		return plan
	}
	if !r.cindyCapabilityHasEndpoint(*capability, CindyEndpointResponses) ||
		!r.cindyCapabilityHasEndpoint(*capability, CindyEndpointAlphaSearch) ||
		strings.TrimSpace(capability.LiveUpstreamID) == "" {
		plan.Reason = "responses_search_unavailable"
		return plan
	}
	plan.Allowed = true
	plan.UpstreamModel = strings.TrimSpace(capability.LiveUpstreamID)
	plan.PrimaryProtocol = "responses"
	plan.ResponsesToolType = "web_search"
	plan.MaxSearchUses = 1
	if r.cindyNativeMessagesSearchQualified() {
		plan.FallbackProtocol = "messages"
		plan.FallbackOnCapabilityMiss = true
		plan.FallbackOnMissingSearchEvidence = true
		plan.NativeMessagesModel = CindyWebSearchModel
	}
	return plan
}

// cindyNativeMessagesSearchQualified is deliberately tied to the hidden
// search helper's own qualification. Public model Messages endpoints are not
// evidence that the dedicated web-search helper can serve this fallback.
func (r Registry) cindyNativeMessagesSearchQualified() bool {
	capability, ok := cindyCapabilityByPublicID[CindyWebSearchModel]
	return ok && capability != nil && capability.Kind == CindyModelKindSpecial &&
		capability.LiveUpstreamID == CindyWebSearchModel &&
		r.cindyCapabilityHasEndpoint(*capability, CindyEndpointAlphaSearch)
}
