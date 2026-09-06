package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func agentTerminalFixture(t *testing.T, reason string, reasoningOnly bool) (*ChatCompletionsToResponsesStreamState, []ResponsesStreamEvent) {
	t.Helper()
	state := NewChatCompletionsToResponsesStreamState("test-model")
	delta := map[string]any{"content": "partial"}
	if reasoningOnly {
		delta = map[string]any{"reasoning_content": "reasoning is not a final answer"}
	}
	wire, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": reason}}})
	require.NoError(t, err)
	var chunk ChatCompletionsChunk
	require.NoError(t, json.Unmarshal(wire, &chunk))
	events := ChatCompletionsChunkToResponsesEvents(&chunk, state)
	events = append(events, FinalizeChatCompletionsResponsesStream(state)...)
	return state, events
}

func TestAgentChatTerminalEventMatchesStatus(t *testing.T) {
	for _, tc := range []struct{ reason, event, status, incomplete string }{
		{"stop", "response.completed", "completed", ""},
		{"length", "response.incomplete", "incomplete", "max_output_tokens"},
		{"content_filter", "response.incomplete", "incomplete", "content_filter"},
		{"", "response.failed", "failed", ""},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			state, events := agentTerminalFixture(t, tc.reason, false)
			var terminal []ResponsesStreamEvent
			for _, event := range events {
				if event.Response != nil && event.Response.Status != "in_progress" {
					terminal = append(terminal, event)
				}
			}
			require.Len(t, terminal, 1)
			require.Equal(t, tc.event, terminal[0].Type)
			require.Equal(t, tc.status, terminal[0].Response.Status)
			if tc.incomplete != "" {
				require.NotNil(t, terminal[0].Response.IncompleteDetails)
				require.Equal(t, tc.incomplete, terminal[0].Response.IncompleteDetails.Reason)
			}
			require.Empty(t, FinalizeChatCompletionsResponsesStream(state))
		})
	}
}

func TestAgentReasoningIsNeverSynthesizedIntoAnswer(t *testing.T) {
	_, events := agentTerminalFixture(t, "length", true)
	for _, event := range events {
		require.NotEqual(t, "response.output_text.delta", event.Type)
		if event.Response != nil && event.Response.Status == "incomplete" {
			for _, item := range event.Response.Output {
				require.NotEqual(t, "message", item.Type)
			}
		}
	}
}

func TestAgentChatConversionNeverDropsRequiredFeatures(t *testing.T) {
	for _, toolType := range []string{"web_search", "image_generation", "file_search", "unknown_tool"} {
		t.Run(toolType, func(t *testing.T) {
			_, err := ResponsesToChatCompletionsRequest(&ResponsesRequest{Model: "fixture", Input: json.RawMessage(`"fixture"`), Tools: []ResponsesTool{{Type: toolType}}})
			require.Error(t, err)
		})
	}
	_, err := ResponsesToChatCompletionsRequest(&ResponsesRequest{Model: "fixture", Input: json.RawMessage(`"fixture"`), Tools: []ResponsesTool{{Type: "function", Name: "read"}}, ToolChoice: json.RawMessage(`{"type":"function","name":"missing"}`)})
	require.Error(t, err)
	_, err = ResponsesToChatCompletionsRequest(&ResponsesRequest{Model: "fixture", Input: json.RawMessage(`"fixture"`), PreviousResponseID: "previous_fixture"})
	require.Error(t, err)
	_, err = ResponsesToChatCompletionsRequest(&ResponsesRequest{Model: "fixture", Input: json.RawMessage(`"fixture"`), Tools: []ResponsesTool{{Type: "namespace", Name: "fixture", Tools: []ResponsesTool{{Type: "custom", Name: "exec"}}}}})
	require.Error(t, err)
}

func TestAgentSparseToolIndexesAreCompletedInOutputOrder(t *testing.T) {
	state := NewChatCompletionsToResponsesStreamState("fixture")
	var events []ResponsesStreamEvent
	for _, pair := range []struct {
		index int
		id    string
	}{{7, "call_first"}, {2, "call_second"}} {
		index := pair.index
		chunk := &ChatCompletionsChunk{Choices: []ChatChunkChoice{{Index: 0, Delta: ChatDelta{ToolCalls: []ChatToolCall{{Index: &index, ID: pair.id, Type: "function", Function: ChatFunctionCall{Name: "read", Arguments: `{"path":"fixture"}`}}}}}}}
		events = append(events, ChatCompletionsChunkToResponsesEvents(chunk, state)...)
	}
	state.FinishReason = "tool_calls"
	events = append(events, FinalizeChatCompletionsResponsesStream(state)...)
	done := 0
	for _, event := range events {
		if event.Type == "response.function_call_arguments.done" {
			done++
		}
	}
	require.Equal(t, 2, done)
	terminal := events[len(events)-1]
	require.Len(t, terminal.Response.Output, 2)
	require.Equal(t, "call_first", terminal.Response.Output[0].CallID)
	require.Equal(t, "call_second", terminal.Response.Output[1].CallID)
	require.Equal(t, state.ToolItemIDs[7], terminal.Response.Output[0].ID)
	require.Equal(t, state.ToolItemIDs[2], terminal.Response.Output[1].ID)
}

func TestAgentToolIdentityArrivingLateIsNotInvented(t *testing.T) {
	state := NewChatCompletionsToResponsesStreamState("fixture")
	i := 3
	first := &ChatCompletionsChunk{Choices: []ChatChunkChoice{{Delta: ChatDelta{ToolCalls: []ChatToolCall{{Index: &i, Function: ChatFunctionCall{Arguments: `{"path":`}}}}}}}
	for _, event := range ChatCompletionsChunkToResponsesEvents(first, state) {
		if event.Item != nil {
			require.NotEqual(t, "function_call", event.Item.Type)
		}
	}
	last := &ChatCompletionsChunk{Choices: []ChatChunkChoice{{Delta: ChatDelta{ToolCalls: []ChatToolCall{{Index: &i, ID: "call_real", Function: ChatFunctionCall{Name: "read", Arguments: `"fixture"}`}}}}}}}
	events := ChatCompletionsChunkToResponsesEvents(last, state)
	state.FinishReason = "tool_calls"
	events = append(events, FinalizeChatCompletionsResponsesStream(state)...)
	for _, event := range events {
		if event.Item != nil && event.Item.Type == "function_call" {
			require.Equal(t, "call_real", event.Item.CallID)
			require.Equal(t, "read", event.Item.Name)
		}
	}
}
