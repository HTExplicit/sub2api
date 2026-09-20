package extensionv1

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
