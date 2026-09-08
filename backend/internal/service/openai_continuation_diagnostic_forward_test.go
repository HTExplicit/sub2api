package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIContinuationDiagnosticForwardPreservesTerminalBehavior(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const missingNamespace = `{"error":{"type":"invalid_request_error","param":"input[1].namespace","message":"Missing required parameter: 'input[1].namespace'. private-validation-message-51937"}}`
	for _, tt := range []struct {
		name            string
		passthrough     bool
		stream          bool
		requestRejected bool
		status          int
		upstreamError   string
		classification  string
		param           string
	}{
		{
			name:           "native_explicit",
			stream:         true,
			status:         http.StatusBadGateway,
			upstreamError:  `{"error":{"code":"previous_response_not_found","message":"previous response not found"}}`,
			classification: "previous_response_not_found",
		},
		{
			name:            "native_generic_stream",
			stream:          true,
			requestRejected: true,
			status:          http.StatusBadRequest,
			upstreamError:   `{"error":{"message":"bad request"}}`,
			classification:  "unclassified_bad_request",
		},
		{
			name:            "passthrough_generic_stream",
			passthrough:     true,
			stream:          true,
			requestRejected: true,
			status:          http.StatusBadRequest,
			upstreamError:   `{"error":{"message":"bad request"}}`,
			classification:  "unclassified_bad_request",
		},
		{
			name:            "native_generic_nonstream",
			requestRejected: true,
			status:          http.StatusBadRequest,
			upstreamError:   `{"error":{"message":"bad request"}}`,
			classification:  "unclassified_bad_request",
		},
		{
			name:            "passthrough_generic_nonstream",
			passthrough:     true,
			requestRejected: true,
			status:          http.StatusBadRequest,
			upstreamError:   `{"error":{"message":"bad request"}}`,
			classification:  "unclassified_bad_request",
		},
		{
			name:            "native_missing_namespace_stream",
			stream:          true,
			requestRejected: true,
			status:          http.StatusBadRequest,
			upstreamError:   missingNamespace,
			classification:  "request_validation",
			param:           "input[1].namespace",
		},
		{
			name:            "passthrough_missing_namespace_stream",
			passthrough:     true,
			stream:          true,
			requestRejected: true,
			status:          http.StatusBadRequest,
			upstreamError:   missingNamespace,
			classification:  "request_validation",
			param:           "input[1].namespace",
		},
		{
			name:            "native_missing_namespace_nonstream",
			requestRejected: true,
			status:          http.StatusBadRequest,
			upstreamError:   missingNamespace,
			classification:  "request_validation",
			param:           "input[1].namespace",
		},
		{
			name:            "passthrough_missing_namespace_nonstream",
			passthrough:     true,
			requestRejected: true,
			status:          http.StatusBadRequest,
			upstreamError:   missingNamespace,
			classification:  "request_validation",
			param:           "input[1].namespace",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"gpt-5.2","stream":%t,"instructions":"diagnostic-test-instructions","prompt_cache_key":"diagnostic-cache-source","input":[{"type":"reasoning","encrypted_content":"diagnostic-cipher-fixture"},{"type":"function_call","call_id":"call_diag","name":"tool","arguments":"{}"},{"type":"function_call_output","call_id":"call_diag","output":"diagnostic-tool-result"}]}`, tt.stream))
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			c.Request.Header.Set("Session_id", "diagnostic-session-source")
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			repo := &openAIPassthroughFailoverRepo{}
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: tt.status,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
					"X-Request-Id": []string{"rid-continuation-diagnostic"},
				},
				Body: io.NopCloser(strings.NewReader(tt.upstreamError)),
			}}
			cfg := &config.Config{}
			svc := &OpenAIGatewayService{
				cfg:              cfg,
				httpUpstream:     upstream,
				rateLimitService: &RateLimitService{accountRepo: repo, cfg: cfg},
			}
			account := &Account{
				ID:          132,
				Name:        "continuation-diagnostic-test",
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Concurrency: 1,
				Credentials: map[string]any{
					"api_key": "sk-test", "base_url": "https://api.example.test",
					"pool_mode": true, "pool_mode_retry_status_codes": []any{float64(http.StatusBadRequest)},
				},
				Extra:       map[string]any{"use_responses_api": true, "openai_passthrough": tt.passthrough},
				Status:      StatusActive,
				Schedulable: true,
			}

			result, err := svc.Forward(context.Background(), c, account, body)
			require.Nil(t, result)
			var terminal *UpstreamFailoverError
			require.ErrorAs(t, err, &terminal)
			expectedMessage, expectedKind := OpenAIContinuationStateUnavailableClientMessage, "continuation_state"
			if tt.requestRejected {
				expectedMessage, expectedKind = OpenAIRequestRejectedClientMessage, "request_rejected"
				require.True(t, terminal.IsOpenAIRequestRejected())
				require.False(t, terminal.IsOpenAIContinuationStateUnavailable())
				require.Equal(t, "invalid_request_error", terminal.ClientErrorType)
				require.Equal(t, OpenAIRequestRejectedCode, terminal.ClientErrorCode)
				require.Empty(t, terminal.ResponseBody, "request rejection must not carry raw upstream content")
			} else {
				require.True(t, terminal.IsOpenAIContinuationStateUnavailable())
				require.False(t, terminal.IsOpenAIRequestRejected())
			}
			require.Equal(t, tt.status, terminal.StatusCode)
			require.Equal(t, http.StatusBadRequest, terminal.ClientStatusCode)
			require.Equal(t, expectedMessage, terminal.ClientMessage)
			require.Equal(t, GatewayFailureScopeRequest, terminal.Scope)
			require.Equal(t, NextAccountStop, terminal.NextAccountAction)
			require.False(t, terminal.RetryableOnSameAccount)
			require.False(t, terminal.ShouldRetryNextAccount())
			require.False(t, terminal.ShouldReportAccountScheduleFailure())
			require.True(t, terminal.SuppressAccountHealthPenalty)
			require.Len(t, upstream.bodies, 1, "diagnostics must not cause an upstream retry")
			require.JSONEq(t, gjson.GetBytes(body, "input").Raw, gjson.GetBytes(upstream.bodies[0], "input").Raw)
			require.False(t, c.Writer.Written())
			require.Empty(t, rec.Body.String())
			require.Empty(t, repo.rateLimitCalls)
			require.Empty(t, repo.overloadCalls)

			value, ok := c.Get(OpsUpstreamErrorsKey)
			require.True(t, ok)
			events, ok := value.([]*OpsUpstreamErrorEvent)
			require.True(t, ok)
			require.Len(t, events, 1, "diagnostics must attach to the existing event only")
			event := events[0]
			require.Equal(t, expectedKind, event.Kind)
			require.Equal(t, tt.passthrough, event.Passthrough)
			require.Equal(t, tt.status, event.UpstreamStatusCode)
			require.Equal(t, expectedMessage, event.Message)
			require.Empty(t, event.Detail, "diagnostics must not change passthrough-rule keyword input")
			require.NotNil(t, event.ContinuationDiagnostic)
			diagnostic := event.ContinuationDiagnostic
			require.Equal(t, tt.classification, diagnostic.Classification)
			require.Equal(t, tt.param, diagnostic.UpstreamError.ErrorParam.Value)
			require.Equal(t, "prepared_fallback", diagnostic.Wire.BodySource)
			require.True(t, diagnostic.Wire.InspectionLimited)
			require.Nil(t, upstream.lastReq.GetBody, "diagnostics must not restore transparent POST replay")
			require.Equal(t, len(upstream.bodies[0]), diagnostic.Wire.BodyBytes)
			wireCacheDigest := sha256.Sum256([]byte(gjson.GetBytes(upstream.bodies[0], "prompt_cache_key").String()))
			require.Equal(t, hex.EncodeToString(wireCacheDigest[:]), diagnostic.Wire.PromptCache.SHA256)
			require.Equal(t, 1, diagnostic.Wire.History.Encrypted)
			require.Equal(t, 1, diagnostic.Wire.History.Calls)
			require.Equal(t, 1, diagnostic.Wire.History.Outputs)
			require.Zero(t, diagnostic.Wire.History.UnmatchedOutputs)
			encoded, encodeErr := json.Marshal(event)
			require.NoError(t, encodeErr)
			require.True(t, gjson.GetBytes(encoded, "continuation_diagnostic").IsObject())
			require.NotContains(t, string(encoded), "private-validation-message-51937")
		})
	}
}
