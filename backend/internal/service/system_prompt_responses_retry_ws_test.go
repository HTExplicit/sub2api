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

func TestSystemPromptResponsesRetryWSIngressUsesWireIndicesAndCleanReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := newOpenAIWSV2TestConfig()
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	prompts := &SystemPromptService{}
	prompts.publish(SystemPromptConfig{Enabled: true, DefaultPromptID: "site", Prompts: []SystemPrompt{
		{ID: "site", Name: "site", Body: "site instructions", Position: SystemPromptPositionPrepend, Role: SystemPromptRoleDeveloper},
	}})
	upstream := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"error","status":400,"error":{"code":"unknown_parameter","param":"input[2].status","message":"Unknown parameter: 'input[2].status'."}}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_prompt_retry_1","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_prompt_retry_2","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`),
	}}
	dialer := &openAIWSCaptureDialer{conn: upstream}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(dialer)
	svc := &OpenAIGatewayService{
		cfg:              cfg,
		httpUpstream:     &httpUpstreamRecorder{},
		cache:            &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
		openaiWSPool:     pool,
		systemPrompts:    prompts,
	}
	t.Cleanup(svc.CloseOpenAIWSPool)
	account := &Account{
		ID:          9903,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       map[string]any{"responses_websockets_v2_enabled": true},
	}
	serverErrCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			serverErrCh <- err
			return
		}
		defer conn.CloseNow()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = r.Clone(r.Context())
		c.Request.Header = r.Header.Clone()
		c.Request.Header.Set("User-Agent", "unit-test-agent/1.0")
		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		_, firstMessage, err := conn.Read(readCtx)
		cancelRead()
		if err != nil {
			serverErrCh <- err
			return
		}
		serverErrCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), c, conn, account, "sk-test", firstMessage, nil)
	}))
	defer server.Close()
	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	client, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer client.CloseNow()
	writeAndRead := func(payload string) []byte {
		t.Helper()
		writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
		err := client.Write(writeCtx, coderws.MessageText, []byte(payload))
		cancelWrite()
		require.NoError(t, err)
		readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
		messageType, event, err := client.Read(readCtx)
		cancelRead()
		require.NoError(t, err)
		require.Equal(t, coderws.MessageText, messageType)
		require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String(), "the repairable rejection must stay upstream")
		return event
	}
	first := writeAndRead(`{"type":"response.create","model":"gpt-5.1","stream":false,"store":false,"input":[{"role":"user","content":"first","status":"keep"},{"type":"message","role":"user","content":"second","status":"completed"}]}`)
	require.Equal(t, "resp_prompt_retry_1", gjson.GetBytes(first, "response.id").String())
	second := writeAndRead(`{"type":"response.create","model":"gpt-5.1","stream":false,"store":false,"previous_response_id":"resp_stale_external","input":[{"role":"user","content":"third"}]}`)
	require.Equal(t, "resp_prompt_retry_2", gjson.GetBytes(second, "response.id").String())
	require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
	select {
	case err := <-serverErrCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for prompt retry ingress session to close")
	}

	require.Equal(t, 1, dialer.DialCount(), "the official field repair keeps the existing upstream lease")
	upstream.mu.Lock()
	writes := append([]map[string]any(nil), upstream.writes...)
	upstream.mu.Unlock()
	require.Len(t, writes, 3, "one field repair plus the next client turn")
	for i, write := range writes {
		wire := requestToJSONString(write)
		developerCount := 0
		for _, item := range gjson.Get(wire, "input").Array() {
			if item.Get("role").String() == "developer" {
				developerCount++
				require.Equal(t, "site instructions", item.Get("content.0.text").String())
			}
		}
		require.Equal(t, 1, developerCount, "wire send %d must contain one final injection", i+1)
	}
	initialWire := requestToJSONString(writes[0])
	retryWire := requestToJSONString(writes[1])
	require.Equal(t, "completed", gjson.Get(initialWire, "input.2.status").String())
	require.False(t, gjson.Get(retryWire, "input.2.status").Exists(), "repair the shifted item named by the upstream")
	require.Equal(t, "keep", gjson.Get(retryWire, "input.1.status").String(), "an item of a different type keeps its original status")
	require.Equal(t, "first", gjson.Get(retryWire, "input.1.content").String())
	require.Equal(t, "second", gjson.Get(retryWire, "input.2.content").String())
	fullReplay := requestToJSONString(writes[2])
	require.False(t, gjson.Get(fullReplay, "previous_response_id").Exists())
	require.Len(t, gjson.Get(fullReplay, "input").Array(), 4, "official full replay contains clean prior input, new input and one site prompt")
	require.Equal(t, "first", gjson.Get(fullReplay, "input.1.content").String())
	require.Equal(t, "second", gjson.Get(fullReplay, "input.2.content").String())
	require.Equal(t, "third", gjson.Get(fullReplay, "input.3.content").String())
}
