package service

import (
	"context"
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

func newCindyNativeMessagesAccount() *Account {
	return &Account{
		ID:          901,
		Name:        "cindy-native-messages",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"api_key":  "cindy-secret",
			"base_url": "https://api.laxarouter.ai",
		},
	}
}

func newCindyNativeMessagesService(upstream HTTPUpstream) *GatewayService {
	cfg := &config.Config{
		Gateway:  config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
		Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}},
	}
	return &GatewayService{
		cfg:                  cfg,
		responseHeaderFilter: compileResponseHeaderFilter(cfg),
		httpUpstream:         upstream,
		rateLimitService:     &RateLimitService{},
		deferredService:      &DeferredService{},
	}
}

func TestForwardCindyAnthropicMessagesJSONUsesNativeWireAndUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("Authorization", "Bearer inbound-secret")
	c.Request.Header.Set("X-Api-Key", "inbound-anthropic-secret")

	upstreamBody := `{"id":"msg_1","type":"message","role":"assistant","model":"anthropic/claude-opus-5","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":11,"output_tokens":3,"cache_read_input_tokens":2,"cache_creation_input_tokens":10,"cache_creation":{"ephemeral_5m_input_tokens":4,"ephemeral_1h_input_tokens":6}}}`
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-cindy-json"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	result, err := newCindyNativeMessagesService(upstream).ForwardCindyAnthropicMessages(
		context.Background(), c, newCindyNativeMessagesAccount(),
		[]byte(`{"model":"claude-opus-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}],"stream":false}`),
		"claude-opus-5",
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "https://api.laxarouter.ai/v1/messages", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer cindy-secret", upstream.lastReq.Header.Get("Authorization"))
	require.Empty(t, upstream.lastReq.Header.Get("x-api-key"))
	require.Equal(t, "anthropic/claude-opus-5", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, 11, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 2, result.Usage.CacheReadInputTokens)
	require.Equal(t, 10, result.Usage.CacheCreationInputTokens)
	require.Equal(t, 4, result.Usage.CacheCreation5mTokens)
	require.Equal(t, 6, result.Usage.CacheCreation1hTokens)
	require.Equal(t, "claude-opus-5", result.Model)
	require.Equal(t, "anthropic/claude-opus-5", result.UpstreamModel)
	require.JSONEq(t, upstreamBody, rec.Body.String())
}

func TestForwardCindyAnthropicMessagesSSEPreservesEventsAndUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	upstreamSSE := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_2","model":"anthropic/claude-sonnet-5","usage":{"input_tokens":7,"cache_read_input_tokens":1}}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}}

	result, err := newCindyNativeMessagesService(upstream).ForwardCindyAnthropicMessages(
		context.Background(), c, newCindyNativeMessagesAccount(),
		[]byte(`{"model":"claude-sonnet-4-5-20250929","max_tokens":16,"messages":[{"role":"user","content":"hi"}],"stream":true}`),
		"claude-sonnet-4-5-20250929",
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "anthropic/claude-sonnet-5", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Equal(t, 1, result.Usage.CacheReadInputTokens)
	require.Contains(t, rec.Body.String(), "event: message_start")
	require.Contains(t, rec.Body.String(), "event: message_stop")
}

func TestForwardCindyAnthropicMessagesRejectsUnverifiedWire(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	_, err := newCindyNativeMessagesService(&anthropicHTTPUpstreamRecorder{}).ForwardCindyAnthropicMessages(
		context.Background(), c, newCindyNativeMessagesAccount(),
		[]byte(`{"model":"deepseek-v4-pro","messages":[]}`), "deepseek-v4-pro",
	)
	require.ErrorContains(t, err, "not verified for native Messages")
}
