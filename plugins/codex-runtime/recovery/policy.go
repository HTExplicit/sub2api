package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

var inputParam = regexp.MustCompile(`^input(?:\[(\d+)\]|\.(\d+))(?:\.encrypted_content)?$`)

func Rejection(in extensionv1.RecoveryEnvelope) extensionv1.RecoveryRejection {
	if !in.ValidJSON || !in.ParamValid || in.Code == nil {
		return extensionv1.RecoveryRejection{}
	}
	for _, status := range in.Statuses {
		switch status {
		case 401, 402, 403, 407, 429:
			return extensionv1.RecoveryRejection{}
		}
	}
	if in.Event != "" && in.Event != "error" && in.Event != "response.failed" && in.Event != "response.done" {
		return extensionv1.RecoveryRejection{}
	}
	if in.Event == "response.done" && in.ResponseStatus != "failed" {
		return extensionv1.RecoveryRejection{}
	}
	if in.Status != "" && in.Status != "failed" {
		return extensionv1.RecoveryRejection{}
	}
	switch in.ErrorLocation {
	case "response":
		if in.Event != "response.failed" && in.ResponseStatus != "failed" {
			return extensionv1.RecoveryRejection{}
		}
	case "error":
	case "root":
		if in.Event != "error" {
			return extensionv1.RecoveryRejection{}
		}
	default:
		return extensionv1.RecoveryRejection{}
	}
	if *in.Code != "thinking_signature_invalid" && *in.Code != "invalid_encrypted_content" {
		return extensionv1.RecoveryRejection{}
	}
	return extensionv1.RecoveryRejection{Recognized: true, Code: *in.Code, Param: in.Param}
}

func Select(in extensionv1.RecoverySelectionQuery) extensionv1.RecoverySelection {
	if !in.BodyValid {
		return extensionv1.RecoverySelection{Reason: "invalid_request_snapshot"}
	}
	if !in.ToolHistoryValid {
		return extensionv1.RecoverySelection{Reason: "invalid_tool_history"}
	}
	if len(in.CipherIndices) == 0 {
		return extensionv1.RecoverySelection{Reason: "no_reasoning_ciphertext"}
	}
	if match := inputParam.FindStringSubmatch(in.Param); match != nil {
		text := match[1]
		if text == "" {
			text = match[2]
		}
		index, err := strconv.Atoi(text)
		if err == nil {
			for _, candidate := range in.CipherIndices {
				if candidate == index {
					return extensionv1.RecoverySelection{Indices: []int{index}, Reason: "recovery_not_dispatched"}
				}
			}
		}
		return extensionv1.RecoverySelection{Reason: "target_not_reasoning_ciphertext"}
	}
	if in.Param != "" && in.Param != "input" && in.Param != "reasoning.encrypted_content" {
		return extensionv1.RecoverySelection{Reason: "unsupported_error_param"}
	}
	if in.ServerContext {
		return extensionv1.RecoverySelection{Reason: "server_held_context"}
	}
	if in.EncryptedFields != len(in.CipherIndices) {
		return extensionv1.RecoverySelection{Reason: "ambiguous_encrypted_carriers"}
	}
	return extensionv1.RecoverySelection{Indices: append([]int(nil), in.CipherIndices...), Reason: "recovery_not_dispatched"}
}

func Invoke(ctx context.Context, in extensionv1.Invocation) (extensionv1.Result, error) {
	if err := ctx.Err(); err != nil {
		return extensionv1.Result{}, err
	}
	if in.Capability != extensionv1.CapabilityRecovery {
		return extensionv1.Result{}, errors.New("unsupported recovery capability")
	}
	var value any
	switch in.Operation {
	case "codex.recovery.available":
		value = true
	case "codex.recovery.enabled":
		var input extensionv1.RecoverySetting
		if json.Unmarshal(in.Payload, &input) != nil {
			return extensionv1.Result{}, errors.New("invalid recovery setting")
		}
		value = !input.Configured || (input.Valid && input.Value)
	case "codex.recovery.rejection":
		var input extensionv1.RecoveryEnvelope
		if json.Unmarshal(in.Payload, &input) != nil {
			return extensionv1.Result{}, errors.New("invalid recovery envelope")
		}
		value = Rejection(input)
	case "codex.recovery.select":
		var input extensionv1.RecoverySelectionQuery
		if json.Unmarshal(in.Payload, &input) != nil {
			return extensionv1.Result{}, errors.New("invalid recovery selection")
		}
		value = Select(input)
	default:
		return extensionv1.Result{}, errors.New("unknown recovery operation")
	}
	raw, err := json.Marshal(value)
	return extensionv1.Result{Payload: raw}, err
}
