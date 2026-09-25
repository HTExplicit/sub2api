package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func promptSendPolicyForPlatform(platform, role, position string) *BusinessSystemPromptService {
	service := newPromptRulesGatewayPolicy(role, position)
	snapshot, _ := service.CurrentSnapshot()
	rule := snapshot.RulePolicy.Rules[0]
	rule.Platforms = []string{platform}
	snapshot.RulePolicy.Rules[0] = rule
	snapshot.ResolvedRules[0].Rule = rule
	service.snapshot.Store(&snapshot)
	return service
}

func promptSendHTTPResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestPromptSendNativeAnthropicFallbackAndGrokActualWire(t *testing.T) {
	t.Run("native_anthropic", func(t *testing.T) {
		account := businessSystemPromptAPIKeyAccount(true)
		account.Platform = PlatformKimi
		account.Credentials["api_protocol"] = APIProtocolAnthropic
		account.Credentials["api_base_urls"] = map[string]any{APIProtocolAnthropic: "https://anthropic.example"}
		policy := promptSendPolicyForPlatform(PlatformKimi, "auto", "control_append")
		input := []byte(`{"model":"k3","max_tokens":32,"stream":false,"system":[{"type":"text","text":"client","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hello"}]}`)
		c, _ := newBusinessSystemPromptGinContext("/v1/messages", input)
		upstream := &httpUpstreamRecorder{resp: promptSendHTTPResponse(200, `{"id":"msg_test","type":"message","role":"assistant","model":"k3","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)}
		gateway := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: policy}
		_, err := gateway.ForwardAsAnthropic(context.Background(), c, account, input, "", "")
		require.NoError(t, err)
		require.Len(t, upstream.bodies, 1)
		require.Equal(t, "site-rule-content", gjson.GetBytes(upstream.lastBody, "system.1.text").String())
		require.Equal(t, "ephemeral", gjson.GetBytes(upstream.lastBody, "system.0.cache_control.type").String())
		require.NotContains(t, string(input), "site-rule-content")
	})
	t.Run("grok_early_responses_route", func(t *testing.T) {
		account := businessSystemPromptAPIKeyAccount(true)
		account.Platform = PlatformGrok
		input := []byte(`{"model":"grok-4.5","stream":false,"prompt_cache_key":"client-seed","instructions":"client","input":[{"role":"user","content":"hello"}]}`)
		c, _ := newBusinessSystemPromptGinContext("/v1/responses", input)
		upstream := &httpUpstreamRecorder{resp: promptSendHTTPResponse(200, `{"id":"resp_grok","object":"response","status":"completed","model":"grok-4.5","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)}
		gateway := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: promptSendPolicyForPlatform(PlatformGrok, "developer", "conversation_tail")}
		_, err := gateway.forwardGrokResponses(context.Background(), c, account, input, "grok-4.5", false, time.Now())
		require.NoError(t, err)
		require.Len(t, upstream.bodies, 1)
		require.Equal(t, "site-rule-content", gjson.GetBytes(upstream.lastBody, "input.1.content").String())
		require.Equal(t, "developer", gjson.GetBytes(upstream.lastBody, "input.1.role").String())
		require.Equal(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String(), upstream.lastReq.Header.Get(grokConversationIDHeader))
		require.NotContains(t, string(input), "site-rule-content")
	})
}

func TestPromptSendStrictChatRejectsDeveloperWithoutChangingRole(t *testing.T) {
	account := businessSystemPromptAPIKeyAccount(false)
	body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hello"}]}`)
	c, recorder := newBusinessSystemPromptGinContext("/v1/chat/completions", body)
	upstream := businessSystemPromptErrorUpstream()
	policy := newPromptRulesGatewayPolicy("developer", "conversation_head")
	gateway := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: policy}
	_, err := gateway.sendCCUpstreamRequest(context.Background(), c, account, "https://api.deepseek.com/v1/chat/completions", body, false, "fixture", "", "")
	require.ErrorIs(t, err, ErrPromptDeliveryUnsupported)
	require.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
	require.Empty(t, upstream.requests)
	responses, application, err := policy.ApplyForSend(c, account, []byte(`{"model":"deepseek-chat","input":"hello"}`), "responses", false)
	require.NoError(t, err)
	require.True(t, application.Applied)
	require.Equal(t, "developer", gjson.GetBytes(responses, "input.0.role").String())
}

func TestPromptSendResponsesRetryKeepsCleanSource(t *testing.T) {
	input := []byte(`{"model":"gpt-5.4","max_output_tokens":32,"stream":false,"prompt_cache_key":"client-seed","instructions":"client","input":[{"role":"user","content":"hello"}]}`)
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", input)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		promptSendHTTPResponse(400, `{"error":{"type":"invalid_request_error","code":"unsupported_parameter","message":"Unsupported parameter: max_output_tokens","param":"max_output_tokens"}}`),
		promptSendHTTPResponse(200, `{"id":"resp_retry","object":"response","status":"completed","model":"gpt-5.4","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`),
	}}
	gateway := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: newPromptRulesGatewayPolicy("developer", "conversation_head")}
	_, err := gateway.Forward(context.Background(), c, businessSystemPromptAPIKeyAccount(true), input)
	require.NoError(t, err)
	require.Len(t, upstream.bodies, 2)
	for _, wire := range upstream.bodies {
		require.Equal(t, 1, strings.Count(string(wire), "site-rule-content"))
		require.Equal(t, "hello", gjson.GetBytes(wire, "input.1.content").String())
	}
	require.False(t, gjson.GetBytes(upstream.bodies[1], "max_output_tokens").Exists())
	require.Equal(t, gjson.GetBytes(upstream.bodies[0], "prompt_cache_key").String(), gjson.GetBytes(upstream.bodies[1], "prompt_cache_key").String())
	require.NotContains(t, string(input), "site-rule-content")
}

func TestPromptSendReasoningRecoveryProjectsOnlyCipherEdits(t *testing.T) {
	policy := newPromptRulesGatewayPolicy("developer", "conversation_head")
	input := []byte(`{"model":"gpt-5.4","input":[{"type":"reasoning","id":"rs_1","encrypted_content":"cipher-one"},{"role":"user","content":"hello"}]}`)
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", input)
	wire, _, err := policy.ApplyForSend(c, businessSystemPromptAPIKeyAccount(true), input, "responses", false)
	require.NoError(t, err)
	retry, err := sjson.DeleteBytes(wire, "input.1.encrypted_content")
	require.NoError(t, err)
	clean, err := projectReasoningCipherEdits(input, wire, retry)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(clean, "input.0.encrypted_content").Exists())
	require.Equal(t, "rs_1", gjson.GetBytes(clean, "input.0.id").String())
	require.NotContains(t, string(clean), "site-rule-content")
	next, _, err := policy.ApplyForSend(c, businessSystemPromptAPIKeyAccount(true), clean, "responses", false)
	require.NoError(t, err)
	require.JSONEq(t, string(retry), string(next))
	changed, err := sjson.SetBytes(retry, "input.2.content", "other")
	require.NoError(t, err)
	_, err = projectReasoningCipherEdits(input, wire, changed)
	require.ErrorIs(t, err, ErrBusinessSystemPromptUnavailable)
}

func TestPromptSendRejectedFieldUsesCustomerIndex(t *testing.T) {
	policy := newPromptRulesGatewayPolicy("developer", "conversation_head")
	input := []byte(`{"model":"gpt-5.4","input":[{"type":"message","role":"assistant","status":"completed","content":"earlier"},{"role":"user","content":"hello"}]}`)
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", input)
	_, _, err := policy.ApplyForSend(c, businessSystemPromptAPIKeyAccount(true), input, "responses", false)
	require.NoError(t, err)
	rejection := []byte(`{"error":{"code":"unsupported_parameter","param":"input[1].status","message":"Unsupported parameter: input[1].status"}}`)
	clean, _, changed, err := normalizeBusinessPromptRejectedFieldRetryBody(c, 400, input, rejection)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(clean, "input.0.status").Exists())
	require.Equal(t, "hello", gjson.GetBytes(clean, "input.1.content").String())
	rejection = []byte(`{"error":{"code":"unsupported_parameter","param":"input[0].status","message":"Unsupported parameter: input[0].status"}}`)
	_, _, changed, err = normalizeBusinessPromptRejectedFieldRetryBody(c, 400, input, rejection)
	require.NoError(t, err)
	require.False(t, changed, "a server prompt rejection cannot remove a customer field")
}

func TestPromptFirstWSTurnRemainsFrozenAcrossAdapterAttempts(t *testing.T) {
	policy := newPromptRulesGatewayPolicy("developer", "conversation_tail")
	account := businessSystemPromptAPIKeyAccount(true)
	input := []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":"hello"}]}`)
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", input)
	beginBusinessSystemPromptFirstWSTurn(c)
	first, app, err := policy.ApplyForSend(c, account, input, "responses", false)
	require.NoError(t, err)
	next := promptRulesSnapshotForTest(BusinessSystemPromptSnapshot{Revision: 9, Enabled: true, Body: "new-revision"})
	policy.snapshot.Store(&next)
	beginBusinessSystemPromptFirstWSTurn(c)
	retry, frozen, err := policy.ApplyForSend(c, account, input, "responses", false)
	require.NoError(t, err)
	require.Equal(t, first, retry)
	require.Equal(t, app.Revision, frozen.Revision)
	beginBusinessSystemPromptRequestTurn(c)
	_, newTurn, err := policy.ApplyForSend(c, account, input, "responses", false)
	require.NoError(t, err)
	require.Equal(t, int64(9), newTurn.Revision)
}

func TestPromptStructuredEchoKeepsPublicClaudeBlocks(t *testing.T) {
	snapshot := unifiedPromptSnapshot(t, "anthropic", []string{"system", "system"}, []string{"after_last_user", "after_last_user"}, []string{"public-site", "private-site"})
	snapshot.ResolvedRules[0].PreserveEcho = true
	input := []byte(`{"model":"claude-opus-4-8","messages":[{"role":"assistant","content":"earlier"},{"role":"user","content":"hello"}]}`)
	target := BusinessSystemPromptTarget{Platform: "anthropic", Protocol: "messages", UpstreamModel: "claude-opus-4-8"}
	wire, app, err := ApplyBusinessSystemPromptToJSON(input, snapshot, target)
	require.NoError(t, err)
	require.True(t, app.Applied)
	c, _ := newBusinessSystemPromptGinContext("/v1/messages", input)
	businessSystemPromptRequestSet(c, businessSystemPromptContextKey(c, businessSystemPromptRequestApplicationKey, "messages"), cacheBusinessSystemPromptState(input, wire, snapshot, target, app))
	echo := rewritePromptRulesStructuredEcho(c, wire, "messages")
	require.Contains(t, string(echo), "public-site")
	require.NotContains(t, string(echo), "private-site")
	require.Equal(t, "earlier", gjson.GetBytes(echo, "messages.0.content").String())
	require.Equal(t, "hello", gjson.GetBytes(echo, "messages.1.content").String())
	require.Len(t, gjson.GetBytes(echo, "messages.2.content").Array(), 1)
}

func TestPromptEchoRedactsNestedInstructionCarrierOnly(t *testing.T) {
	policy := newPromptRulesGatewayPolicy("auto", "control_append")
	input := []byte(`{"model":"gpt-5.4","instructions":"client","input":"hello"}`)
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", input)
	wire, _, err := policy.ApplyForSend(c, businessSystemPromptAPIKeyAccount(true), input, "responses", false)
	require.NoError(t, err)
	echo, err := sjson.SetRawBytes([]byte(`{"text":"site-rule-content"}`), "error.body.request", wire)
	require.NoError(t, err)
	gateway := &OpenAIGatewayService{businessPromptService: policy}
	redacted := gateway.rewriteBusinessSystemPromptJSONForAnyRequest(c, echo)
	require.Equal(t, "client", gjson.GetBytes(redacted, "error.body.request.instructions").String())
	require.Equal(t, "site-rule-content", gjson.GetBytes(redacted, "text").String())
}
