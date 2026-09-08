//go:build reasoning_fidelity && namespace_roundtrip

package service

import "encoding/json"

// NamespaceRoundtripSafeErrorForTest is a test-only projection of the existing
// diagnostic producer and queue sanitizer. It cannot expose a raw error message
// or expand the production type/code/parameter/hint allowlists.
func NamespaceRoundtripSafeErrorForTest(body []byte, limited bool) json.RawMessage {
	shape := continuationDiagnosticError(body)
	shape.InspectionLimited = shape.InspectionLimited || limited
	diagnostic := sanitizeOpenAIContinuationDiagnostic(&OpenAIContinuationDiagnostic{UpstreamError: shape})
	if diagnostic == nil {
		return nil
	}
	safe := diagnostic.UpstreamError
	encoded, err := json.Marshal(struct {
		ErrorType         openAIContinuationFingerprint `json:"error_type"`
		ErrorCode         openAIContinuationFingerprint `json:"error_code"`
		ErrorParam        openAIContinuationFingerprint `json:"error_param"`
		Hints             []string                      `json:"hints,omitempty"`
		InspectionLimited bool                          `json:"inspection_limited"`
	}{safe.ErrorType, safe.ErrorCode, safe.ErrorParam, safe.Hints, safe.InspectionLimited})
	if err != nil {
		return nil
	}
	return encoded
}
