package apicompat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesChatFinalizeDoesNotCompleteProtocolFailure(t *testing.T) {
	for _, failure := range []string{"arguments", "call_id", "missing_terminal_call"} {
		t.Run(failure, func(t *testing.T) {
			state := NewResponsesEventToChatState()
			state.IncludeUsage = true
			item := ResponsesOutput{Type: "function_call", ID: "fc_fixture", CallID: "call_fixture", Name: "lookup", Arguments: `{"x":1}`}
			require.NotEmpty(t, ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: "response.output_item.added", OutputIndex: 3, Item: &item}, state))
			end := item
			var failureEvent ResponsesStreamEvent
			switch failure {
			case "arguments":
				end.Arguments = `{"x":2}`
				failureEvent = ResponsesStreamEvent{Type: "response.output_item.done", OutputIndex: 3, Item: &end}
			case "call_id":
				end.CallID = "call_changed"
				failureEvent = ResponsesStreamEvent{Type: "response.output_item.done", OutputIndex: 3, Item: &end}
			case "missing_terminal_call":
				failureEvent = ResponsesStreamEvent{Type: "response.completed", Response: &ResponsesResponse{
					Status: "completed", Output: []ResponsesOutput{{Type: "message", Role: "assistant"}},
					Usage: &ResponsesUsage{InputTokens: 13, OutputTokens: 5},
				}}
			}
			require.Empty(t, ResponsesEventToChatChunks(&failureEvent, state))
			require.NotEmpty(t, state.ProtocolError, "the real converter must detect the contradiction")
			require.Empty(t, FinalizeResponsesChatStream(state), "a failed stream must never acquire a normal finish or usage chunk")
			require.Empty(t, ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: "response.completed", Response: &ResponsesResponse{
				Status: "completed", Output: []ResponsesOutput{item}, Usage: &ResponsesUsage{InputTokens: 13, OutputTokens: 5},
			}}, state), "a late terminal event cannot recover a corrupted stream")
			require.Empty(t, FinalizeResponsesChatStream(state), "repeated finalization must remain silent")
		})
	}
}

func TestResponsesChatProtocolCompletionPreservesValidToolAndUsage(t *testing.T) {
	state := NewResponsesEventToChatState()
	state.IncludeUsage = true
	initial := ResponsesOutput{Type: "function_call", ID: "fc_fixture", CallID: "call_fixture", Name: "lookup"}
	complete := initial
	complete.Arguments, complete.Status = `{"x":1}`, "completed"
	events := []ResponsesStreamEvent{
		{Type: "response.output_item.added", OutputIndex: 3, Item: &initial},
		{Type: "response.function_call_arguments.delta", OutputIndex: 3, Delta: `{"x":`},
		{Type: "response.output_item.done", OutputIndex: 3, Item: &complete},
		{Type: "response.completed", Response: &ResponsesResponse{Status: "completed", Output: []ResponsesOutput{complete}, Usage: &ResponsesUsage{InputTokens: 13, OutputTokens: 5}}},
	}
	var args, callID, name string
	var finishes, usages int
	for i := range events {
		for _, chunk := range ResponsesEventToChatChunks(&events[i], state) {
			if chunk.Usage != nil {
				usages++
				require.Equal(t, 13, chunk.Usage.PromptTokens)
				require.Equal(t, 5, chunk.Usage.CompletionTokens)
			}
			for _, choice := range chunk.Choices {
				if choice.FinishReason != nil {
					finishes++
					require.Equal(t, "tool_calls", *choice.FinishReason)
				}
				for _, tool := range choice.Delta.ToolCalls {
					args += tool.Function.Arguments
					callID += tool.ID
					name += tool.Function.Name
				}
			}
		}
	}
	require.Empty(t, state.ProtocolError)
	require.Equal(t, complete.Arguments, args)
	require.Equal(t, complete.CallID, callID)
	require.Equal(t, complete.Name, name)
	require.Equal(t, 1, finishes)
	require.Equal(t, 1, usages)
	require.Empty(t, FinalizeResponsesChatStream(state))
}
