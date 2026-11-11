package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIWSProductPromptAndObservationSeams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := newOpenAIWSV2TestConfig()
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 1
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 1
	prompts := &SystemPromptService{}
	prompts.publish(SystemPromptConfig{Enabled: true, DefaultPromptID: "site", Prompts: []SystemPrompt{
		{ID: "site", Name: "site", Body: "site instructions", Position: SystemPromptPositionAppend, Role: SystemPromptRoleAuto},
	}})
	upstream := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.completed","response":{"id":"resp_observation_prompt","status":"completed","instructions":"client instructions\n\nsite instructions","usage":{"input_tokens":1,"output_tokens":1}}}`),
	}}
	pool := newOpenAIWSConnPool(cfg)
	handshake := http.Header{}
	handshake.Set(openAIWSTurnStateHeader, "observed turn state")
	pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: upstream, handshake: handshake})
	store := NewOpenAIWSStateStore(nil)
	svc := &OpenAIGatewayService{cfg: cfg, openaiWSPool: pool, openaiWSStateStore: store, systemPrompts: prompts, toolCorrector: NewCodexToolCorrector()}
	t.Cleanup(svc.CloseOpenAIWSPool)
	account := &Account{ID: 991, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-key"}}
	ctx := WithAccountObservation(context.Background())
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	c.Request.Header.Set("session-id", "observation-session")
	body := map[string]any{"model": "gpt-5.5", "input": "hello", "instructions": "client instructions", "stream": false, "store": false}
	result, err := svc.forwardOpenAIWSV2(ctx, c, account, body, "", "", "test-key", OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}, false, false, "gpt-5.5", "gpt-5.5", time.Now(), 1, "", nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "client instructions\n\nsite instructions", upstream.lastWrite["instructions"])
	require.Equal(t, "client instructions", body["instructions"], "the caller's clean retry body must not retain the injected prompt")
	require.Equal(t, "client instructions", gjson.Get(recorder.Body.String(), "instructions").String())
	_, responseBound := store.GetResponseConn("resp_observation_prompt")
	require.False(t, responseBound)
	sessionHash := svc.GenerateSessionHash(c, nil)
	_, sessionBound := store.GetSessionConn(0, sessionHash)
	require.False(t, sessionBound)
	_, turnBound := store.GetSessionTurnState(0, sessionHash)
	require.False(t, turnBound)
}
