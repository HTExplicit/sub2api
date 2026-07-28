package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIRefusalStreamFailsOpenWhenBufferExceedsOneMiB(t *testing.T) {
	matcher, err := NewOpenAIRefusalMatcher([]string{"cannot"}, "continue")
	require.NoError(t, err)
	state := newOpenAIRefusalStreamState(matcher)

	require.True(t, state.reserveLine(strings.Repeat("x", maxOpenAIRefusalStreamBufferBytes-1)))
	require.False(t, state.reserveLine(""))
	action, replacement, observeErr := state.observe(
		"response.output_text.delta",
		[]byte(`{"type":"response.output_text.delta","delta":"I cannot help."}`),
	)

	require.NoError(t, observeErr)
	require.Equal(t, openAIRefusalStreamPass, action)
	require.Nil(t, replacement)
	require.True(t, state.passthrough)
}

func TestOpenAIRefusalStreamPassesThroughToolAndImageOutputAfterKeywordMatch(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		payload   string
	}{
		{
			name:      "function call",
			eventType: "response.output_item.added",
			payload:   `{"type":"response.output_item.added","item":{"type":"function_call"}}`,
		},
		{
			name:      "image generation",
			eventType: "response.output_item.added",
			payload:   `{"type":"response.output_item.added","item":{"type":"image_generation_call"}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			matcher, err := NewOpenAIRefusalMatcher([]string{"cannot"}, "continue")
			require.NoError(t, err)
			state := newOpenAIRefusalStreamState(matcher)
			action, _, observeErr := state.observe(
				"response.output_text.delta",
				[]byte(`{"type":"response.output_text.delta","delta":"I cannot help."}`),
			)
			require.NoError(t, observeErr)
			require.Equal(t, openAIRefusalStreamHold, action)

			action, replacement, observeErr := state.observe(tc.eventType, []byte(tc.payload))

			require.NoError(t, observeErr)
			require.Equal(t, openAIRefusalStreamPass, action)
			require.Nil(t, replacement)
			require.True(t, state.passthrough)
		})
	}
}

func TestOpenAIRefusalStreamKeepsCheckingAcrossSingleLineBreak(t *testing.T) {
	matcher, err := NewOpenAIRefusalMatcher([]string{"cannot"}, "continue")
	require.NoError(t, err)
	state := newOpenAIRefusalStreamState(matcher)

	action, _, observeErr := state.observe(
		"response.output_text.delta",
		[]byte(`{"type":"response.output_text.delta","delta":"Normal intro\n"}`),
	)
	require.NoError(t, observeErr)
	require.Equal(t, openAIRefusalStreamHold, action)

	action, _, observeErr = state.observe(
		"response.output_text.delta",
		[]byte(`{"type":"response.output_text.delta","delta":"I cannot help."}`),
	)
	require.NoError(t, observeErr)
	require.Equal(t, openAIRefusalStreamHold, action)
	require.True(t, state.matched)
}
