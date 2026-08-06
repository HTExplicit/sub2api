package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newGatewayBusinessSystemPromptPolicy(t *testing.T, expose, compact bool) *BusinessSystemPromptService {
	t.Helper()
	store := &fakeBusinessSystemPromptStore{loaded: BusinessSystemPromptSnapshot{
		Enabled:            true,
		ExposeServerPrompt: expose,
		CompactEnabled:     compact,
		TemplateID:         10,
		VersionID:          20,
		TemplateVersion:    3,
		Revision:           7,
		Body:               "business-server",
	}}
	policy := NewBusinessSystemPromptService(store, nil)
	require.NoError(t, policy.Initialize(context.Background()))
	return policy
}

func businessSystemPromptTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	return cfg
}

func businessSystemPromptErrorUpstream() *httpUpstreamRecorder {
	return &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"x-request-id": []string{"business-prompt-test"},
		},
		Body: io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"capture complete"}}`)),
	}}
}

func businessSystemPromptAPIKeyAccount(responses bool) *Account {
	return &Account{
		ID: 61, Name: "openai-compatible", Platform: PlatformOpenAI,
		Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       map[string]any{"openai_responses_supported": responses},
	}
}

func newBusinessSystemPromptGinContext(path string, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, recorder
}

func TestBusinessSystemPromptNativeResponsesAppliesForAPIKeyAndOAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, account := range map[string]*Account{
		"api key compatible": businessSystemPromptAPIKeyAccount(true),
		"oauth": {
			ID: 62, Name: "openai-oauth", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
			Status: StatusActive, Schedulable: true, Concurrency: 1,
			Credentials: map[string]any{"access_token": "oauth-test", "chatgpt_account_id": "acct-test"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.4","stream":false,"instructions":" client ","prompt_cache_key":"cache","input":[{"role":"user","content":"hello"}]}`)
			c, _ := newBusinessSystemPromptGinContext("/v1/responses", body)
			upstream := businessSystemPromptErrorUpstream()
			svc := &OpenAIGatewayService{
				cfg: businessSystemPromptTestConfig(), httpUpstream: upstream,
				businessPromptService: newGatewayBusinessSystemPromptPolicy(t, false, false),
			}

			result, err := svc.Forward(context.Background(), c, account, body)
			require.Error(t, err)
			require.Nil(t, result)
			require.NotNil(t, upstream.lastReq)
			require.Equal(t, "client\n\nbusiness-server", gjson.GetBytes(upstream.lastBody, "instructions").String())
			require.Contains(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String(), ":business-system-prompt:7:")
			require.Equal(t, 1, strings.Count(string(upstream.lastBody), "business-server"))
		})
	}
}

func TestBusinessSystemPromptUpstreamErrorIsSanitizedBeforeInspection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := newBusinessSystemPromptGinContext("/v1/responses", nil)
	svc := &OpenAIGatewayService{businessPromptService: newGatewayBusinessSystemPromptPolicy(t, false, false)}
	application := BusinessSystemPromptApplication{
		Applied:            true,
		Carrier:            BusinessSystemPromptCarrierInstructions,
		ClientInstructions: "client",
		ServerInstructions: "business-server",
	}
	c.Set(businessSystemPromptRequestApplicationKey+":"+BusinessSystemPromptProtocolResponses, businessSystemPromptRequestState{
		application: application,
	})
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body: io.NopCloser(strings.NewReader(
			`{"error":{"message":"invalid request","response":{"instructions":"client\n\nbusiness-server"}}}`,
		)),
	}

	body, message := svc.readOpenAIUpstreamError(resp, c)
	require.Equal(t, "invalid request", message)
	require.Equal(t, "client", gjson.GetBytes(body, "error.response.instructions").String())
	require.NotContains(t, string(body), "business-server")
	rewound, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, body, rewound)
}

func TestBusinessSystemPromptChatAndMessagesBridgeMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	policy := newGatewayBusinessSystemPromptPolicy(t, false, false)

	t.Run("chat to responses", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5.4","messages":[{"role":"system","content":"client-system"},{"role":"user","content":"hello"}],"stream":false}`)
		c, _ := newBusinessSystemPromptGinContext("/v1/chat/completions", body)
		upstream := businessSystemPromptErrorUpstream()
		svc := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: policy}
		_, err := svc.ForwardAsChatCompletions(context.Background(), c, businessSystemPromptAPIKeyAccount(true), body, "cache", "gpt-5.4")
		require.Error(t, err)
		require.Equal(t, "business-server", gjson.GetBytes(upstream.lastBody, "instructions").String())
		require.Contains(t, string(upstream.lastBody), "client-system")
		require.Equal(t, 1, strings.Count(string(upstream.lastBody), "business-server"))
	})

	t.Run("raw chat", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5.4","messages":[{"role":"developer","content":"client-developer"},{"role":"user","content":"hello"}],"stream":false}`)
		c, _ := newBusinessSystemPromptGinContext("/v1/chat/completions", body)
		upstream := businessSystemPromptErrorUpstream()
		svc := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: policy}
		_, err := svc.forwardAsRawChatCompletions(context.Background(), c, businessSystemPromptAPIKeyAccount(false), body, "")
		require.Error(t, err)
		require.Equal(t, "developer", gjson.GetBytes(upstream.lastBody, "messages.0.role").String())
		require.Equal(t, "system", gjson.GetBytes(upstream.lastBody, "messages.1.role").String())
		require.Equal(t, "business-server", gjson.GetBytes(upstream.lastBody, "messages.1.content").String())
		require.Equal(t, "user", gjson.GetBytes(upstream.lastBody, "messages.2.role").String())
	})

	t.Run("responses to chat fallback", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5.4","instructions":"client-responses","input":[{"role":"user","content":"hello"}],"stream":false}`)
		c, _ := newBusinessSystemPromptGinContext("/v1/responses", body)
		upstream := businessSystemPromptErrorUpstream()
		svc := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: policy}
		_, err := svc.forwardResponsesViaRawChatCompletions(context.Background(), c, businessSystemPromptAPIKeyAccount(false), body, false)
		require.Error(t, err)
		require.Equal(t, 1, strings.Count(string(upstream.lastBody), "business-server"))
		require.True(t, chatBodyHasSystemPrompt(upstream.lastBody, "business-server"))
	})

	t.Run("responses compact fallback honors compact switch", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5.4","instructions":"client-responses","input":[{"role":"user","content":"hello"}],"stream":false}`)
		c, _ := newBusinessSystemPromptGinContext("/v1/responses/compact", body)
		upstream := businessSystemPromptErrorUpstream()
		svc := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: newGatewayBusinessSystemPromptPolicy(t, false, true)}
		_, err := svc.forwardResponsesViaRawChatCompletions(context.Background(), c, businessSystemPromptAPIKeyAccount(false), body, true)
		require.Error(t, err)
		require.Equal(t, 1, strings.Count(string(upstream.lastBody), "business-server"))
		require.True(t, chatBodyHasSystemPrompt(upstream.lastBody, "business-server"))
	})

	t.Run("responses compact fallback skips business prompt when compact is disabled", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5.4","instructions":"client-responses","input":[{"role":"user","content":"hello"}],"stream":false}`)
		c, _ := newBusinessSystemPromptGinContext("/v1/responses/compact", body)
		upstream := businessSystemPromptErrorUpstream()
		svc := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: policy}
		_, err := svc.forwardResponsesViaRawChatCompletions(context.Background(), c, businessSystemPromptAPIKeyAccount(false), body, true)
		require.Error(t, err)
		require.NotContains(t, string(upstream.lastBody), "business-server")
		require.False(t, chatBodyHasSystemPrompt(upstream.lastBody, "business-server"))
	})

	t.Run("messages to responses", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5.4","max_tokens":32,"system":"client-messages","messages":[{"role":"user","content":"hello"}],"stream":false}`)
		c, _ := newBusinessSystemPromptGinContext("/v1/messages", body)
		upstream := businessSystemPromptErrorUpstream()
		svc := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: policy}
		_, err := svc.ForwardAsAnthropic(context.Background(), c, businessSystemPromptAPIKeyAccount(true), body, "cache", "gpt-5.4")
		require.Error(t, err)
		require.Equal(t, "business-server", gjson.GetBytes(upstream.lastBody, "instructions").String())
		require.Contains(t, string(upstream.lastBody), "client-messages")
		require.Equal(t, 1, strings.Count(string(upstream.lastBody), "business-server"))
	})

	t.Run("messages to chat fallback", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5.4","max_tokens":32,"system":"client-messages","messages":[{"role":"user","content":"hello"}],"stream":false}`)
		c, _ := newBusinessSystemPromptGinContext("/v1/messages", body)
		upstream := businessSystemPromptErrorUpstream()
		svc := &OpenAIGatewayService{cfg: businessSystemPromptTestConfig(), httpUpstream: upstream, businessPromptService: policy}
		_, err := svc.forwardAnthropicViaRawChatCompletions(context.Background(), c, businessSystemPromptAPIKeyAccount(false), body, "gpt-5.4")
		require.Error(t, err)
		require.Equal(t, 1, strings.Count(string(upstream.lastBody), "business-server"))
		require.True(t, chatBodyHasSystemPrompt(upstream.lastBody, "business-server"))
	})
}

func chatBodyHasSystemPrompt(body []byte, prompt string) bool {
	for _, message := range gjson.GetBytes(body, "messages").Array() {
		if message.Get("role").String() == "system" && message.Get("content").String() == prompt {
			return true
		}
	}
	return false
}

func TestBusinessSystemPromptPassthroughCannotSatisfyOAuthInstructionsPreflight(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.1-codex","stream":false,"instructions":" ","input":[{"role":"user","content":"hello"}]}`)
	c, recorder := newBusinessSystemPromptGinContext("/v1/responses", body)
	upstream := businessSystemPromptErrorUpstream()
	svc := &OpenAIGatewayService{
		cfg: businessSystemPromptTestConfig(), httpUpstream: upstream,
		businessPromptService: newGatewayBusinessSystemPromptPolicy(t, false, false),
	}
	account := &Account{
		ID: 63, Name: "passthrough-oauth", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Concurrency: 1, Credentials: map[string]any{"access_token": "oauth-test", "chatgpt_account_id": "acct-test"},
	}

	result, err := svc.forwardOpenAIPassthrough(
		context.Background(), c, account, body, body, "gpt-5.1-codex", false, nil, false, time.Now(),
	)
	require.Error(t, err)
	require.Nil(t, result)
	require.Nil(t, upstream.lastReq)
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "non-empty instructions")
}
