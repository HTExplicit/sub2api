//go:build unit

package service

import (
	"context"
	"fmt"
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

func TestNativeResponsesFidelityPreservesCompatibleState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const payload = `{"model":"gpt-5.6-sol","stream":false,"store":false,"previous_response_id":"resp_prior","reasoning":{"effort":"minimal","context":"custom-context","future_option":{"enabled":true}},"input":[{"type":"reasoning","id":"provider_reasoning_1","summary":[{"type":"summary_text","text":"prior summary"}],"content":[{"type":"reasoning_text","text":"prior visible reasoning"}],"encrypted_content":"test-reasoning","provider_extension":9007199254740993},{"type":"item_reference","id":"rs_stored_old"},{"type":"compaction","id":"provider_compaction","encrypted_content":"test-compaction"},{"type":"message","id":"provider_message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"calling tool"}]},{"type":"function_call","id":"provider_call","call_id":"call_1","name":"load_orders","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"[]"},{"role":"user","content":"continue"}]}`
	for _, passthrough := range []bool{false, true} {
		for _, wsEnabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("passthrough=%t/ws=%t", passthrough, wsEnabled), func(t *testing.T) {
				body := []byte(payload)
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"id":"resp_current","status":"completed","output":[{"type":"message","id":"provider_output","role":"assistant","phase":"final_answer","provider_extension":9007199254740993,"content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":1}}`)),
				}}
				cfg := &config.Config{}
				cfg.Gateway.OpenAIWS.Enabled = wsEnabled
				svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
				account := newOpenAIImageGenerationControlTestAccount()
				account.Credentials["base_url"] = "https://compatible.example/v1"
				account.Extra = map[string]any{"use_responses_api": true, "openai_passthrough": passthrough}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
				// Even a previous rejection marker is not authority to delete history.
				svc.markOpenAIWSInvalidEncryptedContentLineage(getOpenAIGroupIDFromContext(c), svc.GenerateSessionHash(c, body), collectOpenAIEncryptedContentDigestsRaw(body))

				result, err := svc.Forward(context.Background(), c, account, body)

				require.NoError(t, err)
				require.NotNil(t, result)
				require.Len(t, upstream.bodies, 1)
				require.Equal(t, payload, string(body), "the canonical caller payload is immutable")
				require.JSONEq(t, gjson.Get(payload, "input").Raw, gjson.GetBytes(upstream.lastBody, "input").Raw)
				require.JSONEq(t, gjson.Get(payload, "reasoning").Raw, gjson.GetBytes(upstream.lastBody, "reasoning").Raw)
				require.Equal(t, "resp_prior", gjson.GetBytes(upstream.lastBody, "previous_response_id").String())
				require.Equal(t, gjson.False, gjson.GetBytes(upstream.lastBody, "store").Type)
				require.NotNil(t, result.ReasoningEffort)
				require.Equal(t, "minimal", *result.ReasoningEffort)
				require.Equal(t, "final_answer", gjson.Get(recorder.Body.String(), "output.0.phase").String())
				require.Equal(t, "9007199254740993", gjson.Get(recorder.Body.String(), "output.0.provider_extension").Raw)
			})
		}
	}
}

func TestNativeResponsesFidelityEffortMatchesWire(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name, model, reasoning, wireModel, effort string
	}{
		{name: "minimal remains minimal", model: "gpt-5.6-sol", reasoning: `,"reasoning":{"effort":"minimal"}`, wireModel: "gpt-5.6-sol", effort: "minimal"},
		{name: "max remains max", model: "gpt-6-astra", reasoning: `,"reasoning":{"effort":"max"}`, wireModel: "gpt-6-astra", effort: "max"},
		{name: "none remains explicit", model: "gpt-5.6-sol", reasoning: `,"reasoning":{"effort":"none"}`, wireModel: "gpt-5.6-sol", effort: "none"},
		{name: "recognized alias materialized", model: "gpt-5.6-sol-xhigh", wireModel: "gpt-5.6-sol", effort: "xhigh"},
		{name: "explicit effort wins alias", model: "gpt-5.6-sol-xhigh", reasoning: `,"reasoning":{"effort":"low","context":"keep"}`, wireModel: "gpt-5.6-sol", effort: "low"},
		{name: "custom suffix not guessed", model: "custom-solver-high", wireModel: "custom-solver-high"},
		{name: "omission remains omission", model: "gpt-6-astra", wireModel: "gpt-6-astra"},
	}
	for _, tt := range tests {
		for _, passthrough := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/passthrough=%t", tt.name, passthrough), func(t *testing.T) {
				body := []byte(fmt.Sprintf(`{"model":%q,"stream":false,"input":"test"%s}`, tt.model, tt.reasoning))
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"id":"resp_test","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)),
				}}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
				account := newOpenAIImageGenerationControlTestAccount()
				account.Credentials["base_url"] = "https://compatible.example/v1"
				account.Extra = map[string]any{"use_responses_api": true, "openai_passthrough": passthrough}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

				result, err := svc.Forward(context.Background(), c, account, body)

				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, tt.wireModel, gjson.GetBytes(upstream.lastBody, "model").String())
				require.Equal(t, tt.effort, gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
				if tt.effort == "" {
					require.Nil(t, result.ReasoningEffort)
				} else {
					require.NotNil(t, result.ReasoningEffort)
					require.Equal(t, tt.effort, *result.ReasoningEffort)
				}
			})
		}
	}
}

func TestNativeChatReasoningAliasIsMaterializedBeforeModelNormalization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, nativeChat := range []bool{false, true} {
		t.Run(fmt.Sprintf("native-chat=%t", nativeChat), func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.6-sol-xhigh","messages":[{"role":"user","content":"hello"}],"stream":false}`)
			responseBody := `{"id":"resp_test","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`
			contentType := "text/event-stream"
			if nativeChat {
				responseBody = `{"id":"chatcmpl_test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
				contentType = "application/json"
			} else {
				responseBody = "data: {\"type\":\"response.completed\",\"response\":" + responseBody + "}\n\ndata: [DONE]\n\n"
			}
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {contentType}},
				Body:       io.NopCloser(strings.NewReader(responseBody)),
			}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			account := newOpenAIImageGenerationControlTestAccount()
			account.Credentials["base_url"] = "https://compatible.example/v1"
			account.Extra = map[string]any{"openai_responses_supported": !nativeChat, "openai_passthrough": false}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

			result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(upstream.lastBody, "model").String())
			effortPath, otherPath := "reasoning.effort", "reasoning_effort"
			if nativeChat {
				effortPath, otherPath = otherPath, effortPath
			}
			require.Equal(t, "xhigh", gjson.GetBytes(upstream.lastBody, effortPath).String())
			require.False(t, gjson.GetBytes(upstream.lastBody, otherPath).Exists())
			require.NotNil(t, result.ReasoningEffort)
			require.Equal(t, "xhigh", *result.ReasoningEffort)
		})
	}
}
