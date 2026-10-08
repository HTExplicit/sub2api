//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type reasoningTestRepo struct {
	AccountRepository
	account *Account
}

func (r *reasoningTestRepo) GetByID(context.Context, int64) (*Account, error)         { return r.account, nil }
func (r *reasoningTestRepo) UpdateExtra(context.Context, int64, map[string]any) error { return nil }

func TestAccountTestReasoningSurvivesMappingAndWireCompression(t *testing.T) {
	a := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Credentials: map[string]any{"access_token": "test-access", "model_mapping": map[string]any{"friendly": "gpt-6-astra"}}}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(200, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")}}
	svc := &AccountTestService{accountRepo: &reasoningTestRepo{account: a}, httpUpstream: upstream}
	c, rec := newTestContext()
	enableCodexRequestZstd(t)
	require.NoError(t, svc.TestAccountConnection(c, a.ID, "friendly", "test", AccountTestModeDefault, AccountTestOptions{ReasoningEffort: "max"}))
	require.Len(t, upstream.requests, 1)
	raw, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	body := zstdDecodeForTest(t, raw)
	require.Equal(t, "gpt-6-astra", gjson.GetBytes(body, "model").String())
	require.Equal(t, "max", gjson.GetBytes(body, "reasoning.effort").String())
	require.Contains(t, rec.Body.String(), `"effective_reasoning_effort":"max"`)
	c, rec = newTestContext()
	require.Error(t, svc.TestAccountConnection(c, a.ID, "friendly", "test", AccountTestModeCompact, AccountTestOptions{ReasoningEffort: "ultra"}))
	require.Contains(t, rec.Body.String(), `"error":"reasoning effort is not supported by the selected account model"`)
	require.Len(t, upstream.requests, 1, "unsupported mode must fail before any upstream request")
	c, rec = newTestContext()
	require.Error(t, svc.TestAccountConnection(c, a.ID, "friendly", "test", AccountTestModeDefault, AccountTestOptions{ReasoningEffort: "invented"}))
	require.Contains(t, rec.Body.String(), `"error":"reasoning effort is not supported by the selected account model"`)
	require.Len(t, upstream.requests, 1, "unsupported effort must fail before any upstream request")
	c, rec = newTestContext()
	require.Error(t, svc.TestAccountConnection(c, a.ID, "friendly", "test", AccountTestModeDefault, AccountTestOptions{ReasoningEffort: "ultra"}))
	require.Contains(t, rec.Body.String(), `"error":"reasoning effort is not supported by the selected account model"`)
	require.Len(t, upstream.requests, 1, "ultra must fail before any upstream request")
}

func TestAccountTestReasoningOpenCodeGoSerializesNativeProtocol(t *testing.T) {
	for _, protocol := range []string{APIProtocolResponses, APIProtocolChatCompletions} {
		t.Run(protocol, func(t *testing.T) {
			account := openCodeGoTestAccount(44)
			account.Credentials["api_protocol"] = protocol
			supported := true
			account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{"test-model": {Reasoning: &supported, SupportedReasoningLevels: []string{"high"}}}})
			response := adaptiveCNChatTestResponse()
			if protocol == APIProtocolResponses {
				response = adaptiveCNResponsesTestResponse()
			}
			svc, upstream := adaptiveCNAccountTestService(account, response)
			c, recorded := newTestContext()
			require.NoError(t, svc.TestAccountConnection(c, account.ID, "test-model", "test", AccountTestModeDefault, AccountTestOptions{ReasoningEffort: "high"}))
			require.Len(t, upstream.requests, 1)
			body, err := io.ReadAll(upstream.requests[0].Body)
			require.NoError(t, err)
			field := "reasoning_effort"
			if protocol == APIProtocolResponses {
				field = "reasoning.effort"
			}
			require.Equal(t, "high", gjson.GetBytes(body, field).String())
			require.Contains(t, recorded.Body.String(), `"effective_reasoning_effort":"high"`)
		})
	}
}

func TestAccountTestReasoningBedrockSonnet55EffortSurvivesRequestPreparation(t *testing.T) {
	account := &Account{ID: 45, Platform: PlatformAnthropic, Type: AccountTypeBedrock, Status: StatusActive,
		Credentials: map[string]any{"auth_mode": "apikey", "api_key": "test-key", "aws_region": "us-east-1"}}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(200, `{"content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn"}`)}}
	svc := &AccountTestService{accountRepo: &reasoningTestRepo{account: account}, httpUpstream: upstream}
	c, recorded := newTestContext()
	require.NoError(t, svc.TestAccountConnection(c, account.ID, "claude-sonnet-5-5", "", AccountTestModeDefault, AccountTestOptions{ReasoningEffort: "high"}))
	require.Len(t, upstream.requests, 1)
	require.Contains(t, upstream.requests[0].URL.Path, "global.anthropic.claude-sonnet-5-5")
	body, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.Equal(t, "high", gjson.GetBytes(body, "output_config.effort").String())
	require.Equal(t, "adaptive", gjson.GetBytes(body, "thinking.type").String())
	require.Equal(t, int64(64000), gjson.GetBytes(body, "max_tokens").Int())
	require.False(t, gjson.GetBytes(body, "temperature").Exists())
	require.Contains(t, recorded.Body.String(), `"effective_reasoning_effort":"high"`)
}

func TestAccountTestReasoningGrokSendsForwardedEffort(t *testing.T) {
	account := &Account{ID: 46, Platform: PlatformGrok, Type: AccountTypeAPIKey, Status: StatusActive, Credentials: map[string]any{"api_key": "test-key"}}
	supported := true
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{"grok-4.3": {Reasoning: &supported, SupportedReasoningLevels: []string{"low", "high", "xhigh"}}}})
	upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(200, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\ndata: {\"type\":\"response.completed\"}\n\n")}}
	svc := &AccountTestService{accountRepo: &reasoningTestRepo{account: account}, httpUpstream: upstream}
	c, recorded := newTestContext()
	require.NoError(t, svc.TestAccountConnection(c, account.ID, "grok-4.3", "", AccountTestModeDefault, AccountTestOptions{ReasoningEffort: "xhigh"}))
	require.Len(t, upstream.requests, 1)
	body, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.Equal(t, "high", gjson.GetBytes(body, "reasoning.effort").String(), "Responses forwarding sends high for this model")
	require.Contains(t, recorded.Body.String(), `"effective_reasoning_effort":"high"`)
}

func TestAccountTestReasoningChatSendsForwardedGLMEffort(t *testing.T) {
	account := adaptiveCNAccountTestAccount(47, PlatformZhipu)
	account.Credentials["api_protocol"] = APIProtocolChatCompletions
	account.Credentials["base_url"] = "http://chat.example/v1"
	supported := true
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{"glm-5": {Reasoning: &supported, SupportedReasoningLevels: []string{"low", "medium", "high"}}}})
	svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNChatTestResponse())
	c, recorded := newTestContext()
	require.NoError(t, svc.TestAccountConnection(c, account.ID, "glm-5", "test", AccountTestModeDefault, AccountTestOptions{ReasoningEffort: "low"}))
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "high", gjson.GetBytes(upstream.lastBody, "reasoning_effort").String(), "chat forwarding moves GLM efforts onto high/max")
	require.Contains(t, recorded.Body.String(), `"effective_reasoning_effort":"high"`)
}
