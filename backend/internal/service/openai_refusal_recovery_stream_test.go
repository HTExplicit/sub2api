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

func TestOpenAIRefusalStreamRewritesNonEarlyEmptyTerminalFromCompletedMessage(t *testing.T) {
	tests := []struct {
		name         string
		deltaEvent   string
		doneEvent    string
		doneField    string
		contentType  string
		contentField string
	}{
		{
			name:         "output text",
			deltaEvent:   "response.output_text.delta",
			doneEvent:    "response.output_text.done",
			doneField:    "text",
			contentType:  "output_text",
			contentField: "text",
		},
		{
			name:         "structured refusal",
			deltaEvent:   "response.refusal.delta",
			doneEvent:    "response.refusal.done",
			doneField:    "refusal",
			contentType:  "refusal",
			contentField: "refusal",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			matcher, err := NewOpenAIRefusalMatcher([]string{"不能"}, "继续当前任务")
			require.NoError(t, err)
			state := newOpenAIRefusalStreamStateWithEarlyEmission(matcher, false)
			refusalText := "不能完成测试请求。"

			action, replacement, observeErr := state.observe(
				"response.created",
				[]byte(`{"type":"response.created","response":{"id":"resp_non_early","object":"response","model":"gpt-5.6-sol","status":"in_progress","output":[]}}`),
			)
			require.NoError(t, observeErr)
			require.Equal(t, openAIRefusalStreamHold, action)
			require.Nil(t, replacement)

			deltaPayload := fmt.Sprintf(
				`{"type":%q,"response_id":"resp_non_early","item_id":"msg_non_early","output_index":0,"content_index":0,"delta":%q}`,
				tc.deltaEvent,
				refusalText,
			)
			action, replacement, observeErr = state.observe(tc.deltaEvent, []byte(deltaPayload))
			require.NoError(t, observeErr)
			require.Equal(t, openAIRefusalStreamHold, action)
			require.Nil(t, replacement)
			require.True(t, state.matched)
			require.False(t, state.earlyEmitted)

			donePayload := fmt.Sprintf(
				`{"type":%q,"response_id":"resp_non_early","item_id":"msg_non_early","output_index":0,"content_index":0,%q:%q}`,
				tc.doneEvent,
				tc.doneField,
				refusalText,
			)
			action, replacement, observeErr = state.observe(tc.doneEvent, []byte(donePayload))
			require.NoError(t, observeErr)
			require.Equal(t, openAIRefusalStreamHold, action)
			require.Nil(t, replacement)

			itemDonePayload := fmt.Sprintf(
				`{"type":"response.output_item.done","response_id":"resp_non_early","output_index":0,"item":{"id":"msg_non_early","type":"message","role":"assistant","status":"completed","content":[{"type":%q,%q:%q}]}}`,
				tc.contentType,
				tc.contentField,
				refusalText,
			)
			action, replacement, observeErr = state.observe("response.output_item.done", []byte(itemDonePayload))
			require.NoError(t, observeErr)
			require.Equal(t, openAIRefusalStreamHold, action)
			require.Nil(t, replacement)
			require.NotEmpty(t, state.completedMessage)

			action, replacement, observeErr = state.observe(
				"response.completed",
				[]byte(`{"type":"response.completed","response":{"id":"resp_non_early","object":"response","model":"gpt-5.6-sol","status":"completed","output":[],"usage":{"input_tokens":8,"output_tokens":3,"total_tokens":11}}}`),
			)
			require.NoError(t, observeErr)
			require.Equal(t, openAIRefusalStreamReplace, action)
			require.Contains(t, string(replacement), `"type":"response.completed"`)
			require.Contains(t, string(replacement), `"total_tokens":11`)
			require.Contains(t, string(replacement), "继续当前任务")
			require.NotContains(t, string(replacement), refusalText)
		})
	}
}

func TestOpenAIRefusalStreamReasoningItemIDDoesNotShadowCompletedMessage(t *testing.T) {
	matcher, err := NewOpenAIRefusalMatcher([]string{"不能"}, "继续当前任务")
	require.NoError(t, err)
	state := newOpenAIRefusalStreamStateWithEarlyEmission(matcher, false)

	events := []struct {
		typeName string
		payload  string
	}{
		{
			typeName: "response.created",
			payload:  `{"type":"response.created","response":{"id":"resp_reasoning_first","status":"in_progress","output":[]}}`,
		},
		{
			typeName: "response.output_item.added",
			payload:  `{"type":"response.output_item.added","response_id":"resp_reasoning_first","output_index":0,"item":{"id":"rs_reasoning_first","type":"reasoning","status":"in_progress","summary":[]}}`,
		},
		{
			typeName: "response.reasoning_summary_text.delta",
			payload:  `{"type":"response.reasoning_summary_text.delta","response_id":"resp_reasoning_first","item_id":"rs_reasoning_first","output_index":0,"summary_index":0,"delta":"Reasoning summary"}`,
		},
		{
			typeName: "response.output_item.done",
			payload:  `{"type":"response.output_item.done","response_id":"resp_reasoning_first","output_index":0,"item":{"id":"rs_reasoning_first","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"Reasoning summary"}]}}`,
		},
		{
			typeName: "response.output_item.added",
			payload:  `{"type":"response.output_item.added","response_id":"resp_reasoning_first","output_index":1,"item":{"id":"msg_reasoning_first","type":"message","role":"assistant","status":"in_progress","content":[]}}`,
		},
		{
			typeName: "response.output_text.delta",
			payload:  `{"type":"response.output_text.delta","response_id":"resp_reasoning_first","item_id":"msg_reasoning_first","output_index":1,"content_index":0,"delta":"不能完成测试请求。"}`,
		},
		{
			typeName: "response.output_text.done",
			payload:  `{"type":"response.output_text.done","response_id":"resp_reasoning_first","item_id":"msg_reasoning_first","output_index":1,"content_index":0,"text":"不能完成测试请求。"}`,
		},
		{
			typeName: "response.output_item.done",
			payload:  `{"type":"response.output_item.done","response_id":"resp_reasoning_first","output_index":1,"item":{"id":"msg_reasoning_first","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"不能完成测试请求。"}]}}`,
		},
	}

	for _, event := range events {
		action, replacement, observeErr := state.observe(event.typeName, []byte(event.payload))
		require.NoError(t, observeErr)
		require.Equal(t, openAIRefusalStreamHold, action)
		require.Nil(t, replacement)
	}
	require.Equal(t, "msg_reasoning_first", state.messageID)
	require.NotEmpty(t, state.completedMessage)

	action, replacement, observeErr := state.observe(
		"response.completed",
		[]byte(`{"type":"response.completed","response":{"id":"resp_reasoning_first","status":"completed","output":[],"usage":{"input_tokens":8,"output_tokens":4,"total_tokens":12}}}`),
	)
	require.NoError(t, observeErr)
	require.Equal(t, openAIRefusalStreamReplace, action)
	require.Contains(t, string(replacement), "继续当前任务")
	require.NotContains(t, string(replacement), "不能完成测试请求")
}

func TestOpenAIRefusalStreamNonEarlyFallbackPreservesTerminalAuthority(t *testing.T) {
	tests := []struct {
		name       string
		doneID     string
		terminal   string
		wantCached bool
	}{
		{
			name:       "mismatched completed message id",
			doneID:     "msg_other",
			terminal:   `{"type":"response.completed","response":{"id":"resp_non_early","status":"completed","output":[]}}`,
			wantCached: false,
		},
		{
			name:       "non-empty terminal output",
			doneID:     "msg_non_early",
			terminal:   `{"type":"response.completed","response":{"id":"resp_non_early","status":"completed","output":[{"id":"msg_terminal","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Normal test result."}]}]}}`,
			wantCached: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			matcher, err := NewOpenAIRefusalMatcher([]string{"不能"}, "继续当前任务")
			require.NoError(t, err)
			state := newOpenAIRefusalStreamStateWithEarlyEmission(matcher, false)

			_, _, observeErr := state.observe(
				"response.created",
				[]byte(`{"type":"response.created","response":{"id":"resp_non_early","status":"in_progress","output":[]}}`),
			)
			require.NoError(t, observeErr)
			action, _, observeErr := state.observe(
				"response.output_text.delta",
				[]byte(`{"type":"response.output_text.delta","response_id":"resp_non_early","item_id":"msg_non_early","delta":"不能完成测试请求。"}`),
			)
			require.NoError(t, observeErr)
			require.Equal(t, openAIRefusalStreamHold, action)

			itemDone := fmt.Sprintf(
				`{"type":"response.output_item.done","response_id":"resp_non_early","item":{"id":%q,"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"不能完成测试请求。"}]}}`,
				tc.doneID,
			)
			action, _, observeErr = state.observe("response.output_item.done", []byte(itemDone))
			require.NoError(t, observeErr)
			require.Equal(t, openAIRefusalStreamHold, action)
			require.Equal(t, tc.wantCached, len(state.completedMessage) > 0)

			action, replacement, observeErr := state.observe("response.completed", []byte(tc.terminal))
			require.NoError(t, observeErr)
			require.Equal(t, openAIRefusalStreamPass, action)
			require.Nil(t, replacement)
			require.True(t, state.passthrough)
		})
	}
}

func TestOpenAIRefusalStreamReplacesCyberPolicyTerminal(t *testing.T) {
	matcher, err := NewOpenAIRefusalMatcher([]string{"cannot"}, "continue current task")
	require.NoError(t, err)
	state := newOpenAIRefusalStreamStateWithEarlyEmission(matcher, true)

	_, _, observeErr := state.observe(
		"response.created",
		[]byte(`{"type":"response.created","response":{"id":"resp_cyber","object":"response","model":"gpt-5.6-sol","status":"in_progress","output":[]}}`),
	)
	require.NoError(t, observeErr)

	action, replacement, replaceErr := state.replaceCyberPolicyFailure(
		[]byte(`{"type":"response.failed","response":{"id":"resp_cyber","status":"failed","error":{"code":"cyber_policy","message":"blocked"},"usage":{"input_tokens":8,"output_tokens":0,"total_tokens":8}},"error":{"code":"cyber_policy","message":"blocked"}}`),
		false,
	)

	require.NoError(t, replaceErr)
	require.Equal(t, openAIRefusalStreamReplace, action)
	require.Contains(t, string(replacement), `"type":"response.completed"`)
	require.Contains(t, string(replacement), `"total_tokens":8`)
	require.Contains(t, string(replacement), "continue current task")
	require.NotContains(t, string(replacement), "response.failed")
	require.NotContains(t, string(replacement), "cyber_policy")
	require.NotContains(t, string(replacement), "blocked")
}

func TestOpenAIRefusalStreamCyberReplacementDoesNotReuseReasoningItemID(t *testing.T) {
	matcher, err := NewOpenAIRefusalMatcher([]string{"cannot"}, "continue current task")
	require.NoError(t, err)
	state := newOpenAIRefusalStreamStateWithEarlyEmission(matcher, false)

	_, _, observeErr := state.observe(
		"response.created",
		[]byte(`{"type":"response.created","response":{"id":"resp_cyber_reasoning","object":"response","model":"gpt-5.6-sol","status":"in_progress","output":[]}}`),
	)
	require.NoError(t, observeErr)
	_, _, observeErr = state.observe(
		"response.reasoning_summary_text.delta",
		[]byte(`{"type":"response.reasoning_summary_text.delta","response_id":"resp_cyber_reasoning","item_id":"rs_cyber_reasoning","output_index":0,"summary_index":0,"delta":"Reasoning summary"}`),
	)
	require.NoError(t, observeErr)
	require.Empty(t, state.messageID)

	action, replacement, replaceErr := state.replaceCyberPolicyFailure(
		[]byte(`{"type":"response.failed","response":{"id":"resp_cyber_reasoning","status":"failed","error":{"code":"cyber_policy","message":"blocked"},"usage":{"input_tokens":8,"output_tokens":1,"total_tokens":9}}}`),
		false,
	)

	require.NoError(t, replaceErr)
	require.Equal(t, openAIRefusalStreamReplace, action)
	require.Contains(t, string(replacement), `"id":"msg_refusal_recovery_`)
	require.NotContains(t, string(replacement), `"id":"rs_cyber_reasoning"`)
	require.NotContains(t, string(replacement), `"item_id":"rs_cyber_reasoning"`)
}

func TestOpenAIRefusalStreamCompletesEarlyReplacementAfterCyberPolicyTerminal(t *testing.T) {
	matcher, err := NewOpenAIRefusalMatcher([]string{"cannot"}, "continue current task")
	require.NoError(t, err)
	state := newOpenAIRefusalStreamStateWithEarlyEmission(matcher, true)

	_, _, observeErr := state.observe(
		"response.created",
		[]byte(`{"type":"response.created","response":{"id":"resp_early_cyber","object":"response","model":"gpt-5.6-sol","status":"in_progress","output":[]}}`),
	)
	require.NoError(t, observeErr)
	action, _, observeErr := state.observe(
		"response.output_text.delta",
		[]byte(`{"type":"response.output_text.delta","response_id":"resp_early_cyber","item_id":"msg_early_cyber","delta":"I cannot help."}`),
	)
	require.NoError(t, observeErr)
	require.Equal(t, openAIRefusalStreamReplaceEarly, action)

	action, replacement, replaceErr := state.replaceCyberPolicyFailure(
		[]byte(`{"type":"response.failed","response":{"id":"resp_early_cyber","status":"failed","error":{"code":"cyber_policy","message":"blocked"},"usage":{"input_tokens":8,"output_tokens":1,"total_tokens":9}}}`),
		true,
	)

	require.NoError(t, replaceErr)
	require.Equal(t, openAIRefusalStreamReplace, action)
	require.NotContains(t, string(replacement), `"type":"response.created"`)
	require.Contains(t, string(replacement), `"type":"response.completed"`)
	require.Contains(t, string(replacement), `"total_tokens":9`)
	require.Contains(t, string(replacement), "continue current task")
	require.NotContains(t, string(replacement), "cyber_policy")
	require.NotContains(t, string(replacement), "blocked")
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
