package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIRefusalRecoveryFailoverExhaustionReturnsRetryable503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, service.NewOpenAICyberFailoverError(nil, nil), false)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Equal(t, "1", recorder.Header().Get("Retry-After"))
	require.JSONEq(t, `{"error":{"type":"server_error","message":"Temporary upstream failure"}}`, recorder.Body.String())
	require.NotContains(t, recorder.Body.String(), "cyber")
}

func TestOpenAIRefusalRecoveryFailoverExhaustionWritesServerErrorSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, service.NewOpenAICyberFailoverError(nil, nil), true)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "event: response.failed")
	require.Contains(t, recorder.Body.String(), `"code":"server_error"`)
	require.NotContains(t, recorder.Body.String(), "cyber")
}

func TestOpenAIRefusalRecoveryCyberAttemptClearsPerAttemptState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	h := &OpenAIGatewayHandler{}
	failoverErr := service.NewOpenAICyberFailoverError(nil, nil)

	for _, inputTokens := range []int{7, 11} {
		service.MarkOpsCyberPolicy(c, service.CyberPolicyMark{Code: "cyber_policy", UpstreamInTok: inputTokens})
		h.recordOpenAIResponsesCyberAttempt(c, nil, nil, nil, "gpt-5.4", fmt.Errorf("wrapped: %w", failoverErr), "must-not-be-used", service.ChannelUsageFields{}, "")

		require.Nil(t, service.GetOpsCyberPolicy(c))
		require.False(t, c.GetBool(cyberPolicyRecordedKey))
	}
}

func TestPrepareOpenAIRefusalPromptRetryIsBoundedAndPreservesRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	body := []byte(`{"model":"gpt-5.6-sol","stream":true,"input":[{"type":"message","role":"user","content":"task"}]}`)
	failoverErr := service.NewOpenAICyberFailoverError(nil, nil)

	repaired, changed, err := prepareOpenAIRefusalPromptRetry(c, body, failoverErr)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(repaired, "model").String())
	require.True(t, gjson.GetBytes(repaired, "stream").Bool())
	require.Equal(t, "developer", gjson.GetBytes(repaired, "input.0.role").String())
	require.Equal(t, "user", gjson.GetBytes(repaired, "input.1.role").String())

	again, changedAgain, err := prepareOpenAIRefusalPromptRetry(c, repaired, failoverErr)
	require.NoError(t, err)
	require.False(t, changedAgain)
	require.Equal(t, repaired, again)
}

func TestPrepareOpenAIRefusalPromptRetryIgnoresOrdinaryFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := []byte(`{"model":"gpt-5.6-sol","input":"task"}`)

	repaired, changed, err := prepareOpenAIRefusalPromptRetry(c, body, nil)

	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, repaired)
}
