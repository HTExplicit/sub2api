package handler

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIWSInitialAccountSwitchReplaySafe(t *testing.T) {
	tests := []struct {
		name            string
		payload         string
		previousCanMove bool
		want            bool
	}{
		{
			name:            "plain first turn",
			payload:         `{"type":"response.create","model":"gpt-5.1","input":"hello"}`,
			previousCanMove: true,
			want:            true,
		},
		{
			name:            "continuation anchor",
			payload:         `{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_1","input":"hello"}`,
			previousCanMove: true,
			want:            false,
		},
		{
			name:            "tool continuation not movable",
			payload:         `{"type":"response.create","model":"gpt-5.1","input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`,
			previousCanMove: false,
			want:            false,
		},
		{
			name:            "encrypted reasoning state",
			payload:         `{"type":"response.create","model":"gpt-5.1","input":[{"type":"reasoning","encrypted_content":"cipher"}]}`,
			previousCanMove: true,
			want:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, openAIWSInitialAccountSwitchReplaySafe([]byte(tt.payload), tt.previousCanMove))
		})
	}
}

func TestOpenAIWSPreviousResponseCanMove(t *testing.T) {
	require.False(t, openAIWSPreviousResponseCanMove(
		[]byte(`{"type":"response.create","previous_response_id":"resp_1","input":"next"}`),
		"resp_1",
	))
	require.True(t, openAIWSPreviousResponseCanMove(
		[]byte(`{"type":"response.create","previous_response_id":"resp_1","input":[{"type":"function_call","call_id":"call_1","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`),
		"resp_1",
	))
	require.False(t, openAIWSPreviousResponseCanMove(
		[]byte(`{"type":"response.create","previous_response_id":"resp_1","input":[{"type":"reasoning","encrypted_content":"cipher"},{"type":"function_call","call_id":"call_1","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`),
		"resp_1",
	))
}

func TestResetStrictCindyCrossGroupContinuation(t *testing.T) {
	tests := []struct {
		name                  string
		strictCindy           bool
		payload               string
		previousResponseID    string
		currentGroupAccountID int64
		wantReset             bool
	}{
		{
			name:               "missing current-group binding resets plain continuation",
			strictCindy:        true,
			payload:            `{"type":"response.create","previous_response_id":"resp_old_group","input":"continue"}`,
			previousResponseID: "resp_old_group",
			wantReset:          true,
		},
		{
			name:                  "current-group binding preserves continuation",
			strictCindy:           true,
			payload:               `{"type":"response.create","previous_response_id":"resp_current_group","input":"continue"}`,
			previousResponseID:    "resp_current_group",
			currentGroupAccountID: 42,
		},
		{
			name:               "non Cindy request preserves continuation",
			payload:            `{"type":"response.create","previous_response_id":"resp_openai","input":"continue"}`,
			previousResponseID: "resp_openai",
		},
		{
			name:               "complete tool context can rebuild without old binding",
			strictCindy:        true,
			payload:            `{"type":"response.create","previous_response_id":"resp_tool","input":[{"type":"function_call","call_id":"call_1","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`,
			previousResponseID: "resp_tool",
			wantReset:          true,
		},
		{
			name:               "orphan tool output preserves required anchor",
			strictCindy:        true,
			payload:            `{"type":"response.create","previous_response_id":"resp_tool","input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`,
			previousResponseID: "resp_tool",
		},
		{
			name:               "encrypted state preserves required anchor",
			strictCindy:        true,
			payload:            `{"type":"response.create","previous_response_id":"resp_encrypted","input":[{"type":"reasoning","encrypted_content":"cipher"},{"type":"input_text","text":"continue"}]}`,
			previousResponseID: "resp_encrypted",
		},
		{
			name:               "invalid payload is unchanged",
			strictCindy:        true,
			payload:            `{"previous_response_id":`,
			previousResponseID: "resp_invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updated, previousResponseID, reset := resetStrictCindyCrossGroupContinuation(
				tt.strictCindy,
				[]byte(tt.payload),
				tt.previousResponseID,
				tt.currentGroupAccountID,
			)
			require.Equal(t, tt.wantReset, reset)
			if tt.wantReset {
				require.Empty(t, previousResponseID)
				require.False(t, gjson.GetBytes(updated, "previous_response_id").Exists())
				return
			}
			require.Equal(t, tt.previousResponseID, previousResponseID)
			require.Equal(t, []byte(tt.payload), updated)
		})
	}
}
