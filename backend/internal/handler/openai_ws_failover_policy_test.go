package handler

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIWSInitialAccountSwitchReplaySafe(t *testing.T) {
	tests := []struct {
		name            string
		payload         string
		previousCanMove bool
		strictCindy     bool
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
		{
			name:            "single object encrypted reasoning state",
			payload:         `{"type":"response.create","model":"gpt-5.1","input":{"type":"reasoning","encrypted_content":"cipher"}}`,
			previousCanMove: true,
			want:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, openAIWSInitialAccountSwitchReplaySafe([]byte(tt.payload), tt.previousCanMove, tt.strictCindy))
		})
	}
}

func TestOpenAIWSPreviousResponseCanMove(t *testing.T) {
	require.False(t, openAIWSPreviousResponseCanMove(
		[]byte(`{"type":"response.create","previous_response_id":"resp_1","input":"next"}`),
		"resp_1",
		false,
	))
	require.True(t, openAIWSPreviousResponseCanMove(
		[]byte(`{"type":"response.create","previous_response_id":"resp_1","input":[{"type":"function_call","call_id":"call_1","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`),
		"resp_1",
		false,
	))
	require.False(t, openAIWSPreviousResponseCanMove(
		[]byte(`{"type":"response.create","previous_response_id":"resp_1","input":[{"type":"function_call","call_id":"call_1","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`),
		"resp_1",
		true,
	))
	require.False(t, openAIWSPreviousResponseCanMove(
		[]byte(`{"type":"response.create","previous_response_id":"resp_1","input":[{"type":"reasoning","encrypted_content":"cipher"},{"type":"function_call","call_id":"call_1","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`),
		"resp_1",
		false,
	))
	require.False(t, openAIWSPreviousResponseCanMove(
		[]byte(`{"type":"response.create","previous_response_id":"","input":[{"type":"item_reference","id":"fc_1"}]}`),
		"",
		true,
	))
}

func TestOpenAIWSLegacyLaxaReplaySafe(t *testing.T) {
	require.True(t, openAIWSLegacyLaxaReplaySafe(
		[]byte(`{"type":"response.create","model":"gpt-5.6-luna","input":"hello"}`),
	))
	require.False(t, openAIWSLegacyLaxaReplaySafe(
		[]byte(`{"type":"response.create","model":"gpt-5.6-luna","previous_response_id":"resp_1","input":"hello"}`),
	))
	require.False(t, openAIWSLegacyLaxaReplaySafe(
		[]byte(`{"type":"response.create","model":"gpt-5.6-luna","input":[{"type":"reasoning","encrypted_content":"opaque"}]}`),
	))
}
