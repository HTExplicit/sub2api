package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type openAIRetryCooldownRecorder struct {
	calls int
}

func (r *openAIRetryCooldownRecorder) CooldownOpenAIRetryExhausted(
	context.Context,
	*service.Account,
	string,
	*service.UpstreamFailoverError,
) {
	r.calls++
}

func openAIRetryTestAccount(retryCount int) *service.Account {
	return &service.Account{
		ID:       41001,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		Credentials: map[string]any{
			"pool_mode":             true,
			"pool_mode_retry_count": retryCount,
		},
	}
}

func TestOpenAISameAccountRetryLimit(t *testing.T) {
	account := openAIRetryTestAccount(10)
	tests := []struct {
		name           string
		failoverErr    *service.UpstreamFailoverError
		allowTransport bool
		want           int
	}{
		{name: "401 never retries", failoverErr: &service.UpstreamFailoverError{StatusCode: http.StatusUnauthorized, RetryableOnSameAccount: true}, want: 0},
		{name: "403 never retries", failoverErr: &service.UpstreamFailoverError{StatusCode: http.StatusForbidden, RetryableOnSameAccount: true}, want: 0},
		{name: "429 never retries", failoverErr: &service.UpstreamFailoverError{StatusCode: http.StatusTooManyRequests, RetryableOnSameAccount: true}, want: 0},
		{name: "408 capped at one", failoverErr: &service.UpstreamFailoverError{StatusCode: http.StatusRequestTimeout}, want: 1},
		{name: "500 capped at one", failoverErr: &service.UpstreamFailoverError{StatusCode: http.StatusInternalServerError}, want: 1},
		{name: "503 capped at one", failoverErr: &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, RetryableOnSameAccount: true}, want: 1},
		{name: "transient transport text endpoint", failoverErr: &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway, Reason: service.OpenAITransientTransportFailureReason}, allowTransport: true, want: 1},
		{name: "transient transport media endpoint retries once", failoverErr: &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway, Reason: service.OpenAITransientTransportFailureReason}, allowTransport: true, want: 1},
		{name: "persistent transport retries once", failoverErr: &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway, Reason: service.OpenAIPersistentTransportFailureReason}, allowTransport: true, want: 1},
		{name: "generic retryable 400 still switches immediately", failoverErr: &service.UpstreamFailoverError{StatusCode: http.StatusBadRequest, RetryableOnSameAccount: true}, want: 0},
		{name: "non retryable 400", failoverErr: &service.UpstreamFailoverError{StatusCode: http.StatusBadRequest}, want: 0},
		{name: "stop action", failoverErr: &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, NextAccountAction: service.NextAccountStop}, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, openAISameAccountRetryLimit(account, tt.failoverErr, tt.allowTransport))
		})
	}
}

func TestOpenAISameAccountRetryLimit_StandardTransientDoesNotRequirePoolMode(t *testing.T) {
	account := &service.Account{ID: 41002, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey}
	require.Equal(t, 1, openAISameAccountRetryLimit(
		account,
		&service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable},
		true,
	))
	require.Equal(t, 1, openAISameAccountRetryLimit(
		account,
		&service.UpstreamFailoverError{
			StatusCode: http.StatusBadGateway,
			Reason:     service.OpenAITransientTransportFailureReason,
		},
		true,
	))
}

func TestOpenAIFailoverRetryState_RetriesOnceThenCoolsAndSwitches(t *testing.T) {
	state := newOpenAIFailoverRetryState()
	account := openAIRetryTestAccount(10)
	failoverErr := &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway}
	cooldown := &openAIRetryCooldownRecorder{}

	action := state.Handle(context.Background(), cooldown, account, "gpt-5.1", failoverErr, true, 0, "test")
	require.Equal(t, openAIFailoverRetrySameAccount, action)
	require.Zero(t, cooldown.calls)

	action = state.Handle(context.Background(), cooldown, account, "gpt-5.1", failoverErr, true, 0, "test")
	require.Equal(t, openAIFailoverRetrySwitchAccount, action)
	require.Equal(t, 1, cooldown.calls)
}

func TestOpenAIFailoverRetryState_AuthFailureCoolsWithoutSameAccountRetry(t *testing.T) {
	state := newOpenAIFailoverRetryState()
	account := openAIRetryTestAccount(10)
	failoverErr := &service.UpstreamFailoverError{StatusCode: http.StatusTooManyRequests, RetryableOnSameAccount: true}
	cooldown := &openAIRetryCooldownRecorder{}

	action := state.Handle(context.Background(), cooldown, account, "gpt-5.1", failoverErr, true, 0, "test")
	require.Equal(t, openAIFailoverRetrySwitchAccount, action)
	require.Equal(t, 1, cooldown.calls)
}

func TestOpenAIResponseHasSemanticWriteIgnoresKeepaliveOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name  string
		start func(*gin.Context, time.Duration) func()
	}{
		{name: "compact SSE", start: service.StartOpenAICompactSSEKeepalive},
		{name: "images JSON", start: service.StartOpenAIImagesJSONKeepalive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			if tc.name == "compact SSE" {
				service.MarkOpenAICompactClientStream(c)
			}
			stop := tc.start(c, time.Millisecond)
			t.Cleanup(stop)
			require.Eventually(t, c.Writer.Written, 100*time.Millisecond, time.Millisecond)
			require.False(t, openAIResponseHasSemanticWrite(c))

			_, err := c.Writer.Write([]byte(`{"ok":true}`))
			require.NoError(t, err)
			require.True(t, openAIResponseHasSemanticWrite(c))
		})
	}
}
