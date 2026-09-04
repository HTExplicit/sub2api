package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func cindyHTTPToWSV2TestAccount() *Account {
	return &Account{
		ID: 1, Platform: PlatformCindy, Type: AccountTypeAPIKey,
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

type cindyHTTPToWSV2DialStep struct {
	conn      openAIWSClientConn
	handshake http.Header
	status    int
	err       error
}

type cindyHTTPToWSV2SequenceDialer struct {
	mu      sync.Mutex
	steps   []cindyHTTPToWSV2DialStep
	headers []http.Header
}

func (d *cindyHTTPToWSV2SequenceDialer) Dial(
	ctx context.Context,
	wsURL string,
	headers http.Header,
	proxyURL string,
) (openAIWSClientConn, int, http.Header, error) {
	_ = ctx
	_ = wsURL
	_ = proxyURL
	d.mu.Lock()
	defer d.mu.Unlock()
	d.headers = append(d.headers, cloneHeader(headers))
	if len(d.steps) == 0 {
		return nil, 0, nil, errors.New("unexpected Cindy WSv2 dial")
	}
	step := d.steps[0]
	d.steps = d.steps[1:]
	return step.conn, step.status, cloneHeader(step.handshake), step.err
}

func (d *cindyHTTPToWSV2SequenceDialer) capturedHeaders() []http.Header {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := make([]http.Header, len(d.headers))
	for i := range d.headers {
		result[i] = cloneHeader(d.headers[i])
	}
	return result
}

func newCindyHTTPToWSV2TurnStateTestService(
	t *testing.T,
	steps ...cindyHTTPToWSV2DialStep,
) (*OpenAIGatewayService, *cindyHTTPToWSV2SequenceDialer) {
	t.Helper()
	cfg := cindyHTTPToWSV2TestService().cfg
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 0
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 1
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	dialer := &cindyHTTPToWSV2SequenceDialer{steps: steps}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(dialer)
	t.Cleanup(pool.Close)
	return &OpenAIGatewayService{
		cfg:              cfg,
		openaiWSPool:     pool,
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
	}, dialer
}

func newCindyHTTPToWSV2TurnStateTestContext(
	sessionID string,
	apiKeyID int64,
	groupID int64,
) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.146.0")
	c.Request.Header.Set("session_id", sessionID)
	c.Set("api_key", &APIKey{ID: apiKeyID, GroupID: &groupID})
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	return c, recorder
}

func TestResolveCindyHTTPToWSV2DecisionEligible(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := cindyHTTPToWSV2TestService()
	decision, ok := svc.resolveCindyHTTPToWSV2Decision(cindyHTTPToWSV2TestContext("/v1/responses"), cindyHTTPToWSV2TestAccount())
	require.True(t, ok)
	require.Equal(t, OpenAIUpstreamTransportResponsesWebsocketV2, decision.Transport)
	require.Equal(t, openAICindyHTTPToWSV2Reason, decision.Reason)
}

func TestLegacyCindyRuntimeCompatibilityUsesHTTPToWSV2(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := cindyHTTPToWSV2TestService()
	account := cindyHTTPToWSV2TestAccount()
	account.Platform = PlatformOpenAI

	decision, ok := svc.resolveCindyHTTPToWSV2Decision(cindyHTTPToWSV2TestContext("/v1/responses"), account)

	require.True(t, ok)
	require.Equal(t, OpenAIUpstreamTransportResponsesWebsocketV2, decision.Transport)
}

func TestLegacyCindyRuntimeCompatibilityMapsResponsesAliasesOverHTTPAndWS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for alias, live := range map[string]string{
		"gpt-5.4-mini": "openai/gpt-5.6-luna",
		"gpt-5.6-luna": "openai/gpt-5.6-luna",
	} {
		t.Run(alias+"/http", func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(
					`{"id":"resp_http_alias","status":"completed","model":"` + live + `","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`,
				)),
			}}
			cfg := &config.Config{}
			cfg.Security.URLAllowlist.Enabled = false
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, toolCorrector: NewCodexToolCorrector()}
			account := cindyHTTPToWSV2TestAccount()
			account.Platform = PlatformOpenAI

			result, err := svc.Forward(
				context.Background(), cindyHTTPToWSV2TestContext("/v1/responses"), account,
				[]byte(`{"model":"`+alias+`","stream":false,"input":"hi"}`),
			)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, live, gjson.GetBytes(upstream.lastBody, "model").String())
		})

		t.Run(alias+"/ws", func(t *testing.T) {
			capture := &openAIWSCaptureConn{events: [][]byte{
				[]byte(`{"type":"response.completed","response":{"id":"resp_ws_alias","model":"` + live + `","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`),
			}}
			svc, _ := newCindyHTTPToWSV2TurnStateTestService(t, cindyHTTPToWSV2DialStep{conn: capture})
			account := cindyHTTPToWSV2TestAccount()
			account.Platform = PlatformOpenAI

			result, err := svc.Forward(
				context.Background(), cindyHTTPToWSV2TestContext("/v1/responses"), account,
				[]byte(`{"model":"`+alias+`","stream":false,"input":"hi"}`),
			)
			require.NoError(t, err)
			require.NotNil(t, result)
			capture.mu.Lock()
			writes := append([]map[string]any(nil), capture.writes...)
			capture.mu.Unlock()
			require.Len(t, writes, 1)
			require.Equal(t, live, openAIWSPayloadString(writes[0], "model"))
		})
	}
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
		{"account_force_chat_completions", func(_ *OpenAIGatewayService, _ *gin.Context, a *Account) {
			a.Extra[CindyResponsesModeExtraKey] = "force_chat_completions"
		}},
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
		[]byte(`{"model":"gpt-5.4-mini","stream":false,"input":"hi"}`),
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
		[]byte(`{"type":"response.completed","response":{"id":"resp_cindy_tool","model":"gpt-5.4-mini","status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"fetch","arguments":"{}"}],"usage":{"input_tokens":1,"output_tokens":1}}}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_cindy_done","model":"gpt-5.4-mini","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":1,"output_tokens":1}}}`),
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
	firstBody := []byte(`{"model":"gpt-5.4-mini","stream":false,"input":[{"role":"user","content":"run tool"}]}`)
	firstResult, err := svc.Forward(context.Background(), firstContext, account, firstBody)
	require.NoError(t, err)
	require.Equal(t, "resp_cindy_tool", firstResult.RequestID)
	require.True(t, firstResult.OpenAIWSMode)
	require.False(t, firstResult.Stream)

	secondContext := cindyHTTPToWSV2TestContext("/v1/responses")
	secondBody := []byte(`{"model":"gpt-5.4-mini","stream":false,"previous_response_id":"resp_cindy_tool","input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`)
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

func TestCindyHTTPToWSV2ContinuationWithoutLocalConnectionReconnectsBoundAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	capture := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.completed","response":{"id":"resp_resumed","model":"gpt-5.4-mini","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`),
	}}
	svc, dialer := newCindyHTTPToWSV2TurnStateTestService(t, cindyHTTPToWSV2DialStep{conn: capture})
	account := cindyHTTPToWSV2TestAccount()
	c := cindyHTTPToWSV2TestContext("/v1/responses")
	body := []byte(`{"model":"gpt-5.4-mini","stream":false,"previous_response_id":"resp_from_old_process","input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`)

	result, err := svc.Forward(context.Background(), c, account, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "resp_resumed", result.RequestID)
	require.Len(t, dialer.capturedHeaders(), 1)
	capture.mu.Lock()
	writes := append([]map[string]any(nil), capture.writes...)
	capture.mu.Unlock()
	require.Len(t, writes, 1)
	require.Equal(t, "resp_from_old_process", openAIWSPayloadString(writes[0], "previous_response_id"))
}

func TestCindyHTTPToWSV2Handshake403FallsBackToOriginalStatelessHTTPStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: " + `{"type":"response.completed","response":{"id":"resp_http_fallback","object":"response","status":"completed","model":"openai/gpt-5.6-luna","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n\ndata: [DONE]\n\n",
		)),
	}}
	svc, dialer := newCindyHTTPToWSV2TurnStateTestService(t, cindyHTTPToWSV2DialStep{
		status: http.StatusForbidden,
		err: &openAIWSHandshakeError{
			Body: []byte(`{"error":{"message":"websocket upgrade forbidden"}}`),
			Err:  errors.New("handshake rejected"),
		},
	})
	svc.httpUpstream = upstream
	body := []byte(`{"model":"openai/gpt-5.6-luna","stream":true,"store":false,"include":["reasoning.encrypted_content"],"input":[` +
		`{"type":"message","role":"user","content":"first"},` +
		`{"type":"reasoning","id":"rs_foreign","encrypted_content":"ENC"},` +
		`{"type":"message","role":"assistant","content":"answer"},` +
		`{"type":"custom_tool_call","call_id":"call_1","name":"shell","input":"{}"},` +
		`{"type":"custom_tool_call_output","call_id":"call_1","output":"ok"},` +
		`{"type":"compaction","encrypted_content":"CMP"},` +
		`{"type":"message","role":"user","content":"continue"}` +
		`]}`)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.146.0")
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	result, err := svc.Forward(context.Background(), c, cindyHTTPToWSV2TestAccount(), body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Contains(t, recorder.Body.String(), `"type":"response.completed"`)
	require.Contains(t, recorder.Body.String(), `"id":"resp_http_fallback"`)
	require.Len(t, dialer.capturedHeaders(), 1)
	require.NotNil(t, upstream.lastReq)
	require.JSONEq(t, string(body), string(upstream.lastBody), "transport fallback must not rewrite conversation state")
	require.Equal(t, "ENC", gjson.GetBytes(upstream.lastBody, `input.#(type=="reasoning").encrypted_content`).String())
	require.Equal(t, "CMP", gjson.GetBytes(upstream.lastBody, `input.#(type=="compaction").encrypted_content`).String())
	require.Equal(t, "answer", gjson.GetBytes(upstream.lastBody, `input.#(role=="assistant").content`).String())
	require.Equal(t, "call_1", gjson.GetBytes(upstream.lastBody, `input.#(type=="custom_tool_call_output").call_id`).String())
	require.Equal(t, "reasoning.encrypted_content", gjson.GetBytes(upstream.lastBody, "include.0").String())
}

func TestCindyHTTPToWSV2InBand403UsesNormalFailoverWithoutPayloadRewrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	firstConn := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.failed","response":{"id":"resp_rejected","status":"failed","error":{"status_code":403,"message":"forbidden"}}}`),
	}}
	svc, dialer := newCindyHTTPToWSV2TurnStateTestService(t,
		cindyHTTPToWSV2DialStep{conn: firstConn},
	)
	body := []byte(`{"model":"openai/gpt-5.6-luna","stream":false,"store":false,"input":[` +
		`{"type":"message","role":"user","content":"first"},` +
		`{"type":"reasoning","id":"rs_foreign","encrypted_content":"ENC"},` +
		`{"type":"message","role":"assistant","content":"answer"},` +
		`{"type":"custom_tool_call","call_id":"call_1","name":"shell","input":"{}"},` +
		`{"type":"custom_tool_call_output","call_id":"call_1","output":"ok"},` +
		`{"type":"compaction","encrypted_content":"CMP"},` +
		`{"type":"message","role":"user","content":"continue"}` +
		`]}`)
	c := cindyHTTPToWSV2TestContext("/v1/responses")
	result, err := svc.Forward(context.Background(), c, cindyHTTPToWSV2TestAccount(), body)

	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.CindyHTTPToWSV2FirstTurn)
	require.Len(t, dialer.capturedHeaders(), 1)
	firstConn.mu.Lock()
	firstWrites := append([]map[string]any(nil), firstConn.writes...)
	firstConn.mu.Unlock()
	require.Len(t, firstWrites, 1)
	wsBody := payloadAsJSONBytes(firstWrites[0])
	require.JSONEq(t, gjson.GetBytes(body, "input").Raw, gjson.GetBytes(wsBody, "input").Raw)
	require.Equal(t, "ENC", gjson.GetBytes(wsBody, `input.#(type=="reasoning").encrypted_content`).String())
	require.Equal(t, "CMP", gjson.GetBytes(wsBody, `input.#(type=="compaction").encrypted_content`).String())
}

func TestCindyHTTPToWSV2Handshake403HTTPFailurePreservesAccountFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"still forbidden"}}`)),
	}}
	svc, dialer := newCindyHTTPToWSV2TurnStateTestService(t, cindyHTTPToWSV2DialStep{
		status: http.StatusForbidden,
		err: &openAIWSHandshakeError{
			Body: []byte(`{"error":{"message":"websocket upgrade forbidden"}}`),
			Err:  errors.New("handshake rejected"),
		},
	})
	svc.httpUpstream = upstream
	body := []byte(`{"model":"openai/gpt-5.6-luna","stream":false,"input":"hello"}`)

	c := cindyHTTPToWSV2TestContext("/v1/responses")
	result, err := svc.Forward(context.Background(), c, cindyHTTPToWSV2TestAccount(), body)

	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusForbidden, failoverErr.StatusCode)
	require.Len(t, dialer.capturedHeaders(), 1)
	require.NotNil(t, upstream.lastReq)
	require.False(t, isOpenAICindyHTTPToWSV2Bypassed(c))
	nextAccount := cindyHTTPToWSV2TestAccount()
	nextAccount.ID = 2
	_, eligible := svc.resolveCindyHTTPToWSV2Decision(c, nextAccount)
	require.True(t, eligible)
}

func TestCindyHTTPToWSV2HandshakeFallbackDoesNotRewriteInvalidEncryptedContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"openai/gpt-5.6-luna","stream":false,"store":false,"input":[` +
		`{"type":"reasoning","encrypted_content":"ENC"},` +
		`{"type":"compaction","encrypted_content":"CMP"},` +
		`{"type":"message","role":"user","content":"continue"}` +
		`]}`)

	for _, tc := range []struct {
		name        string
		passthrough bool
	}{
		{name: "passthrough", passthrough: true},
		{name: "normalized_forward"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(
					`{"error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"encrypted content could not be verified"}}`,
				)),
			}}
			svc, dialer := newCindyHTTPToWSV2TurnStateTestService(t, cindyHTTPToWSV2DialStep{
				status: http.StatusForbidden,
				err: &openAIWSHandshakeError{
					Body: []byte(`{"error":{"message":"websocket upgrade forbidden"}}`),
					Err:  errors.New("handshake rejected"),
				},
			})
			svc.httpUpstream = upstream
			account := cindyHTTPToWSV2TestAccount()
			account.Extra["openai_passthrough"] = tc.passthrough

			result, err := svc.Forward(context.Background(), cindyHTTPToWSV2TestContext("/v1/responses"), account, body)

			require.Nil(t, result)
			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.True(t, failoverErr.IsOpenAIContinuationStateUnavailable())
			require.Len(t, dialer.capturedHeaders(), 1)
			require.Len(t, upstream.requests, 1, "invalid encrypted fallback must not trigger a destructive cleanup retry")
			require.JSONEq(t, gjson.GetBytes(body, "input").Raw, gjson.GetBytes(upstream.bodies[0], "input").Raw)
		})
	}
}

func TestCindyPassthroughInvalidEncryptedContentFailsWithoutPayloadCleanup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"encrypted content could not be verified"}}`,
		)),
	}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
	body := []byte(`{"model":"gpt-5.6-luna","store":false,"previous_response_id":"resp_1","input":[{"type":"reasoning","id":"rs_foreign","encrypted_content":"cipher","phase":"analysis"}]}`)

	result, err := svc.Forward(context.Background(), c, cindyHTTPToWSV2TestAccount(), body)

	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.IsOpenAIContinuationStateUnavailable())
	require.Len(t, upstream.bodies, 1, "strict Cindy must not retry a cleaned request")
	require.Equal(t, "resp_1", gjson.GetBytes(upstream.bodies[0], "previous_response_id").String())
	require.Equal(t, "rs_foreign", gjson.GetBytes(upstream.bodies[0], "input.0.id").String())
	require.Equal(t, "cipher", gjson.GetBytes(upstream.bodies[0], "input.0.encrypted_content").String())
	require.Equal(t, "analysis", gjson.GetBytes(upstream.bodies[0], "input.0.phase").String())
}

func TestLegacyCindyRuntimeCompatibilityPreservesOpaqueHTTPContinuation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"encrypted content could not be verified"}}`,
		)),
	}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
	body := []byte(`{"model":"openai/gpt-5.6-luna","store":false,"previous_response_id":"resp_legacy","input":[{"type":"reasoning","id":"rs_legacy","encrypted_content":"cipher","phase":"analysis"}]}`)
	account := cindyHTTPToWSV2TestAccount()
	account.Platform = PlatformOpenAI

	result, err := svc.Forward(context.Background(), c, account, body)

	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.IsOpenAIContinuationStateUnavailable())
	require.Len(t, upstream.bodies, 1, "legacy Laxa continuation must not retry a destructively cleaned request")
	require.Equal(t, "resp_legacy", gjson.GetBytes(upstream.bodies[0], "previous_response_id").String())
	require.Equal(t, "rs_legacy", gjson.GetBytes(upstream.bodies[0], "input.0.id").String())
	require.Equal(t, "cipher", gjson.GetBytes(upstream.bodies[0], "input.0.encrypted_content").String())
}

func requireCindyPortableOpaqueStateRemoved(t *testing.T, body []byte) {
	t.Helper()
	for _, value := range []string{
		"stale-cipher-reasoning", "stale-cipher-compaction", "stale-cipher-compaction-summary",
		"rs_deleted_account", "cmp_deleted_account", "cmp_summary_deleted_account",
		"reasoning summary", "compaction summary", "compaction-summary summary",
		"reasoning-phase", "compaction-phase", "compaction-summary-phase",
	} {
		require.NotContains(t, string(body), value, "the recovered request must drop the complete portable opaque state item")
	}
	require.Equal(t, int64(3), gjson.GetBytes(body, "input.#").Int())
	require.Equal(t, "message", gjson.GetBytes(body, "input.0.type").String())
	require.Equal(t, "function_call", gjson.GetBytes(body, "input.1.type").String())
	require.Equal(t, "call_1", gjson.GetBytes(body, "input.1.call_id").String())
	require.Equal(t, "function_call_output", gjson.GetBytes(body, "input.2.type").String())
	require.Equal(t, "call_1", gjson.GetBytes(body, "input.2.call_id").String())
	require.Equal(t, "kept", gjson.GetBytes(body, "input.2.output").String())
}

func TestLegacyCindyRuntimeCompatibilityReplaysSelfContainedHistoryWithoutInvalidOpaqueState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"encrypted content could not be verified"}}`,
			)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"id":"resp_recovered","status":"completed","model":"openai/gpt-5.6-luna","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`,
			)),
		},
	}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, toolCorrector: NewCodexToolCorrector()}
	account := cindyHTTPToWSV2TestAccount()
	account.Platform = PlatformOpenAI
	body := []byte(`{"model":"openai/gpt-5.6-luna","store":false,"input":[{"type":"message","role":"user","content":"continue"},{"type":"reasoning","id":"rs_deleted_account","encrypted_content":"stale-cipher-reasoning","summary":"reasoning summary","phase":"reasoning-phase"},{"type":"compaction","id":"cmp_deleted_account","encrypted_content":"stale-cipher-compaction","summary":"compaction summary","phase":"compaction-phase"},{"type":"compaction_summary","id":"cmp_summary_deleted_account","encrypted_content":"stale-cipher-compaction-summary","summary":"compaction-summary summary","phase":"compaction-summary-phase"},{"type":"function_call","call_id":"call_1","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"kept"}]}`)

	result, err := svc.Forward(context.Background(), cindyHTTPToWSV2TestContext("/v1/responses"), account, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2)
	require.Contains(t, string(upstream.bodies[0]), "stale-cipher-compaction-summary")
	requireCindyPortableOpaqueStateRemoved(t, upstream.bodies[1])
}

func TestLegacyCindyRuntimeCompatibilityReplaysSelfContainedHistoryOverNonPassthroughHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"encrypted content could not be verified"}}`,
			)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"id":"resp_recovered","status":"completed","model":"openai/gpt-5.6-luna","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`,
			)),
		},
	}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, toolCorrector: NewCodexToolCorrector()}
	account := cindyHTTPToWSV2TestAccount()
	account.Platform = PlatformOpenAI
	account.Extra = map[string]any{}
	body := []byte(`{"model":"openai/gpt-5.6-luna","store":false,"input":[{"type":"message","role":"user","content":"continue"},{"type":"reasoning","id":"rs_deleted_account","encrypted_content":"stale-cipher-reasoning","summary":"reasoning summary","phase":"reasoning-phase"},{"type":"compaction","id":"cmp_deleted_account","encrypted_content":"stale-cipher-compaction","summary":"compaction summary","phase":"compaction-phase"},{"type":"compaction_summary","id":"cmp_summary_deleted_account","encrypted_content":"stale-cipher-compaction-summary","summary":"compaction-summary summary","phase":"compaction-summary-phase"},{"type":"function_call","call_id":"call_1","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"kept"}]}`)

	result, err := svc.Forward(context.Background(), cindyHTTPToWSV2TestContext("/v1/responses"), account, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2)
	requireCindyPortableOpaqueStateRemoved(t, upstream.bodies[1])
}

func TestCanonicalCindyReplaysSelfContainedHistoryOverNonPassthroughHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"encrypted content could not be verified"}}`,
			)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"id":"resp_recovered","status":"completed","model":"openai/gpt-5.6-luna","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`,
			)),
		},
	}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, toolCorrector: NewCodexToolCorrector()}
	account := cindyHTTPToWSV2TestAccount()
	account.Extra = map[string]any{}
	body := []byte(`{"model":"openai/gpt-5.6-luna","store":false,"input":[{"type":"message","role":"user","content":"continue"},{"type":"reasoning","id":"rs_deleted_account","encrypted_content":"stale-cipher-reasoning","summary":"reasoning summary","phase":"reasoning-phase"},{"type":"compaction","id":"cmp_deleted_account","encrypted_content":"stale-cipher-compaction","summary":"compaction summary","phase":"compaction-phase"},{"type":"compaction_summary","id":"cmp_summary_deleted_account","encrypted_content":"stale-cipher-compaction-summary","summary":"compaction-summary summary","phase":"compaction-summary-phase"},{"type":"function_call","call_id":"call_1","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"kept"}]}`)

	result, err := svc.Forward(context.Background(), cindyHTTPToWSV2TestContext("/v1/responses"), account, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2)
	require.Contains(t, string(upstream.bodies[0]), "stale-cipher-compaction-summary")
	requireCindyPortableOpaqueStateRemoved(t, upstream.bodies[1])
}

func TestLegacyCindyHTTPToWSV2RetriesSelfContainedHistoryAfterInvalidOpaqueState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	failed := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.failed","response":{"id":"resp_invalid","status":"failed","error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"encrypted content could not be verified"}}}`),
	}}
	recovered := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.completed","response":{"id":"resp_recovered_ws","model":"openai/gpt-5.6-luna","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`),
	}}
	svc, dialer := newCindyHTTPToWSV2TurnStateTestService(t,
		cindyHTTPToWSV2DialStep{conn: failed},
		cindyHTTPToWSV2DialStep{conn: recovered},
	)
	account := cindyHTTPToWSV2TestAccount()
	account.Platform = PlatformOpenAI
	body := []byte(`{"model":"openai/gpt-5.6-luna","store":false,"input":[{"type":"message","role":"user","content":"continue"},{"type":"reasoning","id":"rs_deleted_account","encrypted_content":"stale-cipher-reasoning","summary":"reasoning summary","phase":"reasoning-phase"},{"type":"compaction","id":"cmp_deleted_account","encrypted_content":"stale-cipher-compaction","summary":"compaction summary","phase":"compaction-phase"},{"type":"compaction_summary","id":"cmp_summary_deleted_account","encrypted_content":"stale-cipher-compaction-summary","summary":"compaction-summary summary","phase":"compaction-summary-phase"},{"type":"function_call","call_id":"call_1","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"kept"}]}`)

	result, err := svc.Forward(context.Background(), cindyHTTPToWSV2TestContext("/v1/responses"), account, body)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, dialer.capturedHeaders(), 2, "the cleaned history must be sent on a fresh WS connection")
	failed.mu.Lock()
	failedWrites := append([]map[string]any(nil), failed.writes...)
	failed.mu.Unlock()
	recovered.mu.Lock()
	recoveredWrites := append([]map[string]any(nil), recovered.writes...)
	recovered.mu.Unlock()
	require.Len(t, failedWrites, 1)
	require.Len(t, recoveredWrites, 1)
	recoveredPayload, marshalErr := json.Marshal(recoveredWrites[0])
	require.NoError(t, marshalErr)
	requireCindyPortableOpaqueStateRemoved(t, recoveredPayload)
}

func TestCanonicalCindyHTTPToWSV2RetriesSelfContainedHistoryAfterInvalidOpaqueState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	failed := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.failed","response":{"id":"resp_invalid","status":"failed","error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"encrypted content could not be verified"}}}`),
	}}
	recovered := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.completed","response":{"id":"resp_recovered_ws","model":"openai/gpt-5.6-luna","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`),
	}}
	svc, dialer := newCindyHTTPToWSV2TurnStateTestService(t,
		cindyHTTPToWSV2DialStep{conn: failed},
		cindyHTTPToWSV2DialStep{conn: recovered},
	)
	body := []byte(`{"model":"openai/gpt-5.6-luna","store":false,"input":[{"type":"message","role":"user","content":"continue"},{"type":"reasoning","id":"rs_deleted_account","encrypted_content":"stale-cipher-reasoning","summary":"reasoning summary","phase":"reasoning-phase"},{"type":"compaction","id":"cmp_deleted_account","encrypted_content":"stale-cipher-compaction","summary":"compaction summary","phase":"compaction-phase"},{"type":"compaction_summary","id":"cmp_summary_deleted_account","encrypted_content":"stale-cipher-compaction-summary","summary":"compaction-summary summary","phase":"compaction-summary-phase"},{"type":"function_call","call_id":"call_1","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"kept"}]}`)

	result, err := svc.Forward(
		context.Background(),
		cindyHTTPToWSV2TestContext("/v1/responses"),
		cindyHTTPToWSV2TestAccount(),
		body,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, dialer.capturedHeaders(), 2, "the cleaned history must be sent on a fresh WS connection")
	failed.mu.Lock()
	failedWrites := append([]map[string]any(nil), failed.writes...)
	failed.mu.Unlock()
	recovered.mu.Lock()
	recoveredWrites := append([]map[string]any(nil), recovered.writes...)
	recovered.mu.Unlock()
	require.Len(t, failedWrites, 1)
	require.Len(t, recoveredWrites, 1)
	require.Contains(t, string(payloadAsJSONBytes(failedWrites[0])), "stale-cipher-compaction-summary")
	requireCindyPortableOpaqueStateRemoved(t, payloadAsJSONBytes(recoveredWrites[0]))
}

func TestPrepareOpenAICindyStatelessHTTPFallbackSafetyBoundary(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{name: "plain first turn", body: `{"model":"gpt-5.6-luna","input":"hello"}`, want: true},
		{name: "previous response anchor", body: `{"model":"gpt-5.6-luna","previous_response_id":"resp_1","input":"next"}`},
		{name: "orphan tool output", body: `{"model":"gpt-5.6-luna","input":[{"type":"custom_tool_call_output","call_id":"call_1","output":"ok"}]}`},
		{name: "single object orphan tool output", body: `{"model":"gpt-5.6-luna","input":{"type":"custom_tool_call_output","call_id":"call_1","output":"ok"}}`},
		{name: "item reference", body: `{"model":"gpt-5.6-luna","input":[{"type":"item_reference","id":"call_1"}]}`},
		{name: "reasoning id only", body: `{"model":"gpt-5.6-luna","input":[{"type":"reasoning","id":"rs_1"}]}`},
		{name: "concrete tool output", body: `{"model":"gpt-5.6-luna","input":[{"type":"custom_tool_call","call_id":"call_1","name":"tool","input":"{}"},{"type":"custom_tool_call_output","call_id":"call_1","output":"ok"}]}`, want: true},
		{name: "encrypted state remains opaque", body: `{"model":"gpt-5.6-luna","input":[{"type":"reasoning","encrypted_content":"ENC"}]}`, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, safe := prepareOpenAICindyStatelessHTTPFallback([]byte(tc.body))
			require.Equal(t, tc.want, safe)
		})
	}

	cyber := &openAIWSDialError{
		StatusCode:   http.StatusForbidden,
		ResponseBody: []byte(`{"error":{"code":"cyber_policy","message":"blocked"}}`),
	}
	require.False(t, isOpenAICindyHTTPToWSV2HandshakeForbidden(cyber))
}

func TestCindyHTTPToWSV2FirstTurnHandshakeFailoverClassification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := cindyHTTPToWSV2TestService()
	account := cindyHTTPToWSV2TestAccount()
	for _, tc := range []struct {
		statusCode int
		wantScope  GatewayFailureScope
	}{
		{http.StatusForbidden, GatewayFailureScopeRequest},
		{http.StatusTooManyRequests, GatewayFailureScopeRequest},
		{http.StatusRequestTimeout, GatewayFailureScopeAccount},
		{http.StatusInternalServerError, GatewayFailureScopeAccount},
		{http.StatusBadGateway, GatewayFailureScopeAccount},
	} {
		t.Run(http.StatusText(tc.statusCode), func(t *testing.T) {
			dialErr := &openAIWSDialError{
				StatusCode: tc.statusCode,
				ResponseHeaders: http.Header{
					"X-Request-Id": []string{"request-redacted"},
				},
				ResponseBody: []byte(`{"error":{"message":"temporary failure"}}`),
			}
			c := cindyHTTPToWSV2TestContext("/v1/responses")

			classified, ok := svc.cindyHTTPToWSV2FirstTurnFailover(
				context.Background(), c, account, "gpt-5.4-mini", dialErr,
			)

			require.True(t, ok)
			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, classified, &failoverErr)
			require.Equal(t, tc.statusCode, failoverErr.StatusCode)
			require.False(t, failoverErr.RetryableOnSameAccount)
			require.Equal(t, GatewayFailureStageInference, failoverErr.Stage)
			require.Equal(t, tc.wantScope, failoverErr.Scope)
			require.NotEmpty(t, failoverErr.Reason)
			require.True(t, failoverErr.CindyHTTPToWSV2FirstTurn)
			require.JSONEq(t, string(openAITransportFailoverBody), string(failoverErr.ResponseBody))

			rawEvents, exists := c.Get(OpsUpstreamErrorsKey)
			require.True(t, exists)
			events, ok := rawEvents.([]*OpsUpstreamErrorEvent)
			require.True(t, ok)
			require.Len(t, events, 1)
			require.Equal(t, "failover", events[0].Kind)
			require.Equal(t, string(GatewayFailureStageInference), events[0].Stage)
			require.Equal(t, string(tc.wantScope), events[0].Scope)
			require.Equal(t, string(failoverErr.Reason), events[0].Reason)
			require.Equal(t, "request-redacted", events[0].UpstreamRequestID)
		})
	}

	t.Run("transport_without_http_status", func(t *testing.T) {
		classified, ok := svc.cindyHTTPToWSV2FirstTurnFailover(
			context.Background(), cindyHTTPToWSV2TestContext("/v1/responses"), account, "gpt-5.4-mini",
			&openAIWSDialError{Err: errors.New("connection refused")},
		)
		require.True(t, ok)
		var failoverErr *UpstreamFailoverError
		require.ErrorAs(t, classified, &failoverErr)
		require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
		require.Equal(t, GatewayFailureScopeAccount, failoverErr.Scope)
		require.True(t, failoverErr.CindyHTTPToWSV2FirstTurn)
	})

	htmlErr := &openAIWSDialError{StatusCode: http.StatusForbidden, ResponseBody: []byte("<!doctype html><html><body>Forbidden</body></html>")}
	classified, ok := svc.cindyHTTPToWSV2FirstTurnFailover(
		context.Background(), cindyHTTPToWSV2TestContext("/v1/responses"), account, "gpt-5.4-mini", htmlErr,
	)
	require.True(t, ok)
	var htmlFailover *UpstreamFailoverError
	require.ErrorAs(t, classified, &htmlFailover)
	require.True(t, htmlFailover.SuppressAccountHealthPenalty)
	require.Equal(t, GatewayFailureScopeRequest, htmlFailover.Scope)
	require.JSONEq(t, string(openAITransportFailoverBody), string(htmlFailover.ResponseBody))

	classified, ok = svc.cindyHTTPToWSV2FirstTurnFailover(
		context.Background(), cindyHTTPToWSV2TestContext("/v1/responses"), account, "gpt-5.4-mini",
		coderws.CloseError{Code: coderws.StatusTryAgainLater, Reason: "retry later"},
	)
	require.True(t, ok)
	var closeFailover *UpstreamFailoverError
	require.ErrorAs(t, classified, &closeFailover)
	require.Equal(t, http.StatusServiceUnavailable, closeFailover.StatusCode)
	require.Equal(t, GatewayFailureScopeRequest, closeFailover.Scope)
	require.True(t, closeFailover.CindyHTTPToWSV2FirstTurn)
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
				context.Background(), nil, account, "gpt-5.4-mini", http.Header{}, []byte(tc.payload),
			)
			require.True(t, ok)
			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, classified, &failoverErr)
			require.Equal(t, tc.wantStatus, failoverErr.StatusCode)
			require.Equal(t, GatewayFailureScopeRequest, failoverErr.Scope)
			require.True(t, failoverErr.ShouldRetryNextAccount())
			require.True(t, failoverErr.SuppressAccountHealthPenalty)
			require.False(t, failoverErr.ShouldReportAccountScheduleFailure())
			require.JSONEq(t, string(openAITransportFailoverBody), string(failoverErr.ResponseBody))
		})
	}
}

func TestCindyHTTPToWSV2RequestScopedTerminalDoesNotCoolAccountModel(t *testing.T) {
	svc := cindyHTTPToWSV2TestService()
	svc.rateLimitService = NewRateLimitService(nil, nil, &config.Config{}, nil, nil)
	account := cindyHTTPToWSV2TestAccount()
	account.ID = 4709
	const model = "openai/gpt-5.6-luna"
	payload := []byte(`{"type":"error","error":{"status":503,"message":"unavailable"}}`)

	for range 2 {
		classified, ok := svc.cindyHTTPToWSV2FirstTurnEventFailover(
			context.Background(), nil, account, model, http.Header{}, payload,
		)
		require.True(t, ok)
		var failoverErr *UpstreamFailoverError
		require.ErrorAs(t, classified, &failoverErr)
		require.Equal(t, GatewayFailureScopeRequest, failoverErr.Scope)
	}

	require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, model))
}

func TestCindyHTTPToWSV2TurnStateStagesUntilOutputAndRecordsFailoverAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	accountA := cindyHTTPToWSV2TestAccount()
	accountA.ID = 9101
	accountB := cindyHTTPToWSV2TestAccount()
	accountB.ID = 9102
	dialerSteps := []cindyHTTPToWSV2DialStep{
		{
			conn: &openAIWSCaptureConn{events: [][]byte{
				[]byte(`{"type":"response.failed","response":{"id":"resp_a_failed","status":"failed","error":{"status_code":503,"message":"sensitive A failure"}}}`),
			}},
			handshake: http.Header{
				"X-Codex-Turn-State": []string{"turn-state-A-uncommitted"},
				"X-Request-Id":       []string{"upstream-attempt-A"},
			},
		},
		{
			conn: &openAIWSCaptureConn{events: [][]byte{
				[]byte(`{"type":"response.completed","response":{"id":"resp_b_done","model":"gpt-5.4-mini","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`),
			}},
			handshake: http.Header{"X-Codex-Turn-State": []string{"turn-state-B"}},
		},
	}
	svc, dialer := newCindyHTTPToWSV2TurnStateTestService(t, dialerSteps...)
	groupID := int64(77)
	c, recorder := newCindyHTTPToWSV2TurnStateTestContext("session-failover-stage", 7001, groupID)
	body := []byte(`{"model":"gpt-5.4-mini","stream":true,"input":"hi"}`)

	result, err := svc.Forward(context.Background(), c, accountA, body)
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusServiceUnavailable, failoverErr.StatusCode)
	require.Equal(t, GatewayFailureScopeRequest, failoverErr.Scope)
	require.True(t, failoverErr.ShouldRetryNextAccount())
	require.True(t, failoverErr.SuppressAccountHealthPenalty)
	require.False(t, failoverErr.ShouldReportAccountScheduleFailure())
	require.True(t, failoverErr.CindyHTTPToWSV2FirstTurn)
	require.Empty(t, recorder.Body.String(), "pre-output failure must not commit SSE")
	require.Empty(t, recorder.Header().Get(openAICodexTurnStateHeader), "failed account state must remain staged")

	sessionHash := svc.GenerateSessionHash(c, nil)
	store := svc.getOpenAIWSStateStore()
	_, ok := store.GetSessionTurnState(groupID, sessionHash, accountA.ID)
	require.False(t, ok, "failed account state must not be persisted")

	rawEvents, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	attempt := events[0]
	require.Equal(t, PlatformCindy, attempt.Platform)
	require.Equal(t, accountA.ID, attempt.AccountID)
	require.Equal(t, http.StatusServiceUnavailable, attempt.UpstreamStatusCode)
	require.Equal(t, "upstream-attempt-A", attempt.UpstreamRequestID)
	require.Equal(t, "failover", attempt.Kind)
	require.Equal(t, string(GatewayFailureStageInference), attempt.Stage)
	require.Equal(t, string(GatewayFailureScopeRequest), attempt.Scope)
	require.Equal(t, string(openAICindyHTTPToWSV2TerminalReason), attempt.Reason)
	require.Equal(t, "Temporary upstream failure", attempt.Message)
	require.Empty(t, attempt.AccountName)
	require.Empty(t, attempt.UpstreamResponseBody)
	require.Empty(t, attempt.Detail)

	result, err = svc.Forward(context.Background(), c, accountB, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "resp_b_done", result.RequestID)
	headers := dialer.capturedHeaders()
	require.Len(t, headers, 2)
	require.Empty(t, headers[1].Get(openAICodexTurnStateHeader), "B must not receive A's uncommitted state")
	require.Equal(t, "turn-state-B", recorder.Header().Get(openAICodexTurnStateHeader))
	state, ok := store.GetSessionTurnState(groupID, sessionHash, accountB.ID)
	require.True(t, ok)
	require.Equal(t, "turn-state-B", state)
	_, ok = store.GetSessionTurnState(groupID, sessionHash, accountA.ID)
	require.False(t, ok)

	rawOrigin, ok := svc.openaiCodexTurnStateOrigins.Load(openAICodexTurnStateSeed(c))
	require.True(t, ok)
	origin, ok := rawOrigin.(openAICodexTurnStateOrigin)
	require.True(t, ok)
	require.Equal(t, accountB.ID, origin.accountID)
}

func TestCindyHTTPToWSV2TurnStateNoStateClearsPriorOwnerAndProvenance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	completed := func(id string) []byte {
		return []byte(`{"type":"response.completed","response":{"id":"` + id + `","model":"gpt-5.4-mini","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`)
	}
	svc, dialer := newCindyHTTPToWSV2TurnStateTestService(t,
		cindyHTTPToWSV2DialStep{
			conn:      &openAIWSCaptureConn{events: [][]byte{completed("resp_owner_a1")}},
			handshake: http.Header{"X-Codex-Turn-State": []string{"turn-state-A"}},
		},
		cindyHTTPToWSV2DialStep{
			conn: &openAIWSCaptureConn{events: [][]byte{completed("resp_owner_b")}},
		},
		cindyHTTPToWSV2DialStep{
			conn:      &openAIWSCaptureConn{events: [][]byte{completed("resp_owner_a2")}},
			handshake: http.Header{"X-Codex-Turn-State": []string{"turn-state-A-next"}},
		},
	)
	accountA := cindyHTTPToWSV2TestAccount()
	accountA.ID = 9201
	accountB := cindyHTTPToWSV2TestAccount()
	accountB.ID = 9202
	groupID := int64(78)
	body := []byte(`{"model":"gpt-5.4-mini","stream":false,"input":"hi"}`)

	cA1, _ := newCindyHTTPToWSV2TurnStateTestContext("session-owner-reuse", 7002, groupID)
	resultA1, err := svc.Forward(context.Background(), cA1, accountA, body)
	require.NoError(t, err)
	require.NotNil(t, resultA1)
	store := svc.getOpenAIWSStateStore()
	sessionHash := svc.GenerateSessionHash(cA1, nil)
	connIDA1, ok := store.GetResponseConn(resultA1.RequestID)
	require.True(t, ok)
	svc.getOpenAIWSConnPool().evictConn(accountA.ID, connIDA1)

	cB, recorderB := newCindyHTTPToWSV2TurnStateTestContext("session-owner-reuse", 7002, groupID)
	cB.Request.Header.Set(openAICodexTurnStateHeader, "turn-state-A")
	recorderB.Header().Set(openAICodexTurnStateHeader, "stale-response-state")
	resultB, err := svc.Forward(context.Background(), cB, accountB, body)
	require.NoError(t, err)
	require.NotNil(t, resultB)
	require.Empty(t, recorderB.Header().Get(openAICodexTurnStateHeader), "a successful no-state handshake must clear a stale response header")
	_, ok = store.GetSessionTurnState(groupID, sessionHash, accountA.ID)
	require.False(t, ok, "B's successful no-state turn must invalidate A's stale state")
	_, ok = store.GetSessionTurnState(groupID, sessionHash, accountB.ID)
	require.False(t, ok)
	_, ok = svc.openaiCodexTurnStateOrigins.Load(openAICodexTurnStateSeed(cB))
	require.False(t, ok, "a successful no-state turn must clear stale provenance")
	connIDB, ok := store.GetResponseConn(resultB.RequestID)
	require.True(t, ok)
	svc.getOpenAIWSConnPool().evictConn(accountB.ID, connIDB)

	cA2, _ := newCindyHTTPToWSV2TurnStateTestContext("session-owner-reuse", 7002, groupID)
	resultA2, err := svc.Forward(context.Background(), cA2, accountA, body)
	require.NoError(t, err)
	require.NotNil(t, resultA2)
	headers := dialer.capturedHeaders()
	require.Len(t, headers, 3)
	require.Empty(t, headers[1].Get(openAICodexTurnStateHeader), "state owned by A must not be injected into B")
	require.Empty(t, headers[2].Get(openAICodexTurnStateHeader), "A's state must not survive across B's successful no-state turn")
	state, ok := store.GetSessionTurnState(groupID, sessionHash, accountA.ID)
	require.True(t, ok)
	require.Equal(t, "turn-state-A-next", state)
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
				"", "sk-test", decision, true, true,
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
			require.Equal(t, 0, repo.markCalls, "the first exact signal must wait for independent confirmation")
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
