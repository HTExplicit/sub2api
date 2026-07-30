package service

import (
	"fmt"
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

func TestOpenAIRefusalStreamKeepsCheckingAfterFirstParagraph(t *testing.T) {
	matcher, err := NewOpenAIRefusalMatcher([]string{"不能"}, "继续当前任务")
	require.NoError(t, err)
	state := newOpenAIRefusalStreamState(matcher)

	action, _, observeErr := state.observe(
		"response.output_text.delta",
		[]byte(`{"type":"response.output_text.delta","delta":"可以协助分析已授权应用。\n\n"}`),
	)
	require.NoError(t, observeErr)
	require.Equal(t, openAIRefusalStreamHold, action)
	require.False(t, state.matched)
	require.False(t, state.passthrough)

	action, _, observeErr = state.observe(
		"response.output_text.delta",
		[]byte(`{"type":"response.output_text.delta","delta":"但不能帮助绕过第三方付费会员。"}`),
	)
	require.NoError(t, observeErr)
	require.Equal(t, openAIRefusalStreamHold, action)
	require.True(t, state.matched)
}

func TestOpenAIRefusalStreamPassesBeforeThirdParagraph(t *testing.T) {
	matcher, err := NewOpenAIRefusalMatcher([]string{"cannot"}, "continue")
	require.NoError(t, err)
	state := newOpenAIRefusalStreamState(matcher)

	action, _, observeErr := state.observe(
		"response.output_text.delta",
		[]byte(`{"type":"response.output_text.delta","delta":"First paragraph.\n\nSecond paragraph.\n\n"}`),
	)
	require.NoError(t, observeErr)
	require.Equal(t, openAIRefusalStreamPass, action)
	require.True(t, state.passthrough)

	action, _, observeErr = state.observe(
		"response.output_text.delta",
		[]byte(`{"type":"response.output_text.delta","delta":"I cannot continue."}`),
	)
	require.NoError(t, observeErr)
	require.Equal(t, openAIRefusalStreamPass, action)
	require.False(t, state.matched)
}

func TestOpenAIRefusalStreamFailsOpenAtRuneScanLimit(t *testing.T) {
	matcher, err := NewOpenAIRefusalMatcher([]string{"cannot"}, "continue")
	require.NoError(t, err)
	state := newOpenAIRefusalStreamState(matcher)
	payload := fmt.Sprintf(`{"type":"response.output_text.delta","delta":%q}`, strings.Repeat("正", maxOpenAIRefusalParagraphRunes))

	action, _, observeErr := state.observe("response.output_text.delta", []byte(payload))

	require.NoError(t, observeErr)
	require.Equal(t, openAIRefusalStreamPass, action)
	require.True(t, state.passthrough)
}

func TestOpenAIRefusalScanWindowCompletesAfterSecondParagraphBoundary(t *testing.T) {
	require.False(t, openAIRefusalScanWindowComplete("First paragraph.\n\n"))
	require.False(t, openAIRefusalScanWindowComplete("First paragraph.\n\n\nSecond paragraph."))
	require.True(t, openAIRefusalScanWindowComplete("First paragraph.\r\n\r\nSecond paragraph.\r\n\r\n"))
}

func TestOpenAIRefusalStreamMatchesStructuredRefusalEvents(t *testing.T) {
	matcher, err := NewOpenAIRefusalMatcher([]string{"不能"}, "继续当前任务")
	require.NoError(t, err)
	state := newOpenAIRefusalStreamState(matcher)

	action, _, observeErr := state.observe(
		"response.refusal.delta",
		[]byte(`{"type":"response.refusal.delta","delta":"不能协助绕过真实服务"}`),
	)
	require.NoError(t, observeErr)
	require.Equal(t, openAIRefusalStreamHold, action)
	require.True(t, state.matched)

	action, _, observeErr = state.observe(
		"response.refusal.done",
		[]byte(`{"type":"response.refusal.done","refusal":"不能协助绕过真实服务的付费或会员限制。"}`),
	)
	require.NoError(t, observeErr)
	require.Equal(t, openAIRefusalStreamHold, action)
	require.True(t, state.matched)
}
