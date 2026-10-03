package service

import (
	"context"
	"encoding/json"

	codexrecovery "github.com/Wei-Shaw/sub2api/internal/codexruntime/recovery"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/tidwall/gjson"
)

func openAIReasoningPolicyEnabled(ctx context.Context, account *Account, key string) (bool, error) {
	if account == nil || !account.supportsOpenAIReasoningPolicies() {
		return false, nil
	}
	raw, configured := account.Extra[key]
	value, valid := raw.(bool)
	return codexrecovery.Enabled(extensionv1.RecoverySetting{Configured: configured, Valid: valid, Value: value}), nil
}

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

func readOpenAIRecoveryRejection(ctx context.Context, payload []byte) (openAIReasoningRejection, bool, error) {
	result := codexrecovery.Rejection(openAIRecoveryEnvelope(payload))
	return openAIReasoningRejection{result.Code, result.Param}, result.Recognized, nil
}

func selectOpenAIRecoveryIndices(ctx context.Context, body []byte, param string) (extensionv1.RecoverySelection, error) {
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
	return codexrecovery.Select(query), nil
}
