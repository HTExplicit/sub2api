package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIWSReplayHistoryRequiresCompleteMatchingBaseline(t *testing.T) {
	for _, test := range []struct {
		name     string
		payload  string
		baseline string
		verified bool
		want     bool
	}{
		{"new full input", `{"input":"hello"}`, "", false, true},
		{"matching complete baseline", `{"previous_response_id":"resp_1","input":"next"}`, "resp_1", true, true},
		{"matching incomplete baseline", `{"previous_response_id":"resp_1","input":"next"}`, "resp_1", false, false},
		{"external baseline", `{"previous_response_id":"resp_other","input":"next"}`, "resp_1", true, false},
		{"initial anchored delta", `{"previous_response_id":"resp_1","input":"next"}`, "", false, false},
		{"conversation reference", `{"conversation":"conv_1","input":"next"}`, "", false, false},
		{"external item", `{"input":[{"type":"item_reference","id":"rs_1"}]}`, "", false, false},
		{"tool output without call", `{"input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`, "", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, openAIWSReplayHistoryVerified([]byte(test.payload), test.baseline, test.verified))
		})
	}
}

func TestOpenAIWSVerifiedReplayPreservesAllStateAndOrder(t *testing.T) {
	payload := []byte(`{"type":"response.create","model":"gpt-5.6-sol","previous_response_id":"resp_old","input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}],"reasoning":{"effort":"xhigh"}}`)
	fullInput := []json.RawMessage{
		json.RawMessage(`{"role":"user","content":"keep all constraints"}`),
		json.RawMessage(`{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[{"type":"summary_text","text":"summary"}],"extension":{"a":1}}`),
		json.RawMessage(`{"type":"message","id":"msg_1","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"checking"}]}`),
		json.RawMessage(`{"type":"function_call","id":"fc_1","call_id":"call_1","name":"inspect","arguments":"{}"}`),
		json.RawMessage(`{"type":"future_item","opaque_extension":"retained"}`),
		json.RawMessage(`{"type":"function_call_output","call_id":"call_1","output":"ok"}`),
	}
	before := string(payload)
	replayed, safe := prepareOpenAIWSVerifiedReplayPayload(payload, fullInput, true, true)
	require.True(t, safe)
	require.False(t, gjson.GetBytes(replayed, "previous_response_id").Exists())
	wantInput, err := json.Marshal(fullInput)
	require.NoError(t, err)
	require.JSONEq(t, string(wantInput), gjson.GetBytes(replayed, "input").Raw)
	require.Equal(t, "xhigh", gjson.GetBytes(replayed, "reasoning.effort").String())
	require.Equal(t, before, string(payload), "replay must not mutate the original request")

	unchanged, safe := prepareOpenAIWSVerifiedReplayPayload(payload, fullInput, true, false)
	require.False(t, safe)
	require.Equal(t, before, string(unchanged))

	// The same complete payload is replayable to its current upstream, but
	// opaque state does not become portable merely by deleting the anchor.
	portable, safe, err := buildOpenAIWSCurrentTurnRetryPayload(payload, fullInput, true, true, "gpt-5.6-sol")
	require.NoError(t, err)
	require.False(t, safe)
	require.Nil(t, portable)
}

func TestOpenAIWSAccountRetryRequiresProofBeyondToolPairing(t *testing.T) {
	payload := []byte(`{"type":"response.create","model":"mapped","previous_response_id":"resp_old"}`)
	fullInput := []json.RawMessage{
		json.RawMessage(`{"role":"user","content":"original constraints"}`),
		json.RawMessage(`{"type":"function_call","call_id":"call_1","name":"inspect","arguments":"{}"}`),
		json.RawMessage(`{"type":"function_call_output","call_id":"call_1","output":"ok"}`),
	}
	for _, verified := range []bool{false, true} {
		retryPayload, safe, err := buildOpenAIWSCurrentTurnRetryPayload(payload, fullInput, true, verified, "original")
		require.NoError(t, err)
		require.Equal(t, verified, safe)
		if verified {
			require.False(t, gjson.GetBytes(retryPayload, "previous_response_id").Exists())
			require.Equal(t, "original", gjson.GetBytes(retryPayload, "model").String())
			require.Len(t, gjson.GetBytes(retryPayload, "input").Array(), 3)
		} else {
			require.Nil(t, retryPayload)
		}
	}
}

func TestOpenAIWSReplayCollectorUsesCompleteTerminalOrder(t *testing.T) {
	collector := &openAIWSToolCallReplayCollector{}
	collector.AddEvent("response.output_item.done", []byte(`{"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"inspect","arguments":"{}"}}`))
	collector.AddEvent("response.output_item.done", []byte(`{"item":{"type":"reasoning","id":"rs_1","encrypted_content":"opaque"}}`))
	require.False(t, collector.Complete(), "done items before a success terminal do not certify complete output")
	output := `[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[]},{"type":"message","id":"msg_1","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"checking"}]},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"inspect","arguments":"{}"},{"type":"future_item","extension":{"retained":true}},{"type":"future_item","extension":{"retained":true}}]`
	collector.AddEvent("response.completed", []byte(`{"response":{"status":"completed","output":`+output+`}}`))
	require.True(t, collector.Complete())
	got, err := json.Marshal(collector.AllItems())
	require.NoError(t, err)
	require.JSONEq(t, output, string(got), "terminal output order and repeated unkeyed items are authoritative")

	for _, event := range []struct {
		kind string
		body string
		want bool
	}{
		{"response.completed", `{"response":{"output":[]}}`, true},
		{"response.completed", `{"response":{"status":"completed"}}`, false},
		{"response.failed", `{"response":{"status":"failed","output":[]}}`, false},
		{"response.incomplete", `{"response":{"status":"incomplete","output":[]}}`, false},
		{"response.completed", `{"response":{"status":"failed","output":[]}}`, false},
	} {
		candidate := &openAIWSToolCallReplayCollector{}
		candidate.AddEvent(event.kind, []byte(event.body))
		require.Equal(t, event.want, candidate.Complete(), event.body)
	}
	conflicting := &openAIWSToolCallReplayCollector{}
	conflicting.AddEvent("response.output_item.done", []byte(`{"item":{"type":"message","id":"msg_1","content":[]}}`))
	conflicting.AddEvent("response.completed", []byte(`{"response":{"output":[]}}`))
	require.False(t, conflicting.Complete())
	require.Len(t, conflicting.AllItems(), 1, "inconsistent terminal must not delete observed output")
}

func TestOpenAIWSHTTPBridgeStateErrorsAreTerminalWithoutMutation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"http encrypted", http.StatusBadRequest, `{"error":{"code":"invalid_encrypted_content","message":"invalid encrypted content"}}`},
		{"http missing anchor", http.StatusBadGateway, `{"error":{"code":"previous_response_not_found","message":"previous response not found"}}`},
		{"sse encrypted", http.StatusOK, "data: {\"type\":\"error\",\"error\":{\"code\":\"invalid_encrypted_content\"}}\n\n"},
		{"sse missing anchor", http.StatusOK, "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"previous_response_not_found\"}}}\n\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: test.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.body))}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			account := &Account{ID: 10, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			payload := []byte(`{"type":"response.create","model":"gpt-5.6-sol","previous_response_id":"resp_old","input":[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[],"extension":"retained"}]}`)
			writes := 0
			_, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, account, "sk-test", payload, len(payload), "gpt-5.6-sol", "", "", "", "", 1, func([]byte) error { writes++; return nil })
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.True(t, failover.IsOpenAIContinuationStateUnavailable())
			require.False(t, failover.ShouldRetryNextAccount())
			require.False(t, failover.ShouldReportAccountScheduleFailure())
			require.Len(t, upstream.bodies, 1)
			require.Zero(t, writes)
			require.Equal(t, "resp_old", gjson.GetBytes(upstream.bodies[0], "previous_response_id").String())
			require.JSONEq(t, gjson.GetBytes(payload, "input").Raw, gjson.GetBytes(upstream.bodies[0], "input").Raw)
		})
	}
}
