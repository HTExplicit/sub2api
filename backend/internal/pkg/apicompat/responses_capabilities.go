package apicompat

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
