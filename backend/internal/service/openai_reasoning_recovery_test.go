//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const reasoningRecoveryFixture = `{"model":"gpt-5.6-sol","reasoning":{"effort":"xhigh","mode":"standard","context":"keep","extension":{"x":1}},"store":false,"input":[{"role":"user","content":"constraints"},{"type":"reasoning","id":"rs_old","phase":"analysis","summary":[{"type":"summary_text","text":"visible summary"}],"encrypted_content":"opaque-old","unknown":{"keep":true}},{"type":"function_call","id":"fc_one","call_id":"call_one","name":"load_orders","arguments":"{}"},{"type":"function_call_output","call_id":"call_one","output":"[]"}]}`

func newReasoningRecoveryTestState(t *testing.T, ctx context.Context, body string) (*openAIReasoningRecoveryState, *http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	groupID := int64(7)
	c.Set("api_key", &APIKey{ID: 19, UserID: 5, GroupID: &groupID})
	account := &Account{ID: 31, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	svc := &OpenAIGatewayService{}
	state := svc.newOpenAIReasoningRecoveryState(ctx, c, account, "source-token")
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://upstream.example/v1/responses", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer source-token")
	req, _, err = state.PrepareRequest(req, []byte(body), "")
	require.NoError(t, err)
	t.Cleanup(state.Close)
	return state, req, recorder
}

func TestOpenAIReasoningRecoveryStrictErrorsAndScope(t *testing.T) {
	tests := []struct {
		name, body, payload string
		want                bool
	}{
		{"exact_unqualified_single", reasoningRecoveryFixture, `{"error":{"code":"invalid_encrypted_content"}}`, true},
		{"exact_signature_index", reasoningRecoveryFixture, `{"error":{"code":"thinking_signature_invalid","param":"input[1].encrypted_content"}}`, true},
		{"failed_envelope", reasoningRecoveryFixture, `{"type":"response.failed","response":{"status":"failed","error":{"code":"invalid_encrypted_content","param":"input.1"}}}`, true},
		{"done_failed_envelope", reasoningRecoveryFixture, `{"type":"response.done","response":{"status":"failed","error":{"code":"thinking_signature_invalid","param":"input.1"}}}`, true},
		{"bare_error", reasoningRecoveryFixture, `{"type":"error","code":"invalid_encrypted_content"}`, true},
		{"message_substring_not_code", reasoningRecoveryFixture, `{"error":{"message":"invalid_encrypted_content","code":"bad_request"}}`, false},
		{"numeric_code", reasoningRecoveryFixture, `{"error":{"code":400,"message":"thinking_signature_invalid"}}`, false},
		{"duplicate_error_code", reasoningRecoveryFixture, `{"error":{"code":"invalid_encrypted_content","code":"server_error"}}`, false},
		{"completed_is_not_rejected", reasoningRecoveryFixture, `{"type":"response.completed","response":{"status":"completed","error":{"code":"invalid_encrypted_content"}}}`, false},
		{"incomplete_is_not_rejected", reasoningRecoveryFixture, `{"status":"incomplete","error":{"code":"invalid_encrypted_content"}}`, false},
		{"generic_status", reasoningRecoveryFixture, `{"error":{"code":"server_error"}}`, false},
		{"case_sensitive_code", reasoningRecoveryFixture, `{"error":{"code":"INVALID_ENCRYPTED_CONTENT"}}`, false},
		{"wrong_target", reasoningRecoveryFixture, `{"error":{"code":"invalid_encrypted_content","param":"input[2].encrypted_content"}}`, false},
		{"unknown_target", reasoningRecoveryFixture, `{"error":{"code":"invalid_encrypted_content","param":"input[1].summary"}}`, false},
		{"unknown_param_shape", reasoningRecoveryFixture, `{"error":{"code":"invalid_encrypted_content","param":["input",1]}}`, false},
		{"local_reasoning_candidate_set", `{"model":"gpt-5.6-sol","input":[{"type":"reasoning","encrypted_content":"a"},{"type":"reasoning","encrypted_content":"b"}]}`, `{"error":{"code":"invalid_encrypted_content"}}`, true},
		{"ambiguous_compaction", `{"model":"gpt-5.6-sol","input":[{"type":"reasoning","encrypted_content":"a"},{"type":"compaction","encrypted_content":"b"}]}`, `{"error":{"code":"invalid_encrypted_content"}}`, false},
		{"specific_despite_compaction", `{"model":"gpt-5.6-sol","input":[{"type":"reasoning","encrypted_content":"a"},{"type":"compaction","encrypted_content":"b"}]}`, `{"error":{"code":"invalid_encrypted_content","param":"input[0].encrypted_content"}}`, true},
		{"reference_not_local_proof", `{"model":"gpt-5.6-sol","previous_response_id":"resp_old","input":[{"type":"reasoning","encrypted_content":"a"}]}`, `{"error":{"code":"invalid_encrypted_content"}}`, false},
		{"no_reasoning_cipher", `{"model":"gpt-5.6-sol","input":[{"type":"compaction","encrypted_content":"a"}]}`, `{"error":{"code":"invalid_encrypted_content"}}`, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, _, _ := newReasoningRecoveryTestState(t, context.Background(), test.body)
			body, retry := state.TryRecover(http.StatusBadRequest, nil, []byte(test.payload), false)
			require.Equal(t, test.want, retry)
			require.Equal(t, test.body, string(state.original))
			if retry {
				require.NotEqual(t, test.body, string(body))
				require.True(t, state.RecoveryAttempt())
				_, retry = state.TryRecover(http.StatusBadRequest, nil, []byte(test.payload), false)
				require.False(t, retry, "a request has only one extra POST")
			}
		})
	}
}

func TestOpenAIReasoningRecoveryPreservesEveryNonCipherField(t *testing.T) {
	state, _, rec := newReasoningRecoveryTestState(t, context.Background(), reasoningRecoveryFixture)
	before := bytes.Clone(state.wire)
	callbackCalls := 0
	state.SetRejectedCallback(func(hashes []string) {
		callbackCalls++
		require.Equal(t, []string{openAIReasoningDigest([]byte("opaque-old"))}, hashes)
	})
	payload := []byte(`{"type":"response.failed","response":{"error":{"code":"invalid_encrypted_content","param":"input.1.encrypted_content"},"usage":{"input_tokens":3,"output_tokens":2}}}`)
	signal := openAIReasoningRecoverySignal(state.c, payload, false)
	require.Error(t, signal)
	require.False(t, state.RecoveryAttempt(), "SSE signal must be pure before authoritative terminal")
	require.Zero(t, callbackCalls)
	after, retry := state.TryRecoverError(signal)
	require.True(t, retry)
	require.Equal(t, 1, callbackCalls)
	require.Equal(t, before, state.wire)
	require.False(t, gjson.GetBytes(after, "input.1.encrypted_content").Exists())
	for _, path := range []string{"model", "reasoning", "store", "input.0", "input.1.id", "input.1.phase", "input.1.summary", "input.1.unknown", "input.2", "input.3"} {
		require.Equal(t, gjson.GetBytes(before, path).Raw, gjson.GetBytes(after, path).Raw, path)
	}
	require.Equal(t, "retry_without_encrypted_content", rec.Header().Get(openAIReasoningRecoveryHeader))
	events, _ := state.c.Get(OpsUpstreamErrorsKey)
	entry := events.([]*OpsUpstreamErrorEvent)[0]
	require.Contains(t, entry.Detail, `"usage_status":"available"`)
	require.Contains(t, entry.Detail, `"input_tokens":3`)
	require.NotContains(t, entry.Detail, "opaque-old")
	require.Empty(t, entry.UpstreamResponseBody)
}

func TestOpenAIReasoningRecoveryCommittedCanceledDisabledAndFailover(t *testing.T) {
	payload := []byte(`{"error":{"code":"invalid_encrypted_content"}}`)
	state, _, _ := newReasoningRecoveryTestState(t, context.Background(), reasoningRecoveryFixture)
	require.Nil(t, openAIReasoningRecoverySignal(state.c, payload, true))
	_, retry := state.TryRecover(400, nil, payload, true)
	require.False(t, retry)
	state.enabled = false
	_, retry = state.TryRecover(400, nil, payload, false)
	require.False(t, retry)
	state.enabled = true
	_, retry = state.TryRecover(400, nil, payload, false)
	require.True(t, retry)
	var failover *UpstreamFailoverError
	stopped := state.StopError(&UpstreamFailoverError{StatusCode: 503, RetryableOnSameAccount: true})
	require.False(t, errors.As(stopped, &failover))
	require.NotContains(t, stopped.Error(), "503")
	ctx, cancel := context.WithCancel(context.Background())
	canceled, _, _ := newReasoningRecoveryTestState(t, ctx, reasoningRecoveryFixture)
	cancel()
	_, retry = canceled.TryRecover(400, nil, payload, false)
	require.False(t, retry)
	for _, status := range []int{401, 402, 403, 407, 429} {
		protected, _, _ := newReasoningRecoveryTestState(t, context.Background(), reasoningRecoveryFixture)
		_, retry := protected.TryRecover(status, nil, payload, false)
		require.False(t, retry, "authentication and quota statuses never gain an extra inference POST")
	}
}

func TestOpenAIReasoningRecoveryRetryPreservesContextAndIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	state, firstReq, _ := newReasoningRecoveryTestState(t, ctx, reasoningRecoveryFixture)
	body, retry := state.TryRecover(400, nil, []byte(`{"error":{"code":"invalid_encrypted_content"}}`), false)
	require.True(t, retry)
	req := firstReq.Clone(context.Background())
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.GetBody = nil
	req, actual, err := state.PrepareRequest(req, body, "")
	require.NoError(t, err)
	require.Equal(t, body, actual)
	deadline, ok := req.Context().Deadline()
	expected, _ := ctx.Deadline()
	require.True(t, ok)
	require.Equal(t, expected, deadline)
	require.Nil(t, req.GetBody)
	require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
	cancel()
	select {
	case <-req.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("retry detached caller cancellation")
	}
	for _, change := range []string{"auth", "endpoint", "non_responses_endpoint", "method", "nil_request", "proxy", "body"} {
		t.Run(change, func(t *testing.T) {
			s, original, _ := newReasoningRecoveryTestState(t, context.Background(), reasoningRecoveryFixture)
			stripped, _ := s.TryRecover(400, nil, []byte(`{"error":{"code":"invalid_encrypted_content"}}`), false)
			next := original.Clone(context.Background())
			next.GetBody = nil
			proxy := ""
			switch change {
			case "auth":
				next.Header.Set("Authorization", "Bearer different")
			case "endpoint":
				next.URL.Path = "/other/responses"
			case "non_responses_endpoint":
				next.URL.Path = "/v1/chat/completions"
			case "method":
				next.Method = http.MethodGet
			case "nil_request":
				next = nil
			case "proxy":
				proxy = "http://different-proxy.example"
			case "body":
				stripped = append(stripped, ' ')
			}
			_, _, err := s.PrepareRequest(next, stripped, proxy)
			require.Error(t, err)
		})
	}
}

type reasoningRecoveryMemoryStore struct {
	OpenAIReasoningStateStore
	values map[string]OpenAIRejectedReasoning
	puts   int
	fail   bool
}

func (m *reasoningRecoveryMemoryStore) GetOpenAIRejectedReasoning(_ context.Context, scope OpenAIReasoningCacheScope, hashes []string) (map[string]OpenAIRejectedReasoning, error) {
	if m.fail {
		return nil, errors.New("cache unavailable")
	}
	out := map[string]OpenAIRejectedReasoning{}
	for _, hash := range hashes {
		if value, ok := m.values[scope.ScopeHash+":"+hash]; ok {
			out[hash] = value
		}
	}
	return out, nil
}

func (m *reasoningRecoveryMemoryStore) PutOpenAIRejectedReasoning(_ context.Context, scope OpenAIReasoningCacheScope, hashes []string) error {
	if m.fail {
		return errors.New("cache unavailable")
	}
	m.puts++
	if m.values == nil {
		m.values = map[string]OpenAIRejectedReasoning{}
	}
	for _, hash := range hashes {
		m.values[scope.ScopeHash+":"+hash] = OpenAIRejectedReasoning{RejectedAt: time.Now(), ExpiresAt: time.Now().Add(24 * time.Hour)}
	}
	return nil
}

func TestOpenAIReasoningRecoveryNegativeMemoryIsExactAndNonSliding(t *testing.T) {
	store := &reasoningRecoveryMemoryStore{}
	first, _, _ := newReasoningRecoveryTestState(t, context.Background(), reasoningRecoveryFixture)
	first.store = store
	_, retry := first.TryRecover(400, nil, []byte(`{"error":{"code":"invalid_encrypted_content"}}`), false)
	require.True(t, retry)
	require.Equal(t, 1, store.puts)
	oldKey := first.scope.ScopeHash + ":" + openAIReasoningDigest([]byte("opaque-old"))
	oldEntry := store.values[oldKey]
	next, _, _ := newReasoningRecoveryTestState(t, context.Background(), reasoningRecoveryFixture)
	next.store = store
	body := strings.Replace(reasoningRecoveryFixture, `"output":"[]"}`, `"output":"[]"},{"type":"reasoning","encrypted_content":"opaque-new","summary":[]}`, 1)
	stripped := next.skipRejectedHistory([]byte(body))
	require.False(t, gjson.GetBytes(stripped, "input.1.encrypted_content").Exists())
	require.Equal(t, "opaque-new", gjson.GetBytes(stripped, "input.4.encrypted_content").String())
	require.Equal(t, oldEntry, store.values[oldKey], "cache hit must not renew the rejection timestamp")
	require.Equal(t, 1, store.puts)
	next.scope.ScopeHash = strings.Repeat("b", 64)
	require.Equal(t, body, string(next.skipRejectedHistory([]byte(body))))
	next.scope = first.scope
	store.fail = true
	require.Equal(t, body, string(next.skipRejectedHistory([]byte(body))))
	store.fail = false
	store.values[oldKey] = OpenAIRejectedReasoning{RejectedAt: time.Now().Add(-25 * time.Hour), ExpiresAt: time.Now().Add(-time.Hour)}
	require.Equal(t, body, string(next.skipRejectedHistory([]byte(body))))
}

func TestOpenAIReasoningRecoveryIdentityWithoutReasoningStillIsolatesSources(t *testing.T) {
	state, req, _ := newReasoningRecoveryTestState(t, context.Background(), `{"model":"gpt-5.6-sol","input":"test"}`)
	initial, err := buildOpenAIReasoningScope(state.c, state.account, req, []byte(`{"model":"gpt-5.6-sol"}`))
	require.NoError(t, err)
	changed := req.Clone(context.Background())
	changed.Header.Set("Authorization", "Bearer different-source")
	other, err := buildOpenAIReasoningScope(state.c, state.account, changed, []byte(`{"model":"gpt-5.6-sol"}`))
	require.NoError(t, err)
	require.NotEqual(t, initial.ScopeHash, other.ScopeHash)
	a := openAIReasoningRequestIdentity(state.account, req, nil, "", "")
	b := openAIReasoningRequestIdentity(state.account, req, nil, "http://other-proxy.example", "")
	require.NotEqual(t, a, b)
	require.NotEqual(t, openAIReasoningDigest(nil), a)
	one, err := buildOpenAIReasoningScope(state.c, state.account, req, []byte(`{"model":"gpt-5.6-sol","reasoning":{"effort":"xhigh","context":"keep"}}`))
	require.NoError(t, err)
	two, err := buildOpenAIReasoningScope(state.c, state.account, req, []byte(`{"model":"gpt-5.6-sol","reasoning":{"context":"keep","effort":"xhigh"}}`))
	require.NoError(t, err)
	require.Equal(t, one, two, "cache scope uses semantic JSON identity, not reasoning key order")
}

func TestOpenAIReasoningRecoveryForwardHTTPAndPassthroughSingleRetry(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, secondOK := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{true: "passthrough", false: "native"}[passthrough], map[bool]string{true: "completed", false: "rejected"}[secondOK]}, "/"), func(t *testing.T) {
				first := newJSONResponse(400, `{"error":{"code":"thinking_signature_invalid","param":"input[1].encrypted_content"}}`)
				first.Header.Set("Content-Type", "application/json")
				second := newJSONResponse(503, `{"error":{"code":"server_error","message":"private upstream failure"}}`)
				if secondOK {
					second = newJSONResponse(200, `{"id":"resp_final","status":"completed","model":"gpt-5.6-sol","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":11,"output_tokens":7}}`)
				}
				second.Header.Set("Content-Type", "application/json")
				upstream := &httpUpstreamRecorder{responses: []*http.Response{first, second}}
				svc := newOpenAIImageGenerationControlTestService(upstream)
				c, rec := newOpenAIImageGenerationControlTestContext(false, "test-client")
				account := newOpenAIImageGenerationControlTestAccount()
				account.Extra = map[string]any{"openai_passthrough": passthrough, "responses_api_supported": true, "pool_mode_enabled": true, "pool_mode_retry_count": 0}
				original := []byte(reasoningRecoveryFixture)
				result, err := svc.Forward(context.Background(), c, account, original)
				require.Len(t, upstream.requests, 2, "the extra retry is independent of pool retry count and never permits a third")
				require.Equal(t, reasoningRecoveryFixture, string(original))
				require.True(t, gjson.GetBytes(upstream.bodies[0], "input.1.encrypted_content").Exists())
				require.False(t, gjson.GetBytes(upstream.bodies[1], "input.1.encrypted_content").Exists())
				require.Equal(t, gjson.GetBytes(upstream.bodies[0], "reasoning").Raw, gjson.GetBytes(upstream.bodies[1], "reasoning").Raw)
				if secondOK {
					require.NoError(t, err)
					require.Equal(t, 11, result.Usage.InputTokens)
					require.Equal(t, 7, result.Usage.OutputTokens)
					require.Contains(t, rec.Body.String(), "resp_final")
				} else {
					require.Error(t, err)
					var failover *UpstreamFailoverError
					require.False(t, errors.As(err, &failover))
					require.NotContains(t, err.Error(), "private upstream failure")
				}
			})
		}
	}
}

func TestOpenAIReasoningRecoveryForwardSSEAndBufferedPreserveFailedAttemptUsage(t *testing.T) {
	failed := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_failed\",\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"error\",\"code\":\"invalid_encrypted_content\",\"param\":\"input[1].encrypted_content\"}\n\n" +
		"data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_failed\",\"status\":\"failed\",\"error\":{\"code\":\"invalid_encrypted_content\",\"param\":\"input[1].encrypted_content\"},\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n"
	completed := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_success\",\"status\":\"completed\",\"model\":\"gpt-5.6-sol\",\"output\":[{\"id\":\"msg_success\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}],\"usage\":{\"input_tokens\":11,\"output_tokens\":7}}}\n\n"
	for _, passthrough := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			name := map[bool]string{false: "native", true: "passthrough"}[passthrough] + "/" + map[bool]string{false: "buffered", true: "stream"}[stream]
			t.Run(name, func(t *testing.T) {
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(failed))},
					{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(completed))},
				}}
				svc := newOpenAIImageGenerationControlTestService(upstream)
				c, recorder := newOpenAIImageGenerationControlTestContext(false, "test-client")
				account := newOpenAIImageGenerationControlTestAccount()
				account.Extra = map[string]any{"openai_passthrough": passthrough, "responses_api_supported": true}
				body := reasoningRecoveryFixture
				if stream {
					body = strings.Replace(body, `"store":false`, `"stream":true,"store":false`, 1)
				}
				result, err := svc.Forward(context.Background(), c, account, []byte(body))
				require.NoError(t, err)
				require.Len(t, upstream.requests, 2)
				require.Equal(t, 11, result.Usage.InputTokens)
				require.Equal(t, 7, result.Usage.OutputTokens)
				require.NotContains(t, recorder.Body.String(), "resp_failed")
				require.Contains(t, recorder.Body.String(), "resp_success")
				events, _ := c.Get(OpsUpstreamErrorsKey)
				recorded := false
				for _, entry := range events.([]*OpsUpstreamErrorEvent) {
					if entry.Kind == "reasoning_recovery" && entry.Message == "retry_without_encrypted_content" {
						recorded = true
						require.Contains(t, entry.Detail, `"input_tokens":3`)
						require.Contains(t, entry.Detail, `"output_tokens":2`)
					}
				}
				require.True(t, recorded)
			})
		}
	}
}

func TestOpenAIReasoningRecoveryNeverReplaysCommittedSSE(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "passthrough"}[passthrough], func(t *testing.T) {
			body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"visible answer\"}\n\n" +
				"data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"invalid_encrypted_content\",\"param\":\"input[1].encrypted_content\"},\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n"
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			c, recorder := newOpenAIImageGenerationControlTestContext(false, "test-client")
			account := newOpenAIImageGenerationControlTestAccount()
			account.Extra = map[string]any{"openai_passthrough": passthrough, "responses_api_supported": true}
			input := strings.Replace(reasoningRecoveryFixture, `"store":false`, `"stream":true,"store":false`, 1)
			_, err := svc.Forward(context.Background(), c, account, []byte(input))
			require.Error(t, err)
			require.Len(t, upstream.requests, 1)
			require.Contains(t, recorder.Body.String(), "visible answer")
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover), "post-commit terminal errors cannot be externally replayed")
		})
	}
}
