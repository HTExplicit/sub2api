//go:build unit

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

func officialHTTPRetryAccount() *service.Account {
	return &service.Account{
		ID: 45001, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"pool_mode": true},
	}
}

func TestOpenAIOfficialHTTPRetryPolicy(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*service.Account)
		failure   *service.UpstreamFailoverError
		wantCount int
	}{
		{
			name:      "default three retries",
			failure:   &service.UpstreamFailoverError{StatusCode: http.StatusForbidden, RetryableOnSameAccount: true},
			wantCount: 3,
		},
		{
			name:      "explicit zero stays disabled",
			configure: func(account *service.Account) { account.Credentials["pool_mode_retry_count"] = 0 },
			failure:   &service.UpstreamFailoverError{StatusCode: http.StatusForbidden, RetryableOnSameAccount: true},
		},
		{
			name:      "capacity signal overrides the default status list",
			failure:   &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, RetryableOnSameAccount: true, RequestScopedTransient: true},
			wantCount: 3,
		},
		{
			name:      "non pool capacity keeps official default budget",
			configure: func(account *service.Account) { account.Credentials["pool_mode"] = false },
			failure:   &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, RetryableOnSameAccount: true, RequestScopedTransient: true},
			wantCount: 3,
		},
		{
			name:      "classified transient 400 keeps official retry flag",
			failure:   &service.UpstreamFailoverError{StatusCode: http.StatusBadRequest, RetryableOnSameAccount: true},
			wantCount: 3,
		},
		{
			name:    "ordinary 503 does not gain implicit retries",
			failure: &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable},
		},
		{
			name:    "HTTP2 transport failure does not gain implicit retries",
			failure: &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway, Reason: service.OpenAITransientTransportFailureReason},
		},
		{
			name:      "error retry cap stays authoritative",
			failure:   &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, RetryableOnSameAccount: true, SameAccountRetryMax: 1},
			wantCount: 1,
		},
		{
			name:    "expired error deadline does not restart",
			failure: &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, RetryableOnSameAccount: true, SameAccountRetryDeadline: time.Now().Add(-time.Minute)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := officialHTTPRetryAccount()
			if tc.configure != nil {
				tc.configure(account)
			}
			state := newOpenAIFailoverRetryState()
			cooldown := &openAIRetryCooldownRecorder{}
			ctx := service.WithOpenAIOfficialHTTPFailover(context.Background())
			for i := 0; i < tc.wantCount; i++ {
				require.Equal(t, openAIFailoverRetryReselect, state.HandleHTTP(ctx, cooldown, account, "gpt-6-astra", tc.failure, true, 0, "test"))
			}
			require.Equal(t, openAIFailoverRetrySwitchAccount, state.HandleHTTP(ctx, cooldown, account, "gpt-6-astra", tc.failure, true, 0, "test"))
			require.Equal(t, tc.wantCount, state.sameAccountRetryCount[account.ID])
			require.Zero(t, cooldown.calls, "official HTTP retries must not install the downstream retry-exhausted cooldown")
		})
	}
}

func TestOpenAIOfficialHTTPRetryKeepsPerAccountBudget(t *testing.T) {
	ctx := service.WithOpenAIOfficialHTTPFailover(context.Background())
	first := officialHTTPRetryAccount()
	first.Credentials["pool_mode_retry_count"] = 1
	second := officialHTTPRetryAccount()
	second.ID++
	second.Credentials["pool_mode_retry_count"] = 1
	failure := &service.UpstreamFailoverError{StatusCode: http.StatusForbidden, RetryableOnSameAccount: true}
	state := newOpenAIFailoverRetryState()
	for _, account := range []*service.Account{first, second} {
		require.Equal(t, openAIFailoverRetryReselect, state.HandleHTTP(ctx, nil, account, "model", failure, true, 0, "test"))
	}
	for _, account := range []*service.Account{first, second} {
		require.Equal(t, openAIFailoverRetrySwitchAccount, state.HandleHTTP(ctx, nil, account, "model", failure, true, 0, "test"))
		require.Equal(t, 1, state.sameAccountRetryCount[account.ID])
	}
}

func TestOpenAIOfficialHTTPRetryScopeIsolation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		marked    bool
		managed   bool
		configure func(*service.Account)
		want      openAIFailoverRetryAction
	}{
		{name: "marked ordinary API key reselects", marked: true, want: openAIFailoverRetryReselect},
		{name: "unmarked API key remains exact", want: openAIFailoverRetrySameAccount},
		{name: "managed route remains exact", marked: true, managed: true, want: openAIFailoverRetrySameAccount},
		{name: "OAuth keeps its current policy", marked: true, configure: func(account *service.Account) { account.Type = service.AccountTypeOAuth }, want: openAIFailoverRetrySwitchAccount},
		{name: "Cindy keeps exact retries", marked: true, configure: func(account *service.Account) { account.Platform = service.PlatformCindy }, want: openAIFailoverRetrySameAccount},
		{name: "legacy Laxa keeps exact retries", marked: true, configure: func(account *service.Account) { account.Credentials["base_url"] = "https://api.laxarouter.ai" }, want: openAIFailoverRetrySameAccount},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := officialHTTPRetryAccount()
			if tc.configure != nil {
				tc.configure(account)
			}
			ctx := context.Background()
			if tc.marked {
				ctx = service.WithOpenAIOfficialHTTPFailover(ctx)
			}
			if tc.managed {
				ctx = service.WithManagedModelRequest(ctx, &service.ManagedModelRequest{GroupID: 17})
			}
			failure := &service.UpstreamFailoverError{StatusCode: http.StatusForbidden, RetryableOnSameAccount: true}
			require.Equal(t, tc.want, newOpenAIFailoverRetryState().HandleHTTP(ctx, nil, account, "model", failure, true, 0, "test"))
		})
	}

	// Even a marked context cannot change consumers which still call Handle.
	marked := service.WithOpenAIOfficialHTTPFailover(context.Background())
	failure := &service.UpstreamFailoverError{StatusCode: http.StatusForbidden, RetryableOnSameAccount: true}
	require.Equal(t, openAIFailoverRetrySameAccount, newOpenAIFailoverRetryState().Handle(marked, nil, officialHTTPRetryAccount(), "model", failure, true, 0, "test"))
}

func TestOpenAIOfficialHTTPRetryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(service.WithOpenAIOfficialHTTPFailover(context.Background()))
	cancel()
	state := newOpenAIFailoverRetryState()
	cooldown := &openAIRetryCooldownRecorder{}
	failure := &service.UpstreamFailoverError{StatusCode: http.StatusForbidden, RetryableOnSameAccount: true}
	require.Equal(t, openAIFailoverRetryCanceled, state.HandleHTTP(ctx, cooldown, officialHTTPRetryAccount(), "model", failure, true, 0, "test"))
	require.Empty(t, state.sameAccountRetryCount)
	require.Zero(t, cooldown.calls)
}

func TestOpenAIOfficialHTTPAccountScheduleModelUsesActualUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	account := officialHTTPRetryAccount()
	account.Credentials["model_mapping"] = map[string]any{"client-model": "mapped-model"}
	require.Equal(t, "mapped-model", openAIAccountScheduleModel(c, account, "client-model", false, nil))
	c.Set(service.OpsUpstreamModelKey, " observed-model ")
	require.Equal(t, "observed-model", openAIAccountScheduleModel(c, account, "client-model", false, nil))
	require.Equal(t, "result-model", openAIAccountScheduleModel(c, account, "client-model", false, &service.OpenAIForwardResult{UpstreamModel: " result-model "}))
	c.Set(service.OpsUpstreamModelKey, " ")
	require.Equal(t, "mapped-model", openAIAccountScheduleModel(c, account, "client-model", false, &service.OpenAIForwardResult{}))
}
