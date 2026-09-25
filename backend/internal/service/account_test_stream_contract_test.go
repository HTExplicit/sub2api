//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountConnectionStreamContract(t *testing.T) {
	frame := func(data string) string { return "data: " + data + "\n\n" }
	responseText := frame(`{"type":"response.output_text.delta","delta":"OK"}`)
	claudeText := frame(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"OK"}}`)
	geminiText := frame(`{"candidates":[{"content":{"parts":[{"text":"OK"}]}}]}`)
	chatText := frame(`{"choices":[{"index":0,"delta":{"content":"OK"}}]}`)
	cases := []struct {
		name, protocol, wire string
		want                 error
		limited              bool
	}{
		{"empty", "responses", "", ErrAccountTestIncomplete, false},
		{"blank_text", "responses", frame(`{"type":"response.output_text.delta","delta":" \n"}`) + frame(`{"type":"response.completed"}`), ErrAccountTestEmpty, false},
		{"responses_early_eof", "responses", responseText, ErrAccountTestIncomplete, false},
		{"responses_done_only", "responses", responseText + frame(`[DONE]`), ErrAccountTestIncomplete, false},
		{"responses_complete", "responses", responseText + frame(`{"type":"response.completed","response":{"status":"completed"}}`), nil, false},
		{"responses_done_success", "responses", responseText + frame(`{"type":"response.done","response":{"status":"completed"}}`), nil, false},
		{"responses_done_failed", "responses", responseText + frame(`{"type":"response.done","response":{"status":"failed"}}`), ErrAccountTestTerminal, false},
		{"responses_done_unknown", "responses", responseText + frame(`{"type":"response.done"}`), ErrAccountTestTerminal, false},
		{"responses_max_tokens", "responses", responseText + frame(`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`), nil, true},
		{"responses_contradictory_status", "responses", responseText + frame(`{"type":"response.incomplete","response":{"status":"failed","incomplete_details":{"reason":"max_output_tokens"}}}`), ErrAccountTestTerminal, false},
		{"responses_in_progress_not_terminal", "responses", responseText + frame(`{"type":"response.incomplete","response":{"status":"in_progress","incomplete_details":{"reason":"max_output_tokens"}}}`), ErrAccountTestTerminal, false},
		{"responses_completed_not_incomplete", "responses", responseText + frame(`{"type":"response.completed","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`), ErrAccountTestTerminal, false},
		{"responses_refusal", "responses", frame(`{"type":"response.refusal.delta","delta":"I cannot help with that."}`) + frame(`{"type":"response.completed"}`), nil, false},
		{"responses_final_text", "responses", frame(`{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}]}}`), nil, false},
		{"claude_empty", "claude", frame(`{"type":"message_stop"}`), ErrAccountTestEmpty, false},
		{"claude_early_eof", "claude", claudeText, ErrAccountTestIncomplete, false},
		{"claude_done_only", "claude", claudeText + frame(`[DONE]`), ErrAccountTestIncomplete, false},
		{"claude_complete", "claude", claudeText + frame(`{"type":"message_stop"}`), nil, false},
		{"claude_limit", "claude", claudeText + frame(`{"type":"message_delta","delta":{"stop_reason":"max_tokens"}}`) + frame(`{"type":"message_stop"}`), nil, true},
		{"gemini_empty", "gemini", frame(`{"candidates":[{"finishReason":"STOP"}]}`), ErrAccountTestEmpty, false},
		{"gemini_early_eof", "gemini", geminiText, ErrAccountTestIncomplete, false},
		{"gemini_done_only", "gemini", geminiText + frame(`[DONE]`), ErrAccountTestIncomplete, false},
		{"gemini_complete", "gemini", geminiText + frame(`{"candidates":[{"finishReason":"STOP"}]}`), nil, false},
		{"gemini_limit", "gemini", geminiText + frame(`{"candidates":[{"finishReason":"MAX_TOKENS"}]}`), nil, true},
		{"gemini_failure", "gemini", geminiText + frame(`{"candidates":[{"finishReason":"MALFORMED_FUNCTION_CALL"}]}`), ErrAccountTestTerminal, false},
		{"chat_early_eof", "chat", chatText, ErrAccountTestIncomplete, false},
		{"chat_done_only", "chat", chatText + frame(`[DONE]`), ErrAccountTestIncomplete, false},
		{"chat_empty", "chat", frame(`{"choices":[{"finish_reason":"stop"}]}`), ErrAccountTestEmpty, false},
		{"chat_complete", "chat", chatText + frame(`{"choices":[{"finish_reason":"stop"}]}`), nil, false},
		{"chat_limit", "chat", chatText + frame(`{"choices":[{"finish_reason":"length"}]}`), nil, true},
		{"chat_refusal", "chat", frame(`{"choices":[{"delta":{"refusal":"I cannot help with that."},"finish_reason":"stop"}]}`), nil, false},
		{"responses_failed_verbatim", "responses", responseText + frame(`{"type":"response.failed","response":{"status":"failed","error":{"code":"credit_balance_exhausted","message":"You have no credits remaining."}}}`), ErrAccountTestTerminal, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, recorder := newTestContext()
			err := (&AccountTestService{}).processConnectionStream(c, strings.NewReader(tc.wire), tc.protocol, nil)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
			} else {
				require.NoError(t, err)
			}
			var text, errorText string
			completed, limited := false, false
			for _, line := range strings.Split(recorder.Body.String(), "\n") {
				if strings.HasPrefix(line, "data: ") {
					var event TestEvent
					require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event))
					switch event.Type {
					case "content":
						text += event.Text
					case "error":
						errorText = event.Error
					case "test_complete":
						completed, limited = event.Success, event.OutputLimited
					}
				}
			}
			require.Equal(t, tc.want == nil, completed)
			if tc.want == nil {
				require.Equal(t, tc.limited, limited)
				require.NotEmpty(t, strings.TrimSpace(text))
			} else {
				require.Equal(t, err.Error(), errorText, "the event carries the same text as the returned failure")
			}
			if tc.name == "responses_failed_verbatim" {
				require.Equal(t, "You have no credits remaining.", errorText)
			}
		})
	}
}

func TestAccountJobTerminalAndRetryContract(t *testing.T) {
	require.Equal(t, AccountJobStatusFailed, AccountJobTerminalStatus(false, "cancel_check_failed", 1, 0, 0))
	require.Equal(t, AccountJobStatusCanceled, AccountJobTerminalStatus(true, "", 1, 1, 2))
	require.Equal(t, AccountJobStatusPartiallySucceeded, AccountJobTerminalStatus(false, "", 1, 1, 0))
	require.True(t, AccountJobHasRetryableFailures(&AccountJob{Status: AccountJobStatusCanceled, FailedCount: 1}))
	require.False(t, AccountJobHasRetryableFailures(&AccountJob{Status: AccountJobStatusCanceled, CanceledCount: 2}))
}

type accountTestCancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r accountTestCancelOnClose) Close() error { err := r.ReadCloser.Close(); r.cancel(); return err }

func TestAccountConnectionCompletedBeforeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	response := adaptiveCNChatTestResponse()
	response.Body = accountTestCancelOnClose{ReadCloser: response.Body, cancel: cancel}
	account := openCodeGoTestAccount(490)
	svc, _ := adaptiveCNAccountTestService(account, response)
	result, err := svc.RunTestBackgroundDetailed(ctx, account.ID, "deepseek-v4-flash")
	require.ErrorIs(t, ctx.Err(), context.Canceled, "body close canceled immediately after the protocol terminal")
	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.NotEmpty(t, strings.TrimSpace(result.ResponseText))
}

func TestAccountConnectionBackgroundResultKeepsUpstreamError(t *testing.T) {
	account := openCodeGoTestAccount(491)
	svc, _ := adaptiveCNAccountTestService(account, &http.Response{StatusCode: http.StatusPaymentRequired, Header: http.Header{},
		Body: io.NopCloser(strings.NewReader(`{"error":{"message":"This request requires more credits"}}`))})
	result, err := svc.RunTestBackgroundDetailed(context.Background(), account.ID, "deepseek-v4-flash")
	require.NoError(t, err)
	require.Equal(t, `OpenCode Go API returned 402: {"error":{"message":"This request requires more credits"}}`, result.ErrorMessage)
}
