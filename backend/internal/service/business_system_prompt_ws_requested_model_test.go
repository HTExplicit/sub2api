package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPromptWSNativeContinuationRetainsRequestedModelAlias(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := newOpenAIWSExecutionScopeTestConfig()
	snapshot := unifiedPromptSnapshot(t, PlatformOpenAI, []string{"auto"}, []string{"control_append"}, []string{"alias-scoped-prompt"})
	snapshot.RulePolicy.Rules[0].ModelMatch = "requested"
	snapshot.RulePolicy.Rules[0].Models = []string{"client-model"}
	snapshot.ResolvedRules[0].Rule = snapshot.RulePolicy.Rules[0]
	policy := NewBusinessSystemPromptService(nil, nil)
	policy.snapshot.Store(&snapshot)

	captureConn := &openAIWSCaptureConn{
		writeSignals: make(chan struct{}, 2),
		events: [][]byte{
			[]byte(`{"type":"response.completed","response":{"id":"resp_alias_1","model":"gpt-5.1","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`),
			[]byte(`{"type":"response.completed","response":{"id":"resp_alias_2","model":"gpt-5.1","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`),
		},
	}
	captureDialer := &openAIWSCaptureDialer{conn: captureConn}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(captureDialer)
	defer pool.Close()
	gateway := &OpenAIGatewayService{
		cfg:                   cfg,
		httpUpstream:          &httpUpstreamRecorder{},
		cache:                 &stubGatewayCache{},
		openaiWSResolver:      NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:         NewCodexToolCorrector(),
		openaiWSPool:          pool,
		businessPromptService: policy,
	}
	account := &Account{
		ID: 116, Name: "prompt-requested-model", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{
			"api_key":       "sk-test",
			"model_mapping": map[string]any{"client-model": "gpt-5.1"},
		},
		Extra: map[string]any{"responses_websockets_v2_enabled": true},
	}
	serverErrCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			serverErrCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()
		ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ginCtx.Request = r.Clone(r.Context())
		ginCtx.Request.Header.Set("User-Agent", "unit-test-agent/1.0")
		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		_, firstMessage, err := conn.Read(readCtx)
		cancelRead()
		if err != nil {
			serverErrCh <- err
			return
		}
		serverErrCh <- gateway.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "sk-test", firstMessage, nil)
	}))
	defer server.Close()
	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	client, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()

	frames := []string{
		`{"type":"response.create","model":"client-model","instructions":"client-first","input":[{"role":"user","content":"first"}]}`,
		`{"type":"response.create","previous_response_id":"resp_alias_1","instructions":"client-second","input":[{"role":"user","content":"second"}]}`,
	}
	for _, frame := range frames {
		writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
		err = client.Write(writeCtx, coderws.MessageText, []byte(frame))
		cancelWrite()
		require.NoError(t, err)
		readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
		_, event, readErr := client.Read(readCtx)
		cancelRead()
		require.NoError(t, readErr)
		require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
	}
	require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
	select {
	case serverErr := <-serverErrCh:
		require.NoError(t, serverErr)
	case <-time.After(5 * time.Second):
		t.Fatal("native websocket ingress did not finish")
	}

	require.Len(t, captureConn.writes, 2)
	for turn, request := range captureConn.writes {
		wire := requestToJSONString(request)
		require.Equal(t, "gpt-5.1", gjson.Get(wire, "model").String())
		require.Contains(t, gjson.Get(wire, "instructions").String(), gjson.Get(frames[turn], "instructions").String())
		require.Equal(t, 1, strings.Count(wire, "alias-scoped-prompt"), "turn %d must match the requested alias exactly once", turn+1)
	}
	require.Equal(t, "resp_alias_1", gjson.Get(requestToJSONString(captureConn.writes[1]), "previous_response_id").String())
	require.Equal(t, 1, captureDialer.DialCount(), "the continuation must use the native websocket connection")
}
