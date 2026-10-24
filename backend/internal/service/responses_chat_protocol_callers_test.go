package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesAgentMessageRoutingPreservesChatEligibility(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		native     bool
	}{
		{"supported_agent_task", `{"input":[{"type":"agent_message","content":[{"type":"input_text","text":"Task:\n"},{"type":"text","text":"fixture task"}]}]}`, false},
		{"encrypted_agent_task", `{"input":[{"type":"agent_message","content":[{"type":"encrypted_content","encrypted_content":"opaque-fixture"}]}]}`, true},
		{"mixed_encrypted_agent_task", `{"input":[{"type":"agent_message","content":[{"type":"input_text","text":"Task:"},{"type":"encrypted_content","encrypted_content":"opaque-fixture"}]}]}`, true},
		{"agent_image", `{"input":[{"type":"agent_message","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}]}`, true},
		{"unknown_item", `{"input":[{"type":"future_agent_state","content":"fixture"}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.native, OpenAIResponsesRequireNativeUpstream([]byte(tc.body)))
		})
	}
}
