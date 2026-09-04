package handler

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/tidwall/gjson"
)

// legacyLaxaContinuationCanSwitchAccount keeps the temporary OpenAI-platform
// projection from weakening the continuation guarantees used by first-class
// Cindy accounts. A complete fresh replay is portable; an anchor or opaque
// carrier is tied to the credential that created it and must not be replayed
// onto another Laxa key after model_not_supported.
func legacyLaxaContinuationCanSwitchAccount(account *service.Account, payload []byte) bool {
	if account == nil || !service.IsLegacyCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
		return true
	}
	classification, err := service.ClassifyCindyContinuation(payload, service.CindyContinuationProof{})
	return err == nil && classification.CanSwitchAccount()
}

func legacyLaxaContinuationPayloadCandidate(payload []byte) bool {
	if !gjson.ValidBytes(payload) {
		return false
	}
	root := gjson.ParseBytes(payload)
	if !root.IsObject() {
		return false
	}
	if root.Get("previous_response_id").Exists() {
		return true
	}
	// Continuation markers are meaningful in Responses input items. Restrict
	// the recursive walk to `input` so a tool schema or arbitrary metadata field
	// named item_reference/encrypted_content cannot force the whole request into
	// the legacy Laxa affinity path.
	input := root.Get("input")
	var walk func(gjson.Result) bool
	walk = func(value gjson.Result) bool {
		if value.IsObject() {
			itemType := strings.TrimSpace(value.Get("type").String())
			switch itemType {
			case "item_reference":
				return true
			case "reasoning", "compaction", "compaction_summary":
				if strings.TrimSpace(value.Get("id").String()) != "" ||
					value.Get("encrypted_content").Exists() || value.Get("signature").Exists() {
					return true
				}
			}
			if value.Get("encrypted_content").Exists() || value.Get("signature").Exists() {
				return true
			}
		}
		if value.IsObject() || value.IsArray() {
			found := false
			value.ForEach(func(_, child gjson.Result) bool {
				found = walk(child)
				return !found
			})
			return found
		}
		return false
	}
	return walk(input)
}
