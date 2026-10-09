package service

import (
	"context"
	"errors"
	"fmt"
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
)

type borrowWSDialer struct {
	mu      sync.Mutex
	conns   []openAIWSClientConn
	headers []http.Header
	proxies []string
}

func (d *borrowWSDialer) Dial(_ context.Context, _ string, headers http.Header, proxyURL string) (openAIWSClientConn, int, http.Header, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.headers = append(d.headers, cloneHeader(headers))
	d.proxies = append(d.proxies, proxyURL)
	if len(d.conns) == 0 {
		return nil, 0, nil, errors.New("unexpected additional websocket dial")
	}
	conn := d.conns[0]
	d.conns = d.conns[1:]
	return conn, http.StatusSwitchingProtocols, http.Header{}, nil
}

func borrowWSContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "borrow-ws-test")
	c.Request.Header.Set("Session-Id", "borrow-client-session")
	groupID := int64(91)
	c.Set("api_key", &APIKey{ID: 92, GroupID: &groupID})
	return c
}

func borrowWSFixture(t *testing.T, maxConns int) (*OpenAIGatewayService, *Account, *CodexGatewayBorrowService, *httpUpstreamRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := newOpenAIWSV2TestConfig()
	cfg.Gateway.ForceCodexCLI = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = maxConns
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = maxConns
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	cfg.Security.URLAllowlist.Enabled = false
	pool := newOpenAIWSConnPool(cfg)
	t.Cleanup(pool.Close)
	svc := &OpenAIGatewayService{cfg: cfg, cache: &stubGatewayCache{}, openaiWSPool: pool,
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector()}
	proxyID := int64(93)
	account := &Account{ID: 94, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Schedulable: true, Concurrency: maxConns, ProxyID: &proxyID,
		Proxy:       &Proxy{ID: proxyID, Protocol: "http", Host: "target-proxy.invalid", Port: 8080},
		Credentials: map[string]any{"access_token": "target-token", "chatgpt_account_id": "target-chatgpt"},
		Extra:       map[string]any{"openai_oauth_responses_websockets_v2_enabled": true}}
	probe := &httpUpstreamRecorder{err: errors.New("unexpected borrow observation")}
	borrow := &CodexGatewayBorrowService{gateway: svc, upstream: probe, loaded: true, revision: 1, revisionCtx: context.Background(),
		config: CodexGatewayBorrowConfig{Enabled: true, SourceAccountIDs: []int64{95}, TargetAccountIDs: []int64{account.ID}, Models: []string{"gpt-6-astra", "gpt-6.1-sol"}},
		candidate: &codexGatewayBorrowCandidate{cookie: http.Cookie{Name: "__oailb", Value: "shared-native-route", Path: "/backend-api/codex"},
			expires: time.Now().Add(codexGatewayBorrowTTL), sourceID: 95},
		targets: make(map[codexGatewayBorrowTargetKey]codexGatewayBorrowTargetCheck)}
	svc.gatewayBorrow = borrow
	return svc, account, borrow, probe
}

func seedBorrowWSTarget(t *testing.T, svc *OpenAIGatewayService, borrow *CodexGatewayBorrowService, account *Account, c *gin.Context, model string) {
	t.Helper()
	headers, _, err := svc.buildOpenAIWSHeaders(context.Background(), c, account, "target-token",
		svc.getOpenAIWSProtocolResolver().Resolve(account), true, "", "", "", model, "")
	require.NoError(t, err)
	wsURL, err := svc.buildOpenAIResponsesWSURL(account)
	require.NoError(t, err)
	req, err := codexGatewayBorrowWSRequest(context.Background(), wsURL, headers)
	require.NoError(t, err)
	candidate := borrow.candidate
	key := borrowTargetFingerprint(req, account, model, account.Proxy.URL(), nil, candidate.cookie.Value)
	borrow.targets[codexGatewayBorrowTargetKey{account.ID, model}] = codexGatewayBorrowTargetCheck{policyRevision: currentCodexFingerprintPolicyForAccount(account).revision, key: key,
		cookieKey: borrowHash(candidate.cookie.Value), expires: candidate.expires, result: CodexGatewayBorrowVerification{Success: true}}
}

func borrowWSCompleted(model, id string) []byte {
	// Tool-only completions are authoritative native Responses completions.
	return []byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"model":%q,"status":"completed","output":[{"type":"function_call","id":"fc_test","call_id":"call_test","name":"tool","arguments":"{}"}],"usage":{"input_tokens":1,"output_tokens":1}}}`, id, model))
}

func runBorrowWSV2(t *testing.T, svc *OpenAIGatewayService, account *Account, model, previousID string) (*OpenAIForwardResult, error) {
	t.Helper()
	body := map[string]any{"model": model, "input": "hello", "stream": false, "store": false}
	if previousID != "" {
		body["previous_response_id"] = previousID
	}
	recoveryTried := false
	return svc.forwardOpenAIWSV2(context.Background(), borrowWSContext(), account, body, "", "", "target-token",
		svc.getOpenAIWSProtocolResolver().Resolve(account), true, false, model, model, time.Now(), 1, "", &recoveryTried)
}

func TestCodexGatewayBorrowWS_TwoModelsContinueOriginalConnectionsAfterCookieExpiryAndDisable(t *testing.T) {
	svc, account, borrow, probe := borrowWSFixture(t, 2)
	astraConn := &openAIWSCaptureConn{events: [][]byte{borrowWSCompleted("gpt-6-astra", "resp_astra_1"), borrowWSCompleted("gpt-6-astra", "resp_astra_2")}}
	solConn := &openAIWSCaptureConn{events: [][]byte{borrowWSCompleted("gpt-6.1-sol", "resp_sol_1"), borrowWSCompleted("gpt-6.1-sol", "resp_sol_2")}}
	dialer := &borrowWSDialer{conns: []openAIWSClientConn{astraConn, solConn}}
	svc.openaiWSPool.setClientDialerForTest(dialer)
	seedBorrowWSTarget(t, svc, borrow, account, borrowWSContext(), "gpt-6-astra")
	seedBorrowWSTarget(t, svc, borrow, account, borrowWSContext(), "gpt-6.1-sol")
	first, err := runBorrowWSV2(t, svc, account, "gpt-6-astra", "")
	require.NoError(t, err)
	require.Equal(t, "resp_astra_1", first.ResponseID)
	second, err := runBorrowWSV2(t, svc, account, "gpt-6.1-sol", "")
	require.NoError(t, err)
	require.Equal(t, "resp_sol_1", second.ResponseID)
	require.Len(t, dialer.headers, 2)
	for i, headers := range dialer.headers {
		require.Equal(t, "Bearer target-token", headers.Get("Authorization"))
		require.Equal(t, "__oailb=shared-native-route", headers.Get("Cookie"))
		require.Equal(t, account.Proxy.URL(), dialer.proxies[i])
	}
	borrow.wsAnchors.mu.Lock()
	expires := make(map[string]time.Time)
	connections := make(map[string]string)
	for key, entry := range borrow.wsAnchors.entries {
		expires[key.model] = entry.expires
		connections[key.model] = entry.connID
	}
	borrow.wsAnchors.mu.Unlock()
	require.Len(t, expires, 2)
	require.NotEqual(t, connections["gpt-6-astra"], connections["gpt-6.1-sol"])
	borrow.mu.Lock()
	borrow.candidate.expires = time.Now().Add(-time.Second)
	borrow.config.Enabled = false
	borrow.targets = nil
	borrow.mu.Unlock()
	_, err = runBorrowWSV2(t, svc, account, "gpt-6.1-sol", "resp_astra_1")
	require.Error(t, err, "disabling borrowing must not let a different model take over an existing response")
	continued, err := runBorrowWSV2(t, svc, account, "gpt-6-astra", "resp_astra_1")
	require.NoError(t, err)
	require.Equal(t, "resp_astra_2", continued.ResponseID)
	continued, err = runBorrowWSV2(t, svc, account, "gpt-6.1-sol", "resp_sol_1")
	require.NoError(t, err)
	require.Equal(t, "resp_sol_2", continued.ResponseID)
	require.Len(t, dialer.headers, 2, "continuations must not redial")
	require.Empty(t, probe.requests, "cached roots and every continuation must avoid source/target observations")
	require.False(t, astraConn.closed)
	require.False(t, solConn.closed)
	borrow.wsAnchors.mu.Lock()
	defer borrow.wsAnchors.mu.Unlock()
	for key, entry := range borrow.wsAnchors.entries {
		require.Equal(t, expires[key.model], entry.expires, "anchor deadlines never slide")
	}
}

func TestCodexGatewayBorrowWS_NewRootAtCapacityPreservesIdleAnchor(t *testing.T) {
	svc, account, borrow, _ := borrowWSFixture(t, 1)
	conn := &openAIWSCaptureConn{events: [][]byte{borrowWSCompleted("gpt-6-astra", "resp_kept")}}
	dialer := &borrowWSDialer{conns: []openAIWSClientConn{conn}}
	svc.openaiWSPool.setClientDialerForTest(dialer)
	seedBorrowWSTarget(t, svc, borrow, account, borrowWSContext(), "gpt-6-astra")
	_, err := runBorrowWSV2(t, svc, account, "gpt-6-astra", "")
	require.NoError(t, err)
	_, err = runBorrowWSV2(t, svc, account, "gpt-6-astra", "")
	require.ErrorIs(t, err, errOpenAIWSConnQueueFull)
	require.Len(t, dialer.headers, 1)
	require.False(t, conn.closed, "ForceNewConn must not evict a valid idle anchor")
	borrow.wsAnchors.mu.Lock()
	defer borrow.wsAnchors.mu.Unlock()
	require.Len(t, borrow.wsAnchors.entries, 1)
	for _, entry := range borrow.wsAnchors.entries {
		require.Equal(t, "resp_kept", entry.responseID)
	}
}

func TestCodexGatewayBorrowWS_PoolCleanupAndIncompatibleEvictionRespectAnchorUntil(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 0
	pool := newOpenAIWSConnPool(cfg)
	t.Cleanup(pool.Close)
	ap := pool.getOrCreateAccountPool(201)
	conn := newOpenAIWSConn("borrow_anchor", 201, &openAIWSFakeConn{}, nil)
	conn.createdAtNano.Store(time.Now().Add(-2 * time.Hour).UnixNano())
	conn.lastUsedNano.Store(time.Now().Add(-20 * time.Minute).UnixNano())
	conn.anchorUntilNano.Store(time.Now().Add(time.Hour).UnixNano())
	conn.handshakeCompatibility.anchorScope = "borrow-model-session"
	ap.conns[conn.id] = conn
	require.Empty(t, pool.cleanupAccountLocked(ap, time.Now(), 1))
	require.Nil(t, pool.pickOldestIdleConnLocked(ap))
	require.Nil(t, pool.pickOldestIdleConnWithoutHandshakeCompatibilityLocked(ap, openAIWSHandshakeCompatibilityKey{}))
	require.False(t, conn.isClosed())
	conn.anchorUntilNano.Store(time.Now().Add(-time.Second).UnixNano())
	require.Same(t, conn, pool.pickOldestIdleConnLocked(ap), "expired anchors return to ordinary pool retirement")
}

func TestCodexGatewayBorrowWS_ModelIdentityTTLAndCapacityAreStrict(t *testing.T) {
	store := &codexGatewayBorrowWSAnchorStore{}
	now := time.Now()
	key := codexGatewayBorrowWSAnchorKey{account: 1, apiKey: 2, group: 3, scope: "client", model: "gpt-6-astra"}
	turn, err := store.begin(key, "", now)
	require.NoError(t, err)
	require.Equal(t, now.Add(time.Hour), turn.expires)
	turn.connID, turn.qualified = "original", true
	turn.finish(nil, &OpenAIForwardResult{ResponseID: "resp_one", UpstreamResponseModel: key.model}, nil)
	otherModel := key
	otherModel.model = "gpt-6.1-sol"
	_, err = store.begin(otherModel, "resp_one", now.Add(time.Second))
	require.Error(t, err)
	_, err = store.begin(key, "resp_one", now.Add(time.Hour))
	require.Error(t, err)
	require.Empty(t, store.entries)
	for i := 0; i < codexGatewayBorrowWSAnchorLimit; i++ {
		entryKey := key
		entryKey.scope = fmt.Sprint(i)
		store.entries[entryKey] = codexGatewayBorrowWSAnchorEntry{responseID: "r", connID: "c", expires: now.Add(time.Hour)}
	}
	key.scope = "over-limit"
	_, err = store.begin(key, "", now)
	require.EqualError(t, err, "codex gateway borrow websocket anchor capacity reached")
	require.Len(t, store.entries, 1024)
}

func TestCodexGatewayBorrowWS_DirectClosedEvidenceRejectsReconnectAfterDisable(t *testing.T) {
	svc, account, borrow, probe := borrowWSFixture(t, 1)
	c := borrowWSContext()
	seedBorrowWSTarget(t, svc, borrow, account, c, "gpt-6.1-sol")
	headers, _, err := svc.buildOpenAIWSHeaders(context.Background(), c, account, "target-token",
		svc.getOpenAIWSProtocolResolver().Resolve(account), true, "", "", "", "gpt-6.1-sol", "")
	require.NoError(t, err)
	wsURL, err := svc.buildOpenAIResponsesWSURL(account)
	require.NoError(t, err)
	turn, err := svc.prepareCodexGatewayBorrowWSTurn(context.Background(), c, account, wsURL, headers, nil,
		"gpt-6.1-sol", "", account.Proxy.URL())
	require.NoError(t, err)
	session := &codexGatewayBorrowWSDirectSession{turn: turn}
	session.completed(&OpenAIForwardResult{RequestID: "resp_direct", UpstreamResponseModel: "gpt-6.1-sol", UpstreamTerminalEvent: "response.completed"})
	borrow.config.Enabled = false
	borrow.candidate.expires = time.Now().Add(-time.Second)
	require.NoError(t, session.check(c, account, headers, []byte(`{"previous_response_id":"resp_direct"}`), "gpt-6.1-sol", account.Proxy.URL()))
	require.Error(t, session.check(c, account, headers, []byte(`{"previous_response_id":"resp_direct"}`), "gpt-6-astra", account.Proxy.URL()))
	session.close(svc.openaiWSPool)
	_, err = svc.prepareCodexGatewayBorrowWSTurn(context.Background(), c, account, wsURL, headers, nil,
		"gpt-6.1-sol", "resp_direct", account.Proxy.URL())
	require.Error(t, err)
	require.Empty(t, probe.requests)
	require.Len(t, borrow.wsAnchors.entries, 1)
	for _, entry := range borrow.wsAnchors.entries {
		require.Empty(t, entry.connID)
		require.Equal(t, turn.expires, entry.expires)
	}
}

func TestCodexGatewayBorrowWS_NativeIngressDoesNotPrepareAgainOnLaterFrame(t *testing.T) {
	svc, account, borrow, probe := borrowWSFixture(t, 1)
	conn := &openAIWSCaptureConn{events: [][]byte{borrowWSCompleted("gpt-6.1-sol", "resp_ingress_1"), borrowWSCompleted("gpt-6.1-sol", "resp_ingress_2")}}
	dialer := &borrowWSDialer{conns: []openAIWSClientConn{conn}}
	svc.openaiWSPool.setClientDialerForTest(dialer)
	seedBorrowWSTarget(t, svc, borrow, account, borrowWSContext(), "gpt-6.1-sol")
	turns := make(chan struct{}, 2)
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		client, err := coderws.Accept(w, req, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer func() { _ = client.CloseNow() }()
		_, first, err := client.Read(context.Background())
		if err != nil {
			serverErr <- err
			return
		}
		c := borrowWSContext()
		c.Request = req
		serverErr <- svc.ProxyResponsesWebSocketFromClient(context.Background(), c, client, account, "target-token", first,
			&OpenAIWSIngressHooks{AfterTurn: func(_ int, _ *OpenAIForwardResult, _ error) { turns <- struct{}{} }})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &coderws.DialOptions{HTTPHeader: borrowWSContext().Request.Header})
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-6.1-sol","input":"hello","store":false}`)))
	_, _, err = client.Read(ctx)
	require.NoError(t, err)
	select {
	case <-turns:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	borrow.mu.Lock()
	borrow.config.Enabled = false
	borrow.candidate.expires = time.Now().Add(-time.Second)
	borrow.mu.Unlock()
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-6.1-sol","previous_response_id":"resp_ingress_1","input":"next","store":false}`)))
	_, _, err = client.Read(ctx)
	require.NoError(t, err)
	select {
	case <-turns:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_ = client.Close(coderws.StatusNormalClosure, "")
	select {
	case err := <-serverErr:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.Len(t, dialer.headers, 1)
	require.Empty(t, probe.requests)
	require.Len(t, conn.writes, 2)
	require.Equal(t, "resp_ingress_1", conn.writes[1]["previous_response_id"])
}
