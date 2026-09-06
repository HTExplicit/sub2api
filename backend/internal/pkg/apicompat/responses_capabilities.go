package apicompat

import "encoding/json"

// ResponsesConversionError identifies a request that cannot be represented
// faithfully by the selected wire protocol. Never retry a weakened request.
type ResponsesConversionError struct{ Code, Param, Message string }

func (e *ResponsesConversionError) Error() string { return e.Message }

func ResponsesToolsRequireNative(tools []ResponsesTool) bool {
	for _, tool := range tools {
		switch tool.Type {
		case "function", "custom", "tool_search", "x_search":
		case "namespace":
			for _, child := range append(append([]ResponsesTool(nil), tool.Tools...), tool.Children...) {
				if child.Type != "function" {
					return true
				}
			}
		default:
			return true
		}
	}
	return false
}

// A hosted-tool history or opaque reasoning is not equivalent to a Chat message.
// Route these inputs to a native provider instead of discarding the item.
func ResponsesInputRequiresNative(input json.RawMessage) bool {
	var items []json.RawMessage
	if json.Unmarshal(input, &items) != nil {
		return false
	}
	for _, raw := range items {
		var item map[string]json.RawMessage
		if json.Unmarshal(raw, &item) != nil {
			continue
		}
		switch rawString(item["type"]) {
		case "", "message", "additional_tools", "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output", "tool_search_call", "tool_search_output", "input_text", "text", "input_image":
		case "reasoning":
			if rawString(item["encrypted_content"]) != "" && extractResponsesReasoningText(item) == "" {
				return true
			}
		default:
			return true
		}
	}
	return false
}

func unsupportedResponsesInput() *ResponsesConversionError {
	return &ResponsesConversionError{Code: "unsupported_input_item", Param: "input", Message: "This input contains a feature or reasoning state that requires a native Responses upstream"}
}
