//go:build unit

package service

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAccountTestPromptBuildersPreserveUserText(t *testing.T) {
	prompt := "  你的知识库库截止日期是什么时间,直接回复不要联网\n"
	require.NoError(t, ValidateAccountTestPrompt(strings.Repeat("😀", 8192)))
	require.Error(t, ValidateAccountTestPrompt(strings.Repeat("😀", 8193)))
	require.Equal(t, "Reply with OK.", resolveAccountTestPrompt(" \n"))
	claude, err := createTestPayload("claude-sonnet-4-6", prompt)
	require.NoError(t, err)
	raw, _ := json.Marshal(claude)
	require.Equal(t, prompt, gjson.GetBytes(raw, "messages.0.content.0.text").String())
	vertex, err := buildVertexAnthropicRequestBody(raw)
	require.NoError(t, err)
	require.Equal(t, prompt, gjson.GetBytes(vertex, "messages.0.content.0.text").String())
	raw, _ = json.Marshal(createOpenAITestPayload("gpt-5.6-sol", true, prompt))
	require.Equal(t, prompt, gjson.GetBytes(raw, "input.0.content.0.text").String())
	raw, _ = json.Marshal(createOpenAIChatCompletionsTestPayload("gpt-5.6-sol", prompt))
	require.Equal(t, prompt, gjson.GetBytes(raw, "messages.0.content").String())
	require.Equal(t, prompt, gjson.GetBytes(createGeminiTestPayload("gemini-3.1-pro", prompt), "contents.0.parts.0.text").String())
	antigravity := &AntigravityGatewayService{}
	for _, build := range []func(string, string, ...string) ([]byte, error){antigravity.buildGeminiTestRequest, antigravity.buildClaudeTestRequest} {
		raw, err = build("project", "claude-sonnet-4-6", prompt)
		require.NoError(t, err)
		require.Contains(t, string(raw), "你的知识库库截止日期")
	}
}

func TestAccountTestPromptAntigravityScheduledDefaults(t *testing.T) {
	svc := &AntigravityGatewayService{}
	for _, tc := range []struct {
		name, model string
		build       func(string, string, ...string) ([]byte, error)
	}{
		{"gemini", "gemini-3.1-pro", svc.buildGeminiTestRequest},
		{"claude", "claude-sonnet-4-6", svc.buildClaudeTestRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, input := range []struct {
				name    string
				prompts []string
				want    string
				limit   int64
			}{
				{"scheduled", nil, "Reply with OK.", 256},
				{"blank manual", []string{""}, "Reply with OK.", 256},
				{"custom manual", []string{"custom question"}, "custom question", 1024},
			} {
				t.Run(input.name, func(t *testing.T) {
					raw, err := tc.build("project", tc.model, input.prompts...)
					require.NoError(t, err)
					require.Equal(t, input.want, gjson.GetBytes(raw, "request.contents.0.parts.0.text").String())
					require.Equal(t, input.limit, gjson.GetBytes(raw, "request.generationConfig.maxOutputTokens").Int())
				})
			}
		})
	}
}

func TestAccountTestPromptAdaptiveAndOpenCodeFinalRequests(t *testing.T) {
	prompt := "  preserve 用户原文\n"
	account := adaptiveCNAccountTestAccount(991, PlatformDeepseek)
	svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNChatTestResponse(), adaptiveCNAnthropicTestResponse(), adaptiveCNResponsesTestResponse())
	c, _ := newTestContext()
	require.NoError(t, svc.TestAccountConnection(c, account.ID, "deepseek-v4-pro", prompt, ""))
	require.Len(t, upstream.requests, 3)
	for _, req := range upstream.requests {
		raw, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		want, _ := json.Marshal(prompt)
		require.Contains(t, string(raw), string(want))
	}
	for _, protocol := range []string{APIProtocolChatCompletions, APIProtocolAnthropic, APIProtocolResponses} {
		account = adaptiveCNAccountTestAccount(992, PlatformOpenCodeGo)
		account.Credentials["api_protocol"] = protocol
		account.Credentials["base_url"] = "https://opencode.ai/zen/go/v1"
		svc, upstream = adaptiveCNAccountTestService(account, adaptiveCNResponsesTestResponse())
		c, _ = newTestContext()
		_ = svc.TestAccountConnection(c, account.ID, "gpt-5.6-sol", prompt, "")
		require.Len(t, upstream.requests, 1)
		raw, err := io.ReadAll(upstream.requests[0].Body)
		require.NoError(t, err)
		want, _ := json.Marshal(prompt)
		require.Contains(t, string(raw), string(want))
	}
}

func TestAccountTestPromptOAuthFinalTicketUsesMappedModel(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{OpenAICodexTicket: config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, OpenAICodexRequestZstd: true}}
	a := ticketTestAccount(41)
	a.Status = StatusActive
	a.Credentials["model_mapping"] = map[string]any{"alias": "gpt-5.6-sol"}
	a.Credentials["header_override_enabled"] = true
	a.Credentials["header_overrides"] = map[string]any{openAICodexTurnStateHeader: fakeCodexTicketState(312)}
	a.Extra = map[string]any{openAICodexTicketExtraKey("gpt-5.6-sol"): &CodexTicketRecord{State: fakeCodexTicketState(292), Length: 292, AccountID: a.ID, Model: "gpt-5.6-sol", ExpiresAt: time.Now().Add(time.Hour)}}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(200, "data: {\"type\":\"response.completed\"}\n\n")}}
	gateway := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, accountRepo: &routingAccountRepositoryFixture{account: a}}
	// Cookie routing replaced the 292 header: the plugin only references a
	// host-verified qualification, gated like business traffic on the final model.
	var qualification *extensionv1.CodexRoutingQualification
	gateway.nativeCodexRuntime = nativeTicketTestRuntime(t, cfg.Gateway.OpenAICodexTicket, func(in extensionv1.Invocation) (extensionv1.Result, error) {
		if in.Operation == "codex.routing.observe" {
			return extensionv1.Result{Payload: json.RawMessage(`{}`)}, nil
		}
		var request extensionv1.SchedulingRequest
		require.NoError(t, json.Unmarshal(in.Payload, &request))
		require.Equal(t, "gpt-5.6-sol", request.Model)
		if qualification == nil {
			return extensionv1.Result{Code: "ticket_missing"}, nil
		}
		raw, _ := json.Marshal(extensionv1.CodexRoutingInjection{Headers: map[string]string{}, Qualification: qualification})
		return extensionv1.Result{Payload: raw}, nil
	})
	store := &routingMemoryStore{values: map[string]extensionv1.StateResult{}}
	gateway.nativeCodexRuntime.repo = store

	scope, err := gateway.PrepareCodexRoutingScope(t.Context(), a.ID, "http")
	require.NoError(t, err)
	scope.ConnectionLeaseID, scope.RouteEvidence = "account-test-connection", "connection"
	now := time.Now().UTC()
	expiry := now.Add(time.Minute)
	cookie := codexRoutingCookie{Name: "__cflb", Value: "verified-route", Domain: "chatgpt.com", Path: "/", FirstSeen: now, ExpiresAt: expiry}
	clock := codexRoutingCookieClock{Scope: scope}
	clock.apply([]codexRoutingCookieChange{{Cookie: cookie}}, now)
	clockKey := "clock." + codexRoutingScopeKey(scope)
	raw, _ := json.Marshal(clock)
	_, err = store.CompareSwapExtensionState(t.Context(), codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: clockKey, Value: raw})
	require.NoError(t, err)
	raw, _ = json.Marshal(codexRoutingPrivateBundle{Schema: 2, Scope: scope, Cookies: []codexRoutingCookie{cookie}, ClockKey: clockKey, ClockRevision: 1, Status: "qualified", Model: "gpt-5.6-sol", ExpiresAt: expiry})
	_, err = store.CompareSwapExtensionState(t.Context(), codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: "bundle.account-test", Value: raw})
	require.NoError(t, err)
	qualification = &extensionv1.CodexRoutingQualification{Scope: scope, Model: "gpt-5.6-sol", VerifiedAt: now, ExpiresAt: expiry, Bundle: extensionv1.CodexRoutingBundleRef{Key: "bundle.account-test", Revision: 1, ExpiresAt: expiry, ConnectionLeaseID: scope.ConnectionLeaseID}}
	svc := &AccountTestService{cfg: cfg, httpUpstream: upstream, openAIGatewayService: gateway}
	c, rec := newTestContext()
	prompt := "你的知识库库截止日期是什么时间,直接回复不要联网"
	c.Request = c.Request.WithContext(withCodexTransportFixture(c.Request.Context(), true))
	require.NoError(t, svc.testOpenAIAccountConnection(c, a, "alias", prompt, ""))
	require.Len(t, upstream.requests, 1)
	require.Equal(t, []string{"account-test-connection"}, upstream.leases)
	req := upstream.requests[0]
	require.Equal(t, "__cflb=verified-route", req.Header.Get("Cookie"))
	require.Empty(t, req.Header.Get(openAICodexTurnStateHeader), "legacy 292/312 material never reaches the wire")
	raw, err = io.ReadAll(req.Body)
	require.NoError(t, err)
	body := zstdDecodeForTest(t, raw)
	require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(body, "model").String())
	require.Equal(t, prompt, gjson.GetBytes(body, "input.0.content.0.text").String())
	require.NotContains(t, rec.Body.String(), "verified-route")
	require.NotContains(t, rec.Body.String(), fakeCodexTicketState(292))
	require.Equal(t, 1, strings.Count(rec.Body.String(), "Final Codex ticket"), "the leased wire is reported once")
	c, rec = newTestContext()
	qualification = nil // Legacy Extra still has a ticket; only the plugin qualification may admit the model.
	require.Error(t, svc.testOpenAIAccountConnection(c, a, "alias", prompt, ""))
	require.Len(t, upstream.requests, 1)
	require.Contains(t, rec.Body.String(), "没有有效的 Cookie 路由验证")
}
