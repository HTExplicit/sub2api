//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type contextBoundBlockingReadCloser struct {
	data       []byte
	offset     int
	ctx        context.Context
	forceClose chan struct{}
	closeOnce  sync.Once
}

func newContextBoundBlockingReadCloser(data []byte) *contextBoundBlockingReadCloser {
	return &contextBoundBlockingReadCloser{
		data:       data,
		forceClose: make(chan struct{}),
	}
}

func (r *contextBoundBlockingReadCloser) Read(p []byte) (int, error) {
	if r.offset < len(r.data) {
		n := copy(p, r.data[r.offset:])
		r.offset += n
		return n, nil
	}
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	case <-r.forceClose:
		return 0, io.EOF
	}
}

func (r *contextBoundBlockingReadCloser) Close() error {
	select {
	case <-r.ctx.Done():
	case <-r.forceClose:
	}
	return nil
}

func (r *contextBoundBlockingReadCloser) forceUnblock() {
	r.closeOnce.Do(func() { close(r.forceClose) })
}

type contextBoundHTTPUpstream struct {
	body *contextBoundBlockingReadCloser
}

func (u *contextBoundHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.body.ctx = req.Context()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       u.body,
	}, nil
}

func (u *contextBoundHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

func TestForwardAsChatCompletions_CancelsUpstreamBeforeClosingBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := []byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"model\":\"gpt-5.4\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}],\"usage\":{\"input_tokens\":17,\"output_tokens\":8,\"total_tokens\":25}}}\n\n")
	stream := newContextBoundBlockingReadCloser(upstreamBody)
	t.Cleanup(stream.forceUnblock)

	cfg := rawChatCompletionsTestConfig()
	cfg.Gateway.StreamKeepaliveInterval = 1
	svc := &OpenAIGatewayService{
		cfg:          cfg,
		httpUpstream: &contextBoundHTTPUpstream{body: stream},
	}

	type forwardResult struct {
		result *OpenAIForwardResult
		err    error
	}
	resultCh := make(chan forwardResult, 1)
	go func() {
		result, err := svc.ForwardAsChatCompletions(context.Background(), c, rawChatCompletionsTestAccount(), body, "", "gpt-5.1")
		resultCh <- forwardResult{result: result, err: err}
	}()

	select {
	case got := <-resultCh:
		require.NoError(t, got.err)
		require.NotNil(t, got.result)
		require.Equal(t, 17, got.result.Usage.InputTokens)
	case <-time.After(time.Second):
		t.Fatal("ForwardAsChatCompletions did not cancel upstream before closing the body")
	}
}

type contextBoundChatRecoveryUpstream struct {
	httpUpstreamRecorder
	cancelRecovery               context.CancelFunc
	recoveryCancellationObserved bool
}

func (u *contextBoundChatRecoveryUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	if len(u.responses) > 0 {
		if body, ok := u.responses[0].Body.(*contextBoundBlockingReadCloser); ok {
			body.ctx = req.Context()
		}
	}
	resp, err := u.httpUpstreamRecorder.Do(req, proxyURL, accountID, accountConcurrency)
	if err != nil {
		return nil, err
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	if len(u.requests) == 2 && u.cancelRecovery != nil {
		u.cancelRecovery()
		select {
		case <-req.Context().Done():
			u.recoveryCancellationObserved = true
			return nil, req.Context().Err()
		case <-time.After(time.Second):
			return nil, errors.New("reasoning recovery lost client cancellation")
		}
	}
	return resp, nil
}

func (u *contextBoundChatRecoveryUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

func TestForwardAsChatCompletions_StreamCancellationIsPerReasoningAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, outcome := range []string{"completed", "client_canceled"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			body := []byte(strings.Replace(reasoningRecoveryFixture, `"store":false`, `"stream":true,"store":false`, 1))
			c, recorder := openAIChatReplayTestContext(t, body)
			c.Request = c.Request.WithContext(ctx)
			failed := newContextBoundBlockingReadCloser([]byte(`data: {"type":"response.failed","response":{"id":"resp_rejected","status":"failed","error":{"code":"invalid_encrypted_content","param":"input[1].encrypted_content"}}}` + "\n\n"))
			completed := newContextBoundBlockingReadCloser([]byte("data: " + string(openAIChatReplayTestPayload(`[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]`)) + "\n\n"))
			t.Cleanup(failed.forceUnblock)
			t.Cleanup(completed.forceUnblock)
			upstream := &contextBoundChatRecoveryUpstream{httpUpstreamRecorder: httpUpstreamRecorder{responses: []*http.Response{
				{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: failed},
				{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: completed},
			}}}
			if outcome == "client_canceled" {
				upstream.cancelRecovery = cancel
			}
			cfg := rawChatCompletionsTestConfig()
			cfg.Gateway.StreamKeepaliveInterval = 1
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
			type forwardResult struct {
				result *OpenAIForwardResult
				err    error
			}
			resultCh := make(chan forwardResult, 1)
			go func() {
				result, err := svc.ForwardAsChatCompletions(ctx, c, newOpenAIRejectedFieldTestAccount(), body, "", "")
				resultCh <- forwardResult{result: result, err: err}
			}()

			var got forwardResult
			select {
			case got = <-resultCh:
			case <-time.After(2 * time.Second):
				t.Fatal("stream attempt did not cancel before closing or canceled its reasoning recovery")
			}
			require.Len(t, upstream.requests, 2, "the rejected attempt permits exactly one same-source recovery")
			require.ErrorIs(t, upstream.requests[0].Context().Err(), context.Canceled)
			require.ErrorIs(t, upstream.requests[1].Context().Err(), context.Canceled)
			deadline, ok := upstream.requests[1].Context().Deadline()
			require.True(t, ok, "the recovery must retain the original client deadline")
			clientDeadline, _ := ctx.Deadline()
			require.Equal(t, clientDeadline, deadline)
			for _, request := range upstream.requests {
				require.Equal(t, HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileFromContext(request.Context()))
			}
			require.Equal(t, upstream.requests[0].URL, upstream.requests[1].URL)
			require.Equal(t, upstream.requests[0].Header, upstream.requests[1].Header)
			require.True(t, gjson.GetBytes(upstream.bodies[0], "input.1.encrypted_content").Exists())
			expected, err := sjson.DeleteBytes(upstream.bodies[0], "input.1.encrypted_content")
			require.NoError(t, err)
			require.JSONEq(t, string(expected), string(upstream.bodies[1]))
			require.NotContains(t, recorder.Body.String(), "resp_rejected")
			if outcome == "completed" {
				require.NoError(t, got.err)
				require.NotNil(t, got.result)
				require.Equal(t, 4, got.result.Usage.InputTokens)
				require.Equal(t, 6, got.result.Usage.OutputTokens)
			} else {
				require.Error(t, got.err)
				require.True(t, upstream.recoveryCancellationObserved, "the recovery must observe client cancellation before transport cleanup")
				var failover *UpstreamFailoverError
				require.False(t, errors.As(got.err, &failover), "a canceled recovery must not reenter the account pool")
			}
		})
	}
}
