//go:build unit

package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIGatewayHandlerResponses_RequestRejectedStopsAndReportsSemantic400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []struct {
		name        string
		passthrough bool
	}{{"native", false}, {"passthrough", true}} {
		for _, format := range []struct {
			name   string
			stream bool
		}{{"json", false}, {"sse", true}} {
			for _, rejection := range []struct {
				name           string
				body           string
				classification string
			}{
				{"missing_namespace", `{"error":{"type":"invalid_request_error","code":"missing_required_parameter","param":"input[1].namespace","message":"Missing required parameter input[1].namespace; must-not-leak"}}`, "request_validation"},
				{"generic_400", `{"error":{"message":"must-not-leak"}}`, "unclassified_bad_request"},
			} {
				t.Run(path.name+"/"+format.name+"/"+rejection.name, func(t *testing.T) {
					upstream := &openAIResponsesGenericBadRequestUpstream{responseBody: rejection.body}
					accounts := []service.Account{
						{
							ID: 1, Name: "request-validation-1", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
							Status: service.StatusActive, Schedulable: true,
							Credentials: map[string]any{
								"api_key": "fixture", "base_url": "https://api.example.test", "pool_mode": true,
								"pool_mode_retry_status_codes": []any{float64(http.StatusBadRequest)},
							},
							Extra: map[string]any{"use_responses_api": true, "openai_passthrough": path.passthrough},
						},
						{
							ID: 2, Name: "request-validation-2", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
							Status: service.StatusActive, Schedulable: true, Priority: 1,
							Credentials: map[string]any{"api_key": "fixture-2", "base_url": "https://api.example.test"},
							Extra:       map[string]any{"use_responses_api": true, "openai_passthrough": path.passthrough},
						},
					}
					handler := newOpenAIResponsesFailoverTestHandler(t, upstream, accounts...)
					stream := "false"
					if format.stream {
						stream = "true"
					}
					body := []byte(`{"model":"gpt-5.1","stream":` + stream + `,"input":[{"type":"reasoning","encrypted_content":"private-cipher"},{"type":"function_call","id":"fc_test","call_id":"call_test","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_test","output":"private-output"}]}`)
					c, rec := newOpenAIResponsesFailoverTestContextWithBody(t, nil, body)

					handler.Responses(c)

					require.Equal(t, []int64{1}, upstream.calls(), "a rejected request must override pool-mode 400 retries and never select account 2")
					require.NotContains(t, rec.Body.String(), "must-not-leak")
					require.NotContains(t, rec.Body.String(), service.OpenAIContinuationStateUnavailableCode)
					require.NotContains(t, rec.Body.String(), `"retryable":true`)
					if format.stream {
						require.Equal(t, http.StatusOK, rec.Code)
						require.Contains(t, rec.Header().Get("Content-Type"), "text/event-stream")
						require.True(t, strings.HasPrefix(rec.Body.String(), "event: response.failed\n"), "no JSON prefix may precede the SSE terminal")
						require.Equal(t, 1, strings.Count(rec.Body.String(), "event: response.failed\n"))
						require.NotContains(t, rec.Body.String(), "response.completed")
						_, errorObject := parseResponsesFailedSSE(t, rec.Body.String())
						require.Equal(t, service.OpenAIRequestRejectedCode, errorObject["code"])
						require.Equal(t, service.OpenAIRequestRejectedClientMessage, errorObject["message"])
					} else {
						require.Equal(t, http.StatusBadRequest, rec.Code)
						require.Equal(t, "invalid_request_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
						require.Equal(t, service.OpenAIRequestRejectedCode, gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
						require.Equal(t, service.OpenAIRequestRejectedClientMessage, gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
					}

					markValue, exists := c.Get(service.OpsStreamErrorKey)
					require.True(t, exists)
					mark, ok := markValue.(service.OpsStreamError)
					require.True(t, ok)
					require.Equal(t, http.StatusBadRequest, mark.IntendedStatus)
					require.True(t, mark.CountTowardsSLA)
					require.Equal(t, "invalid_request_error", mark.ErrType)
					require.Equal(t, service.OpenAIRequestRejectedCode, mark.Code)
					require.Len(t, mark.UpstreamErrors, 1)
					require.Equal(t, "request_rejected", mark.UpstreamErrors[0].Kind)
					require.NotNil(t, mark.UpstreamErrors[0].ContinuationDiagnostic)
					require.Equal(t, rejection.classification, mark.UpstreamErrors[0].ContinuationDiagnostic.Classification)
					if format.stream {
						setupOpsErrorLogTestQueue(t, 4)
						ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
						logOpsStreamError(c, ops, rec.Code)
						require.Equal(t, int64(1), OpsErrorLogQueueLength())
						job := <-opsErrorLogQueue
						require.Equal(t, http.StatusBadRequest, job.entry.StatusCode, "wire HTTP 200 must not erase request rejection status")
						require.Equal(t, "invalid_request_error", job.entry.ErrorType)
						require.Contains(t, job.entry.ErrorBody, service.OpenAIRequestRejectedCode)
					}
				})
			}
		}
	}
}

func TestOpenAIGatewayHandler_RequestRejectedAfterStreamStartedEmitsOneTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/v1/responses", "/v1/responses/compact"} {
		t.Run(path, func(t *testing.T) {
			c, rec := newOpenAIResponsesFailoverTestContextWithStream(t, nil, true)
			c.Request.URL.Path = path
			c.Header("Content-Type", "text/event-stream")
			_, err := c.Writer.Write([]byte(": existing keepalive\n\n"))
			require.NoError(t, err)
			c.Writer.Flush()

			(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, service.NewOpenAIRequestRejectedError(http.StatusBadRequest, nil), true)

			require.Equal(t, http.StatusOK, rec.Code)
			require.Equal(t, 1, strings.Count(rec.Body.String(), "event: response.failed\n"))
			require.NotContains(t, rec.Body.String(), "response.completed")
			require.NotContains(t, rec.Body.String(), `"retryable":true`)
			_, errorObject := parseResponsesFailedSSE(t, strings.TrimPrefix(rec.Body.String(), ": existing keepalive\n\n"))
			require.Equal(t, service.OpenAIRequestRejectedCode, errorObject["code"])
		})
	}
}

func TestOpenAIGatewayHandler_CompactRequestRejectedBeforeKeepaliveUsesJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := newOpenAIResponsesFailoverTestContext(t, nil)
	c.Request.URL.Path = "/v1/responses/compact"
	service.MarkOpenAICompactClientStream(c)
	// Legacy compact normalizes its upstream request to unary and must retain
	// the existing JSON/status response until a keepalive actually commits SSE.
	setOpsRequestContext(c, "gpt-5.1", false)

	(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, service.NewOpenAIRequestRejectedError(http.StatusBadRequest, nil), false)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid_request_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Equal(t, service.OpenAIRequestRejectedCode, gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
	require.NotContains(t, rec.Body.String(), "event:")
}
