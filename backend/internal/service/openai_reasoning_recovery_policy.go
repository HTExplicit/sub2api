package service

import (
	"encoding/json"
	"strings"

	codexrecovery "github.com/Wei-Shaw/sub2api/internal/codexruntime/recovery"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/tidwall/gjson"
)

// openAIRecoveryErrorSource locates the error object of an upstream payload.
func openAIRecoveryErrorSource(root gjson.Result) (string, gjson.Result) {
	switch {
	case root.Get("response.error").IsObject():
		return "response", root.Get("response.error")
	case root.Get("error").IsObject():
		return "error", root.Get("error")
	default:
		return "root", root
	}
}

// openAIRecoveryEnvelope reduces an upstream error payload to the protocol
// shape the recovery policy decides on.
func openAIRecoveryEnvelope(payload []byte) extensionv1.RecoveryEnvelope {
	var envelope extensionv1.RecoveryEnvelope
	if _, err := canonicalReasoningCacheJSON(payload); err != nil {
		return envelope
	}
	envelope.ValidJSON = true
	root := gjson.ParseBytes(payload)
	for _, path := range []string{"status_code", "error.status_code", "error.status", "response.error.status_code", "response.error.status"} {
		if value := root.Get(path); value.Type == gjson.Number || value.Type == gjson.String {
			envelope.Statuses = append(envelope.Statuses, int(value.Int()))
		}
	}
	envelope.Event, envelope.Status, envelope.ResponseStatus = root.Get("type").String(), root.Get("status").String(), root.Get("response.status").String()
	var source gjson.Result
	envelope.ErrorLocation, source = openAIRecoveryErrorSource(root)
	if code := source.Get("code"); code.Type == gjson.String {
		value := code.String()
		envelope.Code = &value
	}
	param := source.Get("param")
	envelope.ParamValid = !param.Exists() || param.Type == gjson.String || param.Type == gjson.Null
	envelope.Param = param.String()
	return envelope
}

// openAIRecoveryErrorMessage returns the message of the error object the
// envelope takes its code and param from.
func openAIRecoveryErrorMessage(payload []byte) string {
	_, source := openAIRecoveryErrorSource(gjson.ParseBytes(payload))
	if message := source.Get("message"); message.Type == gjson.String {
		return message.String()
	}
	return ""
}

// openAIRecoveryMessageNamesItem reports whether the upstream message contains
// a Responses reasoning item id as a whole identifier. The upstream reports a
// ciphertext it cannot verify as "… for item rs_… could not be verified" with a
// null param; the id is the only place the rejected item is identified.
func openAIRecoveryMessageNamesItem(message, id string) bool {
	if !strings.HasPrefix(id, "rs_") || len(id) == len("rs_") {
		return false
	}
	identifier := func(b byte) bool {
		return b == '_' || b == '-' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
	}
	for offset := 0; ; {
		at := strings.Index(message[offset:], id)
		if at < 0 {
			return false
		}
		start := offset + at
		end := start + len(id)
		if (start == 0 || !identifier(message[start-1])) && (end == len(message) || !identifier(message[end])) {
			return true
		}
		offset = start + 1
	}
}

// selectOpenAIRecoveryIndices asks the recovery policy which reasoning items of
// the sent body may lose their ciphertext for the rejection.
func selectOpenAIRecoveryIndices(body []byte, rejection openAIReasoningRejection) extensionv1.RecoverySelection {
	query := extensionv1.RecoverySelectionQuery{Param: rejection.param}
	if _, err := canonicalReasoningCacheJSON(body); err == nil {
		query.BodyValid = true
		query.ToolHistoryValid = openAIReasoningToolHistoryAllowsRecovery(body)
		for _, item := range openAIReasoningCipherItems(body) {
			query.CipherIndices = append(query.CipherIndices, item.index)
			if !query.NamedCipher && rejection.message != "" && openAIRecoveryMessageNamesItem(rejection.message, item.id) {
				query.NamedCipher = true
			}
		}
		conversation := gjson.GetBytes(body, "conversation")
		query.ServerContext = gjson.GetBytes(body, "previous_response_id").String() != "" || (conversation.Exists() && conversation.Type != gjson.Null)
		var parsed any
		if json.Unmarshal(body, &parsed) == nil {
			query.EncryptedFields = countOpenAIEncryptedFields(parsed)
		}
	}
	return codexrecovery.Select(query)
}
