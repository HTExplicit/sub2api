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
	argumentsDone := ResponsesStreamEvent{Type: "response.function_call_arguments.done", OutputIndex: 7, ItemID: final.ID, CallID: final.CallID, Name: final.Name, Arguments: final.Arguments}
	emptyFinal := final
	emptyFinal.Arguments = ""
	emptyDone := ResponsesStreamEvent{Type: "response.output_item.done", OutputIndex: 7, Item: &emptyFinal}
	emptyCompleted := ResponsesStreamEvent{Type: "response.completed", Response: &ResponsesResponse{ID: "resp_one", Status: "completed", Output: []ResponsesOutput{emptyFinal}}}
	for name, events := range map[string][]ResponsesStreamEvent{
		"added_full":                    {created, {Type: "response.output_item.added", OutputIndex: 7, Item: &final}, done, completed},
		"done_full":                     {created, added, done, completed},
		"terminal_full":                 {created, added, completed},
		"terminal_only":                 {completed},
		"done_only":                     {created, done, completed},
		"partial_suffix":                {created, added, {Type: "response.function_call_arguments.delta", OutputIndex: 7, Delta: `{"city":`}, done, completed},
		"arguments_done":                {created, added, argumentsDone, done, completed},
		"arguments_done_partial_suffix": {created, added, {Type: "response.function_call_arguments.delta", OutputIndex: 7, Delta: `{"city":`}, argumentsDone, done, completed},
		"arguments_done_repeated":       {created, added, argumentsDone, argumentsDone, done, argumentsDone, completed},
		"arguments_done_empty_terminal_snapshots": {created, added, argumentsDone, emptyDone, emptyCompleted},
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

func TestResponsesEventToChatArgumentsDoneRejectsContradictingPrefix(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(map[bool]string{false: "function", true: "custom"}[custom], func(t *testing.T) {
			state := NewResponsesEventToChatState()
			itemType, deltaType, doneType := "function_call", "response.function_call_arguments.delta", "response.function_call_arguments.done"
			if custom {
				itemType, deltaType, doneType = "custom_tool_call", "response.custom_tool_call_input.delta", "response.custom_tool_call_input.done"
			}
			ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: "response.output_item.added", OutputIndex: 3, Item: &ResponsesOutput{Type: itemType, ID: "item", CallID: "call", Name: "load"}}, state)
			ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: deltaType, OutputIndex: 3, Delta: `{"x":1`}, state)
			done := ResponsesStreamEvent{Type: doneType, OutputIndex: 3, Arguments: `{"x":2}`, Input: `{"x":2}`}
			require.Nil(t, ResponsesEventToChatChunks(&done, state))
			require.NotEmpty(t, state.ProtocolError)
		})
	}
}

func TestResponsesEventToChatCustomInputDoneSharesPrefixState(t *testing.T) {
	state := NewResponsesEventToChatState()
	events := []ResponsesStreamEvent{
		{Type: "response.output_item.added", OutputIndex: 4, Item: &ResponsesOutput{Type: "custom_tool_call", CallID: "call_patch", Name: "apply_patch"}},
		{Type: "response.custom_tool_call_input.delta", OutputIndex: 4, Delta: "*** Begin"},
		{Type: "response.custom_tool_call_input.done", OutputIndex: 4, Input: "*** Begin Patch\n*** End Patch"},
		{Type: "response.custom_tool_call_input.done", OutputIndex: 4, Input: "*** Begin Patch\n*** End Patch"},
	}
	var input string
	for i := range events {
		for _, chunk := range ResponsesEventToChatChunks(&events[i], state) {
			for _, choice := range chunk.Choices {
				for _, call := range choice.Delta.ToolCalls {
					input += call.Function.Arguments
				}
			}
		}
	}
	require.Empty(t, state.ProtocolError)
	require.Equal(t, "*** Begin Patch\n*** End Patch", input)
	require.Nil(t, ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: "response.custom_tool_call_input.delta", OutputIndex: 4, Delta: "late"}, state))
	require.NotEmpty(t, state.ProtocolError, "an authoritative done must not accept a second tail")
}

func TestBufferedResponseAccumulator_ArgumentDonePreservesCallIdentity(t *testing.T) {
	acc := NewBufferedResponseAccumulator()
	acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.output_item.added", OutputIndex: 0, Item: &ResponsesOutput{Type: "function_call", CallID: "call_original", Name: "lookup"}})
	acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.function_call_arguments.done", OutputIndex: 0, Arguments: `{"city":"杭州"}`})
	for _, item := range []ResponsesOutput{
		{Type: "function_call", CallID: "call_other", Name: "lookup"},
		{Type: "function_call", CallID: "call_original", Name: "other_tool"},
	} {
		resp := &ResponsesResponse{Output: []ResponsesOutput{item}}
		acc.SupplementResponseOutput(resp)
		require.Equal(t, item, resp.Output[0], "array index must not override a contradictory tool identity")
	}
	resp := &ResponsesResponse{Output: []ResponsesOutput{
		{Type: "message", Role: "assistant"},
		{Type: "function_call", CallID: "call_original", Name: "lookup"},
	}}
	acc.SupplementResponseOutput(resp)
	require.Equal(t, `{"city":"杭州"}`, resp.Output[1].Arguments, "match a real call even after the output array reorders")
}

func TestBufferedResponseAccumulator_PartialDeltaDoesNotFillTerminalArguments(t *testing.T) {
	acc := NewBufferedResponseAccumulator()
	acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.output_item.added", OutputIndex: 0, Item: &ResponsesOutput{Type: "function_call", CallID: "call_one", Name: "lookup"}})
	acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.function_call_arguments.delta", OutputIndex: 0, Delta: `{"city":`})
	resp := &ResponsesResponse{Output: []ResponsesOutput{{Type: "function_call", CallID: "call_one", Name: "lookup"}}}
	acc.SupplementResponseOutput(resp)
	require.Empty(t, resp.Output[0].Arguments)
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
