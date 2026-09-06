package service

import (
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/tidwall/gjson"
)

// OpenAIResponsesRequireNativeUpstream keeps non-representable requests out
// of Chat-only candidates. The converter independently enforces this boundary.
func OpenAIResponsesRequireNativeUpstream(body []byte) bool {
	conversation := gjson.GetBytes(body, "conversation")
	if gjson.GetBytes(body, "previous_response_id").String() != "" || gjson.GetBytes(body, "background").Bool() || (conversation.Exists() && conversation.Type != gjson.Null) {
		return true
	}
	var request apicompat.ResponsesRequest
	if json.Unmarshal(body, &request) != nil {
		return false
	}
	if apicompat.ResponsesInputRequiresNative(request.Input) {
		return true
	}
	tools, err := apicompat.EffectiveResponsesTools(&request)
	return err == nil && apicompat.ResponsesToolsRequireNative(tools)
}
