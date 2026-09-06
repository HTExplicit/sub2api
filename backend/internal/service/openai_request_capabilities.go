package service

import (
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/tidwall/gjson"
)

// OpenAIResponsesRequireNativeUpstream keeps non-representable requests out
// of Chat-only candidates. The converter independently enforces this boundary.
func OpenAIResponsesRequireNativeUpstream(body []byte) bool {
	if gjson.GetBytes(body, "previous_response_id").String() != "" || gjson.GetBytes(body, "background").Bool() || gjson.GetBytes(body, "conversation").Exists() {
		return true
	}
	var request apicompat.ResponsesRequest
	if json.Unmarshal(body, &request) != nil {
		return false
	}
	tools, err := apicompat.EffectiveResponsesTools(&request)
	return err == nil && apicompat.ResponsesToolsRequireNative(tools)
}
