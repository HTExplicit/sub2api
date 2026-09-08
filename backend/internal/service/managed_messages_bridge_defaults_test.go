//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func managedMessagesPureAccount() *Account {
	return &Account{ID: 16062, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://upstream.example/v1", "api_key": "synthetic-unit-key"}}
}

func managedMessagesPureRoute(publicModel string) *ManagedModelRequest {
	return &ManagedModelRequest{GroupID: 34, Endpoint: CompositeRouteEndpointMessages,
		Route: ManagedModelRoute{PublicModel: publicModel, TargetPlatform: PlatformOpenAI}}
}

func managedMessagesPureJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	require.NoError(t, err)
	return body
}

func managedMessagesPureResponses(t *testing.T) *apicompat.ResponsesRequest {
	t.Helper()
	var request apicompat.ResponsesRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"model":"vendor/preserved-wire-model","instructions":"fixed system instruction",
		"input":[{"type":"reasoning","encrypted_content":"synthetic-opaque","summary":[],"x-unknown":9007199254740993},
			{"type":"function_call","call_id":"call_1","name":"capability_ping","arguments":"{}","namespace":null,"phase":"analysis","x-unknown":{"keep":true}},
			{"type":"function_call_output","call_id":"call_1","output":"synthetic-tool-result"}],
		"tools":[{"type":"function","name":"capability_ping","parameters":{"type":"object","additionalProperties":false}}],
		"tool_choice":{"type":"function","name":"capability_ping","x-unknown":9007199254740993},
		"max_output_tokens":256,"parallel_tool_calls":true,"stream":true,"store":false,
		"reasoning":{"effort":"medium","summary":"auto"},"text":{"verbosity":"medium"},
		"include":["reasoning.encrypted_content"],"service_tier":"priority",
		"prompt_cache_key":"synthetic-cache-key","previous_response_id":"synthetic-response-id"
	}`), &request))
	return &request
}

func managedMessagesPureChat(t *testing.T) *apicompat.ChatCompletionsRequest {
	t.Helper()
	var request apicompat.ChatCompletionsRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"model":"vendor/preserved-wire-model",
		"messages":[{"role":"assistant","content":[{"type":"text","text":"fixed","x-unknown":9007199254740993}],"reasoning_content":"synthetic-thinking",
			"tool_calls":[{"id":"call_1","type":"function","function":{"name":"capability_ping","arguments":"{}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"synthetic-tool-result"}],
		"tools":[{"type":"function","function":{"name":"capability_ping","parameters":{"type":"object","additionalProperties":false}}}],
		"tool_choice":{"type":"function","function":{"name":"capability_ping"},"x-unknown":9007199254740993},
		"max_completion_tokens":256,"parallel_tool_calls":true,"stream":true,
		"stream_options":{"include_usage":true},"reasoning_effort":"medium",
		"service_tier":"priority","stop":["END"],"response_format":{"type":"json_object"}
	}`), &request))
	return &request
}

func TestManagedMessagesBridgePureDefaultsPreservePayload(t *testing.T) {
	for _, tc := range []struct {
		name, public, upstream string
		thinking               *apicompat.AnthropicThinking
		outputConfig           *apicompat.AnthropicOutputConfig
		deepseekOff            bool
		parallel               bool
	}{
		{name: "generic model", public: "grok-4.6", upstream: "vendor/grok-4.6", parallel: true},
		{name: "disabled thinking", public: "gemini-3.8-flash", upstream: "gemini-3.8-flash", thinking: &apicompat.AnthropicThinking{Type: " disabled "}},
		{name: "empty output config", public: "MiniMax-M3", upstream: "MiniMax-M3", outputConfig: &apicompat.AnthropicOutputConfig{}, parallel: true},
		{name: "blank effort", public: "kimi-k3", upstream: "kimi-k3", outputConfig: &apicompat.AnthropicOutputConfig{Effort: " \t "}},
		{name: "flash canonical", public: "deepseek-v4-flash", upstream: "deepseek-v4-flash", deepseekOff: true, parallel: true},
		{name: "pro namespaced case", public: "DeepSeek-V4-Pro", upstream: " DeepSeek-AI/DeepSeek-V4-Pro ", deepseekOff: true},
		{name: "flash disabled case", public: "deepseek-v4-flash", upstream: "vendor/DEEPSEEK-V4-FLASH", thinking: &apicompat.AnthropicThinking{Type: " DiSaBlEd "}, deepseekOff: true, parallel: true},
		{name: "known V4 pair", public: "deepseek-v4-flash", upstream: "deepseek-ai/deepseek-v4-pro", deepseekOff: true},
		{name: "unknown public alias", public: "coding-alias", upstream: "deepseek-v4-flash", parallel: true},
		{name: "public namespace is not canonical", public: "deepseek-ai/deepseek-v4-flash", upstream: "deepseek-v4-flash"},
		{name: "unknown mapped target", public: "deepseek-v4-flash", upstream: "vendor/private-flash", parallel: true},
		{name: "dated variant is not exact", public: "deepseek-v4-flash", upstream: "deepseek-v4-flash-0731"},
		{name: "vision variant is not exact", public: "deepseek-v4-flash", upstream: "deepseek-v4-flash-vision-exp", parallel: true},
		{name: "future version is not exact", public: "deepseek-v5-flash", upstream: "deepseek-v5-flash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := managedMessagesPureAccount()
			route := managedMessagesPureRoute(tc.public)
			ctx := WithManagedModelRequest(context.Background(), route)
			requested := &apicompat.AnthropicRequest{Model: "s2pub-internal-selector", MaxTokens: 256, Thinking: tc.thinking, OutputConfig: tc.outputConfig}
			beforeAccount, beforeRoute := managedMessagesPureJSON(t, account), managedMessagesPureJSON(t, route)
			beforeRequested := managedMessagesPureJSON(t, requested)
			profile := managedMessagesDefaultsForRequest(ctx, account, requested, tc.upstream)

			responses := managedMessagesPureResponses(t)
			*responses.ParallelToolCalls = tc.parallel
			opaqueInput, choice := string(responses.Input), string(responses.ToolChoice)
			wantResponses := *responses
			wantResponses.Reasoning, wantResponses.Text, wantResponses.Include = nil, nil, nil
			if tc.deepseekOff {
				wantResponses.Reasoning = &apicompat.ResponsesReasoning{Effort: "none"}
			}
			wantResponsesJSON := managedMessagesPureJSON(t, &wantResponses)
			profile.applyResponses(responses)
			require.Equal(t, wantResponsesJSON, managedMessagesPureJSON(t, responses), "only manufactured defaults may change")
			require.Equal(t, opaqueInput, string(responses.Input))
			require.Equal(t, choice, string(responses.ToolChoice))
			require.Equal(t, 256, *responses.MaxOutputTokens)
			require.Equal(t, tc.parallel, *responses.ParallelToolCalls)

			chat := managedMessagesPureChat(t)
			*chat.ParallelToolCalls = tc.parallel
			wantChat := *chat
			wantChat.ReasoningEffort = ""
			wantChatJSON := managedMessagesPureJSON(t, &wantChat)
			profile.applyChat(chat)
			require.Equal(t, wantChatJSON, managedMessagesPureJSON(t, chat))
			require.Equal(t, 256, *chat.MaxCompletionTokens)
			body := managedMessagesPureJSON(t, chat)
			patched, err := profile.patchChatBody(body)
			require.NoError(t, err)
			require.False(t, gjson.GetBytes(patched, "reasoning_effort").Exists(), "Chat must never gain reasoning_effort:none")
			if tc.deepseekOff {
				require.Equal(t, `{"type":"disabled"}`, gjson.GetBytes(patched, "thinking").Raw)
				require.EqualValues(t, 256, gjson.GetBytes(patched, "max_tokens").Int())
				require.False(t, gjson.GetBytes(patched, "max_completion_tokens").Exists())
				for _, path := range []string{"model", "messages", "tools", "tool_choice", "parallel_tool_calls", "stream_options", "response_format"} {
					require.Equal(t, gjson.GetBytes(body, path).Raw, gjson.GetBytes(patched, path).Raw, path)
				}
			} else {
				require.Equal(t, body, patched, "non-exact V4 routes must not acquire provider-specific thinking controls")
			}
			require.Equal(t, beforeAccount, managedMessagesPureJSON(t, account))
			require.Equal(t, beforeRoute, managedMessagesPureJSON(t, route))
			require.Equal(t, beforeRequested, managedMessagesPureJSON(t, requested))
		})
	}
}

func TestManagedMessagesBridgePureDeepseekChatExistingBudgetPrecedence(t *testing.T) {
	profile := managedMessagesBridgeDefaults{omitImplicitReasoning: true, disableDeepseekV4: true}
	body := []byte(`{"model":"deepseek-v4-flash","max_tokens":96,"max_completion_tokens":256,"tool_choice":{"type":"function","function":{"name":"capability_ping"}},"x-opaque":9007199254740993}`)
	patched, err := profile.patchChatBody(body)
	require.NoError(t, err)
	require.EqualValues(t, 96, gjson.GetBytes(patched, "max_tokens").Int())
	require.False(t, gjson.GetBytes(patched, "max_completion_tokens").Exists())
	require.Equal(t, gjson.GetBytes(body, "tool_choice").Raw, gjson.GetBytes(patched, "tool_choice").Raw)
	require.Equal(t, "9007199254740993", gjson.GetBytes(patched, "x-opaque").Raw)
}

func managedMessagesPureAssertUnchanged(t *testing.T, profile managedMessagesBridgeDefaults) {
	t.Helper()
	responses, chat := managedMessagesPureResponses(t), managedMessagesPureChat(t)
	beforeResponses, beforeChat := managedMessagesPureJSON(t, responses), managedMessagesPureJSON(t, chat)
	profile.applyResponses(responses)
	profile.applyChat(chat)
	require.Equal(t, beforeResponses, managedMessagesPureJSON(t, responses))
	require.Equal(t, beforeChat, managedMessagesPureJSON(t, chat))
	body := []byte(" {\n  \"model\":\"unchanged\",\"max_completion_tokens\":256,\"x-opaque\":9007199254740993,\"thinking\":{\"type\":\"enabled\"}\n } ")
	patched, err := profile.patchChatBody(body)
	require.NoError(t, err)
	require.Equal(t, body, patched, "out-of-scope raw bytes must remain identical")
	profile.applyResponses(nil)
	profile.applyChat(nil)
}

func TestManagedMessagesBridgePureScopeGuardsAreByteEquivalent(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		mutate                 func(*Account, *ManagedModelRequest)
		upstream               string
		noManaged, nilContext  bool
		nilAccount, nilRequest bool
	}{
		{name: "private", noManaged: true},
		{name: "nil context", nilContext: true},
		{name: "nil account", nilAccount: true},
		{name: "nil requested", nilRequest: true},
		{name: "Responses endpoint", mutate: func(_ *Account, r *ManagedModelRequest) { r.Endpoint = CompositeRouteEndpointResponses }},
		{name: "Chat endpoint", mutate: func(_ *Account, r *ManagedModelRequest) { r.Endpoint = CompositeRouteEndpointChatCompletions }},
		{name: "WS endpoint", mutate: func(_ *Account, r *ManagedModelRequest) { r.Endpoint = ManagedModelEndpointResponsesWebSocket }},
		{name: "empty public model", mutate: func(_ *Account, r *ManagedModelRequest) { r.Route.PublicModel = " \t " }},
		{name: "empty upstream", upstream: " \t "},
		{name: "OAuth", mutate: func(a *Account, _ *ManagedModelRequest) { a.Type = AccountTypeOAuth }},
		{name: "setup token", mutate: func(a *Account, _ *ManagedModelRequest) { a.Type = AccountTypeSetupToken }},
		{name: "native DeepSeek", mutate: func(a *Account, _ *ManagedModelRequest) { a.Platform = PlatformDeepseek }},
		{name: "native Anthropic", mutate: func(a *Account, _ *ManagedModelRequest) { a.Platform = PlatformAnthropic }},
		{name: "native Kimi", mutate: func(a *Account, _ *ManagedModelRequest) { a.Platform = PlatformKimi }},
		{name: "Cindy platform", mutate: func(a *Account, _ *ManagedModelRequest) { a.Platform = PlatformCindy }},
		{name: "Cindy profile", mutate: func(a *Account, _ *ManagedModelRequest) { a.ProviderProfile = ProviderProfileCindyLaxaV1 }},
		{name: "legacy Cindy host", mutate: func(a *Account, _ *ManagedModelRequest) { a.Credentials["base_url"] = "https://api.laxarouter.ai" }},
		{name: "public GPT", mutate: func(_ *Account, r *ManagedModelRequest) { r.Route.PublicModel = "gpt-5.6-sol" }},
		{name: "resolved GPT", upstream: "vendor/GPT-5.6-SOL"},
		{name: "GPT underscore spelling", upstream: "vendor/GPT_5.4", mutate: func(_ *Account, r *ManagedModelRequest) { r.Route.PublicModel = "coding-alias" }},
		{name: "GPT compact spelling", upstream: "vendor/gpt5.4", mutate: func(_ *Account, r *ManagedModelRequest) { r.Route.PublicModel = "coding-alias" }},
		{name: "GPT spaced spelling", upstream: "vendor/GPT 5.4", mutate: func(_ *Account, r *ManagedModelRequest) { r.Route.PublicModel = "coding-alias" }},
		{name: "public GPT spaced spelling", mutate: func(_ *Account, r *ManagedModelRequest) { r.Route.PublicModel = "GPT 5.4" }},
		{name: "public o1", mutate: func(_ *Account, r *ManagedModelRequest) { r.Route.PublicModel = "o1" }},
		{name: "resolved o3", upstream: "openai/o3-mini"},
		{name: "resolved o4", upstream: " O4-MINI "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account, route := managedMessagesPureAccount(), managedMessagesPureRoute("deepseek-v4-flash")
			if tc.mutate != nil {
				tc.mutate(account, route)
			}
			var ctx context.Context = WithManagedModelRequest(context.Background(), route)
			if tc.noManaged {
				ctx = context.Background()
			}
			if tc.nilContext {
				ctx = nil
			}
			if tc.nilAccount {
				account = nil
			}
			requested := &apicompat.AnthropicRequest{Model: "s2pub-private-selector", MaxTokens: 256}
			if tc.nilRequest {
				requested = nil
			}
			upstream := tc.upstream
			if upstream == "" {
				upstream = "deepseek-v4-flash"
			}
			managedMessagesPureAssertUnchanged(t, managedMessagesDefaultsForRequest(ctx, account, requested, upstream))
		})
	}
}

func TestManagedMessagesBridgePureExplicitSettingsKeepExistingPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, thinking, effort string
		hasThinking            bool
	}{
		{name: "enabled", thinking: "enabled", hasThinking: true},
		{name: "adaptive", thinking: "adaptive", hasThinking: true},
		{name: "enabled case", thinking: " ENABLED ", hasThinking: true},
		{name: "unknown thinking", thinking: "future-mode", hasThinking: true},
		{name: "empty thinking object", hasThinking: true},
		{name: "explicit none", effort: "none"},
		{name: "explicit low", effort: "low"},
		{name: "explicit max", effort: "max"},
		{name: "unknown explicit effort", effort: "future-effort"},
		{name: "disabled plus effort", thinking: "disabled", hasThinking: true, effort: "high"},
		{name: "disabled plus none", thinking: "disabled", hasThinking: true, effort: "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requested := &apicompat.AnthropicRequest{Model: "s2pub-internal-selector", MaxTokens: 256}
			if tc.hasThinking {
				requested.Thinking = &apicompat.AnthropicThinking{Type: tc.thinking, BudgetTokens: 128}
			}
			if tc.effort != "" {
				requested.OutputConfig = &apicompat.AnthropicOutputConfig{Effort: tc.effort}
			}
			before := managedMessagesPureJSON(t, requested)
			ctx := WithManagedModelRequest(context.Background(), managedMessagesPureRoute("deepseek-v4-pro"))
			profile := managedMessagesDefaultsForRequest(ctx, managedMessagesPureAccount(), requested, "deepseek-ai/DeepSeek-V4-Pro")
			managedMessagesPureAssertUnchanged(t, profile)
			require.Equal(t, before, managedMessagesPureJSON(t, requested))
		})
	}
}

func TestManagedMessagesBridgePureDeepseekNoneSurvivesExistingPolicy(t *testing.T) {
	account := managedMessagesPureAccount()
	ctx := WithManagedModelRequest(context.Background(), managedMessagesPureRoute("deepseek-v4-flash"))
	profile := managedMessagesDefaultsForRequest(ctx, account, &apicompat.AnthropicRequest{MaxTokens: 256}, "deepseek-v4-flash")
	responses := managedMessagesPureResponses(t)
	profile.applyResponses(responses)
	body := managedMessagesPureJSON(t, responses)
	require.Equal(t, "none", gjson.GetBytes(body, "reasoning.effort").String())
	for _, ceiling := range []string{"", "low", "high"} {
		governed, changed, err := ApplyOpenAIReasoningEffortPolicy(body, ceiling, nil, ReasoningEffortOverLimitDeny)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, body, governed, "a ceiling must not erase an explicit none wire value")
	}
	filtered, err := filterOpenAIResponsesNoneReasoningEffortForAccount(account, body)
	require.NoError(t, err)
	require.Equal(t, body, filtered, "ordinary OpenAI API-key wire must preserve none even on a third-party host")
}
