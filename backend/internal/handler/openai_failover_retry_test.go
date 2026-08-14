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
		{name: "default 401 uses configured count", failoverErr: &service.UpstreamFailoverError{StatusCode: http.StatusUnauthorized, RetryableOnSameAccount: true}, want: 10},
		{name: "default 403 uses configured count", failoverErr: &service.UpstreamFailoverError{StatusCode: http.StatusForbidden, RetryableOnSameAccount: true}, want: 10},
		{name: "default 429 uses configured count", failoverErr: &service.UpstreamFailoverError{StatusCode: http.StatusTooManyRequests, RetryableOnSameAccount: true}, want: 10},
		{name: "408 outside configured list", failoverErr: &service.UpstreamFailoverError{StatusCode: http.StatusRequestTimeout, RetryableOnSameAccount: true}, want: 0},
		{name: "500 outside configured list", failoverErr: &service.UpstreamFailoverError{StatusCode: http.StatusInternalServerError, RetryableOnSameAccount: true}, want: 0},
		{name: "503 outside configured list", failoverErr: &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, RetryableOnSameAccount: true}, want: 0},
		{name: "transient transport is not an implicit replay", failoverErr: &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway, Reason: service.OpenAITransientTransportFailureReason, RetryableOnSameAccount: true}, allowTransport: true, want: 0},
		{name: "persistent transport is not an implicit replay", failoverErr: &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway, Reason: service.OpenAIPersistentTransportFailureReason, RetryableOnSameAccount: true}, allowTransport: true, want: 0},
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

func TestOpenAISameAccountRetryLimit_RequiresPoolModeAndExplicitStatus(t *testing.T) {
	account := &service.Account{ID: 41002, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey}
	require.Zero(t, openAISameAccountRetryLimit(
		account,
		&service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, RetryableOnSameAccount: true},
		true,
	))

	account = openAIRetryTestAccount(4)
	account.Credentials["pool_mode_retry_status_codes"] = []any{float64(http.StatusServiceUnavailable)}
	require.Equal(t, 4, openAISameAccountRetryLimit(
		account,
		&service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, RetryableOnSameAccount: true},
		true,
	))
}

func TestOpenAISameAccountRetryLimit_CindyBudget429AlwaysSwitchesAccount(t *testing.T) {
	account := openAIRetryTestAccount(10)
	account.Credentials["base_url"] = "https://api.laxarouter.ai"
	account.Credentials["api_key"] = "test-key"

	require.Zero(t, openAISameAccountRetryLimit(
		account,
		&service.UpstreamFailoverError{
			StatusCode:             http.StatusTooManyRequests,
			ResponseBody:           []byte(`{"error":{"message":"ExceededBudget: User=aigw:v1:cindy:fixture-account over budget. Spend=3.0533505, Budget=3.0","type":"budget_exceeded","param":null,"code":"429"}}`),
			RetryableOnSameAccount: true,
		},
		true,
	))
	require.Equal(t, 10, openAISameAccountRetryLimit(
		account,
		&service.UpstreamFailoverError{
			StatusCode:             http.StatusTooManyRequests,
			ResponseBody:           []byte(`{"error":{"type":"rate_limit_error"}}`),
			RetryableOnSameAccount: true,
		},
		true,
	))
}

func TestOpenAISameAccountRetryLimit_OAuthKeepsBoundedTransientRetry(t *testing.T) {
	account := &service.Account{ID: 41003, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth}
	require.Equal(t, 1, openAISameAccountRetryLimit(
		account,
		&service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable},
		false,
	))
	require.Equal(t, 1, openAISameAccountRetryLimit(
		account,
		&service.UpstreamFailoverError{StatusCode: http.StatusBadGateway, Reason: service.OpenAITransientTransportFailureReason},
		true,
	))
	require.Zero(t, openAISameAccountRetryLimit(
		account,
		&service.UpstreamFailoverError{StatusCode: http.StatusBadGateway, Reason: service.OpenAITransientTransportFailureReason},
		false,
	))
	require.Zero(t, openAISameAccountRetryLimit(
		account,
		&service.UpstreamFailoverError{StatusCode: http.StatusTooManyRequests},
		true,
	))
}

func TestOpenAIFailoverRetryState_UsesConfiguredBudgetThenCoolsAndSwitches(t *testing.T) {
	state := newOpenAIFailoverRetryState()
	account := openAIRetryTestAccount(2)
	account.Credentials["pool_mode_retry_status_codes"] = []any{float64(http.StatusServiceUnavailable)}
	failoverErr := &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, RetryableOnSameAccount: true}
	cooldown := &openAIRetryCooldownRecorder{}

	action := state.Handle(context.Background(), cooldown, account, "gpt-5.1", failoverErr, true, 0, "test")
	require.Equal(t, openAIFailoverRetrySameAccount, action)
	require.Zero(t, cooldown.calls)

	action = state.Handle(context.Background(), cooldown, account, "gpt-5.1", failoverErr, true, 0, "test")
	require.Equal(t, openAIFailoverRetrySameAccount, action)
	require.Zero(t, cooldown.calls)

	action = state.Handle(context.Background(), cooldown, account, "gpt-5.1", failoverErr, true, 0, "test")
	require.Equal(t, openAIFailoverRetrySwitchAccount, action)
	require.Equal(t, 1, cooldown.calls)
}

func TestOpenAIFailoverRetryState_DefaultAuthStatusUsesPoolBudget(t *testing.T) {
	state := newOpenAIFailoverRetryState()
	account := openAIRetryTestAccount(1)
	failoverErr := &service.UpstreamFailoverError{StatusCode: http.StatusTooManyRequests, RetryableOnSameAccount: true}
	cooldown := &openAIRetryCooldownRecorder{}

	action := state.Handle(context.Background(), cooldown, account, "gpt-5.1", failoverErr, true, 0, "test")
	require.Equal(t, openAIFailoverRetrySameAccount, action)
	require.Zero(t, cooldown.calls)
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
