package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesAgentMessageCapabilityMatchesConvertibleContent(t *testing.T) {
	for _, tc := range []struct {
		name, content, wantText string
	}{
		{"string", `"fixture task"`, "fixture task"},
		{"ordered_parts", `[{"type":"input_text","text":"Task:\n"},{"type":"text","text":"fixture payload\nend"}]`, "Task:\nfixture payload\nend"},
		{"empty_parts", `[]`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := json.RawMessage(`[{"type":"message","role":"user","content":"initial"},{"type":"agent_message","content":` + tc.content + `}]`)
			require.False(t, ResponsesInputRequiresNative(input), "lossless agent history must remain eligible for Chat-only accounts")
			out, err := ResponsesToChatCompletionsRequest(&ResponsesRequest{Model: "fixture", Input: input})
			require.NoError(t, err)
			if tc.wantText == "" {
				require.Len(t, out.Messages, 1)
				return
			}
			require.Len(t, out.Messages, 2)
			require.Equal(t, "user", out.Messages[1].Role)
			var text string
			require.NoError(t, json.Unmarshal(out.Messages[1].Content, &text))
			require.Equal(t, tc.wantText, text, "the source converter must preserve every supported fragment in order")
		})
	}
}

func TestResponsesAgentMessageCapabilityKeepsLossyContentNative(t *testing.T) {
	for _, tc := range []struct{ name, item string }{
		{"image_part", `{"type":"agent_message","content":[{"type":"input_text","text":"inspect this"},{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}`},
		{"unknown_part", `{"type":"agent_message","content":[{"type":"future_agent_state","state":"fixture"}]}`},
		{"object_content", `{"type":"agent_message","content":{"text":"fixture"}}`},
		{"non_string_text", `{"type":"agent_message","content":[{"type":"input_text","text":{"value":"fixture"}}]}`},
		{"encrypted_only", `{"type":"agent_message","content":[{"type":"encrypted_content","encrypted_content":"opaque-fixture"}]}`},
		{"encrypted_with_plaintext", `{"type":"agent_message","content":[{"type":"input_text","text":"Task:"},{"type":"encrypted_content","encrypted_content":"opaque-fixture"}]}`},
		{"opaque_body_object", `{"type":"agent_message","content":[{"type":"encrypted_content","encrypted_content":{"ciphertext":"opaque-fixture"}}]}`},
		{"unknown_item", `{"type":"future_agent_message","content":"fixture"}`},
		{"native_only_item", `{"type":"web_search_call","id":"ws_fixture"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.True(t, ResponsesInputRequiresNative(json.RawMessage("["+tc.item+"]")), "routing must not permit a converter that discards this content")
		})
	}
}
