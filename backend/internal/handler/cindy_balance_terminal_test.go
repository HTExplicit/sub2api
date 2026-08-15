//go:build unit

package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCindyBalanceAfterStreamWriteUsesGenericTerminalErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	raw := []byte(`{"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429","message":"sensitive upstream detail"}}}`)
	failoverErr := &service.UpstreamFailoverError{
		StatusCode:               http.StatusTooManyRequests,
		ResponseBody:             raw,
		RetryableOnSameAccount:   false,
		CindyBalanceInsufficient: true,
	}

	t.Run("anthropic_messages", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		_, _ = c.Writer.WriteString("event: content_block_delta\ndata: {\"type\":\"content_block_delta\"}\n\n")

		(&OpenAIGatewayHandler{}).handleAnthropicFailoverExhausted(c, failoverErr, true)

		body := recorder.Body.String()
		require.Contains(t, body, "event: error\n")
		require.Contains(t, body, `"type":"rate_limit_error"`)
		require.NotContains(t, body, "budget_exceeded")
		require.NotContains(t, body, "sensitive upstream detail")
	})

	t.Run("responses", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		_, _ = c.Writer.WriteString("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")

		(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, failoverErr, true)

		body := recorder.Body.String()
		require.Contains(t, body, "event: response.failed\n")
		require.Contains(t, body, `"code":"rate_limit_exceeded"`)
		require.NotContains(t, body, "budget_exceeded")
		require.NotContains(t, body, "sensitive upstream detail")
	})
}
