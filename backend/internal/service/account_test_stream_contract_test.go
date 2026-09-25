//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, background := range []bool{false, true} {
				c, recorder := newTestContext()
				collector := &accountTestEventCollector{}
				if background {
					c.Set("account_test_event_collector", collector)
				}
				err := (&AccountTestService{}).processConnectionStream(c, strings.NewReader(tc.wire), tc.protocol, nil)
				if tc.want != nil {
					require.ErrorIs(t, err, tc.want)
				} else {
					require.NoError(t, err)
				}
				if !background {
					for _, line := range strings.Split(recorder.Body.String(), "\n") {
						if strings.HasPrefix(line, "data: ") {
							var event TestEvent
							require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event))
							collector.Add(event)
						}
					}
				}
				require.Equal(t, tc.want == nil, collector.completed)
				if tc.want == nil {
					require.Equal(t, tc.limited, collector.outputLimited)
					require.NotEmpty(t, strings.TrimSpace(collector.text.String()))
				}
			}
		})
	}
}

func TestAccountConnectionTransportClassification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &AccountTestService{}
	for _, tc := range []struct {
		status int
		code   string
	}{{401, "test_authentication_failed"}, {403, "test_authentication_failed"}, {429, "test_rate_limited"}, {502, "test_upstream_failed"}} {
		c, _ := newTestContext()
		require.Equal(t, tc.code, AccountTestFailureCode(svc.sendAccountTestHTTPError(c, tc.status)))
	}
	for _, tc := range []struct {
		cause error
		code  string
	}{{context.DeadlineExceeded, "test_timeout"}, {errors.New("network-secret-material"), "test_network_failed"}} {
		c, recorder := newTestContext()
		err := svc.sendAccountTestRequestError(c, tc.cause)
		require.Equal(t, tc.code, AccountTestFailureCode(err))
		require.NotContains(t, recorder.Body.String(), "network-secret-material")
	}
	for _, tc := range []struct {
		failure     error
		code, label string
	}{
		{accountTestHTTPFailure(404, accountTestEndpointAdaptiveAnthropic), "test_upstream_failed", "Adaptive Anthropic endpoint returned 404"},
		{accountTestHTTPFailure(401, accountTestEndpointAnthropic), "test_authentication_failed", "Anthropic endpoint returned 401"},
		{accountTestHTTPFailure(402, accountTestEndpointGrokResponses), "test_upstream_failed", "Grok Responses API returned 402"},
		{accountTestRequestFailure(context.DeadlineExceeded, accountTestEndpointChat), "test_timeout", "Chat Completions API (/v1/chat/completions) request failed"},
		{accountTestRequestFailure(errors.New("network-secret-material"), accountTestEndpointChat), "test_network_failed", "Chat Completions API (/v1/chat/completions) request failed"},
	} {
		c, recorder := newTestContext()
		err := svc.sendAccountTestFailure(c, tc.failure)
		require.Equal(t, tc.code, AccountTestFailureCode(err))
		require.Contains(t, recorder.Body.String(), tc.label)
		require.Contains(t, AccountTestSafeFailureMessage(err), tc.label, "batch and single adapters share the safe diagnostic")
		require.NotContains(t, AccountTestSafeFailureMessage(err), "network-secret-material")
	}
	require.Equal(t, "account connection test failed or did not complete", AccountTestSafeFailureMessage(errors.New("network-secret-material")))
}

func TestAccountJobTerminalAndRetryContract(t *testing.T) {
	require.Equal(t, AccountJobStatusFailed, AccountJobTerminalStatus(false, "cancel_check_failed", 1, 0, 0))
	require.Equal(t, AccountJobStatusCanceled, AccountJobTerminalStatus(true, "", 1, 1, 2))
	require.Equal(t, AccountJobStatusPartiallySucceeded, AccountJobTerminalStatus(false, "", 1, 1, 0))
	require.True(t, AccountJobHasRetryableFailures(&AccountJob{Status: AccountJobStatusCanceled, FailedCount: 1}))
	require.False(t, AccountJobHasRetryableFailures(&AccountJob{Status: AccountJobStatusCanceled, CanceledCount: 2}))
}

func TestAccountConnectionAdapterFailurePreservesProtocolCause(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &AccountTestService{}
	for _, test := range []struct {
		failure error
		code    string
	}{
		{ErrAccountTestIncomplete, "test_incomplete"},
		{accountTestHTTPFailure(429), "test_rate_limited"},
		{accountTestRequestFailure(errors.New("adapter-secret-material")), "test_network_failed"},
		{errors.New("adapter-secret-material"), "test_failed"},
	} {
		c, recorder := newTestContext()
		forwarded := svc.sendAccountTestFailure(c, test.failure)
		require.ErrorIs(t, forwarded, test.failure)
		require.Equal(t, test.code, AccountTestFailureCode(forwarded))
		require.Contains(t, recorder.Body.String(), test.code)
		require.NotContains(t, recorder.Body.String(), "adapter-secret-material")
	}
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
	result, err := svc.RunBatchTestBackgroundWithOptions(ctx, account.ID, "deepseek-v4-flash", "", AccountTestOptions{})
	require.ErrorIs(t, ctx.Err(), context.Canceled, "body close canceled immediately after the protocol terminal")
	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.NotEmpty(t, strings.TrimSpace(result.ResponseText))
}
