package nativeapi

// Only structured error fields cross the boundary; message text, response
// bodies, account credentials and prompt content are not policy inputs.
type CindyObservedResponse struct {
	Status            int     `json:"status"`
	ValidJSON         bool    `json:"valid_json"`
	EventType         *string `json:"event_type,omitempty"`
	ErrorType         *string `json:"error_type,omitempty"`
	ErrorCode         *string `json:"error_code,omitempty"`
	ResponseErrorType *string `json:"response_error_type,omitempty"`
	ResponseErrorCode *string `json:"response_error_code,omitempty"`
}
type CindyResponseDecision struct {
	Balance uint8 `json:"balance"`
	Health  uint8 `json:"health"`
}
