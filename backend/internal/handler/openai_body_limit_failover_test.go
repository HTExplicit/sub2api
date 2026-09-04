package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIBodyLimitFailoverExhausted_ReturnsRedactedJSON413(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))

	(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, bodyLimitFailoverTestError(), false)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	errBody, ok := envelope["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "invalid_request_error", errBody["type"])
	require.Equal(t, "Request payload is too large", errBody["message"])
	require.NotContains(t, rec.Body.String(), "must-not-leak")
}

func TestOpenAIBodyLimitFailoverExhausted_ReturnsRedactedResponsesSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))

	(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, bodyLimitFailoverTestError(), true)

	body := rec.Body.String()
	require.True(t, strings.HasPrefix(body, "event: response.failed\n"))
	require.Contains(t, body, `"code":"invalid_request"`)
	require.Contains(t, body, `"message":"Request payload is too large"`)
	require.NotContains(t, body, "must-not-leak")
}

func TestOpenAIContinuationStateFailoverExhausted_ReturnsTerminalJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))

	(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, continuationStateFailoverTestError(), false)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	errBody, ok := envelope["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "invalid_request_error", errBody["type"])
	require.Equal(t, service.OpenAIContinuationStateUnavailableCode, errBody["code"])
	require.Equal(t, service.OpenAIContinuationStateUnavailableClientMessage, errBody["message"])
	require.NotContains(t, rec.Body.String(), "must-not-leak")
}

func TestOpenAIContinuationStateFailoverExhausted_ReturnsTerminalResponsesSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))

	(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, continuationStateFailoverTestError(), true)

	body := rec.Body.String()
	require.True(t, strings.HasPrefix(body, "event: response.failed\n"))
	require.Contains(t, body, `"code":"continuation_state_unavailable"`)
	require.Contains(t, body, service.OpenAIContinuationStateUnavailableClientMessage)
	require.NotContains(t, body, `"retryable":true`)
	require.NotContains(t, body, "must-not-leak")
}

func TestOpenAIModelNotSupportedFailoverExhausted_ReturnsStructured400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, &service.UpstreamFailoverError{
		StatusCode: http.StatusBadRequest,
		Reason:     service.GatewayFailureReason("upstream_400_model_not_supported"),
	}, false)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	errBody, ok := envelope["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, service.OpenAIModelNotSupportedCode, errBody["type"])
	require.Equal(t, service.OpenAIModelNotSupportedCode, errBody["code"])
	require.Equal(t, service.OpenAIModelNotSupportedClientMessage, errBody["message"])
}

func bodyLimitFailoverTestError() *service.UpstreamFailoverError {
	return &service.UpstreamFailoverError{
		StatusCode:        http.StatusRequestEntityTooLarge,
		ResponseBody:      []byte(`{"error":{"message":"proxy limit secret=must-not-leak"}}`),
		Scope:             service.GatewayFailureScopeAccount,
		Reason:            service.GatewayFailureReason("openai_request_body_too_large"),
		NextAccountAction: service.NextAccountRetry,
		ClientStatusCode:  http.StatusRequestEntityTooLarge,
		ClientMessage:     "Request payload is too large",
	}
}

func continuationStateFailoverTestError() *service.UpstreamFailoverError {
	return service.NewOpenAIContinuationStateUnavailableError(
		http.StatusBadGateway,
		nil,
		[]byte(`{"error":{"message":"must-not-leak"}}`),
	)
}
