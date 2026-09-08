package service

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const openAICodexResponsesTestURL = "https://chatgpt.com/backend-api/codex/responses"

// newOpenAIAstraProRetryContext returns a gin context plus its recorder so the
// caller can assert whether the service prematurely committed a client error.
func newOpenAIAstraProRetryContext(body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, recorder
}

// TestOpenAIGatewayService_OAuthAstraProModeRejectionStopsWithoutDowngrade verifies that
// when the Codex OAuth upstream rejects an Astra pro+max request with a
// deterministic HTTP 400 on error.param=reasoning.mode, the gateway returns a
// request-scoped rejection for the handler to render. It makes exactly one
// upstream call, keeps reasoning.mode=pro and reasoning.effort=max in the sent
// body, and does not replay without mode or expose the upstream message.
func TestOpenAIGatewayService_OAuthAstraProModeRejectionStopsWithoutDowngrade(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","reasoning":{"mode":"pro","effort":"max"},"input":"hello"}`)
	upstream := &httpUpstreamRecorder{resp: newOpenAIRejectedFieldTestResponse(
		http.StatusBadRequest,
		`{"error":{"type":"invalid_request_error","code":"model_specific_rejection","message":"reasoning.mode is not supported for this model","param":"reasoning.mode"}}`,
	)}
	svc := newOpenAIRejectedFieldTestService(upstream)
	c, recorder := newOpenAIAstraProRetryContext(body)

	_, err := svc.Forward(context.Background(), c, newOpenAIOAuthNamespaceTestAccount(), body)
	var rejected *UpstreamFailoverError
	require.ErrorAs(t, err, &rejected)
	require.True(t, rejected.IsOpenAIRequestRejected())
	require.False(t, rejected.ShouldRetryNextAccount())
	require.False(t, c.Writer.Written())
	require.Empty(t, recorder.Body.String())
	require.Len(t, upstream.bodies, 1, "deterministic 400 must not trigger a retry")
	require.Len(t, upstream.requests, 1)
	require.Equal(t, openAICodexResponsesTestURL, upstream.requests[0].URL.String())

	sent := upstream.bodies[0]
	require.Equal(t, "gpt-6-astra", gjson.GetBytes(sent, "model").String())
	require.Equal(t, "pro", gjson.GetBytes(sent, "reasoning.mode").String())
	require.Equal(t, "max", gjson.GetBytes(sent, "reasoning.effort").String())

	require.Equal(t, "invalid_request_error", rejected.ClientErrorType)
	require.Equal(t, OpenAIRequestRejectedCode, rejected.ClientErrorCode)
	require.Equal(t, OpenAIRequestRejectedClientMessage, rejected.ClientMessage)
	value, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := value.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "request_validation", events[0].ContinuationDiagnostic.Classification)
	require.Equal(t, "reasoning.mode", events[0].ContinuationDiagnostic.UpstreamError.ErrorParam.Value)
}

// TestOpenAIGatewayService_OAuthAstraProModeKeptAcrossRejectedFieldRetry reuses
// the existing OAuth rejected-field retry fixture (input[0].status is rejected,
// stripped, then replayed to success) to confirm Astra pro+max reasoning is kept
// across every in-service retry: each captured Codex URL request body still
// carries reasoning.mode=pro and reasoning.effort=max with model=gpt-6-astra,
// and only the second attempt drops the rejected status field.
func TestOpenAIGatewayService_OAuthAstraProModeKeptAcrossRejectedFieldRetry(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","stream":true,"instructions":"test","reasoning":{"mode":"pro","effort":"max"},"input":[{"type":"message","role":"user","status":"completed","content":"hello"}]}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, `{"error":{"code":"unknown_parameter","message":"Unknown parameter: 'input[0].status'.","param":"input[0].status"}}`),
		newOpenAIRejectedFieldTestResponse(http.StatusOK, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ok\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\ndata: [DONE]\n\n"),
	}}
	upstream.responses[1].Header.Set("Content-Type", "text/event-stream")
	svc := newOpenAIRejectedFieldTestService(upstream)
	c, _ := newOpenAIAstraProRetryContext(body)

	result, err := svc.Forward(context.Background(), c, newOpenAIOAuthNamespaceTestAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2)
	require.Len(t, upstream.requests, 2)
	for i := range upstream.bodies {
		require.Equal(t, openAICodexResponsesTestURL, upstream.requests[i].URL.String(), "attempt %d URL", i)
		sent := upstream.bodies[i]
		require.Equal(t, "gpt-6-astra", gjson.GetBytes(sent, "model").String(), "attempt %d model", i)
		require.Equal(t, "pro", gjson.GetBytes(sent, "reasoning.mode").String(), "attempt %d reasoning.mode", i)
		require.Equal(t, "max", gjson.GetBytes(sent, "reasoning.effort").String(), "attempt %d reasoning.effort", i)
	}
	require.Equal(t, "completed", gjson.GetBytes(upstream.bodies[0], "input.0.status").String())
	require.False(t, gjson.GetBytes(upstream.bodies[1], "input.0.status").Exists())
}
