package apicompat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
