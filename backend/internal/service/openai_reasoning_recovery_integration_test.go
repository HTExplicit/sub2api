//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func assertReasoningRecoveryUsageEvidence(t *testing.T, c *gin.Context, input int64, output *int64) {
	t.Helper()
	value, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	found := false
	for _, event := range value.([]*OpsUpstreamErrorEvent) {
		if event.Kind != "reasoning_recovery" || event.Message != "retry_without_encrypted_content" {
			continue
		}
		found = true
		detail := gjson.Parse(event.Detail)
		require.Equal(t, "available", detail.Get("usage_status").String())
		require.Equal(t, input, detail.Get("usage.input_tokens").Int())
		if output == nil {
			require.False(t, detail.Get("usage.output_tokens").Exists(), "missing output usage must remain unavailable, not zero")
		} else {
			require.Equal(t, *output, detail.Get("usage.output_tokens").Int())
		}
	}
	require.True(t, found)
}

func TestOpenAIReasoningRecoveryLegacyDoneAndEarlierUsage(t *testing.T) {
	for _, mode := range []string{"native", "native_async", "passthrough", "buffered", "passthrough_buffered"} {
		for _, terminal := range []string{"response.failed", "response.done"} {
			t.Run(mode+"/"+terminal, func(t *testing.T) {
				state, _, _ := newReasoningRecoveryTestState(t, context.Background(), reasoningRecoveryFixture)
				progress := `{"type":"response.in_progress","response":{"usage":{"input_tokens":3}}}`
				failed := fmt.Sprintf(`{"type":%q,"response":{"status":"failed","error":{"code":"thinking_signature_invalid","param":"input[1].encrypted_content"}}}`, terminal)
				_, err := runOpenAIHTTPTerminalHandler(mode, state.c, io.NopCloser(strings.NewReader("data: "+progress+"\n\ndata: "+failed+"\n\n")), "text/event-stream")
				var signal *openAIReasoningRecoverySignalError
				require.ErrorAs(t, err, &signal)
				_, recovered := state.TryRecoverError(err)
				require.True(t, recovered)
				assertReasoningRecoveryUsageEvidence(t, state.c, 3, nil)
			})
		}
	}
}

// A Chat message has no field for reasoning ciphertext. On the Chat Completions
// endpoint only a Responses-shaped body carries it to the Responses upstream.
const reasoningRecoveryChatFixture = `{"model":"gpt-5.6-sol","reasoning":{"effort":"xhigh"},"store":false,"input":[{"role":"user","content":"constraints"},{"type":"reasoning","summary":[],"encrypted_content":"opaque-old"},{"role":"user","content":"continue"}]}`

const reasoningRecoveryChatCompleted = `{"type":"response.completed","response":{"id":"resp_final","model":"gpt-5.6-sol","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":4,"output_tokens":6}}}`

func reasoningRecoveryChatBody(t *testing.T, stream bool) []byte {
	t.Helper()
	body, err := sjson.SetBytes([]byte(reasoningRecoveryChatFixture), "stream", stream)
	require.NoError(t, err)
	return body
}

func reasoningRecoveryChatContext(t *testing.T, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	group := int64(4)
	c.Set("api_key", &APIKey{ID: 71, UserID: 9, GroupID: &group})
	return c, recorder
}

func reasoningRecoverySSEResponse(payload string, status int) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(payload))}
}

func TestOpenAIReasoningRecoveryChatPathWaitsForAuthoritativeRejection(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, terminal := range []string{"response.failed", "response.done", "response.completed", "eof"} {
			t.Run(fmt.Sprintf("stream_%t/%s", stream, terminal), func(t *testing.T) {
				store := &reasoningRecoveryMemoryStore{GatewayCache: &stubGatewayCache{}}
				account := newOpenAIRejectedFieldTestAccount()
				progress := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_rejected\"}}\n\n" +
					"data: {\"type\":\"response.in_progress\",\"response\":{\"usage\":{\"input_tokens\":3}}}\n\n"
				bare := "data: {\"type\":\"error\",\"error\":{\"code\":\"invalid_encrypted_content\",\"param\":\"input[1].encrypted_content\"}}\n\n"
				completed := "data: " + reasoningRecoveryChatCompleted + "\n\n"
				firstFailure := progress + bare
				switch terminal {
				case "response.completed":
					firstFailure += completed
				case "response.failed", "response.done":
					firstFailure += fmt.Sprintf("data: {\"type\":%q,\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"invalid_encrypted_content\",\"param\":\"input[1].encrypted_content\"}}}\n\n", terminal)
				}
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					reasoningRecoverySSEResponse(firstFailure, http.StatusOK),
					reasoningRecoverySSEResponse(completed, http.StatusOK),
				}}
				svc := newOpenAIRejectedFieldTestService(upstream)
				svc.cache = store
				body := reasoningRecoveryChatBody(t, stream)
				c, writer := reasoningRecoveryChatContext(t, body)
				result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
				require.NoError(t, err)
				require.Equal(t, 4, result.Usage.InputTokens)
				require.Equal(t, 6, result.Usage.OutputTokens)
				if terminal == "response.completed" {
					require.Len(t, upstream.requests, 1)
					require.Empty(t, store.values)
				} else {
					require.NotContains(t, writer.Body.String(), "resp_rejected")
					require.Len(t, upstream.requests, 2)
					require.Len(t, store.values, 1)
					assertReasoningRecoveryUsageEvidence(t, c, 3, nil)
				}
			})
		}
	}
}

func TestOpenAIReasoningRecoveryChatPathRecoversOnce(t *testing.T) {
	for _, failure := range []string{"http", "sse_before_output", "sse_after_output", "recovery_fails"} {
		t.Run(failure, func(t *testing.T) {
			store := &reasoningRecoveryMemoryStore{GatewayCache: &stubGatewayCache{}}
			account := newOpenAIRejectedFieldTestAccount()
			account.Extra["same_account_retry_count"] = 0
			rejection := reasoningRecoverySSEResponse(`{"error":{"code":"invalid_encrypted_content","param":"input[1].encrypted_content"}}`, http.StatusBadRequest)
			if strings.HasPrefix(failure, "sse_") {
				payload := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"first\"}}\n\n"
				if failure == "sse_after_output" {
					payload += "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"already thinking\"}\n\n"
				}
				payload += `data: {"type":"response.failed","response":{"status":"failed","error":{"code":"invalid_encrypted_content","param":"input[1].encrypted_content"}}}` + "\n\n"
				rejection = reasoningRecoverySSEResponse(payload, http.StatusOK)
			}
			final := reasoningRecoverySSEResponse("data: "+reasoningRecoveryChatCompleted+"\n\n", http.StatusOK)
			if failure == "recovery_fails" {
				final = reasoningRecoverySSEResponse(`{"error":{"code":"server_error"}}`, http.StatusInternalServerError)
			}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{rejection, final}}
			svc := newOpenAIRejectedFieldTestService(upstream)
			svc.cache = store
			body := reasoningRecoveryChatBody(t, true)
			c, _ := reasoningRecoveryChatContext(t, body)
			_, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
			if failure == "sse_after_output" {
				require.Error(t, err)
				require.Len(t, upstream.requests, 1)
				require.Empty(t, store.values)
				return
			}
			if failure == "recovery_fails" {
				require.Error(t, err)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover), "one recovery cannot reenter the account pool")
			} else {
				require.NoError(t, err)
			}
			require.Len(t, upstream.requests, 2)
			require.Len(t, store.values, 1)
			require.Equal(t, "opaque-old", gjson.GetBytes(upstream.bodies[0], "input.1.encrypted_content").String())
			expected, err := sjson.DeleteBytes(upstream.bodies[0], "input.1.encrypted_content")
			require.NoError(t, err)
			require.JSONEq(t, string(expected), string(upstream.bodies[1]))
			require.Equal(t, upstream.requests[0].Header, upstream.requests[1].Header)
			require.Equal(t, upstream.requests[0].URL, upstream.requests[1].URL)
			require.Nil(t, upstream.requests[1].GetBody)
		})
	}
}

type reasoningPartialWriter struct{ gin.ResponseWriter }

func (w reasoningPartialWriter) Write(p []byte) (int, error) {
	n, _ := w.ResponseWriter.Write(p[:min(len(p), 4)])
	return n, io.ErrClosedPipe
}

func TestOpenAIReasoningRecoveryChatPartialWriteCannotRecover(t *testing.T) {
	state, _, writer := newReasoningRecoveryTestState(t, context.Background(), reasoningRecoveryFixture)
	state.c.Writer = reasoningPartialWriter{state.c.Writer}
	body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_partial\",\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"visible\"}\n\n" +
		"data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"invalid_encrypted_content\",\"param\":\"input[1].encrypted_content\"}}}\n\n"
	svc := newOpenAIRejectedFieldTestService(&httpUpstreamRecorder{})
	_, err := svc.handleChatStreamingResponse(reasoningRecoverySSEResponse(body, 200), state.c, state.account, "gpt-5.6-sol", "gpt-5.6-sol", "gpt-5.6-sol", time.Now(), 32)
	require.Error(t, err)
	require.NotEmpty(t, writer.Body.String())
	var signal *openAIReasoningRecoverySignalError
	require.False(t, errors.As(err, &signal))
	_, recovered := state.TryRecoverError(err)
	require.False(t, recovered)
}

// A tool round on the Chat Completions endpoint is forwarded as the plain
// conversion of the client's messages: the upstream reasoning of the first turn
// is neither kept nor put back, and no reasoning state is read or written.
func TestOpenAIReasoningRecoveryChatToolRoundKeepsNoReasoningState(t *testing.T) {
	const firstOutput = `[{"type":"reasoning","id":"rs_original","status":"completed","summary":[{"type":"summary_text","text":"checking carefully "}],"encrypted_content":"opaque-original"},{"type":"function_call","id":"fc_original","status":"completed","call_id":"call_orders","name":"load_orders","arguments":"{}"}]`
	messages := []apicompat.ChatMessage{{Role: "user", Content: json.RawMessage(`"Use the orders tool."`)}}
	chatBody := func(stream bool) []byte {
		body, err := json.Marshal(map[string]any{
			"model": "gpt-5.6-sol", "messages": messages, "stream": stream,
			"tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "load_orders", "parameters": map[string]any{"type": "object"}}}},
		})
		require.NoError(t, err)
		return body
	}
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream_%t", stream), func(t *testing.T) {
			messages = messages[:1]
			store := &reasoningRecoveryMemoryStore{GatewayCache: &stubGatewayCache{}}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				reasoningRecoverySSEResponse("data: "+strings.Replace(reasoningRecoveryChatCompleted, `[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]`, firstOutput, 1)+"\n\n", http.StatusOK),
				reasoningRecoverySSEResponse("data: "+reasoningRecoveryChatCompleted+"\n\n", http.StatusOK),
			}}
			svc := newOpenAIRejectedFieldTestService(upstream)
			svc.cache = store
			account := newOpenAIRejectedFieldTestAccount()
			first := chatBody(stream)
			c, writer := reasoningRecoveryChatContext(t, first)
			_, err := svc.ForwardAsChatCompletions(context.Background(), c, account, first, "", "")
			require.NoError(t, err)
			require.Contains(t, writer.Body.String(), "call_orders")

			messages = append(messages,
				apicompat.ChatMessage{Role: "assistant", ReasoningContent: "checking carefully ", ToolCalls: []apicompat.ChatToolCall{{ID: "call_orders", Type: "function", Function: apicompat.ChatFunctionCall{Name: "load_orders", Arguments: "{}"}}}},
				apicompat.ChatMessage{Role: "tool", ToolCallID: "call_orders", Content: json.RawMessage(`"order data"`)})
			second := chatBody(stream)
			c, _ = reasoningRecoveryChatContext(t, second)
			_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, second, "", "")
			require.NoError(t, err)

			require.Len(t, upstream.bodies, 2)
			var request apicompat.ChatCompletionsRequest
			require.NoError(t, json.Unmarshal(second, &request))
			converted, err := apicompat.ChatCompletionsToResponses(&request)
			require.NoError(t, err)
			require.JSONEq(t, string(converted.Input), gjson.GetBytes(upstream.bodies[1], "input").Raw)
			require.NotContains(t, string(upstream.bodies[1]), "encrypted_content")
			require.NotContains(t, string(upstream.bodies[1]), "rs_original")
			require.Zero(t, store.gets)
			require.Zero(t, store.puts)
			for key := range c.Keys {
				require.NotContains(t, key, "replay")
			}
		})
	}
}
