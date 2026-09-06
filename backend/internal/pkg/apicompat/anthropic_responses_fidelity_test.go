package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnthropicResponsesFidelity_OpaqueSignature(t *testing.T) {
	for _, tc := range []struct {
		name      string
		model     string
		signature string
	}{
		{"codex", "gpt-6-astra", "gAAAA-codex-state"},
		{"sol", "gpt-5.6-sol", "gAAAA-sol-state"},
		{"compatible", "grok-4.5", "opaque-provider-state"},
		{"unknown_format", "custom-compatible", "new-format:state"},
		{"opaque_whitespace", "gpt-6-astra", "\t gAAAA-state \n"},
		{"empty", "gpt-6-astra", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content, err := json.Marshal([]AnthropicContentBlock{
				{Type: "thinking", Thinking: "Visible summary, not assistant speech.", Signature: tc.signature},
				{Type: "text", Text: "The answer."},
			})
			require.NoError(t, err)
			req := &AnthropicRequest{
				Model: tc.model,
				Messages: []AnthropicMessage{
					{Role: "assistant", Content: content},
				},
			}
			original := append([]byte(nil), req.Messages[0].Content...)
			converted, err := AnthropicToResponses(req)
			require.NoError(t, err)
			require.Equal(t, original, []byte(req.Messages[0].Content), "conversion must not mutate caller-owned history")

			var items []ResponsesInputItem
			require.NoError(t, json.Unmarshal(converted.Input, &items))
			require.Len(t, items, 2)
			require.Equal(t, "reasoning", items[0].Type)
			require.Equal(t, tc.signature, items[0].EncryptedContent)
			require.Equal(t, []ResponsesSummary{{Type: "summary_text", Text: "Visible summary, not assistant speech."}}, items[0].Summary)
			require.Empty(t, items[0].Content)
			require.Equal(t, "assistant", items[len(items)-1].Role)
			require.JSONEq(t, `[{"type":"output_text","text":"The answer."}]`, string(items[len(items)-1].Content))
		})
	}
}

func TestAnthropicResponsesFidelity_EmptyThinkingDoesNotInventState(t *testing.T) {
	items, err := anthropicAssistantToResponses(json.RawMessage(`[{"type":"thinking","thinking":"","signature":""}]`))
	require.NoError(t, err)
	require.Empty(t, items)
}

func TestAnthropicResponsesFidelity_ReasoningSummaryWire(t *testing.T) {
	items := []ResponsesInputItem{
		{Type: "reasoning", EncryptedContent: "gAAAA-signature-only"},
		{Type: "reasoning", EncryptedContent: "state-with-summary", Summary: []ResponsesSummary{{Type: "summary_text", Text: "summary"}}},
		{Type: "message", Role: "assistant", Content: json.RawMessage(`[{"type":"output_text","text":"answer"}]`)},
		{Type: "function_call", CallID: "call_1", Name: "read", Arguments: "{}"},
		{Type: "function_call_output", CallID: "call_1", Output: "result"},
	}

	raw, err := json.Marshal(items)
	require.NoError(t, err)
	var wire []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &wire))
	require.JSONEq(t, `[]`, string(wire[0]["summary"]))
	require.JSONEq(t, `[{"type":"summary_text","text":"summary"}]`, string(wire[1]["summary"]))
	for _, item := range wire[2:] {
		require.NotContains(t, item, "summary", "summary must not leak into other input item types")
	}

	var replay []ResponsesInputItem
	require.NoError(t, json.Unmarshal(raw, &replay))
	require.Equal(t, []ResponsesSummary{}, replay[0].Summary)
	require.Equal(t, items[1].Summary, replay[1].Summary)
	require.Equal(t, items[2:], replay[2:])
}

func TestAnthropicResponsesFidelity_AssistantBlockOrder(t *testing.T) {
	raw := json.RawMessage(`[
		{"type":"text","text":"A"},
		{"type":"text","text":"A2"},
		{"type":"thinking","thinking":"S1","signature":"gAAAA-one"},
		{"type":"text","text":"B"},
		{"type":"tool_use","id":"toolu_one","name":"first","input":{"step":1}},
		{"type":"text","text":"C"},
		{"type":"thinking","thinking":"S2","signature":"opaque-two"},
		{"type":"tool_use","id":"fc_call_two","name":"second"},
		{"type":"text","text":"D"}
	]`)
	items, err := anthropicAssistantToResponses(raw)
	require.NoError(t, err)
	require.Len(t, items, 8)
	types := make([]string, len(items))
	for i, item := range items {
		types[i] = item.Type
	}
	require.Equal(t, []string{"message", "reasoning", "message", "function_call", "message", "reasoning", "function_call", "message"}, types)
	require.JSONEq(t, `[{"type":"output_text","text":"A"},{"type":"output_text","text":"A2"}]`, string(items[0].Content))
	require.JSONEq(t, `[{"type":"output_text","text":"B"}]`, string(items[2].Content))
	require.JSONEq(t, `[{"type":"output_text","text":"C"}]`, string(items[4].Content))
	require.JSONEq(t, `[{"type":"output_text","text":"D"}]`, string(items[7].Content))
	require.Equal(t, "gAAAA-one", items[1].EncryptedContent)
	require.Equal(t, []ResponsesSummary{{Type: "summary_text", Text: "S1"}}, items[1].Summary)
	require.Equal(t, "opaque-two", items[5].EncryptedContent)
	require.Equal(t, []ResponsesSummary{{Type: "summary_text", Text: "S2"}}, items[5].Summary)
	require.Equal(t, "toolu_one", items[3].CallID)
	require.Equal(t, "first", items[3].Name)
	require.JSONEq(t, `{"step":1}`, items[3].Arguments)
	require.Equal(t, "fc_call_two", items[6].CallID)
	require.Equal(t, "second", items[6].Name)
	require.Equal(t, "{}", items[6].Arguments)
}

func TestAnthropicResponsesFidelity_UserBlockOrder(t *testing.T) {
	raw := json.RawMessage(`[
		{"type":"text","text":"before"},
		{"type":"tool_result","tool_use_id":"toolu_one","content":[
			{"type":"text","text":"result one"},
			{"type":"image","source":{"type":"base64","media_type":"image/png","data":"ONE"}}
		]},
		{"type":"text","text":"middle"},
		{"type":"tool_result","tool_use_id":"fc_call_two","content":[
			{"type":"image","source":{"type":"base64","media_type":"image/png","data":"TWO"}}
		]},
		{"type":"image","source":{"type":"base64","media_type":"image/png","data":"USER"}},
		{"type":"text","text":"after"}
	]`)
	items, err := anthropicUserToResponses(raw)
	require.NoError(t, err)
	require.Len(t, items, 5)
	require.JSONEq(t, `[{"type":"input_text","text":"before"}]`, string(items[0].Content))
	require.Equal(t, "function_call_output", items[1].Type)
	require.Equal(t, "toolu_one", items[1].CallID)
	require.Equal(t, "result one", items[1].Output)
	require.JSONEq(t, `[
		{"type":"input_image","image_url":"data:image/png;base64,ONE"},
		{"type":"input_text","text":"middle"}
	]`, string(items[2].Content))
	require.Equal(t, "function_call_output", items[3].Type)
	require.Equal(t, "fc_call_two", items[3].CallID)
	require.Equal(t, "(empty)", items[3].Output)
	require.JSONEq(t, `[
		{"type":"input_image","image_url":"data:image/png;base64,TWO"},
		{"type":"input_image","image_url":"data:image/png;base64,USER"},
		{"type":"input_text","text":"after"}
	]`, string(items[4].Content))
}

func TestAnthropicResponsesFidelity_ResponseRoundTrip(t *testing.T) {
	upstream := &ResponsesResponse{
		ID: "resp_fixture", Model: "gpt-6-astra", Status: "completed",
		Output: []ResponsesOutput{
			{Type: "message", Role: "assistant", Content: []ResponsesContentPart{{Type: "output_text", Text: "before"}}},
			{Type: "reasoning", EncryptedContent: "gAAAA-first", Summary: []ResponsesSummary{{Type: "summary_text", Text: "summary one"}}},
			{Type: "message", Role: "assistant", Content: []ResponsesContentPart{{Type: "output_text", Text: "between"}}},
			{Type: "function_call", CallID: "toolu_1", Name: "read", Arguments: `{"key":"a"}`},
			{Type: "reasoning", EncryptedContent: "\t gAAAA-second \n"},
		},
	}
	response := ResponsesToAnthropic(upstream, "client-alias")
	content, err := json.Marshal(response.Content)
	require.NoError(t, err)
	request, err := AnthropicToResponses(&AnthropicRequest{
		Model: "gpt-6-astra",
		Messages: []AnthropicMessage{
			{Role: "assistant", Content: content},
			{Role: "user", Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"toolu_1","content":"result"}]`)},
		},
	})
	require.NoError(t, err)
	var items []ResponsesInputItem
	require.NoError(t, json.Unmarshal(request.Input, &items))
	require.Len(t, items, 6)
	for i, original := range upstream.Output {
		require.Equal(t, original.Type, items[i].Type, "output %d must retain its position", i)
		if original.Type == "reasoning" {
			require.Equal(t, original.EncryptedContent, items[i].EncryptedContent)
		}
	}
	require.Equal(t, upstream.Output[1].Summary, items[1].Summary)
	require.Empty(t, items[4].Summary)
	require.Equal(t, "toolu_1", items[3].CallID)
	require.Equal(t, items[3].CallID, items[5].CallID)
	require.Equal(t, "result", items[5].Output)
}

func TestAnthropicResponsesFidelity_StreamSignatureRoundTrip(t *testing.T) {
	const signature = "\t gAAAA-opaque-state \n"
	for _, tc := range []struct {
		name, doneSignature, doneStatus, wantSignature string
	}{
		{"completed_done_replaces_partial_added", signature, "completed", signature},
		{"missing_done_signature_does_not_replay_partial_added", "", "completed", ""},
		{"incomplete_done_is_not_replayed", signature, "incomplete", ""},
		{"in_progress_done_is_not_replayed", signature, "in_progress", ""},
		{"done_without_optional_status", signature, "", signature},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := NewResponsesEventToAnthropicState()
			added := &ResponsesOutput{Type: "reasoning", ID: "rs_fixture", EncryptedContent: "gAAAA-incomplete-prefix"}
			done := &ResponsesOutput{Type: "reasoning", ID: "rs_fixture", Status: tc.doneStatus, EncryptedContent: tc.doneSignature}
			var block AnthropicContentBlock
			for _, event := range []*ResponsesStreamEvent{
				{Type: "response.output_item.added", OutputIndex: 0, Item: added},
				{Type: "response.reasoning_summary_text.delta", OutputIndex: 0, Delta: "visible summary"},
				{Type: "response.reasoning_summary_text.done", OutputIndex: 0},
				{Type: "response.output_item.done", OutputIndex: 0, Item: done},
			} {
				for _, converted := range ResponsesEventToAnthropicEvents(event, state) {
					if converted.Type == "content_block_start" {
						block = *converted.ContentBlock
					}
					if converted.Delta != nil {
						block.Thinking += converted.Delta.Thinking
						block.Signature += converted.Delta.Signature
					}
				}
			}
			require.Equal(t, "thinking", block.Type)
			require.Equal(t, tc.wantSignature, block.Signature)
			raw, err := json.Marshal([]AnthropicContentBlock{block})
			require.NoError(t, err)
			items, err := anthropicAssistantToResponses(raw)
			require.NoError(t, err)
			require.Len(t, items, 1)
			require.Equal(t, tc.wantSignature, items[0].EncryptedContent)
			require.Equal(t, []ResponsesSummary{{Type: "summary_text", Text: "visible summary"}}, items[0].Summary)
		})
	}
}
