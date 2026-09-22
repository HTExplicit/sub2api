//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestForwardAsChatCompletions_GrokRawFailoverPreservesRetryMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name      string
		pool      bool
		stream    bool
		response  string
		retryable bool
		delay     time.Duration
		maximum   int
	}{
		{
			name: "capacity_without_pool_stream", stream: true,
			response:  `{"error":{"message":"model capacity exceeded"}}`,
			retryable: true, delay: 500 * time.Millisecond, maximum: 1,
		},
		{
			name: "ordinary_pool_throttle", pool: true,
			response:  `{"error":{"message":"rate limit exceeded"}}`,
			retryable: true,
		},
		{
			name: "exhausted_pool_does_not_retry", pool: true,
			response: `{"error":{"code":"subscription:free-usage-exhausted"}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{"model":"grok-4.5","messages":[{"role":"user","content":"fixture"}],"stream":` + strconv.FormatBool(test.stream) + `}`)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
			c.Request.Header.Set("User-Agent", "fixture-client/1.0")
			account := &Account{
				ID: 92701, Platform: PlatformGrok, Type: AccountTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{
					"api_key": "fixture-key", "base_url": "https://grok.example.test/v1", "pool_mode": test.pool,
				},
			}
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header: http.Header{
					"Content-Type": []string{"application/json"}, "Retry-After": []string{"1"},
					"X-Request-Id": []string{"fixture-raw-grok-failover"},
				},
				Body: io.NopCloser(strings.NewReader(test.response)),
			}}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

			started := time.Now()
			result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
			finished := time.Now()

			require.Nil(t, result)
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.Len(t, upstream.requests, 1, "metadata must not start another upstream attempt")
			require.Equal(t, "https://grok.example.test/v1/chat/completions", upstream.lastReq.URL.String())
			require.Equal(t, "fixture-client/1.0", upstream.lastReq.Header.Get("User-Agent"))
			require.False(t, c.Writer.Written(), "failover must remain before stream or buffered output commitment")
			require.Empty(t, recorder.Body.String())
			require.Equal(t, http.StatusTooManyRequests, failover.StatusCode)
			require.JSONEq(t, test.response, string(failover.ResponseBody))
			require.Equal(t, "1", failover.ResponseHeaders.Get("Retry-After"))
			require.Equal(t, test.retryable, failover.RetryableOnSameAccount)
			require.Equal(t, test.retryable, failover.RequestScopedTransient)
			require.Equal(t, test.delay, failover.SameAccountRetryDelay)
			require.Equal(t, test.maximum, failover.SameAccountRetryMax)
			if test.maximum == 1 {
				require.False(t, failover.SameAccountRetryDeadline.Before(started.Add(30*time.Second)))
				require.False(t, failover.SameAccountRetryDeadline.After(finished.Add(30*time.Second)))
			} else {
				require.True(t, failover.SameAccountRetryDeadline.IsZero())
			}
			upstream.resp.Header.Set("Retry-After", "2")
			require.Equal(t, "1", failover.ResponseHeaders.Get("Retry-After"), "failover headers must remain a snapshot")
		})
	}
}
