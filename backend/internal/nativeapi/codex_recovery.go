package nativeapi

// Recovery receives protocol shape and item indices. Neither ciphertext,
// prompt history, credentials nor complete upstream errors cross this boundary.
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

type RecoverySetting struct {
	Configured bool `json:"configured"`
	Valid      bool `json:"valid"`
	Value      bool `json:"value"`
}

type ReplayRules struct {
	Version               string   `json:"version"`
	Enabled               bool     `json:"enabled"`
	MaxToolCalls          int      `json:"max_tool_calls"`
	AllowOmittedReasoning bool     `json:"allow_omitted_reasoning"`
	ChatFields            []string `json:"chat_fields"`
	OutputKinds           []string `json:"output_kinds"`
}

// A structured request-state rejection must stay request-scoped even when an
// optional recovery plugin is absent. This validates protocol framing only;
// the plugin separately decides recovery eligibility and rewrite selection.
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
