package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/tidwall/gjson"
)

func invokeCodexRecoveryPolicy(ctx context.Context, accountType, operation string, input, output any) error {
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > extensionv1.MaxPayloadBytes {
		return ErrExtensionOperationUnavailable
	}
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	invocation := extensionv1.Invocation{Capability: extensionv1.CapabilityRecovery, Operation: operation, Payload: raw}
	var result extensionv1.Result
	cached := operation == "codex.recovery.enabled" || operation == "codex.recovery.available"
	if accountType == "" {
		result, err = invokeProcessDomainExtension(call, invocation, cached)
	} else if cached {
		result, err = invokeProcessExtensionCached(call, PlatformOpenAI, accountType, invocation)
	} else {
		result, err = invokeProcessExtension(call, PlatformOpenAI, accountType, invocation)
	}
	if err != nil {
		return err
	}
	if result.Code != "" || json.Unmarshal(result.Payload, output) != nil {
		return ErrExtensionOperationUnavailable
	}
	return nil
}

func openAIReasoningPolicyEnabled(ctx context.Context, account *Account, key string) (bool, error) {
	if account == nil || !account.supportsOpenAIReasoningPolicies() {
		return false, nil
	}
	raw, configured := account.Extra[key]
	value, valid := raw.(bool)
	var enabled bool
	err := invokeCodexRecoveryPolicy(ctx, account.Type, "codex.recovery.enabled", extensionv1.RecoverySetting{Configured: configured, Valid: valid, Value: value}, &enabled)
	if errors.Is(err, ErrExtensionOperationDisabled) {
		return false, nil
	}
	return enabled, err
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
	var result extensionv1.RecoveryRejection
	err := invokeCodexRecoveryPolicy(ctx, "", "codex.recovery.rejection", openAIRecoveryEnvelope(payload), &result)
	return openAIReasoningRejection{result.Code, result.Param}, result.Recognized, err
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
	var selection extensionv1.RecoverySelection
	err := invokeCodexRecoveryPolicy(ctx, "", "codex.recovery.select", query, &selection)
	if err != nil {
		return selection, err
	}
	allowed := map[int]bool{}
	for _, index := range query.CipherIndices {
		allowed[index] = true
	}
	for _, index := range selection.Indices {
		if !allowed[index] {
			return extensionv1.RecoverySelection{}, ErrExtensionOperationUnavailable
		}
		delete(allowed, index)
	}
	return selection, nil
}
