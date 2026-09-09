//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOpenAIOfficialHTTPTransportState(t *testing.T) {
	for _, tt := range []struct {
		name       string
		err        error
		persistent bool
		terminal   bool
	}{
		{name: "transient H2", err: errors.New("http2: client connection lost")},
		{name: "persistent proxy", err: errors.New("proxy authentication required"), persistent: true},
		{name: "client canceled", err: context.Canceled, terminal: true},
		{name: "plugin already sent", err: &PluginTransportError{Code: "SENT", Message: "connection refused", RequestSent: true}, terminal: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := WithOpenAIOfficialHTTPFailover(context.Background())
			repo := &openaiTransportAccountRepoStub{}
			svc := &OpenAIGatewayService{accountRepo: repo}
			account := &Account{ID: 401, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			c, rec := newOpenAITransportErrTestContext()
			before := time.Now()
			err := svc.handleOpenAIUpstreamTransportError(ctx, c, account, tt.err, false)
			var failoverErr *UpstreamFailoverError
			if tt.terminal {
				require.ErrorIs(t, err, tt.err)
				require.False(t, errors.As(err, &failoverErr))
			} else {
				require.ErrorAs(t, err, &failoverErr)
				require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
				require.False(t, failoverErr.RetryableOnSameAccount)
				svc.CooldownOpenAIRetryExhausted(ctx, account, "gpt-6-astra", failoverErr)
			}
			require.Empty(t, rec.Body.String(), "the HTTP handler owns the response")
			if tt.persistent {
				require.Len(t, repo.tempUnschedCalls, 1)
				require.WithinDuration(t, before.Add(10*time.Minute), repo.tempUnschedCalls[0].until, time.Second)
				require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
			} else {
				require.Empty(t, repo.tempUnschedCalls)
				require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
			}
			require.Empty(t, svc.getOpenAIAccountModelTransientState().entries)
		})
	}
}

func TestOpenAIOfficialHTTP503StreakHasNoExhaustionPenalty(t *testing.T) {
	ctx := WithOpenAIOfficialHTTPFailover(context.Background())
	repo := &errorPolicyRepoStub{}
	svc := &OpenAIGatewayService{rateLimitService: NewRateLimitService(repo, nil, &config.Config{}, nil, nil)}
	account := &Account{ID: 402, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"pool_mode": true}}
	model := "gpt-6-astra"
	body := []byte(`{"error":{"message":"upstream unavailable"}}`)
	state := svc.getOpenAIAccountModelTransientState()
	key, ok := openAIAccountModelTransientKey(account.ID, model)
	require.True(t, ok)
	for i, wantCooldown := range []time.Duration{0, 10 * time.Second, 45 * time.Second} {
		before := time.Now()
		require.False(t, svc.handleOpenAIAccountUpstreamError(ctx, account, http.StatusServiceUnavailable, nil, body, model))
		svc.CooldownOpenAIRetryExhausted(ctx, account, model, &UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable})
		entry := state.entries[key]
		require.Equal(t, i+1, entry.failureStreak, "only the service may count this failure")
		if wantCooldown == 0 {
			require.True(t, entry.blockUntil.IsZero())
		} else {
			require.WithinDuration(t, before.Add(wantCooldown), entry.blockUntil, time.Second)
		}
	}
	require.Zero(t, repo.tempCalls)
	require.Zero(t, repo.setErrCalls)
}

func TestOpenAIOfficialHTTPPoolAuthQuotaRules(t *testing.T) {
	for _, statusCode := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			for _, explicitRule := range []bool{false, true} {
				ctx := WithOpenAIOfficialHTTPFailover(context.Background())
				repo := &errorPolicyRepoStub{}
				rateLimits := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
				svc := &OpenAIGatewayService{rateLimitService: rateLimits}
				account := &Account{
					ID: 403, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
					Credentials: map[string]any{"pool_mode": true},
				}
				if explicitRule {
					account.Credentials["temp_unschedulable_enabled"] = true
					account.Credentials["temp_unschedulable_rules"] = []any{map[string]any{
						"error_code": float64(statusCode), "keywords": []any{"temporary test rejection"}, "duration_minutes": float64(3),
					}}
				}
				body := []byte(`{"error":{"message":"temporary test rejection"}}`)
				handled := svc.handleOpenAIAccountUpstreamError(ctx, account, statusCode, nil, body, "gpt-6-astra")
				wantRule := explicitRule && statusCode != http.StatusUnauthorized
				require.Equal(t, wantRule, handled)
				if wantRule {
					require.Len(t, repo.modelRateLimitCalls, 1)
					require.Equal(t, "gpt-6-astra", repo.modelRateLimitCalls[0].scope)
				} else {
					require.Empty(t, repo.modelRateLimitCalls)
				}
				svc.CooldownOpenAIRetryExhausted(ctx, account, "gpt-6-astra", &UpstreamFailoverError{StatusCode: statusCode})
				require.False(t, svc.isOpenAIAccountRuntimeBlocked(account), "pool exhaustion must not add an account-wide cooldown")
				require.Zero(t, repo.tempCalls)
				require.Zero(t, repo.setErrCalls)
			}
		})
	}
}

func TestOpenAIOfficialHTTPPipelineRetryMarkers(t *testing.T) {
	processingBody := []byte(`{"error":{"type":"invalid_request_error","message":"An error occurred while processing your request. You can retry your request."}}`)
	capacityBody := []byte(`{"error":{"code":"server_is_overloaded","message":"Server is overloaded"}}`)
	for _, tt := range []struct {
		name   string
		status int
		body   []byte
	}{
		{name: "transient 400", status: http.StatusBadRequest, body: processingBody},
		{name: "capacity 503", status: http.StatusServiceUnavailable, body: capacityBody},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := WithOpenAIOfficialHTTPFailover(context.Background())
			account := &Account{ID: 404, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"pool_mode": true}}
			svc := &OpenAIGatewayService{}
			resp := &http.Response{StatusCode: tt.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(tt.body)))}
			failure := svc.failoverOpenAIUpstreamHTTPError(ctx, nil, account, resp, tt.body, "gpt-6-astra")
			require.NotNil(t, failure)
			require.True(t, failure.RetryableOnSameAccount, "restore the upstream error-specific retry marker")
			require.Empty(t, classifyOpenAIRequestRejection(tt.status, "", tt.body), "residual request rejection must not swallow the official transient error")
		})
	}
	account := &Account{ID: 405, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"pool_mode": true}}
	require.False(t, openAIHTTPPoolRetryable(context.Background(), account, http.StatusBadRequest, "", processingBody, false), "unmarked paths retain their current policy")
	require.False(t, openAIHTTPPoolRetryable(WithOpenAIOfficialHTTPFailover(context.Background()), account, http.StatusBadRequest, "", processingBody, true), "an explicit rule that stopped scheduling still takes precedence")
}
