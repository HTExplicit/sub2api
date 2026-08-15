package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func cindyHTTPToWSV2TestAccount() *Account {
	return &Account{
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": "sk-test"},
		Extra:       map[string]any{"openai_passthrough": true},
	}
}

func cindyHTTPToWSV2TestContext(path string) *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.146.0")
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	return c
}

func cindyHTTPToWSV2TestService() *OpenAIGatewayService {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.CindyHTTPToWSV2Enabled = true
	return &OpenAIGatewayService{cfg: cfg}
}

func TestResolveCindyHTTPToWSV2DecisionEligible(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := cindyHTTPToWSV2TestService()
	decision, ok := svc.resolveCindyHTTPToWSV2Decision(cindyHTTPToWSV2TestContext("/v1/responses"), cindyHTTPToWSV2TestAccount())
	require.True(t, ok)
	require.Equal(t, OpenAIUpstreamTransportResponsesWebsocketV2, decision.Transport)
	require.Equal(t, openAICindyHTTPToWSV2Reason, decision.Reason)
}

func TestResolveCindyHTTPToWSV2DecisionHonorsAllGates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name   string
		mutate func(*OpenAIGatewayService, *gin.Context, *Account)
	}{
		{"toggle_off", func(s *OpenAIGatewayService, _ *gin.Context, _ *Account) {
			s.cfg.Gateway.OpenAIWS.CindyHTTPToWSV2Enabled = false
		}},
		{"global_disabled", func(s *OpenAIGatewayService, _ *gin.Context, _ *Account) { s.cfg.Gateway.OpenAIWS.Enabled = false }},
		{"apikey_disabled", func(s *OpenAIGatewayService, _ *gin.Context, _ *Account) {
			s.cfg.Gateway.OpenAIWS.APIKeyEnabled = false
		}},
		{"wsv2_disabled", func(s *OpenAIGatewayService, _ *gin.Context, _ *Account) {
			s.cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = false
		}},
		{"global_force_http", func(s *OpenAIGatewayService, _ *gin.Context, _ *Account) { s.cfg.Gateway.OpenAIWS.ForceHTTP = true }},
		{"account_force_http", func(_ *OpenAIGatewayService, _ *gin.Context, a *Account) { a.Extra["openai_ws_force_http"] = true }},
		{"non_cindy", func(_ *OpenAIGatewayService, _ *gin.Context, a *Account) {
			a.Credentials["base_url"] = "https://api.openai.com"
		}},
		{"oauth", func(_ *OpenAIGatewayService, _ *gin.Context, a *Account) { a.Type = AccountTypeOAuth }},
		{"zero_concurrency", func(_ *OpenAIGatewayService, _ *gin.Context, a *Account) { a.Concurrency = 0 }},
		{"non_codex", func(_ *OpenAIGatewayService, c *gin.Context, _ *Account) { c.Request.Header.Del("User-Agent") }},
		{"compact", func(_ *OpenAIGatewayService, c *gin.Context, _ *Account) {
			c.Request.URL.Path = "/v1/responses/compact"
		}},
		{"alpha_search", func(_ *OpenAIGatewayService, c *gin.Context, _ *Account) { c.Request.URL.Path = "/v1/alpha/search" }},
		{"websocket_ingress", func(_ *OpenAIGatewayService, c *gin.Context, _ *Account) {
			SetOpenAIClientTransport(c, OpenAIClientTransportWS)
		}},
		{"get_method", func(_ *OpenAIGatewayService, c *gin.Context, _ *Account) { c.Request.Method = http.MethodGet }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := cindyHTTPToWSV2TestService()
			ctx := cindyHTTPToWSV2TestContext("/v1/responses")
			account := cindyHTTPToWSV2TestAccount()
			tt.mutate(svc, ctx, account)
			_, ok := svc.resolveCindyHTTPToWSV2Decision(ctx, account)
			require.False(t, ok)
		})
	}
}

func TestCindyHTTPToWSV2FailoverDoesNotDowngradeToNonCindyHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":1}}`)),
		},
	}
	svc := cindyHTTPToWSV2TestService()
	svc.httpUpstream = upstream
	c := cindyHTTPToWSV2TestContext("/v1/responses")
	markOpenAICindyHTTPToWSV2Required(c)
	nonCindy := cindyHTTPToWSV2TestAccount()
	nonCindy.ID = 2
	nonCindy.Credentials["base_url"] = "https://api.openai.com"

	result, err := svc.Forward(
		context.Background(), c, nonCindy,
		[]byte(`{"model":"gpt-5.4","stream":false,"input":"hi"}`),
	)

	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.ShouldRetryNextAccount())
	require.True(t, failoverErr.SuppressAccountHealthPenalty)
	require.Nil(t, upstream.lastReq)
}

func TestCindyHTTPToWSV2ContinuationReusesOneConnectionAcrossIndependentHTTPRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := cindyHTTPToWSV2TestService().cfg
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.StickyResponseIDTTLSeconds = 60
	captureConn := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.completed","response":{"id":"resp_cindy_tool","model":"gpt-5.4","status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"fetch","arguments":"{}"}],"usage":{"input_tokens":1,"output_tokens":1}}}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_cindy_done","model":"gpt-5.4","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":1,"output_tokens":1}}}`),
	}}
	dialer := &openAIWSCaptureDialer{conn: captureConn}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(dialer)
	t.Cleanup(pool.Close)
	svc := &OpenAIGatewayService{
		cfg: cfg, openaiWSPool: pool, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector: NewCodexToolCorrector(),
	}
	account := cindyHTTPToWSV2TestAccount()

	firstContext := cindyHTTPToWSV2TestContext("/v1/responses")
	firstBody := []byte(`{"model":"gpt-5.4","stream":false,"input":[{"role":"user","content":"run tool"}]}`)
	firstResult, err := svc.Forward(context.Background(), firstContext, account, firstBody)
	require.NoError(t, err)
	require.Equal(t, "resp_cindy_tool", firstResult.RequestID)
	require.True(t, firstResult.OpenAIWSMode)
	require.False(t, firstResult.Stream)

	secondContext := cindyHTTPToWSV2TestContext("/v1/responses")
	secondBody := []byte(`{"model":"gpt-5.4","stream":false,"previous_response_id":"resp_cindy_tool","input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`)
	secondResult, err := svc.Forward(context.Background(), secondContext, account, secondBody)
	require.NoError(t, err)
	require.Equal(t, "resp_cindy_done", secondResult.RequestID)
	require.True(t, secondResult.OpenAIWSMode)
	require.False(t, secondResult.Stream)
	require.Equal(t, 1, dialer.DialCount())

	captureConn.mu.Lock()
	writes := append([]map[string]any(nil), captureConn.writes...)
	captureConn.mu.Unlock()
	require.Len(t, writes, 2)
	require.Equal(t, true, writes[0]["stream"])
	require.Equal(t, true, writes[1]["stream"])
	require.Empty(t, openAIWSPayloadString(writes[0], "previous_response_id"))
	require.Equal(t, "resp_cindy_tool", openAIWSPayloadString(writes[1], "previous_response_id"))
	require.True(t, HasFunctionCallOutput(writes[1]))
}

func TestCindyHTTPToWSV2ContinuationWithoutStickyConnectionFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := cindyHTTPToWSV2TestService()
	svc.toolCorrector = NewCodexToolCorrector()
	account := cindyHTTPToWSV2TestAccount()
	c := cindyHTTPToWSV2TestContext("/v1/responses")
	body := []byte(`{"model":"gpt-5.4","stream":false,"previous_response_id":"resp_missing","input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`)

	result, err := svc.Forward(context.Background(), c, account, body)

	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.IsOpenAIContinuationStateUnavailable())
}

func TestCindyHTTPToWSV2FirstTurnHandshakeFailoverClassification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := cindyHTTPToWSV2TestService()
	account := cindyHTTPToWSV2TestAccount()
	for _, statusCode := range []int{http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			dialErr := &openAIWSDialError{
				StatusCode: statusCode,
				ResponseHeaders: http.Header{
					"X-Request-Id": []string{"request-redacted"},
				},
				ResponseBody: []byte(`{"error":{"message":"temporary failure"}}`),
			}

			classified, ok := svc.cindyHTTPToWSV2FirstTurnFailover(
				context.Background(), cindyHTTPToWSV2TestContext("/v1/responses"), account, "gpt-5.4", dialErr,
			)

			require.True(t, ok)
			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, classified, &failoverErr)
			require.Equal(t, statusCode, failoverErr.StatusCode)
			require.False(t, failoverErr.RetryableOnSameAccount)
			require.JSONEq(t, string(openAITransportFailoverBody), string(failoverErr.ResponseBody))
		})
	}

	htmlErr := &openAIWSDialError{StatusCode: http.StatusForbidden, ResponseBody: []byte("<!doctype html><html><body>Forbidden</body></html>")}
	classified, ok := svc.cindyHTTPToWSV2FirstTurnFailover(
		context.Background(), cindyHTTPToWSV2TestContext("/v1/responses"), account, "gpt-5.4", htmlErr,
	)
	require.True(t, ok)
	var htmlFailover *UpstreamFailoverError
	require.ErrorAs(t, classified, &htmlFailover)
	require.True(t, htmlFailover.SuppressAccountHealthPenalty)
	require.JSONEq(t, string(openAITransportFailoverBody), string(htmlFailover.ResponseBody))

	classified, ok = svc.cindyHTTPToWSV2FirstTurnFailover(
		context.Background(), cindyHTTPToWSV2TestContext("/v1/responses"), account, "gpt-5.4",
		coderws.CloseError{Code: coderws.StatusTryAgainLater, Reason: "retry later"},
	)
	require.True(t, ok)
	var closeFailover *UpstreamFailoverError
	require.ErrorAs(t, classified, &closeFailover)
	require.Equal(t, http.StatusServiceUnavailable, closeFailover.StatusCode)
}

func TestCindyHTTPToWSV2FirstTurnTerminalEventFailoverClassification(t *testing.T) {
	svc := cindyHTTPToWSV2TestService()
	account := cindyHTTPToWSV2TestAccount()
	for _, tc := range []struct {
		name       string
		payload    string
		wantStatus int
	}{
		{"forbidden", `{"type":"response.failed","response":{"error":{"status_code":403,"message":"forbidden"}}}`, http.StatusForbidden},
		{"rate_limit", `{"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded","message":"slow down"}}}`, http.StatusTooManyRequests},
		{"server", `{"type":"error","error":{"status":503,"message":"unavailable"}}`, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			classified, ok := svc.cindyHTTPToWSV2FirstTurnEventFailover(
				context.Background(), nil, account, "gpt-5.4", http.Header{}, []byte(tc.payload),
			)
			require.True(t, ok)
			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, classified, &failoverErr)
			require.Equal(t, tc.wantStatus, failoverErr.StatusCode)
			require.JSONEq(t, string(openAITransportFailoverBody), string(failoverErr.ResponseBody))
		})
	}
}

func TestCindyWSV2BalanceTerminalWriteOrdering(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name         string
		reason       string
		events       [][]byte
		wantFailover bool
		wantOutput   bool
	}{
		{
			name: "ordinary_response_failed_before_output",
			events: [][]byte{
				[]byte(`{"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429","message":"sensitive upstream detail"}}}`),
			},
			wantFailover: true,
		},
		{
			name: "ordinary_bare_error_before_output",
			events: [][]byte{
				[]byte(`{"type":"error","error":{"type":"budget_exceeded","code":"429","message":"sensitive upstream detail"}}`),
			},
			wantFailover: true,
		},
		{
			name: "ordinary_after_output",
			events: [][]byte{
				[]byte(`{"type":"response.output_text.delta","delta":"ok"}`),
				[]byte(`{"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429","message":"sensitive upstream detail"}}}`),
			},
			wantOutput: true,
		},
		{
			name:   "http_to_wsv2_after_output",
			reason: openAICindyHTTPToWSV2Reason,
			events: [][]byte{
				[]byte(`{"type":"response.output_text.delta","delta":"ok"}`),
				[]byte(`{"type":"error","error":{"type":"budget_exceeded","code":"429","message":"sensitive upstream detail"}}`),
			},
			wantOutput: true,
		},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := cindyHTTPToWSV2TestService().cfg
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
			cfg.Gateway.OpenAIWS.QueueLimitPerConn = 4
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
			cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
			captureConn := &openAIWSCaptureConn{events: tt.events}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: captureConn})
			t.Cleanup(pool.Close)
			repo := &cindyRateLimitAccountRepoStub{}
			rateLimitService := NewRateLimitService(repo, nil, cfg, nil, nil)
			gateway := &OpenAIGatewayService{
				cfg:              cfg,
				openaiWSPool:     pool,
				toolCorrector:    NewCodexToolCorrector(),
				rateLimitService: rateLimitService,
			}
			rateLimitService.SetAccountRuntimeBlocker(gateway)
			account := cindyHTTPToWSV2TestAccount()
			account.ID = int64(8520 + index)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Request.Header.Set("User-Agent", "codex_cli_rs/0.146.0")
			decision := OpenAIWSProtocolDecision{
				Transport: OpenAIUpstreamTransportResponsesWebsocketV2,
				Reason:    tt.reason,
			}

			_, err := gateway.forwardOpenAIWSV2(
				context.Background(), c, account,
				map[string]any{"model": "openai/gpt-5.6-luna", "stream": true, "input": "hi"},
				"sk-test", decision, true, true,
				"gpt-5.6-luna", "openai/gpt-5.6-luna", time.Now(), 0, "", new(bool),
			)

			var failoverErr *UpstreamFailoverError
			if tt.wantFailover {
				require.ErrorAs(t, err, &failoverErr)
				require.True(t, failoverErr.CindyBalanceInsufficient)
				require.False(t, failoverErr.RetryableOnSameAccount)
				require.Empty(t, recorder.Body.String())
			} else {
				require.Error(t, err)
				require.False(t, errors.As(err, &failoverErr), "output already sent must not trigger account replay")
				require.True(t, tt.wantOutput)
				require.Contains(t, recorder.Body.String(), `"delta":"ok"`)
				require.Contains(t, recorder.Body.String(), "upstream_retry_exhausted")
			}
			require.NotContains(t, recorder.Body.String(), "budget_exceeded")
			require.NotContains(t, recorder.Body.String(), "sensitive upstream detail")
			require.Equal(t, 1, repo.markCalls)
		})
	}
}

func TestSanitizeOpenAICindyFailoverErrorDropsRawBody(t *testing.T) {
	raw := []byte(`{"error":{"message":"account-specific forbidden detail"}}`)
	failoverErr := &UpstreamFailoverError{
		StatusCode:   http.StatusForbidden,
		ResponseBody: append([]byte(nil), raw...),
	}

	require.Same(t, failoverErr, sanitizeOpenAICindyFailoverError(failoverErr))
	require.JSONEq(t, string(openAITransportFailoverBody), string(failoverErr.ResponseBody))
	require.NotContains(t, string(failoverErr.ResponseBody), "account-specific")

	cyberErr := NewOpenAICyberFailoverError(raw, nil)
	require.Same(t, cyberErr, sanitizeOpenAICindyFailoverError(cyberErr))
	require.Contains(t, string(cyberErr.ResponseBody), OpenAIUpstreamRetryExhaustedCode)
}
