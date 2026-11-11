package service

import (
	"context"
	"errors"
	"fmt"
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
	runSystemPromptResponsesRetryWSIngress(t, true, "")
}

func TestSystemPromptResponsesRetryWSIngressKeepsRepairAcrossReconnect(t *testing.T) {
	for _, promptEnabled := range []bool{false, true} {
		for _, disconnect := range []string{"read", "write"} {
			t.Run(fmt.Sprintf("prompt_%t/%s", promptEnabled, disconnect), func(t *testing.T) {
				runSystemPromptResponsesRetryWSIngress(t, promptEnabled, disconnect)
			})
		}
	}
}

type systemPromptResponsesRetryWriteFailConn struct {
	openAIWSClientConn
	writes int
}

func (c *systemPromptResponsesRetryWriteFailConn) WriteJSON(ctx context.Context, value any) error {
	if err := c.openAIWSClientConn.WriteJSON(ctx, value); err != nil {
		return err
	}
	c.writes++
	if c.writes == 2 {
		return errors.New("connection lost while writing repaired request")
	}
	return nil
}

type systemPromptResponsesRetryReconnectConn struct {
	openAIWSClientConn
	capture    *openAIWSCaptureConn
	statusPath string
	rejection  []byte
	checked    bool
}

func (c *systemPromptResponsesRetryReconnectConn) ReadMessage(ctx context.Context) ([]byte, error) {
	if !c.checked {
		c.checked = true
		c.capture.mu.Lock()
		hasRejectedStatus := gjson.Get(requestToJSONString(c.capture.lastWrite), c.statusPath).Exists()
		c.capture.mu.Unlock()
		if hasRejectedStatus {
			return c.rejection, nil
		}
	}
	return c.openAIWSClientConn.ReadMessage(ctx)
}

func runSystemPromptResponsesRetryWSIngress(t *testing.T, promptEnabled bool, disconnect string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := newOpenAIWSV2TestConfig()
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	prompts := &SystemPromptService{}
	prompts.publish(SystemPromptConfig{Enabled: promptEnabled, DefaultPromptID: "site", Prompts: []SystemPrompt{
		{ID: "site", Name: "site", Body: "site instructions", Position: SystemPromptPositionPrepend, Role: SystemPromptRoleDeveloper},
	}})
	promptOffset := 0
	if promptEnabled {
		promptOffset = 1
	}
	statusPath := fmt.Sprintf("input.%d.status", promptOffset+1)
	rejection := []byte(fmt.Sprintf(`{"type":"error","status":400,"error":{"code":"unknown_parameter","param":"input[%d].status","message":"Unknown parameter: 'input[%d].status'."}}`, promptOffset+1, promptOffset+1))
	completedEvents := [][]byte{
		[]byte(`{"type":"response.completed","response":{"id":"resp_prompt_retry_1","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_prompt_retry_2","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`),
	}
	upstream := &openAIWSCaptureConn{events: append([][]byte{rejection}, completedEvents...)}
	var firstConn openAIWSClientConn = upstream
	var reconnected *openAIWSCaptureConn
	conns := []openAIWSClientConn{firstConn}
	if disconnect != "" {
		upstream.events = [][]byte{rejection}
		if disconnect == "write" {
			firstConn = &systemPromptResponsesRetryWriteFailConn{openAIWSClientConn: upstream}
		}
		reconnected = &openAIWSCaptureConn{events: completedEvents}
		conns = []openAIWSClientConn{firstConn, &systemPromptResponsesRetryReconnectConn{
			openAIWSClientConn: reconnected, capture: reconnected, statusPath: statusPath, rejection: rejection,
		}}
	}
	dialer := &openAIWSQueueDialer{conns: conns}
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
		defer func() { _ = conn.CloseNow() }()
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
	defer func() { _ = client.CloseNow() }()
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

	expectedDials := 1
	if disconnect != "" {
		expectedDials = 2
	}
	require.Equal(t, expectedDials, dialer.DialCount(), "only the official network retry reconnects the repaired turn")
	upstream.mu.Lock()
	writes := append([]map[string]any(nil), upstream.writes...)
	upstream.mu.Unlock()
	if reconnected != nil {
		reconnected.mu.Lock()
		writes = append(writes, reconnected.writes...)
		reconnected.mu.Unlock()
	}
	require.Len(t, writes, expectedDials+2, "one field repair, any official reconnect, and the next client turn")
	for i, write := range writes {
		wire := requestToJSONString(write)
		developerCount := 0
		for _, item := range gjson.Get(wire, "input").Array() {
			if item.Get("role").String() == "developer" {
				developerCount++
				require.Equal(t, "site instructions", item.Get("content.0.text").String())
			}
		}
		require.Equal(t, promptOffset, developerCount, "wire send %d must contain only its configured final injection", i+1)
	}
	initialWire := requestToJSONString(writes[0])
	retryWire := requestToJSONString(writes[1])
	require.Equal(t, "completed", gjson.Get(initialWire, statusPath).String())
	require.False(t, gjson.Get(retryWire, statusPath).Exists(), "repair the shifted item named by the upstream")
	require.Equal(t, "keep", gjson.Get(retryWire, fmt.Sprintf("input.%d.status", promptOffset)).String(), "an item of a different type keeps its original status")
	require.Equal(t, "first", gjson.Get(retryWire, fmt.Sprintf("input.%d.content", promptOffset)).String())
	require.Equal(t, "second", gjson.Get(retryWire, fmt.Sprintf("input.%d.content", promptOffset+1)).String())
	if reconnected != nil {
		require.JSONEq(t, retryWire, requestToJSONString(writes[2]), "official reconnection resends the already repaired wire body")
	}
	fullReplay := requestToJSONString(writes[len(writes)-1])
	require.False(t, gjson.Get(fullReplay, "previous_response_id").Exists())
	require.Len(t, gjson.Get(fullReplay, "input").Array(), promptOffset+3, "official full replay contains clean prior input, new input and the configured prompt")
	require.Equal(t, "first", gjson.Get(fullReplay, fmt.Sprintf("input.%d.content", promptOffset)).String())
	require.Equal(t, "second", gjson.Get(fullReplay, fmt.Sprintf("input.%d.content", promptOffset+1)).String())
	require.Equal(t, "third", gjson.Get(fullReplay, fmt.Sprintf("input.%d.content", promptOffset+2)).String())
	require.Equal(t, "completed", gjson.Get(fullReplay, statusPath).String(), "the next turn uses clean client history rather than the previous turn's repaired wire body")
}
