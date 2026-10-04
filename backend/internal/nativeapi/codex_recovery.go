package nativeapi

// The recovery policy decides on protocol shape and item indices. Neither
// ciphertext, prompt history, credentials nor complete upstream errors are part
// of its inputs.
type RecoveryEnvelope struct {
	ValidJSON      bool    `json:"valid_json"`
	Statuses       []int   `json:"statuses"`
	Event          string  `json:"event"`
	Status         string  `json:"status"`
	ResponseStatus string  `json:"response_status"`
	ErrorLocation  string  `json:"error_location"`
	Code           *string `json:"code,omitempty"`
	Param          string  `json:"param"`
	ParamValid     bool    `json:"param_valid"`
}

type RecoveryRejection struct {
	Recognized bool   `json:"recognized"`
	Code       string `json:"code,omitempty"`
	Param      string `json:"param,omitempty"`
}

type RecoverySelectionQuery struct {
	BodyValid        bool   `json:"body_valid"`
	ToolHistoryValid bool   `json:"tool_history_valid"`
	CipherIndices    []int  `json:"cipher_indices"`
	EncryptedFields  int    `json:"encrypted_fields"`
	ServerContext    bool   `json:"server_context"`
	Param            string `json:"param"`
}

type RecoverySelection struct {
	Indices []int  `json:"indices,omitempty"`
	Reason  string `json:"reason"`
}

// A structured request-state rejection must stay request-scoped even when
// recovery is switched off. This validates protocol framing only; the recovery
// policy separately decides recovery eligibility and rewrite selection.
func ValidReasoningRejectionEnvelope(in RecoveryEnvelope) bool {
	if !in.ValidJSON || !in.ParamValid || in.Code == nil {
		return false
	}
	for _, status := range in.Statuses {
		switch status {
		case 401, 402, 403, 407, 429:
			return false
		}
	}
	if in.Event != "" && in.Event != "error" && in.Event != "response.failed" && in.Event != "response.done" {
		return false
	}
	if in.Event == "response.done" && in.ResponseStatus != "failed" {
		return false
	}
	if in.Status != "" && in.Status != "failed" {
		return false
	}
	switch in.ErrorLocation {
	case "response":
		return in.Event == "response.failed" || in.ResponseStatus == "failed"
	case "error":
		return true
	case "root":
		return in.Event == "error"
	default:
		return false
	}
}
