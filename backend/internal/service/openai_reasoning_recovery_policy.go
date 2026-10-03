package service

import (
	"encoding/json"

	codexrecovery "github.com/Wei-Shaw/sub2api/internal/codexruntime/recovery"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/tidwall/gjson"
)

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
	switch {
	case root.Get("response.error").IsObject():
		envelope.ErrorLocation, source = "response", root.Get("response.error")
	case root.Get("error").IsObject():
		envelope.ErrorLocation, source = "error", root.Get("error")
	default:
		envelope.ErrorLocation, source = "root", root
	}
	if code := source.Get("code"); code.Type == gjson.String {
		value := code.String()
		envelope.Code = &value
	}
	param := source.Get("param")
	envelope.ParamValid = !param.Exists() || param.Type == gjson.String || param.Type == gjson.Null
	envelope.Param = param.String()
	return envelope
}

// selectOpenAIRecoveryIndices asks the recovery policy which reasoning items of
// the sent body may lose their ciphertext for the rejection's param.
func selectOpenAIRecoveryIndices(body []byte, param string) extensionv1.RecoverySelection {
	query := extensionv1.RecoverySelectionQuery{Param: param}
	if _, err := canonicalReasoningCacheJSON(body); err == nil {
		query.BodyValid = true
		query.ToolHistoryValid = openAIReasoningToolHistoryAllowsRecovery(body)
		for _, item := range openAIReasoningCipherItems(body) {
			query.CipherIndices = append(query.CipherIndices, item.index)
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
