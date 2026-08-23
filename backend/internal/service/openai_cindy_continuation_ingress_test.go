package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIGatewayService_ProxyResponsesWebSocketFromClient_CindyPreviousNotFoundReplaysVerifiedFullHistoryOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.IngressPreviousResponseRecoveryEnabled = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 2
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 2
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

	firstConn := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.completed","response":{"id":"resp_cindy_full_1","model":"gpt-5.6-sol","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`),
		[]byte(`{"type":"error","error":{"type":"invalid_request_error","code":"previous_response_not_found","message":"missing anchor"}}`),
	}}
	secondConn := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.completed","response":{"id":"resp_cindy_full_2","model":"gpt-5.6-sol","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`),
	}}
	dialer := &openAIWSQueueDialer{conns: []openAIWSClientConn{firstConn, secondConn}}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(dialer)
	t.Cleanup(pool.Close)

	svc := &OpenAIGatewayService{
		cfg:              cfg,
		httpUpstream:     &httpUpstreamRecorder{},
		cache:            &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
		openaiWSPool:     pool,
	}
	account := cindyHTTPToWSV2TestAccount()
	account.Platform = PlatformCindy
	account.ID = 9411
	account.Extra = map[string]any{
		"openai_passthrough":              true,
		"responses_websockets_v2_enabled": true,
	}

	serverErrCh := make(chan error, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			serverErrCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		recorder := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(recorder)
		ginCtx.Request = r.Clone(r.Context())
		ginCtx.Request.Header.Set("User-Agent", "codex_cli_rs/0.147.0")

		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		msgType, firstMessage, readErr := conn.Read(readCtx)
		cancelRead()
		if readErr != nil {
			serverErrCh <- readErr
			return
		}
		if msgType != coderws.MessageText && msgType != coderws.MessageBinary {
			serverErrCh <- errors.New("unsupported websocket client message type")
			return
		}
		serverErrCh <- svc.ProxyResponsesWebSocketFromClient(
			r.Context(), ginCtx, conn, account, "sk-test", firstMessage, nil,
		)
	}))
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	writeMessage := func(payload string) {
		writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelWrite()
		require.NoError(t, clientConn.Write(writeCtx, coderws.MessageText, []byte(payload)))
	}
	readMessage := func() []byte {
		readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelRead()
		msgType, payload, readErr := clientConn.Read(readCtx)
		require.NoError(t, readErr)
		require.Equal(t, coderws.MessageText, msgType)
		return payload
	}

	writeMessage(`{"type":"response.create","model":"gpt-5.6-sol","stream":false,"store":false,"input":[{"type":"message","id":"msg_foreign","role":"user","phase":"analysis","content":[{"type":"input_text","text":"first"}]},{"type":"function_call","id":"fc_foreign","call_id":"call_foreign","name":"tool","arguments":"{}","phase":"analysis"}]}`)
	require.Equal(t, "resp_cindy_full_1", gjson.GetBytes(readMessage(), "response.id").String())

	writeMessage(`{"type":"response.create","model":"gpt-5.6-sol","stream":false,"store":false,"previous_response_id":"resp_cindy_full_1","input":[{"type":"function_call_output","id":"out_foreign","call_id":"call_foreign","output":"ok","phase":"final"}]}`)
	require.Equal(t, "resp_cindy_full_2", gjson.GetBytes(readMessage(), "response.id").String())
	require.NoError(t, clientConn.Close(coderws.StatusNormalClosure, "done"))

	select {
	case serverErr := <-serverErrCh:
		require.NoError(t, serverErr)
	case <-time.After(5 * time.Second):
		t.Fatal("waiting for Cindy continuation replay result timed out")
	}

	require.Equal(t, 2, dialer.DialCount(), "verified full history may reconnect exactly once")
	firstConn.mu.Lock()
	firstWrites := append([]map[string]any(nil), firstConn.writes...)
	firstConn.mu.Unlock()
	require.Len(t, firstWrites, 2)
	secondConn.mu.Lock()
	secondWrites := append([]map[string]any(nil), secondConn.writes...)
	secondConn.mu.Unlock()
	require.Len(t, secondWrites, 1)

	replayed := requestToJSONString(secondWrites[0])
	require.False(t, gjson.Get(replayed, "previous_response_id").Exists())
	require.False(t, gjson.Get(replayed, "store").Bool())
	require.Len(t, gjson.Get(replayed, "input").Array(), 3)
	require.Equal(t, "msg_foreign", gjson.Get(replayed, "input.0.id").String())
	require.Equal(t, "analysis", gjson.Get(replayed, "input.0.phase").String())
	require.Equal(t, "fc_foreign", gjson.Get(replayed, "input.1.id").String())
	require.Equal(t, "call_foreign", gjson.Get(replayed, "input.1.call_id").String())
	require.Equal(t, "analysis", gjson.Get(replayed, "input.1.phase").String())
	require.Equal(t, "out_foreign", gjson.Get(replayed, "input.2.id").String())
	require.Equal(t, "call_foreign", gjson.Get(replayed, "input.2.call_id").String())
	require.Equal(t, "final", gjson.Get(replayed, "input.2.phase").String())
}

func TestLegacyCindyRuntimeCompatibilityWSIngressInitialAnchorNeverLeavesBusyBoundConn(t *testing.T) {
	for _, storeField := range []struct {
		name  string
		field string
	}{
		{name: "omitted"},
		{name: "true", field: `,"store":true`},
		{name: "false", field: `,"store":false`},
	} {
		t.Run(storeField.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			cfg := newCindyStrictConnAffinityTestConfig()
			cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
			cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
			pool := newOpenAIWSConnPool(cfg)
			t.Cleanup(pool.Close)
			svc := &OpenAIGatewayService{
				cfg:              cfg,
				cache:            &stubGatewayCache{},
				openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
				toolCorrector:    NewCodexToolCorrector(),
				openaiWSPool:     pool,
			}
			stateStore := NewOpenAIWSStateStore(svc.cache)
			svc.openaiWSStateStore = stateStore
			account := cindyHTTPToWSV2TestAccount()
			account.Platform = PlatformOpenAI
			account.ID = 9420
			account.Extra = map[string]any{
				"responses_websockets_v2_enabled":            true,
				"openai_apikey_responses_websockets_v2_mode": OpenAIWSIngressModePassthrough,
			}

			boundCapture := &openAIWSCaptureConn{}
			otherCapture := &openAIWSCaptureConn{events: [][]byte{
				[]byte(`{"type":"error","error":{"type":"invalid_request_error","code":"previous_response_not_found","message":"wrong connection"}}`),
			}}
			passthroughDialer := &openAIWSCaptureDialer{conn: otherCapture}
			svc.openaiWSPassthroughDialer = passthroughDialer
			serverErrCh := make(chan error, 1)
			wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, acceptErr := coderws.Accept(w, r, nil)
				if acceptErr != nil {
					serverErrCh <- acceptErr
					return
				}
				defer func() { _ = conn.CloseNow() }()
				ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ginCtx.Request = r.Clone(r.Context())
				readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
				_, firstMessage, readErr := conn.Read(readCtx)
				cancelRead()
				if readErr != nil {
					serverErrCh <- readErr
					return
				}

				decision := svc.getOpenAIWSProtocolResolver().Resolve(account)
				headers, _, headerErr := svc.buildOpenAIWSHeaders(r.Context(), ginCtx, account, "sk-test", decision, false, "", "", "", "", "")
				if headerErr != nil {
					serverErrCh <- headerErr
					return
				}
				bound := newOpenAIWSConn("cindy_bound_busy", account.ID, boundCapture, nil)
				other := newOpenAIWSConn("cindy_other_idle", account.ID, otherCapture, nil)
				compatibility := normalizeOpenAIWSHandshakeCompatibility(account, headers)
				bound.handshakeCompatibility = compatibility
				other.handshakeCompatibility = compatibility
				accountPool := pool.getOrCreateAccountPool(account.ID)
				accountPool.mu.Lock()
				accountPool.conns[bound.id] = bound
				accountPool.conns[other.id] = other
				accountPool.mu.Unlock()
				require.True(t, bound.tryAcquire())
				bound.waiters.Store(1)
				defer func() {
					bound.waiters.Store(0)
					bound.release()
				}()
				stateStore.BindResponseConn("resp_bound_busy", bound.id, time.Minute)
				serverErrCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "sk-test", firstMessage, nil)
			}))
			defer wsServer.Close()

			clientConn := dialCindyContinuationTestClient(t, wsServer.URL)
			defer func() { _ = clientConn.CloseNow() }()
			payload := `{"type":"response.create","model":"gpt-5.6-sol","stream":false,"previous_response_id":"resp_bound_busy","input":"next"` + storeField.field + `}`
			writeCindyContinuationTestMessage(t, clientConn, payload)
			select {
			case serverErr := <-serverErrCh:
				require.Error(t, serverErr)
			case <-time.After(5 * time.Second):
				t.Fatal("waiting for strict Cindy busy-binding failure timed out")
			}
			otherCapture.mu.Lock()
			otherWrites := len(otherCapture.writes)
			otherCapture.mu.Unlock()
			require.Zero(t, otherWrites, "a busy bound Cindy anchor must never drift to another idle connection")
			require.Zero(t, passthroughDialer.DialCount(), "strict Cindy passthrough mode must remain on the contract-aware pool")
		})
	}
}

func TestOpenAIGatewayService_ProxyResponsesWebSocketFromClient_CindyModeRouterPassthroughLaterAnchorRejectsDifferentConn(t *testing.T) {
	for _, storeField := range []struct {
		name  string
		field string
	}{
		{name: "omitted"},
		{name: "true", field: `,"store":true`},
		{name: "false", field: `,"store":false`},
	} {
		t.Run(storeField.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			cfg := newCindyStrictConnAffinityTestConfig()
			cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
			cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
			capture := &openAIWSCaptureConn{events: [][]byte{
				[]byte(`{"type":"response.completed","response":{"id":"resp_cindy_conn_1","model":"gpt-5.6-sol","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`),
				[]byte(`{"type":"error","error":{"type":"invalid_request_error","code":"previous_response_not_found","message":"wrong connection"}}`),
			}}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(&openAIWSQueueDialer{conns: []openAIWSClientConn{capture}})
			t.Cleanup(pool.Close)
			passthroughDialer := &openAIWSCaptureDialer{conn: capture}
			svc := &OpenAIGatewayService{
				cfg:                       cfg,
				cache:                     &stubGatewayCache{},
				openaiWSResolver:          NewOpenAIWSProtocolResolver(cfg),
				toolCorrector:             NewCodexToolCorrector(),
				openaiWSPool:              pool,
				openaiWSPassthroughDialer: passthroughDialer,
			}
			stateStore := NewOpenAIWSStateStore(svc.cache)
			svc.openaiWSStateStore = stateStore
			account := cindyHTTPToWSV2TestAccount()
			account.Platform = PlatformCindy
			account.ID = 9421
			account.Extra = map[string]any{
				"responses_websockets_v2_enabled":            true,
				"openai_apikey_responses_websockets_v2_mode": OpenAIWSIngressModePassthrough,
			}

			serverErrCh := make(chan error, 1)
			wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, acceptErr := coderws.Accept(w, r, nil)
				if acceptErr != nil {
					serverErrCh <- acceptErr
					return
				}
				defer func() { _ = conn.CloseNow() }()
				ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ginCtx.Request = r.Clone(r.Context())
				readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
				_, firstMessage, readErr := conn.Read(readCtx)
				cancelRead()
				if readErr != nil {
					serverErrCh <- readErr
					return
				}
				serverErrCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "sk-test", firstMessage, nil)
			}))
			defer wsServer.Close()

			clientConn := dialCindyContinuationTestClient(t, wsServer.URL)
			defer func() { _ = clientConn.CloseNow() }()
			writeCindyContinuationTestMessage(t, clientConn, `{"type":"response.create","model":"gpt-5.6-sol","stream":false,"input":"first"}`)
			firstResponse := readCindyContinuationTestMessage(t, clientConn)
			require.Equal(t, "resp_cindy_conn_1", gjson.GetBytes(firstResponse, "response.id").String())

			stateStore.BindResponseConn("resp_cross_conn", "different_live_conn", time.Minute)
			secondPayload := `{"type":"response.create","model":"gpt-5.6-sol","stream":false,"previous_response_id":"resp_cross_conn","input":"second"` + storeField.field + `}`
			writeCindyContinuationTestMessage(t, clientConn, secondPayload)
			select {
			case serverErr := <-serverErrCh:
				var continuationErr *UpstreamFailoverError
				require.ErrorAs(t, serverErr, &continuationErr)
				require.True(t, continuationErr.IsOpenAIContinuationStateUnavailable())
			case <-time.After(5 * time.Second):
				t.Fatal("waiting for strict Cindy cross-connection rejection timed out")
			}
			capture.mu.Lock()
			writes := len(capture.writes)
			capture.mu.Unlock()
			require.Equal(t, 1, writes, "a cross-connection Cindy anchor must fail before the second upstream write")
			require.Zero(t, passthroughDialer.DialCount(), "strict Cindy passthrough mode must remain on the contract-aware pool")
		})
	}
}

func TestOpenAIGatewayService_ProxyResponsesWebSocketFromClient_CindyModeRouterPassthroughRejectsInvalidAnchorOnLaterTurn(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "number", value: `123`},
		{name: "boolean", value: `true`},
		{name: "object", value: `{"id":"resp_123"}`},
		{name: "array", value: `["resp_123"]`},
		{name: "unknown string", value: `"other_123"`},
		{name: "message string", value: `"msg_123"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientConn, capture, passthroughDialer, serverErrCh := newCindyContinuationAnchorValidationHarness(t, [][]byte{
				[]byte(`{"type":"response.completed","response":{"id":"resp_anchor_validation_1","model":"gpt-5.6-sol","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`),
			})
			writeCindyContinuationTestMessage(t, clientConn, `{"type":"response.create","model":"gpt-5.6-sol","stream":false,"input":"first"}`)
			firstResponse := readCindyContinuationTestMessage(t, clientConn)
			require.Equal(t, "resp_anchor_validation_1", gjson.GetBytes(firstResponse, "response.id").String())

			writeCindyContinuationTestMessage(t, clientConn, `{"type":"response.create","model":"gpt-5.6-sol","stream":false,"previous_response_id":`+tt.value+`,"input":"second"}`)
			select {
			case serverErr := <-serverErrCh:
				var closeErr *OpenAIWSClientCloseError
				require.ErrorAs(t, serverErr, &closeErr)
				require.Equal(t, coderws.StatusPolicyViolation, closeErr.StatusCode())
				require.Equal(t, OpenAIContinuationAnchorValidationMessage, closeErr.Reason())
			case <-time.After(5 * time.Second):
				t.Fatal("waiting for invalid continuation anchor rejection timed out")
			}
			capture.mu.Lock()
			writes := len(capture.writes)
			capture.mu.Unlock()
			require.Equal(t, 1, writes, "invalid later-turn anchor must fail before a second upstream write")
			require.Zero(t, passthroughDialer.DialCount(), "strict Cindy passthrough mode must remain on the contract-aware pool")
		})
	}
}

func TestOpenAIGatewayService_ProxyResponsesWebSocketFromClient_CindyModeRouterPassthroughNullAndBlankLaterAnchorAreNoAnchor(t *testing.T) {
	for _, value := range []string{`null`, `""`, `"  "`} {
		t.Run(value, func(t *testing.T) {
			clientConn, capture, passthroughDialer, _ := newCindyContinuationAnchorValidationHarness(t, [][]byte{
				[]byte(`{"type":"response.completed","response":{"id":"resp_anchor_blank_1","model":"gpt-5.6-sol","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`),
				[]byte(`{"type":"response.completed","response":{"id":"resp_anchor_blank_2","model":"gpt-5.6-sol","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`),
			})
			writeCindyContinuationTestMessage(t, clientConn, `{"type":"response.create","model":"gpt-5.6-sol","stream":false,"input":"first"}`)
			firstResponse := readCindyContinuationTestMessage(t, clientConn)
			require.Equal(t, "resp_anchor_blank_1", gjson.GetBytes(firstResponse, "response.id").String())

			writeCindyContinuationTestMessage(t, clientConn, `{"type":"response.create","model":"gpt-5.6-sol","stream":false,"previous_response_id":`+value+`,"input":"second"}`)
			secondResponse := readCindyContinuationTestMessage(t, clientConn)
			require.Equal(t, "resp_anchor_blank_2", gjson.GetBytes(secondResponse, "response.id").String())
			capture.mu.Lock()
			writes := len(capture.writes)
			capture.mu.Unlock()
			require.Equal(t, 2, writes)
			require.Zero(t, passthroughDialer.DialCount(), "strict Cindy passthrough mode must remain on the contract-aware pool")
		})
	}
}

func TestOpenAIGatewayService_ProxyResponsesWebSocketFromClient_CindyModeRouterPassthroughBindsOpaqueOutputForReconnect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := newCindyStrictConnAffinityTestConfig()
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool

	poolCapture := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.completed","response":{"id":"resp_mode_router_opaque_1","model":"gpt-5.6-sol","status":"completed","output":[{"type":"function_call","id":"fc_mode_router_opaque","call_id":"call_mode_router_opaque","name":"tool","arguments":"{}","encrypted_content":"mode-router-stable-cipher","phase":"analysis"}],"usage":{"input_tokens":1,"output_tokens":1}}}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_mode_router_opaque_2","model":"gpt-5.6-sol","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`),
	}}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(&openAIWSQueueDialer{conns: []openAIWSClientConn{poolCapture}})
	t.Cleanup(pool.Close)
	passthroughCapture := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.completed","response":{"id":"resp_unbound_passthrough","model":"gpt-5.6-sol","status":"completed","output":[{"type":"function_call","id":"fc_mode_router_opaque","call_id":"call_mode_router_opaque","name":"tool","arguments":"{}","encrypted_content":"mode-router-stable-cipher","phase":"analysis"}],"usage":{"input_tokens":1,"output_tokens":1}}}`),
	}}
	passthroughDialer := &openAIWSCaptureDialer{conn: passthroughCapture}
	stateStore := NewOpenAIWSStateStore(nil)
	svc := &OpenAIGatewayService{
		cfg:                       cfg,
		cache:                     &stubGatewayCache{},
		openaiWSResolver:          NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:             NewCodexToolCorrector(),
		openaiWSPool:              pool,
		openaiWSPassthroughDialer: passthroughDialer,
		openaiWSStateStore:        stateStore,
	}
	account := cindyHTTPToWSV2TestAccount()
	account.Platform = PlatformCindy
	account.ID = 9423
	account.Extra = map[string]any{
		"responses_websockets_v2_enabled":            true,
		"openai_apikey_responses_websockets_v2_mode": OpenAIWSIngressModePassthrough,
	}

	serverErrCh := make(chan error, 2)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, acceptErr := coderws.Accept(w, r, nil)
		if acceptErr != nil {
			serverErrCh <- acceptErr
			return
		}
		defer func() { _ = conn.CloseNow() }()
		ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ginCtx.Request = r.Clone(r.Context())
		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		_, firstMessage, readErr := conn.Read(readCtx)
		cancelRead()
		if readErr != nil {
			serverErrCh <- readErr
			return
		}
		serverErrCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "sk-test", firstMessage, nil)
	}))
	defer wsServer.Close()

	runSession := func(payload, wantResponseID string) {
		clientConn := dialCindyContinuationTestClient(t, wsServer.URL)
		writeCindyContinuationTestMessage(t, clientConn, payload)
		response := readCindyContinuationTestMessage(t, clientConn)
		require.Equal(t, wantResponseID, gjson.GetBytes(response, "response.id").String())
		require.NoError(t, clientConn.Close(coderws.StatusNormalClosure, "done"))
		select {
		case serverErr := <-serverErrCh:
			require.NoError(t, serverErr)
		case <-time.After(5 * time.Second):
			t.Fatal("waiting for strict Cindy opaque continuation session timed out")
		}
	}

	runSession(
		`{"type":"response.create","model":"gpt-5.6-sol","stream":false,"input":"establish opaque state"}`,
		"resp_mode_router_opaque_1",
	)
	secondPayload := `{"type":"response.create","model":"gpt-5.6-sol","stream":false,"store":false,"input":[{"type":"function_call","id":"fc_mode_router_opaque","call_id":"call_mode_router_opaque","name":"tool","arguments":"{}","encrypted_content":"mode-router-stable-cipher","phase":"analysis"},{"type":"function_call_output","id":"out_mode_router_opaque","call_id":"call_mode_router_opaque","output":"ok","phase":"final"}]}`
	classification, classifyErr := ClassifyCindyContinuation([]byte(secondPayload), CindyContinuationProof{})
	require.NoError(t, classifyErr)
	require.Equal(t, CindyContinuationOpaqueFull, classification.Mode)
	require.NotEmpty(t, classification.OpaqueBindingIDs)
	lookup := svc.LookupCindyOpaqueContinuationBinding(context.Background(), 0, classification.OpaqueBindingIDs)
	require.Equal(t, OpenAIContinuationBindingHit, lookup.State, "the completed opaque output must establish authoritative reconnect affinity")
	require.Equal(t, account.ID, lookup.AccountID)

	runSession(secondPayload, "resp_mode_router_opaque_2")
	require.Zero(t, passthroughDialer.DialCount(), "strict Cindy passthrough mode must remain on the contract-aware pool")
	poolCapture.mu.Lock()
	writes := append([]map[string]any(nil), poolCapture.writes...)
	poolCapture.mu.Unlock()
	require.Len(t, writes, 2)
	require.Equal(t, "mode-router-stable-cipher", gjson.Get(requestToJSONString(writes[1]), "input.0.encrypted_content").String())
}

func TestOpenAIGatewayService_ProxyResponsesWebSocketFromClient_CindyInitialAnchorAfterRestartOpensNewConnection(t *testing.T) {
	clientConn, capture, passthroughDialer, serverErrCh := newCindyContinuationAnchorValidationHarness(t, [][]byte{
		[]byte(`{"type":"response.completed","response":{"id":"resp_after_restart","model":"gpt-5.6-sol","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`),
	})

	writeCindyContinuationTestMessage(t, clientConn,
		`{"type":"response.create","model":"gpt-5.6-sol","stream":false,"previous_response_id":"resp_before_restart","input":"continue"}`)
	response := readCindyContinuationTestMessage(t, clientConn)
	require.Equal(t, "resp_after_restart", gjson.GetBytes(response, "response.id").String())
	require.NoError(t, clientConn.Close(coderws.StatusNormalClosure, "done"))

	select {
	case serverErr := <-serverErrCh:
		require.NoError(t, serverErr)
	case <-time.After(5 * time.Second):
		t.Fatal("waiting for restarted Cindy continuation timed out")
	}
	require.Zero(t, passthroughDialer.DialCount(), "strict Cindy mode must stay on the contract-aware pool")
	capture.mu.Lock()
	writes := append([]map[string]any(nil), capture.writes...)
	capture.mu.Unlock()
	require.Len(t, writes, 1)
	require.Equal(t, "resp_before_restart", openAIWSPayloadString(writes[0], "previous_response_id"))
}

func newCindyContinuationAnchorValidationHarness(
	t *testing.T,
	events [][]byte,
) (*coderws.Conn, *openAIWSCaptureConn, *openAIWSCaptureDialer, <-chan error) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := newCindyStrictConnAffinityTestConfig()
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
	capture := &openAIWSCaptureConn{events: events}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(&openAIWSQueueDialer{conns: []openAIWSClientConn{capture}})
	t.Cleanup(pool.Close)
	passthroughDialer := &openAIWSCaptureDialer{conn: capture}
	svc := &OpenAIGatewayService{
		cfg:                       cfg,
		cache:                     &stubGatewayCache{},
		openaiWSResolver:          NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:             NewCodexToolCorrector(),
		openaiWSPool:              pool,
		openaiWSPassthroughDialer: passthroughDialer,
	}
	account := cindyHTTPToWSV2TestAccount()
	account.Platform = PlatformCindy
	account.ID = 9422
	account.Extra = map[string]any{
		"responses_websockets_v2_enabled":            true,
		"openai_apikey_responses_websockets_v2_mode": OpenAIWSIngressModePassthrough,
	}

	serverErrCh := make(chan error, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, acceptErr := coderws.Accept(w, r, nil)
		if acceptErr != nil {
			serverErrCh <- acceptErr
			return
		}
		defer func() { _ = conn.CloseNow() }()
		ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ginCtx.Request = r.Clone(r.Context())
		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		_, firstMessage, readErr := conn.Read(readCtx)
		cancelRead()
		if readErr != nil {
			serverErrCh <- readErr
			return
		}
		serverErrCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "sk-test", firstMessage, nil)
	}))
	t.Cleanup(wsServer.Close)
	clientConn := dialCindyContinuationTestClient(t, wsServer.URL)
	t.Cleanup(func() { _ = clientConn.CloseNow() })
	return clientConn, capture, passthroughDialer, serverErrCh
}

func newCindyStrictConnAffinityTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 2
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 2
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 1
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	return cfg
}

func dialCindyContinuationTestClient(t *testing.T, serverURL string) *coderws.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(serverURL, "http"), nil)
	require.NoError(t, err)
	return conn
}

func writeCindyContinuationTestMessage(t *testing.T, conn *coderws.Conn, payload string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(payload)))
}

func readCindyContinuationTestMessage(t *testing.T, conn *coderws.Conn) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	messageType, payload, err := conn.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, coderws.MessageText, messageType)
	return payload
}
