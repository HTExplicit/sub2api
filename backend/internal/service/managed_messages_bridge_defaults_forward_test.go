//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func managedMessagesForwardFixture(public, actual string, chat bool) (*Account, *ManagedModelRequest) {
	const groupID = 34
	selector := ManagedModelSelector(groupID, public)
	mode := openai_compat.ResponsesSupportModeForceResponses
	if chat {
		mode = openai_compat.ResponsesSupportModeForceChatCompletions
	}
	account := &Account{
		ID: 16062, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
		Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID},
		Credentials: map[string]any{
			"api_key": "synthetic-not-a-real-key", "base_url": "https://relay.example.test/v1",
			"model_mapping": map[string]any{selector: actual, public: "private-route-must-stay"},
		},
		Extra: map[string]any{openai_compat.ExtraKeyResponsesMode: string(mode)},
	}
	endpoints := []string{CompositeRouteEndpointMessages, CompositeRouteEndpointResponses, CompositeRouteEndpointChatCompletions}
	request := &ManagedModelRequest{
		GroupID: groupID, Endpoint: CompositeRouteEndpointMessages, SubmittedModel: public,
		Route: ManagedModelRoute{PublicModel: public, Selector: selector, TargetPlatform: PlatformOpenAI, Endpoints: endpoints,
			Accounts: []ManagedModelRouteAccount{{AccountID: account.ID, UpstreamModel: actual,
				AccountFingerprint: ManagedModelAccountFingerprint(account), Endpoints: endpoints}}},
	}
	return account, request
}

func TestManagedMessagesBridgeForwardWireDefaults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name, public, actual, thinking string
		chat                           bool
		wantEffort, wantThinking       string
	}{
		{name: "Grok Responses no implicit medium", public: "grok-4.6", actual: "vendor/Grok-4.6"},
		{name: "DeepSeek Responses none", public: "deepseek-v4-flash", actual: "deepseek-ai/DeepSeek-V4-Flash", wantEffort: "none"},
		{name: "DeepSeek Chat disabled", public: "deepseek-v4-pro", actual: "DeepSeek-V4-Pro", chat: true, wantThinking: "disabled"},
		{name: "explicit thinking stays enabled", public: "deepseek-v4-flash", actual: "deepseek-v4-flash", thinking: `,"thinking":{"type":"enabled","budget_tokens":1024}`, wantEffort: "medium"},
	} {
		t.Run(test.name, func(t *testing.T) {
			account, managed := managedMessagesForwardFixture(test.public, test.actual, test.chat)
			body := []byte(`{"model":"` + managed.Route.Selector + `","max_tokens":256,"messages":[{"role":"user","content":"Call capability_ping exactly once with value ok."}],"tools":[{"name":"capability_ping","input_schema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}}],"tool_choice":{"type":"tool","name":"capability_ping"},"stream":false` + test.thinking + `}`)
			before, err := json.Marshal(account)
			require.NoError(t, err)
			upstream := &capabilityProbeFakeUpstream{do: func(req *http.Request, sent []byte, count int) (*http.Response, error) {
				require.Equal(t, 1, count, "normalization must not add retries")
				require.Equal(t, test.actual, gjson.GetBytes(sent, "model").String(), "preserve exact mapped namespace/case")
				if test.chat {
					require.Equal(t, "/v1/chat/completions", req.URL.Path)
					require.EqualValues(t, 256, gjson.GetBytes(sent, "max_tokens").Int())
					require.False(t, gjson.GetBytes(sent, "max_completion_tokens").Exists(), "DeepSeek Chat must not send both budget spellings")
					require.Equal(t, test.wantEffort, gjson.GetBytes(sent, "reasoning_effort").String())
					require.Equal(t, test.wantThinking, gjson.GetBytes(sent, "thinking.type").String())
					require.Equal(t, "capability_ping", gjson.GetBytes(sent, "tool_choice.function.name").String())
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}},
						Body: io.NopCloser(strings.NewReader(`{"id":"chat_offline","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_offline","type":"function","function":{"name":"capability_ping","arguments":"{\"value\":\"ok\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`))}, nil
				}
				require.Equal(t, "/v1/responses", req.URL.Path)
				require.EqualValues(t, 256, gjson.GetBytes(sent, "max_output_tokens").Int())
				require.Equal(t, test.wantEffort, gjson.GetBytes(sent, "reasoning.effort").String())
				require.Equal(t, "function", gjson.GetBytes(sent, "tool_choice.type").String())
				require.Equal(t, "capability_ping", gjson.GetBytes(sent, "tool_choice.name").String())
				if test.thinking == "" {
					require.False(t, gjson.GetBytes(sent, "text.verbosity").Exists())
					require.False(t, gjson.GetBytes(sent, "include").Exists())
					require.False(t, gjson.GetBytes(sent, "reasoning.summary").Exists())
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
					Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_offline\",\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"call_id\":\"call_offline\",\"name\":\"capability_ping\",\"arguments\":\"{\\\"value\\\":\\\"ok\\\"}\"}],\"usage\":{\"input_tokens\":4,\"output_tokens\":2,\"total_tokens\":6}}}\n\ndata: [DONE]\n\n"))}, nil
			}}
			ctx := WithManagedModelRequest(context.Background(), managed)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body)).WithContext(ctx)
			c.Request.Header.Set("Content-Type", "application/json")
			service := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream,
				accountRepo: &managedLatestAccountRepo{account: account}}
			result, err := service.ForwardAsAnthropic(ctx, c, account, body, "", "")
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, upstream.requests, 1)
			if test.wantEffort == "" {
				require.Nil(t, result.ReasoningEffort)
			} else {
				require.NotNil(t, result.ReasoningEffort)
				require.Equal(t, test.wantEffort, *result.ReasoningEffort)
			}
			after, err := json.Marshal(account)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after), "no account config or scheduling changes")
		})
	}
}
