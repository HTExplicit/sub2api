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
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type routingLeaseHTTPFixture struct {
	calls   atomic.Int32
	request *http.Request
	body    []byte
}

func (*routingLeaseHTTPFixture) Do(*http.Request, string, int64, int) (*http.Response, error) {
	return nil, errors.New("unexpected unqualified transport")
}
func (*routingLeaseHTTPFixture) DoWithTLS(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
	return nil, errors.New("unexpected alternate TLS transport")
}
func (upstream *routingLeaseHTTPFixture) DoWithCodexConnectionLease(request *http.Request, _ string, account int64, _ string, lease string, _ time.Time, profile *tlsfingerprint.Profile) (*http.Response, string, error) {
	if account != 7 || lease != "verified-connection" || profile != nil || request.Header.Get("Cookie") != "__cflb=route" {
		return nil, "", errCodexRoutingUnavailable
	}
	upstream.calls.Add(1)
	upstream.request = request.Clone(request.Context())
	upstream.body, _ = io.ReadAll(request.Body)
	return &http.Response{StatusCode: 200, Proto: "HTTP/1.1", Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_verified\",\"status\":\"completed\",\"model\":\"gpt-6-astra\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"))}, lease, nil
}

func TestCodexRoutingWebSocketIngressUsesVerifiedHTTPConnection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1, Credentials: map[string]any{"chatgpt_account_id": "principal"}, Extra: map[string]any{"openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModePassthrough}}
	upstream := &routingLeaseHTTPFixture{}
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	var qualification *extensionv1.CodexRoutingQualification
	manager := ticketTestManager(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true, Models: []string{"gpt-6-astra"}}, func(in extensionv1.Invocation) (extensionv1.Result, error) {
		if in.Operation == "codex.routing.observe" || in.Operation == "codex.routing.demand" {
			return extensionv1.Result{Payload: json.RawMessage(`{}`)}, nil
		}
		raw, _ := json.Marshal(extensionv1.CodexRoutingInjection{Headers: map[string]string{}, Qualification: qualification})
		return extensionv1.Result{Payload: raw}, nil
	})
	installation := manager.extensions.Load().installations[1]
	installation.State = PluginStateEnabled
	store := &routingMemoryStore{PluginRepository: &pluginTokenRepository{installation: installation}, values: map[string]extensionv1.StateResult{}}
	manager.repo = store
	service := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, accountRepo: &routingAccountRepositoryFixture{account: account}, pluginManager: manager, cache: &stubGatewayCache{}, toolCorrector: NewCodexToolCorrector(), openaiWSResolver: NewOpenAIWSProtocolResolver(cfg)}
	scope, err := service.PrepareCodexRoutingScope(context.Background(), 7, "http")
	require.NoError(t, err)
	scope.ConnectionLeaseID = "verified-connection"
	scope.RouteEvidence = "connection"
	now := time.Now().UTC()
	expiry := now.Add(time.Minute)
	cookie := codexRoutingCookie{Name: "__cflb", Value: "route", Domain: "chatgpt.com", Path: "/", FirstSeen: now, ExpiresAt: expiry}
	clockKey := "clock." + codexRoutingScopeKey(scope)
	clock := codexRoutingCookieClock{Scope: scope}
	clock.apply([]codexRoutingCookieChange{{Cookie: cookie}}, now)
	raw, _ := json.Marshal(clock)
	_, err = store.CompareSwapExtensionState(context.Background(), codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: clockKey, Value: raw})
	require.NoError(t, err)
	bundle := codexRoutingPrivateBundle{Schema: 2, Scope: scope, Cookies: []codexRoutingCookie{cookie}, ClockKey: clockKey, ClockRevision: 1, Status: "qualified", Model: "gpt-6-astra", ExpiresAt: expiry}
	raw, _ = json.Marshal(bundle)
	_, err = store.CompareSwapExtensionState(context.Background(), codexRuntimePluginKey, extensionv1.StateRequest{Namespace: codexRoutingPrivateNamespace, Key: "bundle.fixture", Value: raw})
	require.NoError(t, err)
	qualification = &extensionv1.CodexRoutingQualification{Scope: scope, Model: "gpt-6-astra", VerifiedAt: now, ExpiresAt: expiry, Bundle: extensionv1.CodexRoutingBundleRef{Key: "bundle.fixture", Revision: 1, ExpiresAt: expiry, ConnectionLeaseID: scope.ConnectionLeaseID}}
	errorsChannel := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			errorsChannel <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		_, message, err := conn.Read(ctx)
		if err != nil {
			errorsChannel <- err
			return
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = r
		errorsChannel <- service.ProxyResponsesWebSocketFromClient(ctx, c, conn, account, "synthetic-token", message, nil)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	client, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()
	require.NoError(t, client.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-6-astra","input":"hello","client_metadata":{"turn_id":"turn-one"}}`)))
	_, message, err := client.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, "response.completed", gjson.GetBytes(message, "type").String())
	require.Equal(t, "gpt-6-astra", gjson.GetBytes(message, "response.model").String())
	_ = client.Close(websocket.StatusNormalClosure, "done")
	select {
	case err := <-errorsChannel:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("bridge did not finish")
	}
	require.EqualValues(t, 1, upstream.calls.Load())
	require.Equal(t, "ws", upstream.request.Context().Value(codexRoutingIngressKey{}))
	require.Empty(t, upstream.request.Header.Get(openAICodexTurnStateHeader))
}

type routingNativeConnFixture struct{ *stagedPassthroughConn }

func (connection *routingNativeConnFixture) WriteJSON(ctx context.Context, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return connection.WriteFrame(ctx, websocket.MessageText, raw)
}

func TestCodexRoutingNativeWebSocketCannotSwitchIntoQualifiedModel(t *testing.T) {
	for _, mode := range []string{OpenAIWSIngressModePassthrough, OpenAIWSIngressModeCtxPool} {
		t.Run(mode, func(t *testing.T) {
			cfg := passthroughLifecycleConfig()
			cfg.Gateway.OpenAIWS.OAuthEnabled = true
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
			upstream := &routingNativeConnFixture{newStagedPassthroughConn()}
			service := newPassthroughLifecycleService(cfg, upstream.stagedPassthroughConn)
			service.openaiWSPassthroughDialer = &stagedPassthroughDialer{conn: upstream}
			service.pluginManager = ticketTestManager(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true, Models: []string{"gpt-6-astra"}}, func(extensionv1.Invocation) (extensionv1.Result, error) {
				return extensionv1.Result{Payload: json.RawMessage(`{"headers":{}}`)}, nil
			})
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(&stagedPassthroughDialer{conn: upstream})
			service.openaiWSPool = pool
			t.Cleanup(pool.Close)
			account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1, Credentials: map[string]any{"chatgpt_account_id": "owner"}, Extra: map[string]any{"openai_oauth_responses_websockets_v2_mode": mode}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			server, serverErrors := startPassthroughLifecycleServer(t, ctx, service, account)
			defer server.Close()
			client, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			require.NoError(t, err)
			defer func() { _ = client.CloseNow() }()
			require.NoError(t, client.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-5.5","input":"first"}`)))
			select {
			case first := <-upstream.writes:
				require.Equal(t, "gpt-5.5", gjson.GetBytes(first, "model").String())
			case <-ctx.Done():
				t.Fatal("first allowed model was not forwarded")
			}
			upstream.Send(`{"type":"response.completed","response":{"id":"resp_first","status":"completed","model":"gpt-5.5","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`)
			_, message, err := client.Read(ctx)
			require.NoError(t, err)
			require.Equal(t, "response.completed", gjson.GetBytes(message, "type").String())
			require.NoError(t, client.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-6-astra","input":"second"}`)))
			_, _, err = client.Read(ctx)
			require.Error(t, err)
			require.Equal(t, websocket.StatusPolicyViolation, websocket.CloseStatus(err))
			select {
			case proxyErr := <-serverErrors:
				var closed *OpenAIWSClientCloseError
				require.ErrorAs(t, proxyErr, &closed)
			case <-ctx.Done():
				t.Fatal("switch was not rejected")
			}
			select {
			case <-upstream.writes:
				t.Fatal("gated second frame reached a native upstream connection")
			default:
			}
		})
	}
}
