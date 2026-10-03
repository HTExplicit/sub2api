package recovery

import (
	"regexp"
	"strconv"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

var inputParam = regexp.MustCompile(`^input(?:\[(\d+)\]|\.(\d+))(?:\.encrypted_content)?$`)

func Rejection(in extensionv1.RecoveryEnvelope) extensionv1.RecoveryRejection {
	if !extensionv1.ValidReasoningRejectionEnvelope(in) {
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

// Enabled reports an account's reasoning policy setting: on unless it is
// configured to anything but true.
func Enabled(in extensionv1.RecoverySetting) bool {
	return !in.Configured || (in.Valid && in.Value)
}
