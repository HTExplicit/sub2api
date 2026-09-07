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

const openAIChatReplayTestOutput = `[{"type":"reasoning","id":"rs_original","status":"completed","summary":[{"type":"summary_text","text":"checking carefully "}],"encrypted_content":"opaque-original","extension":{"precision":9007199254740993}},{"type":"message","id":"msg_original","status":"completed","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Checking orders."}],"future":{"keep":true}},{"type":"function_call","id":"fc_original","status":"completed","call_id":"call_orders","name":"load_orders","arguments":"{}","phase":"analysis","future":"retained"}]`

func openAIChatReplayTestMessage() apicompat.ChatMessage {
	return apicompat.ChatMessage{Role: "assistant", Content: json.RawMessage(`"Checking orders."`), ReasoningContent: "checking carefully ", ToolCalls: []apicompat.ChatToolCall{{ID: "call_orders", Type: "function", Function: apicompat.ChatFunctionCall{Name: "load_orders", Arguments: "{}"}}}}
}

func openAIChatReplayTestPayload(output string) []byte {
	return []byte(`{"type":"response.completed","response":{"id":"resp_original","model":"gpt-5.6-sol","status":"completed","output":` + output + `,"usage":{"input_tokens":4,"output_tokens":6}}}`)
}

func openAIChatReplayTestSSE(output string, streamDeltas bool) string {
	var frames []string
	if streamDeltas {
		frames = append(frames,
			`{"type":"response.created","response":{"id":"resp_original","model":"gpt-5.6-sol","status":"in_progress"}}`,
			`{"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"checking carefully "}`,
			`{"type":"response.output_text.delta","output_index":1,"delta":"Checking orders."}`,
			`{"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","id":"fc_original","call_id":"call_orders","name":"load_orders","arguments":"","status":"in_progress"}}`,
			`{"type":"response.function_call_arguments.delta","output_index":2,"delta":"{}"}`,
		)
	}
	frames = append(frames, string(openAIChatReplayTestPayload(output)))
	return "data: " + strings.Join(frames, "\n\ndata: ") + "\n\ndata: [DONE]\n\n"
}

func openAIChatReplayTestResponse(payload string, status int) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(payload))}
}

func TestOpenAIChatReasoningReplayRecorderRequiresMatchingCompletedProjection(t *testing.T) {
	t.Parallel()
	var recorder openAIChatReasoningReplayRecorder
	recorder.ObserveMessage(openAIChatReplayTestMessage())
	_, ok := recorder.Batch()
	require.False(t, ok, "downstream projection is not proof of completion")
	recorder.ObservePayload(openAIChatReplayTestPayload(openAIChatReplayTestOutput))
	batch, ok := recorder.Batch()
	require.True(t, ok)
	require.Equal(t, "commentary", gjson.GetBytes(batch.Output[1], "phase").String())
	require.Equal(t, "9007199254740993", gjson.GetBytes(batch.Output[0], "extension.precision").Raw)
	require.JSONEq(t, openAIChatReplayTestOutput, string(mustJSONChatReplay(t, batch.Output)))

	for _, kind := range []string{"response.failed", "response.incomplete", "error"} {
		r := openAIChatReasoningReplayRecorder{}
		r.ObservePayload([]byte(`{"type":"` + kind + `"}`))
		r.ObservePayload(openAIChatReplayTestPayload(openAIChatReplayTestOutput))
		r.ObserveMessage(openAIChatReplayTestMessage())
		_, ok := r.Batch()
		require.False(t, ok, kind)
	}
	wrong := openAIChatReplayTestMessage()
	wrong.Content = json.RawMessage(`"changed visible text"`)
	recorder.ObserveMessage(wrong)
	_, ok = recorder.Batch()
	require.False(t, ok, "rewritten text cannot claim the old raw batch")
}

func TestOpenAIChatReasoningReplayRecorderStreamingProjection(t *testing.T) {
	t.Parallel()
	for _, mutate := range []string{"none", "different_reasoning", "missing_delta_suffix", "conflicting_arguments", "no_deltas"} {
		t.Run(mutate, func(t *testing.T) {
			recorder := openAIChatReasoningReplayRecorder{}
			state := apicompat.NewResponsesEventToChatState()
			payload := openAIChatReplayTestSSE(openAIChatReplayTestOutput, mutate != "no_deltas")
			if mutate == "different_reasoning" {
				payload = strings.Replace(payload, `"delta":"checking carefully "`, `"delta":"different reasoning"`, 1)
			}
			if mutate == "missing_delta_suffix" {
				payload = strings.Replace(payload, `"delta":"{}"`, `"delta":"{"`, 1)
			}
			if mutate == "conflicting_arguments" {
				payload = strings.Replace(payload, `"delta":"{}"`, `"delta":"[]"`, 1)
			}
			for _, frame := range strings.Split(payload, "\n\n") {
				raw := []byte(strings.TrimPrefix(frame, "data: "))
				var event apicompat.ResponsesStreamEvent
				if json.Unmarshal(raw, &event) != nil {
					continue
				}
				recorder.ObservePayload(raw)
				recorder.ObserveChunks(apicompat.ResponsesEventToChatChunks(&event, state))
			}
			_, ok := recorder.Batch()
			require.Equal(t, mutate == "none" || mutate == "missing_delta_suffix", ok)
		})
	}
}

func TestOpenAIChatReasoningReplayRejectsNonReversibleBatches(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"custom":          strings.Replace(openAIChatReplayTestOutput, `"type":"function_call"`, `"type":"custom_tool_call"`, 1),
		"builtin":         strings.Replace(openAIChatReplayTestOutput, `"type":"function_call"`, `"type":"web_search_call"`, 1),
		"unknown":         strings.Replace(openAIChatReplayTestOutput, `"type":"message"`, `"type":"future_item"`, 1),
		"refusal":         strings.Replace(openAIChatReplayTestOutput, `"type":"output_text"`, `"type":"refusal"`, 1),
		"partial":         strings.Replace(openAIChatReplayTestOutput, `"status":"completed"`, `"status":"in_progress"`, 1),
		"args_empty":      strings.Replace(openAIChatReplayTestOutput, `"arguments":"{}"`, `"arguments":""`, 1),
		"call_id_numeric": strings.Replace(openAIChatReplayTestOutput, `"call_id":"call_orders"`, `"call_id":123`, 1),
		"namespace":       strings.Replace(openAIChatReplayTestOutput, `"name":"load_orders"`, `"namespace":"orders","name":"load_orders"`, 1),
		"summary_missing": strings.Replace(openAIChatReplayTestOutput, `"summary":[{"type":"summary_text","text":"checking carefully "}],`, "", 1),
		"duplicate_call":  strings.TrimSuffix(openAIChatReplayTestOutput, "]") + `,{"type":"function_call","call_id":"call_orders","name":"load_orders","arguments":"{}"}]`,
	}
	for name, output := range cases {
		t.Run(name, func(t *testing.T) {
			var raw []json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(output), &raw))
			_, ok := projectOpenAIChatReasoningRawBatch(raw)
			require.False(t, ok)
		})
	}
}

func TestOpenAIChatReasoningReplayOptionalReasoningButRequiredVisibleContent(t *testing.T) {
	t.Parallel()
	stored, ok := normalizeOpenAIChatReasoningProjection(openAIChatReplayTestMessage())
	require.True(t, ok)
	for _, variant := range []string{"normal", "omitted", "alias", "different_reasoning", "different_content", "different_arguments"} {
		message := openAIChatReplayTestMessage()
		switch variant {
		case "omitted":
			message.ReasoningContent = ""
		case "alias":
			message.Reasoning, message.ReasoningContent = message.ReasoningContent, ""
		case "different_reasoning":
			message.ReasoningContent = " changed "
		case "different_content":
			message.Content = json.RawMessage(`"changed"`)
		case "different_arguments":
			message.ToolCalls[0].Function.Arguments = `{"changed":true}`
		}
		incoming, ok := normalizeOpenAIChatReasoningProjection(message)
		require.True(t, ok)
		want := variant == "normal" || variant == "omitted" || variant == "alias"
		require.Equal(t, want, openAIChatReasoningProjectionMatches(stored, incoming), variant)
		if want {
			require.Equal(t, openAIChatReasoningBatchKey("prefix", stored), openAIChatReasoningBatchKey("prefix", incoming))
		}
	}
}

type openAIChatReplayTestCache struct {
	GatewayCache
	batches  map[string]OpenAIReasoningBatch
	rejected map[string]OpenAIRejectedReasoning
	readErr  error
	deletes  int
}

func newOpenAIChatReplayTestCache() *openAIChatReplayTestCache {
	return &openAIChatReplayTestCache{batches: map[string]OpenAIReasoningBatch{}, rejected: map[string]OpenAIRejectedReasoning{}}
}

func (s *openAIChatReplayTestCache) GetOpenAIReasoningBatches(_ context.Context, scope OpenAIReasoningCacheScope, keys []string) (map[string]OpenAIReasoningBatch, error) {
	result := map[string]OpenAIReasoningBatch{}
	for _, key := range keys {
		if value, ok := s.batches[scope.ScopeHash+":"+key]; ok {
			result[key] = value
		}
	}
	return result, s.readErr
}

func (s *openAIChatReplayTestCache) PutOpenAIReasoningBatch(_ context.Context, scope OpenAIReasoningCacheScope, key string, batch OpenAIReasoningBatch) (bool, error) {
	encoded, err := json.Marshal(batch)
	if err != nil {
		return false, err
	}
	batch.PayloadHash = openAIChatReasoningHash(encoded)
	s.batches[scope.ScopeHash+":"+key] = batch
	return true, nil
}

func (s *openAIChatReplayTestCache) DeleteOpenAIReasoningBatchIfMatch(_ context.Context, scope OpenAIReasoningCacheScope, key, payloadHash string) (bool, error) {
	key = scope.ScopeHash + ":" + key
	if batch, ok := s.batches[key]; ok && batch.PayloadHash == payloadHash {
		delete(s.batches, key)
		s.deletes++
		return true, nil
	}
	return false, nil
}

func (s *openAIChatReplayTestCache) GetOpenAIRejectedReasoning(_ context.Context, scope OpenAIReasoningCacheScope, hashes []string) (map[string]OpenAIRejectedReasoning, error) {
	result := map[string]OpenAIRejectedReasoning{}
	for _, hash := range hashes {
		if value, ok := s.rejected[scope.ScopeHash+":"+hash]; ok {
			result[hash] = value
		}
	}
	return result, s.readErr
}

func (s *openAIChatReplayTestCache) PutOpenAIRejectedReasoning(_ context.Context, scope OpenAIReasoningCacheScope, hashes []string) error {
	for _, hash := range hashes {
		s.rejected[scope.ScopeHash+":"+hash] = OpenAIRejectedReasoning{RejectedAt: time.Now(), ExpiresAt: time.Now().Add(24 * time.Hour)}
	}
	return nil
}

func mustJSONChatReplay(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}

func openAIChatReplayTestBody(t *testing.T, followup bool, stream bool, includeReasoning bool) []byte {
	t.Helper()
	messages := []apicompat.ChatMessage{{Role: "user", Content: json.RawMessage(`"Use the orders tool."`)}}
	if followup {
		message := openAIChatReplayTestMessage()
		if !includeReasoning {
			message.ReasoningContent = ""
		}
		messages = append(messages, message, apicompat.ChatMessage{Role: "tool", ToolCallID: "call_orders", Content: json.RawMessage(`"order data"`)})
	}
	body := map[string]any{
		"model": "gpt-5.6-sol", "messages": messages, "stream": stream,
		"tools":     []any{map[string]any{"type": "function", "function": map[string]any{"name": "load_orders", "parameters": map[string]any{"type": "object"}}}},
		"reasoning": json.RawMessage(`{"effort":"xhigh","mode":"auto","context":"auto","future":{"precise":9007199254740993}}`),
	}
	return mustJSONChatReplay(t, body)
}

func openAIChatReplayTestContext(t *testing.T, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	group := int64(4)
	c.Set("api_key", &APIKey{ID: 71, UserID: 9, GroupID: &group})
	return c, recorder
}

func TestOpenAIChatReasoningReplayForwardAPIKeyAndOAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, oauth := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, includeReasoning := range []bool{false, true} {
				t.Run(fmt.Sprintf("oauth_%t_stream_%t_optional_reasoning_%t", oauth, stream, includeReasoning), func(t *testing.T) {
					account := newOpenAIRejectedFieldTestAccount()
					if oauth {
						account = newOpenAIOAuthNamespaceTestAccount()
					}
					upstream := &httpUpstreamRecorder{responses: []*http.Response{
						openAIChatReplayTestResponse(openAIChatReplayTestSSE(openAIChatReplayTestOutput, true), http.StatusOK),
						openAIChatReplayTestResponse(string(openAIChatReplayTestPayload(`[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]`)), http.StatusOK),
					}}
					upstream.responses[1] = openAIChatReplayTestResponse("data: "+string(openAIChatReplayTestPayload(`[]`))+"\n\n", http.StatusOK)
					cache := newOpenAIChatReplayTestCache()
					svc := newOpenAIRejectedFieldTestService(upstream)
					svc.cache = cache
					first := openAIChatReplayTestBody(t, false, stream, true)
					c, _ := openAIChatReplayTestContext(t, first)
					_, err := svc.ForwardAsChatCompletions(context.Background(), c, account, first, "", "")
					require.NoError(t, err)
					require.True(t, c.GetBool("openai_chat_reasoning_replay_stored"))
					require.Len(t, cache.batches, 1)
					second := openAIChatReplayTestBody(t, true, stream, includeReasoning)
					c, _ = openAIChatReplayTestContext(t, second)
					_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, second, "", "")
					require.NoError(t, err)
					require.Equal(t, 1, c.GetInt("openai_chat_reasoning_replay_hits"))
					require.Len(t, upstream.bodies, 2)
					wire := upstream.bodies[1]
					require.Equal(t, "9007199254740993", gjson.GetBytes(wire, "reasoning.future.precise").Raw)
					require.NotContains(t, string(wire), "<thinking>")
					input := gjson.GetBytes(wire, "input").Array()
					require.Len(t, input, 5)
					require.JSONEq(t, openAIChatReplayTestOutput, "["+input[1].Raw+","+input[2].Raw+","+input[3].Raw+"]")
					require.Equal(t, input[3].Get("call_id").String(), input[4].Get("call_id").String())
				})
			}
		}
	}
}

func TestOpenAIChatReasoningReplayMissDoesNotChangeInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, variant := range []string{"missing", "cache_error", "disabled", "unknown_tenant", "changed_account", "changed_reasoning", "changed_prefix", "changed_actual_prefix"} {
		t.Run(variant, func(t *testing.T) {
			cache := newOpenAIChatReplayTestCache()
			account := newOpenAIRejectedFieldTestAccount()
			svc := &OpenAIGatewayService{cache: cache}
			chat := openAIChatReplayTestBody(t, true, false, true)
			var req apicompat.ChatCompletionsRequest
			require.NoError(t, json.Unmarshal(chat, &req))
			converted, err := apicompat.ChatCompletionsToResponses(&req)
			require.NoError(t, err)
			wire := mustJSONChatReplay(t, converted)
			request, err := http.NewRequest(http.MethodPost, "https://compat.example/v1/responses", bytes.NewReader(wire))
			require.NoError(t, err)
			request.Header.Set("Authorization", "Bearer test")
			c, _ := openAIChatReplayTestContext(t, chat)
			scope, err := buildOpenAIReasoningScope(c, account, request, wire)
			require.NoError(t, err)
			input, _ := openAIChatReasoningInput(wire)
			prefix, err := openAIChatReasoningPrefixHash(wire, input[:1])
			require.NoError(t, err)
			projection, _ := normalizeOpenAIChatReasoningProjection(openAIChatReplayTestMessage())
			var output []json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(openAIChatReplayTestOutput), &output))
			batch := OpenAIReasoningBatch{Output: output, Projection: projection, InputPrefixHash: prefix}
			if variant == "changed_actual_prefix" {
				batch.InputPrefixHash = strings.Repeat("0", 64)
			}
			_, err = cache.PutOpenAIReasoningBatch(context.Background(), scope, openAIChatReasoningBatchKey(prefix, projection), batch)
			require.NoError(t, err)
			switch variant {
			case "missing":
				cache.batches = map[string]OpenAIReasoningBatch{}
			case "cache_error":
				cache.readErr = errors.New("cache unavailable")
			case "disabled":
				account.Extra["openai_chat_reasoning_replay_enabled"] = false
			case "unknown_tenant":
				c.Set("api_key", &APIKey{ID: 71})
			case "changed_account":
				account.ID++
			case "changed_reasoning":
				wire, err = sjson.SetBytes(wire, "reasoning.future.precise", json.Number("9007199254740994"))
			case "changed_prefix":
				wire, err = sjson.SetBytes(wire, "input.0.content.0.text", "changed")
			}
			require.NoError(t, err)
			_, got := svc.prepareOpenAIChatReasoningReplay(context.Background(), c, account, request, chat, wire, true)
			require.Equal(t, wire, got)
			require.Equal(t, 0, c.GetInt("openai_chat_reasoning_replay_hits"))
		})
	}
}

func TestOpenAIChatReasoningReplayRecoversOnceAndInvalidatesCAS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, failure := range []string{"http", "sse_before_output", "sse_after_output", "recovery_fails"} {
		t.Run(failure, func(t *testing.T) {
			cache := newOpenAIChatReplayTestCache()
			account := newOpenAIRejectedFieldTestAccount()
			account.Extra["same_account_retry_count"] = 0
			rejected := `{"error":{"code":"invalid_encrypted_content","param":"input[1].encrypted_content"}}`
			rejection := openAIChatReplayTestResponse(rejected, http.StatusBadRequest)
			if strings.HasPrefix(failure, "sse_") {
				payload := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"first\"}}\n\n"
				if failure == "sse_after_output" {
					payload += "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"already thinking\"}\n\n"
				}
				payload += `data: {"type":"response.failed","response":{"status":"failed","error":{"code":"invalid_encrypted_content","param":"input[1].encrypted_content"}}}` + "\n\n"
				rejection = openAIChatReplayTestResponse(payload, http.StatusOK)
			}
			final := openAIChatReplayTestResponse("data: "+string(openAIChatReplayTestPayload(`[]`))+"\n\n", http.StatusOK)
			if failure == "recovery_fails" {
				final = openAIChatReplayTestResponse(`{"error":{"code":"server_error"}}`, http.StatusInternalServerError)
			}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{openAIChatReplayTestResponse(openAIChatReplayTestSSE(openAIChatReplayTestOutput, true), http.StatusOK), rejection, final}}
			svc := newOpenAIRejectedFieldTestService(upstream)
			svc.cache = cache
			first := openAIChatReplayTestBody(t, false, true, true)
			c, _ := openAIChatReplayTestContext(t, first)
			_, err := svc.ForwardAsChatCompletions(context.Background(), c, account, first, "", "")
			require.NoError(t, err)
			require.Len(t, cache.batches, 1)
			second := openAIChatReplayTestBody(t, true, true, false)
			c, _ = openAIChatReplayTestContext(t, second)
			_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, second, "", "")
			if failure == "sse_after_output" {
				require.Error(t, err)
				require.Len(t, upstream.requests, 2)
				require.Zero(t, cache.deletes)
				return
			}
			if failure == "recovery_fails" {
				require.Error(t, err)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover), "one recovery cannot reenter the account pool")
			} else {
				require.NoError(t, err)
			}
			require.Len(t, upstream.requests, 3)
			require.Equal(t, 1, cache.deletes)
			require.Len(t, cache.rejected, 1)
			expected, err := sjson.DeleteBytes(upstream.bodies[1], "input.1.encrypted_content")
			require.NoError(t, err)
			require.JSONEq(t, string(expected), string(upstream.bodies[2]))
			require.Equal(t, upstream.requests[1].Header, upstream.requests[2].Header)
			require.Equal(t, upstream.requests[1].URL, upstream.requests[2].URL)
			require.Nil(t, upstream.requests[2].GetBody)
		})
	}
}

func TestOpenAIChatReasoningReplayHooksAreNoopOutsideChat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := openAIChatReplayTestContext(t, nil)
	require.NotPanics(t, func() {
		observeOpenAIChatReasoningReplayPayload(nil, openAIChatReplayTestPayload(openAIChatReplayTestOutput))
		observeOpenAIChatReasoningReplayPayload(c, openAIChatReplayTestPayload(openAIChatReplayTestOutput))
	})
	_, found := c.Get(openAIChatReasoningReplayContextKey)
	require.False(t, found)
}

func TestOpenAIChatReasoningReplayRejectsDuplicateJSONMembers(t *testing.T) {
	_, err := openAIChatReasoningCanonical(json.RawMessage(`{"input":[{"role":"user","content":"a","content":"b"}]}`))
	require.Error(t, err)
	var output []json.RawMessage
	ambiguous := strings.Replace(openAIChatReplayTestOutput, `"precision":9007199254740993`, `"precision":9007199254740993,"precision":9007199254740994`, 1)
	require.NoError(t, json.Unmarshal([]byte(ambiguous), &output))
	_, ok := projectOpenAIChatReasoningRawBatch(output)
	require.False(t, ok)
}

func TestOpenAIChatReasoningReplayTerminalFunctionSnapshotRoundTrip(t *testing.T) {
	output := `[{"type":"reasoning","id":"rs_snapshot","status":"completed","summary":[],"encrypted_content":"opaque-snapshot"},{"type":"function_call","id":"fc_snapshot","call_id":"call_orders","name":"load_orders","arguments":"{}","status":"completed"}]`
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		openAIChatReplayTestResponse(openAIChatReplayTestSSE(output, false), 200),
		openAIChatReplayTestResponse("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: "+string(openAIChatReplayTestPayload(`[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]`))+"\n\n", 200),
	}}
	svc := newOpenAIRejectedFieldTestService(upstream)
	cache := newOpenAIChatReplayTestCache()
	svc.cache = cache
	account := newOpenAIRejectedFieldTestAccount()
	first := openAIChatReplayTestBody(t, false, true, false)
	c, writer := openAIChatReplayTestContext(t, first)
	_, err := svc.ForwardAsChatCompletions(context.Background(), c, account, first, "", "")
	require.NoError(t, err)
	require.Contains(t, writer.Body.String(), `"arguments":"{}"`)
	require.Len(t, cache.batches, 1)
	var request map[string]any
	require.NoError(t, decodeOpenAIJSONUseNumber(first, &request))
	messages, ok := request["messages"].([]any)
	require.True(t, ok)
	request["messages"] = append(messages,
		map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "call_orders", "type": "function", "function": map[string]any{"name": "load_orders", "arguments": "{}"}}}},
		map[string]any{"role": "tool", "tool_call_id": "call_orders", "content": "orders"})
	second := mustJSONChatReplay(t, request)
	c, _ = openAIChatReplayTestContext(t, second)
	_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, second, "", "")
	require.NoError(t, err)
	require.Equal(t, 1, c.GetInt("openai_chat_reasoning_replay_hits"))
	require.Contains(t, string(upstream.bodies[1]), "opaque-snapshot")
}
