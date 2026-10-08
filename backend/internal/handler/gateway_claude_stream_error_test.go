package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClaudeStreamErrorTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		errorType string
		message   string
		status    int
	}{
		{"invalid_request_error", "Invalid thinking signature", 400},
		{"overloaded_error", "Overloaded", 503},
		{"service_error", "Service temporarily unavailable", 502},
	} {
		for _, started := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", test.errorType, started), func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				if started {
					_, err := c.Writer.WriteString(partialMessageStartSSE)
					require.NoError(t, err)
				}
				body := []byte(fmt.Sprintf(`{"type":"error","error":{"type":%q,"message":%q}}`, test.errorType, test.message))
				failoverErr := service.ClassifyClaudeStreamError(body).FailoverError(body)
				(&GatewayHandler{}).handleFailoverExhausted(c, failoverErr, service.PlatformAnthropic, started)
				if started {
					require.Equal(t, 200, recorder.Code)
					require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: message_start"))
					require.Equal(t, 1, strings.Count(recorder.Body.String(), `"type":"error"`))
					parsed := parseOpsErrorResponse(recorder.Body.Bytes())
					require.Equal(t, test.errorType, parsed.ErrorType)
					require.Equal(t, test.message, parsed.Message)
				} else {
					require.Equal(t, test.status, recorder.Code)
					require.Equal(t, test.errorType, gjson.Get(recorder.Body.String(), "error.type").String())
					require.Equal(t, test.message, gjson.Get(recorder.Body.String(), "error.message").String())
				}
				require.NotContains(t, recorder.Body.String(), "Upstream access forbidden")
			})
		}
	}
}

type claudeStreamPassthroughRepo struct {
	service.ErrorPassthroughRepository
	rules []*model.ErrorPassthroughRule
}

func (repo *claudeStreamPassthroughRepo) List(context.Context) ([]*model.ErrorPassthroughRule, error) {
	return repo.rules, nil
}

func TestClaudeStreamErrorTerminal_PassthroughRulePrecedence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	responseCode, message := 418, "configured overload response"
	repo := &claudeStreamPassthroughRepo{rules: []*model.ErrorPassthroughRule{{
		Enabled: true, ErrorCodes: []int{529}, Platforms: []string{service.PlatformAnthropic}, MatchMode: model.MatchModeAny,
		ResponseCode: &responseCode, CustomMessage: &message, SkipMonitoring: true,
	}}}
	handler := &GatewayHandler{errorPassthroughService: service.NewErrorPassthroughService(repo, nil)}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	body := []byte(`{"error":{"type":"overloaded_error","message":"Overloaded"}}`)
	handler.handleFailoverExhausted(c, service.ClassifyClaudeStreamError(body).FailoverError(body), service.PlatformAnthropic, false)
	require.Equal(t, responseCode, recorder.Code)
	require.Equal(t, message, gjson.Get(recorder.Body.String(), "error.message").String())
	require.Equal(t, true, c.GetBool(service.OpsSkipPassthroughKey))
}

func TestClaudeStreamErrorTerminal_RealHTTPAuthMappingUnchanged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, status := range []int{401, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			(&GatewayHandler{}).handleFailoverExhausted(c, &service.UpstreamFailoverError{StatusCode: status}, service.PlatformAnthropic, false)
			require.Equal(t, 502, recorder.Code)
			require.Equal(t, "upstream_error", gjson.Get(recorder.Body.String(), "error.type").String())
			require.Contains(t, gjson.Get(recorder.Body.String(), "error.message").String(), "please contact administrator")
		})
	}
}

func TestClaudeStreamFailureStatus_EventGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name   string
		events []*service.OpsUpstreamErrorEvent
		want   int
	}{
		{"new overload", []*service.OpsUpstreamErrorEvent{{Platform: service.PlatformAnthropic, Kind: "stream_error", UpstreamStatusCode: 529, UpstreamHTTPStatusCode: 200}}, 529},
		{"new service failure", []*service.OpsUpstreamErrorEvent{{Platform: service.PlatformAnthropic, Kind: "stream_error", UpstreamStatusCode: 500, UpstreamHTTPStatusCode: 200}}, 500},
		{"other platform", []*service.OpsUpstreamErrorEvent{{Platform: service.PlatformOpenAI, Kind: "stream_error", UpstreamStatusCode: 500, UpstreamHTTPStatusCode: 200}}, 503},
		{"legacy stream", []*service.OpsUpstreamErrorEvent{{Kind: "stream_error", UpstreamStatusCode: 403}}, 503},
		{"ordinary HTTP", []*service.OpsUpstreamErrorEvent{{Kind: "http_error", UpstreamStatusCode: 502, UpstreamHTTPStatusCode: 502}}, 503},
		{"invalid semantic status", []*service.OpsUpstreamErrorEvent{{Kind: "stream_error", UpstreamStatusCode: 200, UpstreamHTTPStatusCode: 200}}, 503},
		{"later HTTP attempt wins", []*service.OpsUpstreamErrorEvent{{Kind: "stream_error", UpstreamStatusCode: 529, UpstreamHTTPStatusCode: 200}, {Kind: "http_error", UpstreamStatusCode: 502}}, 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set(service.OpsUpstreamErrorsKey, test.events)
			require.Equal(t, test.want, inferStreamFailureStatus(c, parsedOpsError{ErrorType: "overloaded_error"}))
		})
	}
}

func TestClaudeOpsErrorLogger_StreamErrorSemanticStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		errorType string
		status    int
	}{
		{"invalid_request_error", 400},
		{"rate_limit_error", 429},
		{"overloaded_error", 529},
		{"service_error", 500},
	} {
		t.Run(test.errorType, func(t *testing.T) {
			setupOpsErrorLogTestQueue(t, 2)
			ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			router := gin.New()
			router.Use(OpsErrorLoggerMiddleware(ops))
			router.POST("/v1/messages", func(c *gin.Context) {
				body := []byte(fmt.Sprintf(`{"type":"error","error":{"type":%q,"message":"provider failure"}}`, test.errorType))
				c.Set(service.OpsUpstreamErrorsKey, []*service.OpsUpstreamErrorEvent{{Platform: service.PlatformAnthropic, Kind: "stream_error", UpstreamStatusCode: test.status, UpstreamHTTPStatusCode: 200, Message: "provider failure"}})
				_, err := c.Writer.WriteString(partialMessageStartSSE)
				require.NoError(t, err)
				(&GatewayHandler{}).handleFailoverExhausted(c, service.ClassifyClaudeStreamError(body).FailoverError(body), service.PlatformAnthropic, true)
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/messages", nil))
			require.Equal(t, 200, recorder.Code)
			require.Equal(t, int64(1), OpsErrorLogQueueLength())
			job := <-opsErrorLogQueue
			require.Equal(t, test.status, job.entry.StatusCode)
			require.Equal(t, test.status, *job.entry.UpstreamStatusCode)
			require.Equal(t, "provider failure", *job.entry.UpstreamErrorMessage)
			require.NotNil(t, job.entry.UpstreamErrorsJSON)
			events, err := service.ParseOpsUpstreamErrors(*job.entry.UpstreamErrorsJSON)
			require.NoError(t, err)
			require.Equal(t, 200, events[0].UpstreamHTTPStatusCode)
			require.Equal(t, test.status, events[0].UpstreamStatusCode)
		})
	}
}
