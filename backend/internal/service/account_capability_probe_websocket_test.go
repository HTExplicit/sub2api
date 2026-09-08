package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	coderws "github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type capabilityWSHTTPGuard struct{ calls int }

func (g *capabilityWSHTTPGuard) Do(*http.Request, string, int64, int) (*http.Response, error) {
	g.calls++
	return nil, errors.New("HTTP fallback must not be called")
}

func (g *capabilityWSHTTPGuard) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return g.Do(req, proxy, accountID, concurrency)
}

type capabilityWSFakeConn struct {
	frames    [][]byte
	writes    [][]byte
	deadlines []time.Duration
	writeErr  error
	readErr   error
	closed    int
}

func (c *capabilityWSFakeConn) WriteJSON(ctx context.Context, value any) error {
	wire, err := json.Marshal(value)
	if err != nil {
		return err
	}
	c.writes = append(c.writes, wire)
	if deadline, ok := ctx.Deadline(); ok {
		c.deadlines = append(c.deadlines, time.Until(deadline))
	}
	return c.writeErr
}

func (c *capabilityWSFakeConn) ReadMessage(context.Context) ([]byte, error) {
	if len(c.frames) == 0 {
		if c.readErr != nil {
			return nil, c.readErr
		}
		return nil, io.EOF
	}
	frame := c.frames[0]
	c.frames = c.frames[1:]
	return frame, nil
}

func (*capabilityWSFakeConn) Ping(context.Context) error { return nil }
func (c *capabilityWSFakeConn) Close() error {
	c.closed++
	return nil
}

type capabilityWSFakeDialer struct {
	conn    *capabilityWSFakeConn
	count   int
	url     string
	headers http.Header
	proxy   string
	status  int
	err     error
}

func (d *capabilityWSFakeDialer) Dial(_ context.Context, target string, headers http.Header, proxy string) (openAIWSClientConn, int, http.Header, error) {
	d.count++
	d.url, d.headers, d.proxy = target, cloneHeader(headers), proxy
	if d.err != nil {
		return nil, d.status, nil, d.err
	}
	return d.conn, 0, nil, nil
}

func capabilityWSFixture(dialer openAIWSClientDialer) (*AccountCapabilityProbeService, *Account, *capabilityWSHTTPGuard) {
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	guard := &capabilityWSHTTPGuard{}
	tests := &AccountTestService{
		cfg: cfg, httpUpstream: guard,
		openAIGatewayService: &OpenAIGatewayService{cfg: cfg, openaiWSPassthroughDialer: dialer},
	}
	account := &Account{
		ID: 7001, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1, Status: StatusActive, Schedulable: false,
		Credentials: map[string]any{"api_key": "fixture-secret", "base_url": "https://upstream.example/relay/v1"},
		Extra:       map[string]any{"openai_apikey_responses_websockets_v2_enabled": true},
	}
	return NewAccountCapabilityProbeService(tests), account, guard
}

func capabilityWSFrames(events ...string) [][]byte {
	frames := make([][]byte, len(events))
	for i, event := range events {
		frames[i] = []byte(event)
	}
	return frames
}

const capabilityWSCompletedText = `{"type":"response.completed","response":{"id":"resp-final","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":9,"output_tokens":1,"total_tokens":10}}}`

func TestAccountCapabilityWebSocketExactSingleRequest(t *testing.T) {
	conn := &capabilityWSFakeConn{frames: capabilityWSFrames(capabilityWSCompletedText)}
	dialer := &capabilityWSFakeDialer{conn: conn}
	svc, account, httpGuard := capabilityWSFixture(dialer)
	account.Credentials[credKeyHeaderOverrideEnabled] = true
	account.Credentials[credKeyHeaderOverrides] = map[string]any{"user-agent": "account-observer/1", "x-fixture": "preserved"}
	account.Credentials["model_mapping"] = map[string]any{"vendor/Exact-Model": "DO-NOT-MAP"}
	proxyID := int64(6)
	account.ProxyID, account.Proxy = &proxyID, &Proxy{Protocol: "http", Host: "proxy.example", Port: 1080}
	before, err := json.Marshal(account)
	require.NoError(t, err)

	result := svc.Probe(context.Background(), account, "vendor/Exact-Model", AccountCapabilityProtocolResponsesWebSocket, AccountCapabilityProfileText)

	require.Equal(t, "alive", result.Status)
	require.Equal(t, AccountCapabilityProtocolResponsesWebSocket, result.Protocol)
	require.Equal(t, 1, result.HandshakeCount)
	require.Equal(t, 1, result.RequestCount)
	require.True(t, result.Streaming)
	require.Len(t, result.Attempts, 1)
	require.Equal(t, int64(10), *result.Usage.TotalTokens)
	require.Equal(t, 1, dialer.count)
	require.Equal(t, "wss://upstream.example/relay/v1/responses", dialer.url)
	require.Equal(t, "Bearer fixture-secret", dialer.headers.Get("Authorization"))
	require.Equal(t, openAIWSBetaV2Value, dialer.headers.Get("OpenAI-Beta"))
	require.Equal(t, "account-observer/1", dialer.headers.Get("User-Agent"))
	require.Equal(t, "preserved", getHeaderRaw(dialer.headers, "x-fixture"))
	require.Equal(t, "http://proxy.example:1080", dialer.proxy)
	require.Len(t, conn.writes, 1)
	wire := conn.writes[0]
	require.Equal(t, "response.create", gjson.GetBytes(wire, "type").String())
	require.Equal(t, "vendor/Exact-Model", gjson.GetBytes(wire, "model").String())
	require.Equal(t, int64(256), gjson.GetBytes(wire, "max_output_tokens").Int())
	require.True(t, gjson.GetBytes(wire, "stream").Bool())
	require.False(t, gjson.GetBytes(wire, "store").Bool())
	require.False(t, gjson.GetBytes(wire, "reasoning").Exists())
	require.Len(t, conn.deadlines, 1)
	require.Greater(t, conn.deadlines[0], time.Duration(0))
	require.LessOrEqual(t, conn.deadlines[0], accountCapabilityRequestTimeout)
	require.Equal(t, 1, conn.closed)
	require.Zero(t, httpGuard.calls)
	after, err := json.Marshal(account)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "fixture-secret")
	require.NotContains(t, string(encoded), "resp-final")
}

func TestAccountCapabilityWebSocketGateDoesNotDial(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Account)
	}{
		{"missing_flag", func(a *Account) { a.Extra = nil }},
		{"disabled", func(a *Account) { a.Extra["openai_apikey_responses_websockets_v2_enabled"] = false }},
		{"force_http", func(a *Account) { a.Extra["openai_ws_force_http"] = true }},
		{"mode_off", func(a *Account) { a.Extra["openai_apikey_responses_websockets_v2_mode"] = "off" }},
		{"http_bridge", func(a *Account) { a.Extra["openai_apikey_responses_websockets_v2_mode"] = "http_bridge" }},
		{"oauth", func(a *Account) { a.Type = AccountTypeOAuth }},
		{"non_openai", func(a *Account) { a.Platform = PlatformAnthropic }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dialer := &capabilityWSFakeDialer{conn: &capabilityWSFakeConn{}}
			svc, account, guard := capabilityWSFixture(dialer)
			tc.mutate(account)
			result := svc.Probe(context.Background(), account, "fixture-model", AccountCapabilityProtocolResponsesWebSocket, AccountCapabilityProfileText)
			require.Equal(t, "unsupported", result.Status)
			require.Zero(t, result.RequestCount)
			require.Zero(t, result.HandshakeCount)
			require.Zero(t, dialer.count)
			require.Zero(t, guard.calls)
		})
	}
}

func TestAccountCapabilityWebSocketRequiresAuthorityAndSemantics(t *testing.T) {
	cases := []struct {
		name           string
		events         []string
		classification string
	}{
		{"delta_then_eof", []string{`{"type":"response.output_text.delta","delta":"OK"}`}, "incomplete_response"},
		{"completed_without_status", []string{`{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}]}}`}, "incomplete_response"},
		{"empty_completed", []string{`{"type":"response.completed","response":{"status":"completed","output":[]}}`}, "no_semantic_output"},
		{"done_only", []string{`[DONE]`}, "invalid_response"},
		{"non_json", []string{`not-json`}, "invalid_response"},
		{"budget", []string{`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`}, "output_budget_exhausted"},
		{"model_error", []string{`{"type":"error","error":{"code":"model_not_found","message":"sensitive fixture-secret"}}`}, "model_unavailable"},
		{"invalid_key_event_is_not_account_verdict", []string{`{"type":"error","error":{"code":"invalid_api_key","message":"sensitive fixture-secret"}}`}, "request_rejected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &capabilityWSFakeConn{frames: capabilityWSFrames(tc.events...)}
			dialer := &capabilityWSFakeDialer{conn: conn}
			svc, account, guard := capabilityWSFixture(dialer)
			result := svc.Probe(context.Background(), account, "fixture-model", AccountCapabilityProtocolResponsesWebSocket, AccountCapabilityProfileText)
			require.NotEqual(t, "alive", result.Status)
			require.Equal(t, tc.classification, result.Classification)
			require.Equal(t, 1, result.RequestCount)
			require.Equal(t, 1, dialer.count)
			require.False(t, result.AccountFailure)
			require.Zero(t, guard.calls)
			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "fixture-secret")
		})
	}
}

func TestAccountCapabilityWebSocketToolRoundtripSameConnection(t *testing.T) {
	toolEvent := `{"type":"response.completed","response":{"status":"completed","output":[{"type":"reasoning","encrypted_content":"opaque-local-only","x_extension":"preserved"},{"type":"function_call","id":"item_1","call_id":"call_1","name":"capability_ping","arguments":"{\"value\":\"ok\"}","namespace":null,"phase":"analysis"}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`
	conn := &capabilityWSFakeConn{frames: capabilityWSFrames(toolEvent, capabilityWSCompletedText)}
	dialer := &capabilityWSFakeDialer{conn: conn}
	svc, account, guard := capabilityWSFixture(dialer)
	result := svc.Probe(context.Background(), account, "fixture-model", AccountCapabilityProtocolResponsesWebSocket, AccountCapabilityProfileToolRoundtrip)
	require.Equal(t, "alive", result.Status)
	require.Equal(t, "tool_roundtrip_passed", result.Classification)
	require.Equal(t, 2, result.RequestCount)
	require.Equal(t, 1, result.HandshakeCount)
	require.Equal(t, 1, dialer.count)
	require.Len(t, result.Attempts, 2)
	require.Equal(t, "tool_call_completed", result.Attempts[0].Classification)
	require.Len(t, conn.writes, 2)
	require.Equal(t, int64(15), *result.Usage.TotalTokens)
	require.Equal(t, "capability_ping", gjson.GetBytes(conn.writes[0], "tool_choice.name").String())
	second := conn.writes[1]
	require.Equal(t, "response.create", gjson.GetBytes(second, "type").String())
	require.Equal(t, "fixture-model", gjson.GetBytes(second, "model").String())
	require.Equal(t, "none", gjson.GetBytes(second, "tool_choice").String())
	require.Equal(t, int64(256), gjson.GetBytes(second, "max_output_tokens").Int())
	require.True(t, gjson.GetBytes(second, "stream").Bool())
	require.False(t, gjson.GetBytes(second, "store").Bool())
	require.Equal(t, "opaque-local-only", gjson.GetBytes(second, "input.1.encrypted_content").String())
	require.Equal(t, "preserved", gjson.GetBytes(second, "input.1.x_extension").String())
	require.Equal(t, "null", gjson.GetBytes(second, "input.2.namespace").Raw)
	require.Equal(t, "analysis", gjson.GetBytes(second, "input.2.phase").String())
	require.Equal(t, "function_call_output", gjson.GetBytes(second, "input.3.type").String())
	require.Equal(t, "call_1", gjson.GetBytes(second, "input.3.call_id").String())
	require.JSONEq(t, `{"value":"ok"}`, gjson.GetBytes(second, "input.3.output").String())
	require.False(t, gjson.GetBytes(second, "previous_response_id").Exists())
	require.Len(t, conn.deadlines, 2)
	require.Equal(t, 1, conn.closed)
	require.Zero(t, guard.calls)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "opaque-local-only")
}

func TestAccountCapabilityWebSocketToolMismatchStopsAfterFirstTurn(t *testing.T) {
	for _, arguments := range []string{`{"value":"wrong"}`, `{"value":"ok","extra":true}`} {
		tool := map[string]any{"type": "function_call", "call_id": "call_1", "name": accountCapabilityToolName, "arguments": arguments}
		event, err := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{tool}}})
		require.NoError(t, err)
		conn := &capabilityWSFakeConn{frames: [][]byte{event}}
		dialer := &capabilityWSFakeDialer{conn: conn}
		svc, account, guard := capabilityWSFixture(dialer)
		result := svc.Probe(context.Background(), account, "fixture-model", AccountCapabilityProtocolResponsesWebSocket, AccountCapabilityProfileToolRoundtrip)
		require.Equal(t, "tool_contract_mismatch", result.Classification)
		require.Equal(t, 1, result.RequestCount)
		require.Len(t, conn.writes, 1)
		require.Equal(t, 1, dialer.count)
		require.Zero(t, guard.calls)
	}
}

func TestAccountCapabilityWebSocketHandshakeAndWriteFailuresNeverRetry(t *testing.T) {
	t.Run("structured_auth_handshake", func(t *testing.T) {
		dialer := &capabilityWSFakeDialer{status: 401, err: &openAIWSHandshakeError{Body: []byte(`{"error":{"code":"invalid_api_key","message":"fixture-secret"}}`), Err: errors.New("sensitive handshake details")}}
		svc, account, guard := capabilityWSFixture(dialer)
		result := svc.Probe(context.Background(), account, "fixture-model", AccountCapabilityProtocolResponsesWebSocket, AccountCapabilityProfileText)
		require.Equal(t, "credential_invalid", result.Classification)
		require.True(t, result.AccountFailure)
		require.Equal(t, 401, result.HTTPStatus)
		require.Equal(t, 1, result.HandshakeCount)
		require.Zero(t, result.RequestCount)
		require.Equal(t, 1, dialer.count)
		require.Zero(t, guard.calls)
		require.False(t, account.Schedulable)
	})
	t.Run("uncertain_write", func(t *testing.T) {
		conn := &capabilityWSFakeConn{writeErr: context.DeadlineExceeded}
		dialer := &capabilityWSFakeDialer{conn: conn}
		svc, account, guard := capabilityWSFixture(dialer)
		result := svc.Probe(context.Background(), account, "fixture-model", AccountCapabilityProtocolResponsesWebSocket, AccountCapabilityProfileText)
		require.Equal(t, "uncertain", result.Status)
		require.Equal(t, "timeout", result.Classification)
		require.Equal(t, 1, result.RequestCount)
		require.Equal(t, 1, dialer.count)
		require.Len(t, conn.writes, 1)
		require.Equal(t, 1, conn.closed)
		require.Zero(t, guard.calls)
	})
}

func TestAccountCapabilityWebSocketRealCodecAndRedirectPolicy(t *testing.T) {
	t.Run("local_websocket", func(t *testing.T) {
		requestPayload := make(chan []byte, 1)
		serverError := make(chan error, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := coderws.Accept(w, r, nil)
			if err != nil {
				serverError <- err
				return
			}
			defer func() { _ = conn.CloseNow() }()
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			var request json.RawMessage
			if err = wsjson.Read(ctx, conn, &request); err == nil {
				requestPayload <- request
				err = conn.Write(ctx, coderws.MessageText, []byte(capabilityWSCompletedText))
			}
			serverError <- err
		}))
		defer server.Close()
		svc, account, guard := capabilityWSFixture(nil)
		account.Credentials["base_url"] = server.URL + "/v1"
		result := svc.Probe(context.Background(), account, "fixture-model", AccountCapabilityProtocolResponsesWebSocket, AccountCapabilityProfileText)
		require.Equal(t, "alive", result.Status)
		require.Equal(t, 1, result.HandshakeCount)
		require.Equal(t, 1, result.RequestCount)
		require.NoError(t, <-serverError)
		require.Equal(t, "fixture-model", gjson.GetBytes(<-requestPayload, "model").String())
		require.Zero(t, guard.calls)
	})
	t.Run("redirect_not_followed", func(t *testing.T) {
		var targetCalls atomic.Int64
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			targetCalls.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer target.Close()
		redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL, http.StatusFound)
		}))
		defer redirect.Close()
		svc, account, guard := capabilityWSFixture(nil)
		account.Credentials["base_url"] = redirect.URL + "/v1"
		result := svc.Probe(context.Background(), account, "fixture-model", AccountCapabilityProtocolResponsesWebSocket, AccountCapabilityProfileText)
		require.Equal(t, "redirect_blocked", result.Classification)
		require.Equal(t, http.StatusFound, result.HTTPStatus)
		require.Equal(t, 1, result.HandshakeCount)
		require.Zero(t, result.RequestCount)
		require.Zero(t, targetCalls.Load())
		require.Zero(t, guard.calls)
	})
}

func TestAccountCapabilityWebSocketBoundsEvents(t *testing.T) {
	conn := &capabilityWSFakeConn{frames: capabilityWSFrames(`{"type":"ignored","data":"` + strings.Repeat("x", accountCapabilityBodyLimit) + `"}`)}
	dialer := &capabilityWSFakeDialer{conn: conn}
	svc, account, guard := capabilityWSFixture(dialer)
	result := svc.Probe(context.Background(), account, "fixture-model", AccountCapabilityProtocolResponsesWebSocket, AccountCapabilityProfileText)
	require.Equal(t, "invalid_response", result.Classification)
	require.Equal(t, 1, result.RequestCount)
	require.Equal(t, 1, dialer.count)
	require.Zero(t, guard.calls)
}
