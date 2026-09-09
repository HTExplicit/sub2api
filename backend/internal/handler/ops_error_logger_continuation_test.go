package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpsErrorLoggerMiddleware_ContinuationTerminalStatusPersistence(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		code           string
		message        string
		keepalive      bool
		upstreamStatus int
		wantType       string
		wantStatus     int
	}{
		{
			name: "request_rejected_before_keepalive", code: service.OpenAIRequestRejectedCode,
			message: service.OpenAIRequestRejectedClientMessage, upstreamStatus: http.StatusBadRequest,
			wantType: "invalid_request_error", wantStatus: http.StatusBadRequest,
		},
		{
			name: "request_rejected_after_keepalive", code: service.OpenAIRequestRejectedCode,
			message: service.OpenAIRequestRejectedClientMessage, upstreamStatus: http.StatusBadRequest, keepalive: true,
			wantType: "invalid_request_error", wantStatus: http.StatusBadRequest,
		},
		{
			name: "continuation_unavailable_before_keepalive", code: service.OpenAIContinuationStateUnavailableCode,
			message: service.OpenAIContinuationStateUnavailableClientMessage, upstreamStatus: http.StatusBadRequest,
			wantType: "invalid_request_error", wantStatus: http.StatusBadRequest,
		},
		{
			name: "continuation_unavailable_after_keepalive", code: service.OpenAIContinuationStateUnavailableCode,
			message: service.OpenAIContinuationStateUnavailableClientMessage, upstreamStatus: http.StatusBadRequest, keepalive: true,
			wantType: "invalid_request_error", wantStatus: http.StatusBadRequest,
		},
		{
			name: "request_rejected_preserves_upstream_502", code: service.OpenAIRequestRejectedCode,
			message: service.OpenAIRequestRejectedClientMessage, upstreamStatus: http.StatusBadGateway,
			wantType: "invalid_request_error", wantStatus: http.StatusBadRequest,
		},
		{
			name: "continuation_unavailable_preserves_upstream_502", code: service.OpenAIContinuationStateUnavailableCode,
			message: service.OpenAIContinuationStateUnavailableClientMessage, upstreamStatus: http.StatusBadGateway, keepalive: true,
			wantType: "invalid_request_error", wantStatus: http.StatusBadRequest,
		},
		{
			name: "unknown_code_does_not_classify_by_message_or_overwrite_upstream_400", code: "new_provider_code",
			message: service.OpenAIRequestRejectedClientMessage, upstreamStatus: http.StatusBadRequest,
			wantType: "upstream_error", wantStatus: http.StatusBadGateway,
		},
		{
			name: "unknown_server_failure_without_attempt_context", code: "new_provider_code",
			message: "upstream stream failed", keepalive: true,
			wantType: "upstream_error", wantStatus: http.StatusBadGateway,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupOpsErrorLogTestQueue(t, 2)
			repo := &ingressRejectOpsRepo{}
			ops := service.NewOpsService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			router := gin.New()
			router.Use(OpsErrorLoggerMiddleware(ops))
			router.POST("/v1/responses", func(c *gin.Context) {
				setOpsRequestContext(c, "gpt-test", true)
				if tt.upstreamStatus > 0 {
					service.SetOpsUpstreamError(c, tt.upstreamStatus, "recorded upstream failure", "")
					c.Set(service.OpsUpstreamErrorsKey, []*service.OpsUpstreamErrorEvent{{
						UpstreamStatusCode: tt.upstreamStatus,
						Message:            "recorded upstream failure",
					}})
				}
				if tt.keepalive {
					c.Header("Content-Type", "text/event-stream")
					_, err := c.Writer.WriteString(": existing keepalive\n\n")
					require.NoError(t, err)
					c.Writer.Flush()
				}
				// Match the real handler: the structured mark is correct, but the
				// middleware sees the emitted terminal first. The writer deliberately
				// emits only error.code/message, so the parser must infer the type.
				service.MarkOpsStreamFailure(c, tt.wantType, tt.code, tt.message, tt.wantStatus)
				require.True(t, writeResponsesFailedSSE(c, tt.wantType, tt.code, tt.message))
			})

			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))

			require.Equal(t, http.StatusOK, recorder.Code)
			require.Contains(t, recorder.Header().Get("Content-Type"), "text/event-stream")
			prefix := "event: response.failed\n"
			if tt.keepalive {
				prefix = ": existing keepalive\n\n" + prefix
			}
			require.True(t, strings.HasPrefix(recorder.Body.String(), prefix))
			require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: response.failed\n"))
			require.NotContains(t, recorder.Body.String(), "response.completed")
			require.NotContains(t, recorder.Body.String(), `"type":"invalid_request_error"`)
			require.NotContains(t, recorder.Body.String(), `"retryable":true`)

			require.Equal(t, int64(1), OpsErrorLogEnqueuedTotal(), "terminal parsing and the stream mark must not enqueue duplicate failures")
			require.Equal(t, int64(1), OpsErrorLogQueueLength())
			job := <-opsErrorLogQueue
			flushOpsErrorLogBatch([]opsErrorLogJob{job})
			require.Equal(t, 1, repo.insertCalls)
			require.Len(t, repo.entries, 1)
			entry := repo.entries[0]
			require.Equal(t, tt.wantStatus, entry.StatusCode, "persist the client semantic status even though the wire was HTTP 200")
			require.Equal(t, tt.wantType, entry.ErrorType)
			require.Equal(t, tt.message, entry.ErrorMessage)
			require.Contains(t, entry.ErrorBody, tt.code)
			if tt.wantStatus == http.StatusBadRequest {
				require.Equal(t, "P3", entry.Severity, "a request rejection must not reappear as a 502/P1 failure")
			} else {
				require.Equal(t, "P1", entry.Severity)
			}
			require.NotNil(t, entry.UpstreamStatusCode)
			if tt.upstreamStatus > 0 {
				require.Equal(t, tt.upstreamStatus, *entry.UpstreamStatusCode, "the client terminal must not overwrite actual upstream HTTP status")
				require.NotNil(t, entry.UpstreamErrorsJSON)
				events, err := service.ParseOpsUpstreamErrors(*entry.UpstreamErrorsJSON)
				require.NoError(t, err)
				require.Len(t, events, 1)
				require.Equal(t, tt.upstreamStatus, events[0].UpstreamStatusCode)
			} else {
				require.Equal(t, tt.wantStatus, *entry.UpstreamStatusCode, "without attempt context retain the existing inferred-status fallback")
			}
		})
	}
}

func TestOpsErrorLoggerMiddleware_ContinuationStoreUnavailableRetains503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupOpsErrorLogTestQueue(t, 2)
	repo := &ingressRejectOpsRepo{}
	ops := service.NewOpsService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.Use(OpsErrorLoggerMiddleware(ops))
	router.POST("/v1/responses", func(c *gin.Context) {
		setOpsRequestContext(c, "gpt-test", true)
		c.Header("Content-Type", "text/event-stream")
		_, err := c.Writer.WriteString(": existing keepalive\n\n")
		require.NoError(t, err)
		c.Writer.Flush()
		(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, service.NewOpenAIContinuationStoreUnavailableError(), true)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: response.failed\n"))
	require.NotContains(t, recorder.Body.String(), "response.completed")
	require.Contains(t, recorder.Body.String(), service.OpenAIContinuationStateUnavailableCode)
	require.Equal(t, int64(1), OpsErrorLogEnqueuedTotal())
	require.Equal(t, int64(1), OpsErrorLogQueueLength())
	job := <-opsErrorLogQueue
	flushOpsErrorLogBatch([]opsErrorLogJob{job})
	require.Equal(t, 1, repo.insertCalls)
	require.Len(t, repo.entries, 1)
	entry := repo.entries[0]
	require.Equal(t, http.StatusServiceUnavailable, entry.StatusCode)
	// Keep the existing persistent normalization of a server_error to api_error.
	require.Equal(t, "api_error", entry.ErrorType)
	require.Equal(t, "P1", entry.Severity)
	require.NotNil(t, entry.UpstreamStatusCode)
	require.Equal(t, http.StatusServiceUnavailable, *entry.UpstreamStatusCode)
}

func TestOpsResponsesServerFailureMarker_IgnoresUnrelatedOrNonSLAMarks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		code string
		mark service.OpsStreamError
	}{
		{
			name: "unknown_terminal_code", code: "new_provider_code",
			mark: service.OpsStreamError{Code: "new_provider_code", ErrType: "server_error", IntendedStatus: http.StatusServiceUnavailable, CountTowardsSLA: true},
		},
		{
			name: "different_terminal_code", code: service.OpenAIContinuationStateUnavailableCode,
			mark: service.OpsStreamError{Code: service.OpenAIRequestRejectedCode, ErrType: "server_error", IntendedStatus: http.StatusServiceUnavailable, CountTowardsSLA: true},
		},
		{
			name: "non_sla_fallback", code: service.OpenAIContinuationStateUnavailableCode,
			mark: service.OpsStreamError{Code: service.OpenAIContinuationStateUnavailableCode, ErrType: "server_error", IntendedStatus: http.StatusServiceUnavailable},
		},
		{
			name: "stale_websocket_turn", code: service.OpenAIContinuationStateUnavailableCode,
			mark: service.OpsStreamError{Code: service.OpenAIContinuationStateUnavailableCode, ErrType: "server_error", IntendedStatus: http.StatusServiceUnavailable, CountTowardsSLA: true, Turn: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set(service.OpsStreamErrorKey, tt.mark)
			parsed := parsedOpsError{Code: tt.code, ErrorType: inferResponsesFailedOpsErrorType(tt.code), StreamFailure: true}
			require.Equal(t, parsed, applyOpsResponsesServerFailureMarker(c, parsed))
		})
	}
}
