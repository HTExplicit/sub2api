//go:build unit

package service

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

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

// An OAuth account test sends the mapped model over the ordinary transport
// and reports the final wire headers once, without Authorization.
func TestAccountTestPromptOAuthMappedModelReportsFinalWire(t *testing.T) {
	a := &Account{ID: 41, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "tok", "chatgpt_account_id": "acc-1"}}
	a.Status = StatusActive
	a.Credentials["access_token"] = "account-test-access-token-canary"
	a.Credentials["model_mapping"] = map[string]any{"alias": "gpt-5.6-sol"}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(200, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")}}
	svc := &AccountTestService{httpUpstream: upstream}
	c, rec := newTestContext()
	prompt := "你的知识库库截止日期是什么时间,直接回复不要联网"
	enableCodexRequestZstd(t)
	require.NoError(t, svc.testOpenAIAccountConnection(c, a, "alias", prompt, ""))
	require.Len(t, upstream.requests, 1)
	req := upstream.requests[0]
	raw, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	body := zstdDecodeForTest(t, raw)
	require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(body, "model").String())
	require.Equal(t, prompt, gjson.GetBytes(body, "input.0.content.0.text").String())
	// The reported wire headers leave out Authorization: the access token went
	// on the wire but never appears in the test output.
	require.Equal(t, "Bearer account-test-access-token-canary", req.Header.Get("Authorization"))
	require.NotContains(t, rec.Body.String(), "account-test-access-token-canary")
	var wireHeaders map[string]string
	reports := 0
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if payload, ok := strings.CutPrefix(line, "data: "); ok && gjson.Get(payload, "text").String() == "Codex wire headers" {
			reports++
			require.NoError(t, json.Unmarshal([]byte(gjson.Get(payload, "data").Raw), &wireHeaders))
		}
	}
	require.Equal(t, 1, reports, "the final wire is reported once")
	require.NotEmpty(t, wireHeaders)
	for name := range wireHeaders {
		require.False(t, strings.EqualFold(name, "Authorization"), "Authorization is never reported")
	}
}
