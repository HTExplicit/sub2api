package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func requireInvalidResponsesToolHistory(t *testing.T, input string) {
	t.Helper()
	messages, err := responsesInputToChatMessages("", json.RawMessage(input))
	require.Nil(t, messages)
	var conversion *ResponsesConversionError
	require.ErrorAs(t, err, &conversion)
	require.Equal(t, "input", conversion.Param)
	require.Equal(t, "invalid_tool_history", conversion.Code)
}

func TestAgentResponseConversionRejectsAmbiguousToolHistory(t *testing.T) {
	for _, input := range []string{
		`[{"type":"function_call","call_id":"call_fixture","name":"read","arguments":"{}"},{"role":"user","content":"fixture"}]`,
		`[{"type":"function_call_output","call_id":"call_orphan","output":"fixture"}]`,
		`[{"type":"function_call","call_id":"call_fixture","name":"read","arguments":"{}"},{"type":"function_call_output","call_id":"call_fixture","output":"first"},{"type":"function_call_output","call_id":"call_fixture","output":"second"}]`,
	} {
		_, err := ResponsesToChatCompletionsRequest(&ResponsesRequest{Model: "fixture", Input: json.RawMessage(input)})
		var conversion *ResponsesConversionError
		require.ErrorAs(t, err, &conversion)
		require.Equal(t, "input", conversion.Param)
		require.Equal(t, "invalid_tool_history", conversion.Code)
	}
}

func TestAgentResponseConversionRejectsUnrepresentableInput(t *testing.T) {
	for _, input := range []string{
		`[{"type":"web_search_call","id":"ws_fixture","status":"completed"}]`,
		`[{"type":"reasoning","id":"rs_fixture","summary":[],"encrypted_content":"opaque-fixture"}]`,
	} {
		_, err := ResponsesToChatCompletionsRequest(&ResponsesRequest{Model: "fixture", Input: json.RawMessage(input)})
		var conversion *ResponsesConversionError
		require.ErrorAs(t, err, &conversion)
		require.Equal(t, "input", conversion.Param)
		require.Equal(t, "unsupported_input_item", conversion.Code)
	}
}
