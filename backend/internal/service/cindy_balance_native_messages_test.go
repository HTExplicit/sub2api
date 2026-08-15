//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const cindyBalanceTransportRollbackHelperEnv = "SUB2API_CINDY_BALANCE_TRANSPORT_ROLLBACK_HELPER"

func TestForwardCindyAnthropicMessagesHTTP200ErrorFailsOverBeforeWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader("event: error\n" +
			`data: {"type":"error","error":{"type":"budget_exceeded","code":"429","message":"request failed"}}` + "\n\n")),
	}}
	repo := &cindyRateLimitAccountRepoStub{}
	service := newCindyNativeMessagesService(upstream)
	service.rateLimitService = NewRateLimitService(repo, nil, nil, nil, nil)
	account := newCindyNativeMessagesAccount()

	_, err := service.ForwardCindyAnthropicMessages(
		context.Background(), c, account,
		[]byte(`{"model":"claude-opus-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}],"stream":true}`),
		"claude-opus-5",
	)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.CindyBalanceInsufficient)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.NotContains(t, string(failoverErr.ResponseBody), "budget_exceeded")
	require.Equal(t, 1, repo.markCalls)
	require.NotNil(t, account.CindyBalanceInsufficientAt)
	require.False(t, c.Writer.Written())
	require.Empty(t, recorder.Body.String())
}

func TestForwardCindyAnthropicMessagesHTTP200ErrorAfterOutputDropsRawPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	upstreamBody := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":1}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
		``,
		`event: error`,
		`data: {"type":"error","error":{"type":"budget_exceeded","code":"429","message":"sensitive upstream detail"}}`,
		``,
	}, "\n")
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	repo := &cindyRateLimitAccountRepoStub{}
	service := newCindyNativeMessagesService(upstream)
	service.rateLimitService = NewRateLimitService(repo, nil, nil, nil, nil)
	account := newCindyNativeMessagesAccount()

	_, err := service.ForwardCindyAnthropicMessages(
		context.Background(), c, account,
		[]byte(`{"model":"claude-opus-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}],"stream":true}`),
		"claude-opus-5",
	)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.CindyBalanceInsufficient)
	require.True(t, c.Writer.Written(), "semantic output prevents switching the current client stream")
	require.Contains(t, recorder.Body.String(), `"text":"ok"`)
	require.NotContains(t, recorder.Body.String(), "budget_exceeded")
	require.NotContains(t, recorder.Body.String(), "sensitive upstream detail")
	require.NotContains(t, string(failoverErr.ResponseBody), "budget_exceeded")
	require.Equal(t, 1, repo.markCalls)
}

func TestForwardCindyAnthropicMessagesExact429PrecedesPoolRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(exactCindyBudgetExceededBody)),
	}}
	repo := &cindyRateLimitAccountRepoStub{}
	rateLimitService := NewRateLimitService(repo, nil, nil, nil, nil)
	runtimeBlocker := &OpenAIGatewayService{}
	rateLimitService.SetAccountRuntimeBlocker(runtimeBlocker)
	service := newCindyNativeMessagesService(upstream)
	service.rateLimitService = rateLimitService
	account := newCindyNativeMessagesAccount()
	account.Credentials["pool_mode"] = true
	account.Credentials["pool_mode_retry_count"] = float64(3)
	account.Credentials["pool_mode_retry_status_codes"] = []any{float64(http.StatusTooManyRequests)}

	_, err := service.ForwardCindyAnthropicMessages(
		context.Background(), c, account,
		[]byte(`{"model":"gpt-5.6-luna","max_tokens":16,"messages":[{"role":"user","content":"hi"}],"stream":false}`),
		"gpt-5.6-luna",
	)

	var failoverErr *UpstreamFailoverError
	require.Error(t, err)
	require.True(t, errors.As(err, &failoverErr))
	require.True(t, failoverErr.CindyBalanceInsufficient)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.Equal(t, 1, upstream.calls, "exact budget exhaustion must not enter the pool retry loop")
	require.Equal(t, 1, repo.markCalls)
	require.True(t, runtimeBlocker.isOpenAIAccountRuntimeBlocked(account))
}

func TestForwardCindyAnthropicMessagesHTTP200JSONErrorFailsOverBeforeWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"type":"error","error":{"type":"budget_exceeded","code":"429","message":"request failed"}}`,
		)),
	}}
	repo := &cindyRateLimitAccountRepoStub{}
	rateLimitService := NewRateLimitService(repo, nil, nil, nil, nil)
	runtimeBlocker := &OpenAIGatewayService{}
	rateLimitService.SetAccountRuntimeBlocker(runtimeBlocker)
	service := newCindyNativeMessagesService(upstream)
	service.rateLimitService = rateLimitService
	account := newCindyNativeMessagesAccount()

	_, err := service.ForwardCindyAnthropicMessages(
		context.Background(), c, account,
		[]byte(`{"model":"gpt-5.6-luna","max_tokens":16,"messages":[{"role":"user","content":"hi"}],"stream":false}`),
		"gpt-5.6-luna",
	)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.CindyBalanceInsufficient)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.False(t, c.Writer.Written())
	require.Empty(t, recorder.Body.String())
	require.Equal(t, 1, repo.markCalls)
	require.True(t, runtimeBlocker.isOpenAIAccountRuntimeBlocked(account))
}

func TestCindyAnthropicPassthroughBuffersPreambleAndSuppressesIdlePingUntilClassification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	service := newCindyNativeMessagesService(nil)
	service.cfg.Gateway.StreamKeepaliveInterval = 1
	repo := &cindyRateLimitAccountRepoStub{}
	service.rateLimitService = NewRateLimitService(repo, nil, nil, nil, nil)
	account := newCindyNativeMessagesAccount()
	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       pr,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = pw.Write([]byte("event: message_start\n" +
			`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":1}}}` + "\n\n"))
		time.Sleep(1200 * time.Millisecond)
		_, _ = pw.Write([]byte("event: error\n" +
			`data: {"type":"error","error":{"type":"budget_exceeded","code":"429","message":"sensitive upstream detail"}}` + "\n\n"))
		_ = pw.Close()
	}()

	_, err := service.handleStreamingResponseAnthropicAPIKeyPassthrough(
		context.Background(), resp, c, account, time.Now(), "claude-opus-5",
	)
	_ = pr.Close()
	<-done

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.CindyBalanceInsufficient)
	require.Empty(t, recorder.Body.String(), "Cindy preamble and local ping must remain replay-safe before an exact balance event")
	require.Equal(t, 1, repo.markCalls)
}

func TestCindyBalanceRollbackPreservesImmediateTransportPreambles(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestCindyBalanceTransportRollbackHelper$")
	cmd.Env = append(withoutEnvironmentKeys(os.Environ(),
		CindyBalanceDetectionEnabledEnv,
		cindyBalanceTransportRollbackHelperEnv,
	),
		CindyBalanceDetectionEnabledEnv+"=false",
		cindyBalanceTransportRollbackHelperEnv+"=1",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated balance transport rollback test failed: %v\n%s", err, output)
	}
}

func TestCindyBalanceTransportRollbackHelper(t *testing.T) {
	if os.Getenv(cindyBalanceTransportRollbackHelperEnv) == "" {
		t.Skip("subprocess helper")
	}
	if CindyBalanceDetectionFeatureEnabled() {
		t.Fatal("balance transport rollback helper started with detection enabled")
	}

	gin.SetMode(gin.TestMode)
	account := newCindyNativeMessagesAccount()
	t.Run("native Messages", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		service := newCindyNativeMessagesService(nil)
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader("event: message_start\n" +
				`data: {"type":"message_start","message":{"id":"msg_rollback","usage":{"input_tokens":1}}}` + "\n\n")),
		}

		_, _ = service.handleStreamingResponseAnthropicAPIKeyPassthrough(
			context.Background(), resp, c, account, time.Now(), "claude-opus-5",
		)

		require.Contains(t, recorder.Body.String(), "message_start")
	})

	t.Run("HTTP to WS bridge", func(t *testing.T) {
		upstream := &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				`data: {"type":"response.created","response":{"id":"resp_rollback"}}` + "\n\n",
			)),
		}}
		gateway := &OpenAIGatewayService{
			cfg:           &config.Config{},
			httpUpstream:  upstream,
			toolCorrector: NewCodexToolCorrector(),
		}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
		payload := []byte(`{"type":"response.create","model":"gpt-5.6-luna","input":"hi"}`)
		var writes [][]byte

		_, _ = gateway.proxyOpenAIWSHTTPBridgeTurn(
			context.Background(), c, account, "sk-test", payload, len(payload),
			"gpt-5.6-luna", "", "", "", "", 1,
			func(message []byte) error {
				writes = append(writes, append([]byte(nil), message...))
				return nil
			},
		)

		require.NotEmpty(t, writes)
		require.Equal(t, "response.created", gjson.GetBytes(writes[0], "type").String())
	})
}
