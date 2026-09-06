package apicompat

// ChatCompletionTerminal describes the semantic end of one model response.
// Transport EOF and the [DONE] sentinel cannot supply a missing finish_reason.
type ChatCompletionTerminal struct {
	EventType         string
	Status            string
	IncompleteDetails *ResponsesIncompleteDetails
	Error             *ResponsesError
}

func ChatCompletionTerminalForReason(reason string) ChatCompletionTerminal {
	switch reason {
	case "stop", "tool_calls", "function_call":
		return ChatCompletionTerminal{EventType: "response.completed", Status: "completed"}
	case "length":
		return ChatCompletionTerminal{EventType: "response.incomplete", Status: "incomplete", IncompleteDetails: &ResponsesIncompleteDetails{Reason: "max_output_tokens"}}
	case "content_filter":
		return ChatCompletionTerminal{EventType: "response.incomplete", Status: "incomplete", IncompleteDetails: &ResponsesIncompleteDetails{Reason: "content_filter"}}
	default:
		return FailedChatCompletionTerminal("upstream_incomplete_response", "Upstream response ended without a supported finish reason")
	}
}

func FailedChatCompletionTerminal(code, message string) ChatCompletionTerminal {
	return ChatCompletionTerminal{EventType: "response.failed", Status: "failed", Error: &ResponsesError{Code: code, Message: message}}
}
