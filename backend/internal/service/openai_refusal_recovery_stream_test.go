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

func TestOpenAIRefusalStreamEmitsReplacementBeforeTerminalAndCompletesWithUsage(t *testing.T) {
	matcher, err := NewOpenAIRefusalMatcher([]string{"cannot"}, "continue current task")
	require.NoError(t, err)
	state := newOpenAIRefusalStreamStateWithEarlyEmission(matcher, true)

	action, replacement, observeErr := state.observe(
		"response.created",
		[]byte(`{"type":"response.created","response":{"id":"resp_early","object":"response","model":"gpt-5.6-sol","status":"in_progress","output":[]}}`),
	)
	require.NoError(t, observeErr)
	require.Equal(t, openAIRefusalStreamHold, action)
	require.Nil(t, replacement)

	action, replacement, observeErr = state.observe(
		"response.output_text.delta",
		[]byte(`{"type":"response.output_text.delta","response_id":"resp_early","item_id":"msg_early","output_index":0,"content_index":0,"delta":"I cannot help."}`),
	)
	require.NoError(t, observeErr)
	require.Equal(t, openAIRefusalStreamReplaceEarly, action)
	require.True(t, state.earlyEmitted)
	require.Contains(t, string(replacement), `"type":"response.output_text.delta"`)
	require.Contains(t, string(replacement), "continue current task")
	require.NotContains(t, string(replacement), "response.completed")
	require.NotContains(t, string(replacement), "I cannot")

	action, replacement, observeErr = state.observe(
		"response.output_text.done",
		[]byte(`{"type":"response.output_text.done","response_id":"resp_early","item_id":"msg_early","output_index":0,"content_index":0,"text":"I cannot help."}`),
	)
	require.NoError(t, observeErr)
	require.Equal(t, openAIRefusalStreamHold, action)
	require.Nil(t, replacement)

	action, replacement, observeErr = state.observe(
		"response.output_item.done",
		[]byte(`{"type":"response.output_item.done","response_id":"resp_early","output_index":0,"item":{"id":"msg_early","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"I cannot help."}]}}`),
	)
	require.NoError(t, observeErr)
	require.Equal(t, openAIRefusalStreamHold, action)
	require.Nil(t, replacement)

	action, replacement, observeErr = state.observe(
		"response.completed",
		[]byte(`{"type":"response.completed","response":{"id":"resp_early","object":"response","model":"gpt-5.6-sol","status":"completed","output":[],"usage":{"input_tokens":8,"output_tokens":3,"total_tokens":11}}}`),
	)
	require.NoError(t, observeErr)
	require.Equal(t, openAIRefusalStreamReplace, action)
	require.Contains(t, string(replacement), `"type":"response.output_text.done"`)
	require.Contains(t, string(replacement), `"type":"response.completed"`)
	require.Contains(t, string(replacement), `"total_tokens":11`)
	require.Contains(t, string(replacement), "continue current task")
	require.NotContains(t, string(replacement), "I cannot")
}

func TestOpenAIRefusalStreamValidatesCompletedMessageFallback(t *testing.T) {
	tests := []struct {
		name       string
		done       string
		terminal   string
		wantErr    string
		wantCached bool
	}{
		{
			name:       "mismatched message id",
			done:       `{"type":"response.output_item.done","response_id":"resp_early","output_index":0,"item":{"id":"msg_other","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"I cannot help."}]}}`,
			terminal:   `{"type":"response.completed","response":{"id":"resp_early","status":"completed","output":[]}}`,
			wantErr:    "early replacement terminal response is not text-only refusal output",
			wantCached: false,
		},
		{
			name:       "non-empty terminal output",
			done:       `{"type":"response.output_item.done","response_id":"resp_early","output_index":0,"item":{"id":"msg_early","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"I cannot help."}]}}`,
			terminal:   `{"type":"response.completed","response":{"id":"resp_early","status":"completed","output":[{"id":"msg_terminal","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"I cannot help."}]}]}}`,
			wantErr:    "replacement terminal message id does not match early emission",
			wantCached: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			matcher, err := NewOpenAIRefusalMatcher([]string{"cannot"}, "continue current task")
			require.NoError(t, err)
			state := newOpenAIRefusalStreamStateWithEarlyEmission(matcher, true)

			_, _, observeErr := state.observe(
				"response.created",
				[]byte(`{"type":"response.created","response":{"id":"resp_early","status":"in_progress","output":[]}}`),
			)
			require.NoError(t, observeErr)

			action, _, observeErr := state.observe(
				"response.output_text.delta",
				[]byte(`{"type":"response.output_text.delta","response_id":"resp_early","item_id":"msg_early","delta":"I cannot help."}`),
			)
			require.NoError(t, observeErr)
			require.Equal(t, openAIRefusalStreamReplaceEarly, action)

			action, replacement, observeErr := state.observe("response.output_item.done", []byte(tc.done))
			require.NoError(t, observeErr)
			require.Equal(t, openAIRefusalStreamHold, action)
			require.Nil(t, replacement)
			require.Equal(t, tc.wantCached, len(state.completedMessage) > 0)

			action, replacement, observeErr = state.observe("response.completed", []byte(tc.terminal))
			require.EqualError(t, observeErr, tc.wantErr)
			require.Equal(t, openAIRefusalStreamHold, action)
			require.Nil(t, replacement)
		})
	}
}
