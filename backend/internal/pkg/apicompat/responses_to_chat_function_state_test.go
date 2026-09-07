package apicompat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesEventToChatFunctionSnapshots(t *testing.T) {
	final := ResponsesOutput{Type: "function_call", ID: "fc_one", CallID: "call_one", Name: "load_orders", Arguments: `{"city":"杭州"}`, Status: "completed"}
	initial := final
	initial.Arguments, initial.Status = "", "in_progress"
	created := ResponsesStreamEvent{Type: "response.created", Response: &ResponsesResponse{ID: "resp_one"}}
	added := ResponsesStreamEvent{Type: "response.output_item.added", OutputIndex: 7, Item: &initial}
	done := ResponsesStreamEvent{Type: "response.output_item.done", OutputIndex: 7, Item: &final}
	completed := ResponsesStreamEvent{Type: "response.completed", Response: &ResponsesResponse{ID: "resp_one", Status: "completed", Output: []ResponsesOutput{final}}}
	for name, events := range map[string][]ResponsesStreamEvent{
		"added_full":     {created, {Type: "response.output_item.added", OutputIndex: 7, Item: &final}, done, completed},
		"done_full":      {created, added, done, completed},
		"terminal_full":  {created, added, completed},
		"terminal_only":  {completed},
		"done_only":      {created, done, completed},
		"partial_suffix": {created, added, {Type: "response.function_call_arguments.delta", OutputIndex: 7, Delta: `{"city":`}, done, completed},
	} {
		t.Run(name, func(t *testing.T) {
			state := NewResponsesEventToChatState()
			state.Model = "fixture"
			var arguments, callID, toolName, finish, role string
			for i := range events {
				chunks := ResponsesEventToChatChunks(&events[i], state)
				for _, chunk := range chunks {
					require.Equal(t, "resp_one", chunk.ID)
					for _, choice := range chunk.Choices {
						role += choice.Delta.Role
						for _, call := range choice.Delta.ToolCalls {
							require.NotNil(t, call.Index)
							require.Equal(t, 0, *call.Index)
							arguments += call.Function.Arguments
							callID += call.ID
							toolName += call.Function.Name
						}
						if choice.FinishReason != nil {
							finish = *choice.FinishReason
						}
					}
				}
			}
			require.Empty(t, state.ProtocolError)
			require.Equal(t, final.Arguments, arguments, "the complete snapshot fills only the missing suffix")
			require.Equal(t, final.CallID, callID, "never duplicate or invent call identity")
			require.Equal(t, final.Name, toolName)
			require.Equal(t, "assistant", role)
			require.Equal(t, "tool_calls", finish)
		})
	}
}

func TestResponsesEventToChatFunctionSnapshotRejectsContradiction(t *testing.T) {
	for _, change := range []string{"arguments", "call_id", "name", "malformed", "after_done"} {
		t.Run(change, func(t *testing.T) {
			state := NewResponsesEventToChatState()
			item := ResponsesOutput{Type: "function_call", ID: "fc", CallID: "call", Name: "load", Arguments: `{"x":1}`}
			ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: "response.output_item.added", Item: &item}, state)
			end := item
			switch change {
			case "arguments":
				end.Arguments = `{"x":2}`
			case "call_id":
				end.CallID = "different"
			case "name":
				end.Name = "different"
			case "malformed":
				end.Arguments = `{`
			}
			ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: "response.output_item.done", Item: &end}, state)
			if change == "after_done" {
				ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: "response.function_call_arguments.delta", Delta: "extra"}, state)
			}
			require.NotEmpty(t, state.ProtocolError)
			require.Nil(t, ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: "response.completed", Response: &ResponsesResponse{Status: "completed"}}, state))
		})
	}
}

func TestResponsesEventToChatFunctionSnapshotLateIdentity(t *testing.T) {
	state := NewResponsesEventToChatState()
	ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: "response.output_item.added", OutputIndex: 2, Item: &ResponsesOutput{Type: "function_call", ID: "fc"}}, state)
	ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: "response.function_call_arguments.delta", OutputIndex: 2, Delta: "{"}, state)
	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: "response.output_item.done", OutputIndex: 2, Item: &ResponsesOutput{Type: "function_call", ID: "fc", CallID: "call_real", Name: "load_orders", Arguments: "{}"}}, state)
	require.Empty(t, state.ProtocolError)
	require.Len(t, chunks, 1)
	call := chunks[0].Choices[0].Delta.ToolCalls[0]
	require.Equal(t, "call_real", call.ID)
	require.Equal(t, "load_orders", call.Function.Name)
	require.Equal(t, "}", call.Function.Arguments)
}

func TestResponsesEventToChatFunctionSnapshotCannotDropAnnouncedCall(t *testing.T) {
	state := NewResponsesEventToChatState()
	ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: "response.output_item.added", OutputIndex: 7, Item: &ResponsesOutput{Type: "function_call", CallID: "call_original", Name: "load", Arguments: "{}"}}, state)
	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: "response.completed", Response: &ResponsesResponse{Status: "completed", Output: []ResponsesOutput{{Type: "function_call", CallID: "call_other", Name: "load", Arguments: "{}"}}}}, state)
	require.NotEmpty(t, state.ProtocolError)
	require.Nil(t, chunks)
}
