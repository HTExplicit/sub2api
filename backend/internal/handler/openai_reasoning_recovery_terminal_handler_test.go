//go:build unit

package handler

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type recoveryTerminalHandlerUpstream struct {
	service.HTTPUpstream
	accountIDs []int64
	bodies     [][]byte
	finalCode  int
	committed  bool
}

func (u *recoveryTerminalHandlerUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.accountIDs = append(u.accountIDs, accountID)
	u.bodies = append(u.bodies, body)
	if u.committed {
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"already delivered\"}\n\n" +
				"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_partial\",\"status\":\"failed\",\"error\":{\"type\":\"invalid_request_error\",\"code\":\"invalid_encrypted_content\",\"message\":\"signature rejected\"}}}\n\n")),
		}, nil
	}
	status := http.StatusBadRequest
	payload := `{"error":{"type":"invalid_request_error","code":"invalid_encrypted_content","param":"input[0].encrypted_content","message":"do-not-leak-first"}}`
	if len(u.accountIDs) > 1 {
		status = u.finalCode
		payload = `{"error":{"type":"invalid_request_error","param":"input","message":"do-not-leak-second"}}`
		if status >= 500 {
			payload = `{"error":{"type":"server_error","message":"do-not-leak-server"}}`
		}
	}
	return &http.Response{
		StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(payload)),
	}, nil
}

func TestOpenAIGatewayHandler_ReasoningFailureAlreadyForwardedIsNotDuplicated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		mode := "native"
		if passthrough {
			mode = "passthrough"
		}
		t.Run(mode, func(t *testing.T) {
			upstream := &recoveryTerminalHandlerUpstream{committed: true}
			account := service.Account{
				ID: 1, Name: "response-already-forwarded", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: true,
				Credentials: map[string]any{"api_key": "fixture", "base_url": "https://api.example.test"},
				Extra:       map[string]any{"use_responses_api": true, "openai_passthrough": passthrough},
			}
			handler := newOpenAIResponsesFailoverTestHandler(t, upstream, account)
			c, recorder := newOpenAIResponsesFailoverTestContextWithStream(t, nil, true)
			handler.Responses(c)
			require.Equal(t, []int64{1}, upstream.accountIDs, "semantic output forbids any replay")
			require.Contains(t, recorder.Body.String(), "already delivered")
			require.Equal(t, 1, strings.Count(recorder.Body.String(), `"type":"response.failed"`))
			require.NotContains(t, recorder.Body.String(), service.OpenAIContinuationStateUnavailableClientMessage)
			require.Zero(t, handler.gatewayService.SnapshotOpenAIAccountSchedulerMetrics().RuntimeStatsAccountCount)
		})
	}
}

func TestOpenAIGatewayHandler_ReasoningRecoveryTerminalNeverReentersScheduler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		mode := "native"
		if passthrough {
			mode = "passthrough"
		}
		for _, failure := range []struct {
			name         string
			status       int
			clientStatus int
			healthEvents int
		}{
			{"request_rejected", http.StatusBadRequest, http.StatusBadRequest, 0},
			{"provider_error", http.StatusInternalServerError, http.StatusBadGateway, 1},
		} {
			t.Run(mode+"/"+failure.name, func(t *testing.T) {
				setupOpsErrorLogTestQueue(t, 4)
				upstream := &recoveryTerminalHandlerUpstream{finalCode: failure.status}
				accounts := []service.Account{
					{ID: 1, Name: "recovery-source", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
						Status: service.StatusActive, Schedulable: true,
						Credentials: map[string]any{"api_key": "fixture", "base_url": "https://api.example.test", "pool_mode": true, "pool_mode_retry_count": 5},
						Extra:       map[string]any{"use_responses_api": true, "openai_passthrough": passthrough}},
					{ID: 2, Name: "must-not-replay", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
						Status: service.StatusActive, Schedulable: true, Priority: 1,
						Credentials: map[string]any{"api_key": "other-fixture", "base_url": "https://other.example.test"},
						Extra:       map[string]any{"use_responses_api": true}},
				}
				handler := newOpenAIResponsesFailoverTestHandler(t, upstream, accounts...)
				body := []byte(`{"model":"gpt-5.1","stream":true,"input":[{"type":"reasoning","encrypted_content":"private-old-cipher","summary":[]},{"type":"function_call","call_id":"call-1","name":"probe","arguments":"{}"},{"type":"function_call_output","call_id":"call-1","output":"private-tool-output"}]}`)
				authContext, _ := newOpenAIResponsesFailoverTestContextWithBody(t, nil, body)
				ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
				router := gin.New()
				router.Use(func(c *gin.Context) {
					for key, value := range authContext.Keys {
						c.Set(key, value)
					}
					c.Next()
				}, OpsErrorLoggerMiddleware(ops))
				router.POST("/v1/responses", handler.Responses)
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				router.ServeHTTP(recorder, request)

				require.Equal(t, []int64{1, 1}, upstream.accountIDs, "no third POST, same-account scheduler retry, or second account")
				require.True(t, gjson.GetBytes(upstream.bodies[0], "input.0.encrypted_content").Exists())
				require.False(t, gjson.GetBytes(upstream.bodies[1], "input.0.encrypted_content").Exists())
				require.Equal(t, gjson.GetBytes(upstream.bodies[0], "input.1").Raw, gjson.GetBytes(upstream.bodies[1], "input.1").Raw)
				require.Equal(t, gjson.GetBytes(upstream.bodies[0], "input.2").Raw, gjson.GetBytes(upstream.bodies[1], "input.2").Raw)
				require.NotContains(t, recorder.Body.String(), "do-not-leak")
				require.NotContains(t, recorder.Body.String(), "response.completed")
				if failure.clientStatus == http.StatusBadRequest {
					require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: response.failed\n"))
					require.Contains(t, recorder.Body.String(), service.OpenAIRequestRejectedCode)
				}
				require.Equal(t, int64(1), OpsErrorLogQueueLength())
				job := <-opsErrorLogQueue
				require.Equal(t, failure.clientStatus, job.entry.StatusCode)
				require.NotNil(t, job.entry.UpstreamStatusCode)
				require.Equal(t, failure.status, *job.entry.UpstreamStatusCode)
				// The minimal router fixture intentionally disables the optional
				// advanced scheduler. Exercise the same terminal finalizer with an
				// observable reporter instead of treating inert metrics as evidence.
				reporter := &cindyFailoverSelectionReporter{}
				classified := &service.UpstreamFailoverError{StatusCode: failure.status}
				if failure.clientStatus == http.StatusBadRequest {
					classified = service.NewOpenAIRequestRejectedError(failure.status, nil)
				}
				probe, _ := newOpenAIResponsesFailoverTestContextWithStream(t, nil, true)
				terminal := &service.OpenAIReasoningRecoveryTerminalError{Failure: classified}
				require.True(t, handler.handleReasoningRecoveryTerminal(probe, terminal,
					&service.AccountSelectionResult{Account: &accounts[0]}, &accounts[0], "gpt-5.1", true, reporter))
				require.Equal(t, failure.healthEvents, reporter.reportedFailures, "request rejection must not emit account-health failure feedback")
				require.Equal(t, 1-failure.healthEvents, reporter.releasedProbes)
				require.Zero(t, reporter.sameAccountRetries)
			})
		}
	}
}
