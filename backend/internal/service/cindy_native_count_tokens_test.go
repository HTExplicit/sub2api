package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type cindyNativeCountTokensBalanceRepo struct {
	AccountRepository
	marked bool
}

func (r *cindyNativeCountTokensBalanceRepo) MarkCindyBalanceInsufficient(context.Context, int64, time.Time) (bool, error) {
	changed := !r.marked
	r.marked = true
	return changed, nil
}

func (r *cindyNativeCountTokensBalanceRepo) ClearCindyBalanceInsufficient(context.Context, int64) (bool, error) {
	return false, nil
}

func (r *cindyNativeCountTokensBalanceRepo) PreviewCindyInsufficientDeletion(context.Context) (*CindyInsufficientDeletePreview, error) {
	return &CindyInsufficientDeletePreview{}, nil
}

func (r *cindyNativeCountTokensBalanceRepo) DeleteCindyInsufficient(context.Context, int, string) (*CindyInsufficientDeleteResult, error) {
	return &CindyInsufficientDeleteResult{}, nil
}

func TestForwardCindyAnthropicCountTokensUsesNativeWire(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name  string
		model string
	}{
		{name: "public", model: "claude-opus-5"},
		{name: "alias", model: "claude-opus-4-6"},
		{name: "live", model: "anthropic/claude-opus-5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
			c.Request.Header.Set("Authorization", "Bearer inbound-secret")
			c.Request.Header.Set("X-Api-Key", "inbound-anthropic-secret")

			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"input_tokens":23}`)),
			}}
			body := []byte(`{"model":"` + tt.model + `","messages":[{"role":"user","content":"hi"}]}`)

			err := newCindyNativeMessagesService(upstream).ForwardCindyAnthropicCountTokens(
				context.Background(), c, newCindyNativeMessagesAccount(), body, tt.model,
			)

			require.NoError(t, err)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.JSONEq(t, `{"input_tokens":23}`, recorder.Body.String())
			require.Equal(t, "https://api.laxarouter.ai/v1/messages/count_tokens", upstream.lastReq.URL.String())
			require.Equal(t, "Bearer cindy-secret", upstream.lastReq.Header.Get("Authorization"))
			require.Empty(t, upstream.lastReq.Header.Get("x-api-key"))
			require.Equal(t, "anthropic/claude-opus-5", gjson.GetBytes(upstream.lastBody, "model").String())
			require.False(t, bytes.Contains(upstream.lastBody, []byte("gpt-5.4")))
		})
	}
}

func TestForwardCindyAnthropicCountTokensRejectsUnverifiedMessagesModel(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)

	err := newCindyNativeMessagesService(&anthropicHTTPUpstreamRecorder{}).ForwardCindyAnthropicCountTokens(
		context.Background(), c, newCindyNativeMessagesAccount(),
		[]byte(`{"model":"seed-2.1-pro","messages":[]}`), "seed-2.1-pro",
	)

	require.ErrorContains(t, err, "not verified for native Messages")
}

func TestForwardCindyAnthropicCountTokensBudgetErrorIsSanitizedForFailoverWithoutImmediateMarker(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"error":{"type":"budget_exceeded","code":"429","message":"sensitive upstream budget detail"}}`,
		)),
	}}
	repo := &cindyNativeCountTokensBalanceRepo{}
	gatewayService := newCindyNativeMessagesService(upstream)
	gatewayService.rateLimitService = NewRateLimitService(repo, nil, &config.Config{}, nil, nil)

	err := gatewayService.ForwardCindyAnthropicCountTokens(
		context.Background(), c, newCindyNativeMessagesAccount(),
		[]byte(`{"model":"claude-opus-5","messages":[]}`), "claude-opus-5",
	)

	var failoverErr *UpstreamFailoverError
	require.Error(t, err)
	require.True(t, errors.As(err, &failoverErr))
	require.True(t, failoverErr.CindyBalanceInsufficient)
	require.True(t, failoverErr.ShouldRetryNextAccount())
	require.False(t, repo.marked, "the first exact signal must wait for independent confirmation")
	require.Empty(t, recorder.Body.String(), "failover must occur before a client response is written")
	require.NotContains(t, string(failoverErr.ResponseBody), "budget_exceeded")
	require.NotContains(t, string(failoverErr.ResponseBody), "sensitive upstream budget detail")
}

func TestValidAnthropicCountTokensResponse(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "zero", body: `{"input_tokens":0}`, want: true},
		{name: "positive integer", body: `{"input_tokens":23}`, want: true},
		{name: "missing", body: `{}`, want: false},
		{name: "string", body: `{"input_tokens":"23"}`, want: false},
		{name: "negative", body: `{"input_tokens":-1}`, want: false},
		{name: "decimal", body: `{"input_tokens":1.5}`, want: false},
		{name: "exponent", body: `{"input_tokens":1e2}`, want: false},
		{name: "malformed", body: `{"input_tokens":`, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, validAnthropicCountTokensResponse([]byte(tt.body)))
		})
	}
}
