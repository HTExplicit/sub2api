package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/tidwall/gjson"
)

type codexRecoveryScopeKey struct{}
type codexRecoveryScope struct {
	accountID             int64
	platform, accountType string
}

func withCodexRecoveryScope(ctx context.Context, account *Account) context.Context {
	if account == nil {
		return ctx
	}
	return context.WithValue(ctx, codexRecoveryScopeKey{}, codexRecoveryScope{account.ID, account.Platform, account.Type})
}

func invokeCodexRecoveryPolicy(ctx context.Context, accountType, operation string, input, output any) error {
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > extensionv1.MaxPayloadBytes {
		return ErrNativeCodexRuntimeUnavailable
	}
	call, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	invocation := extensionv1.Invocation{Capability: extensionv1.CapabilityRecovery, Operation: operation, Payload: raw}
	if scope, ok := ctx.Value(codexRecoveryScopeKey{}).(codexRecoveryScope); ok {
		if scope.platform != PlatformOpenAI {
			return ErrNativeCodexPolicyDisabled
		}
		accountType, invocation.AccountID = scope.accountType, scope.accountID
	}
	var result extensionv1.Result
	if accountType == "" {
		result, err = invokeNativeCodex(call, "", "", invocation)
	} else {
		result, err = invokeNativeCodex(call, PlatformOpenAI, accountType, invocation)
	}
	if err != nil {
		return err
	}
	if result.Code != "" || json.Unmarshal(result.Payload, output) != nil {
		return ErrNativeCodexRuntimeUnavailable
	}
	return nil
}

func readOpenAIReplayRules(ctx context.Context) (extensionv1.ReplayRules, error) {
	var rules extensionv1.ReplayRules
	if err := invokeCodexRecoveryPolicy(ctx, "", "codex.replay.rules", struct{}{}, &rules); err != nil {
		return rules, err
	}
	if !rules.Enabled {
		return rules, nil
	}
	if rules.Version == "" || len(rules.Version) > 64 || rules.MaxToolCalls < 1 || rules.MaxToolCalls > 32 || len(rules.ChatFields) > 6 || len(rules.OutputKinds) > 3 {
		return rules, ErrNativeCodexRuntimeUnavailable
	}
	validate := func(values, allowed []string) bool {
		seen := map[string]bool{}
		for _, value := range values {
			if !slices.Contains(allowed, value) || seen[value] {
				return false
			}
			seen[value] = true
		}
		return true
	}
	if !validate(rules.ChatFields, []string{"role", "content", "reasoning_content", "reasoning", "tool_calls", "refusal"}) || !validate(rules.OutputKinds, []string{"reasoning", "message", "function_call"}) {
		return rules, ErrNativeCodexRuntimeUnavailable
	}
	slices.Sort(rules.ChatFields)
	slices.Sort(rules.OutputKinds)
	return rules, nil
}

func openAIReasoningPolicyEnabled(ctx context.Context, account *Account, key string) (bool, error) {
	if account == nil || !account.supportsOpenAIReasoningPolicies() {
		return false, nil
	}
	raw, configured := account.Extra[key]
	value, valid := raw.(bool)
	var enabled bool
	err := invokeCodexRecoveryPolicy(withCodexRecoveryScope(ctx, account), account.Type, "codex.recovery.enabled", extensionv1.RecoverySetting{Configured: configured, Valid: valid, Value: value}, &enabled)
	if errors.Is(err, ErrNativeCodexPolicyDisabled) {
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
			return extensionv1.RecoverySelection{}, ErrNativeCodexRuntimeUnavailable
		}
		delete(allowed, index)
	}
	return selection, nil
}
