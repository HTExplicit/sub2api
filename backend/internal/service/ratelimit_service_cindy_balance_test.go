//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRateLimitServiceCindy402NeverMarksBalanceInsufficient(t *testing.T) {
	for _, poolMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "pool"}[poolMode], func(t *testing.T) {
			repo := &cindyRateLimitAccountRepoStub{}
			blocker := &runtimeBlockRecorder{}
			svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			svc.SetAccountRuntimeBlocker(blocker)
			account := newCindyRateLimitAccount(8101, poolMode)

			shouldDisable := svc.HandleUpstreamError(context.Background(), account, http.StatusPaymentRequired, http.Header{}, []byte(`{"error":{"type":"budget_exceeded","code":"429"}}`))

			require.False(t, shouldDisable)
			require.Zero(t, repo.markCalls)
			require.Zero(t, repo.markChanged)
			require.Zero(t, repo.setErrorCalls)
			require.Nil(t, account.CindyBalanceInsufficientAt)
			require.Empty(t, blocker.accounts)
		})
	}
}

func TestHandleUpstreamErrorHTTP400EventShapeDoesNotMarkCindyBalance(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "response_failed", body: `{"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429"}}}`},
		{name: "error_event", body: `{"type":"error","error":{"type":"budget_exceeded","code":"429"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &cindyRateLimitAccountRepoStub{}
			svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			account := newCindyRateLimitAccount(8103, true)

			shouldDisable := svc.HandleUpstreamError(
				context.Background(), account, http.StatusBadRequest, http.Header{}, []byte(tc.body),
			)

			require.False(t, shouldDisable)
			require.Zero(t, repo.markCalls)
			require.Zero(t, repo.markChanged)
			require.Nil(t, account.CindyBalanceInsufficientAt)
		})
	}
}

func TestRateLimitServiceCindyBudget429RequiresConfirmationBeforePoolModeSkip(t *testing.T) {
	for _, poolMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "pool"}[poolMode], func(t *testing.T) {
			repo := &cindyRateLimitAccountRepoStub{}
			blocker := &runtimeBlockRecorder{}
			svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			svc.SetAccountRuntimeBlocker(blocker)
			account := newCindyRateLimitAccount(8102, poolMode)
			body := []byte(exactCindyBudgetExceededBody)

			shouldDisable := svc.HandleUpstreamError(context.Background(), account, http.StatusTooManyRequests, http.Header{}, body)

			require.True(t, shouldDisable)
			require.Zero(t, repo.markCalls)
			require.Zero(t, repo.markChanged)
			require.Zero(t, repo.setErrorCalls)
			require.Nil(t, account.CindyBalanceInsufficientAt)
			require.Empty(t, blocker.reasons)
		})
	}
}

func TestRateLimitServiceCindyBalanceMarkerDoesNotMatchOtherErrors(t *testing.T) {
	t.Run("non_cindy_402_keeps_existing_behavior", func(t *testing.T) {
		repo := &cindyRateLimitAccountRepoStub{}
		svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		account := &Account{ID: 8201, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.openai.com"}}

		require.True(t, svc.HandleUpstreamError(context.Background(), account, http.StatusPaymentRequired, http.Header{}, nil))
		require.Zero(t, repo.markCalls)
		require.Equal(t, 1, repo.setErrorCalls)
	})

	t.Run("cindy_non_402_is_not_marked", func(t *testing.T) {
		repo := &cindyRateLimitAccountRepoStub{}
		svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		account := newCindyRateLimitAccount(8202, true)

		require.False(t, svc.HandleUpstreamError(context.Background(), account, http.StatusForbidden, http.Header{}, nil))
		require.Zero(t, repo.markCalls)
		require.Nil(t, account.CindyBalanceInsufficientAt)
	})

	t.Run("ordinary_cindy_429_is_not_marked", func(t *testing.T) {
		repo := &cindyRateLimitAccountRepoStub{}
		svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		account := newCindyRateLimitAccount(8203, true)

		require.False(t, svc.HandleUpstreamError(
			context.Background(), account, http.StatusTooManyRequests, http.Header{},
			[]byte(`{"error":{"type":"rate_limit_error","message":"too many requests"}}`),
		))
		require.Zero(t, repo.markCalls)
		require.Nil(t, account.CindyBalanceInsufficientAt)
	})

	t.Run("incomplete_budget_429_is_not_marked", func(t *testing.T) {
		repo := &cindyRateLimitAccountRepoStub{}
		svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		account := newCindyRateLimitAccount(8204, true)

		require.False(t, svc.HandleUpstreamError(
			context.Background(), account, http.StatusTooManyRequests, http.Header{},
			[]byte(`{"error":{"type":"budget_exceeded"}}`),
		))
		require.Zero(t, repo.markCalls)
		require.Nil(t, account.CindyBalanceInsufficientAt)
	})
}

func TestRateLimitServiceConcurrentCindySignalsDoNotPersistBeforeConfirmation(t *testing.T) {
	repo := &cindyRateLimitAccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)

	const requests = 24
	var wg sync.WaitGroup
	results := make(chan bool, requests)
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			account := newCindyRateLimitAccount(8301, true)
			results <- svc.HandleUpstreamError(context.Background(), account, http.StatusTooManyRequests, http.Header{}, []byte(exactCindyBudgetExceededBody))
		}()
	}
	wg.Wait()
	close(results)
	for shouldDisable := range results {
		require.True(t, shouldDisable)
	}

	require.Zero(t, repo.markCalls)
	require.Zero(t, repo.markChanged)
}

func TestOpenAIGatewayCindyBudget429FailsOverWithoutImmediatePermanentBlock(t *testing.T) {
	repo := &cindyRateLimitAccountRepoStub{}
	rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	gateway := &OpenAIGatewayService{rateLimitService: rateLimitService}
	rateLimitService.SetAccountRuntimeBlocker(gateway)
	account := newCindyRateLimitAccount(8401, true)
	body := []byte(exactCindyBudgetExceededBody)

	shouldDisable := gateway.handleOpenAIAccountUpstreamError(
		context.Background(), account, http.StatusTooManyRequests, http.Header{}, body, "gpt-5.6-sol",
	)

	require.True(t, shouldDisable)
	require.Zero(t, repo.markCalls)
	require.Nil(t, account.CindyBalanceInsufficientAt)
	require.False(t, gateway.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.6-sol"))
}

func TestOpenAIGatewayCindyTerminalEventsFailOverWithoutImmediatePermanentBlock(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "response_failed", body: `{"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429"}}}`},
		{name: "error_event", body: `{"type":"error","error":{"type":"budget_exceeded","code":"429"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &cindyRateLimitAccountRepoStub{}
			rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			gateway := &OpenAIGatewayService{rateLimitService: rateLimitService}
			rateLimitService.SetAccountRuntimeBlocker(gateway)
			account := newCindyRateLimitAccount(8451, true)

			recognized := gateway.handleCindyBalanceTerminalEvent(
				context.Background(), account, http.Header{}, []byte(tc.body), "openai/gpt-5.6-luna",
			)

			require.True(t, recognized)
			require.Zero(t, repo.markCalls)
			require.Nil(t, account.CindyBalanceInsufficientAt)
			require.False(t, gateway.isOpenAIAccountRequestRuntimeBlocked(account, "openai/gpt-5.6-luna"))
		})
	}
}

func TestOpenAIGatewayCindyWSTerminalEventsUseProvisionalCentralHandler(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		handle func(*OpenAIGatewayService, *Account, []byte)
	}{
		{
			name: "response_failed",
			body: `{"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429"}}}`,
			handle: func(gateway *OpenAIGatewayService, account *Account, body []byte) {
				gateway.handleOpenAIWSTerminalTransientFailure(context.Background(), account, "openai/gpt-5.6-luna", nil, body)
			},
		},
		{
			name: "error_event",
			body: `{"type":"error","error":{"type":"budget_exceeded","code":"429"}}`,
			handle: func(gateway *OpenAIGatewayService, account *Account, body []byte) {
				gateway.handleOpenAIWSErrorEventTransientFailure(context.Background(), account, "openai/gpt-5.6-luna", nil, body)
			},
		},
	}
	for index, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &cindyRateLimitAccountRepoStub{}
			rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			gateway := &OpenAIGatewayService{rateLimitService: rateLimitService}
			rateLimitService.SetAccountRuntimeBlocker(gateway)
			account := newCindyRateLimitAccount(int64(8460+index), true)

			tc.handle(gateway, account, []byte(tc.body))

			require.Zero(t, repo.markCalls)
			require.Nil(t, account.CindyBalanceInsufficientAt)
			require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
		})
	}
}

func TestOpenAIGatewayCindyImagesTerminalFailureForcesAccountSwitch(t *testing.T) {
	repo := &cindyRateLimitAccountRepoStub{}
	rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	gateway := &OpenAIGatewayService{rateLimitService: rateLimitService}
	rateLimitService.SetAccountRuntimeBlocker(gateway)
	account := newCindyRateLimitAccount(8470, true)
	account.Extra = map[string]any{
		"pool_mode":                    true,
		"pool_mode_retry_status_codes": []any{float64(http.StatusTooManyRequests)},
	}
	payload := []byte(`{"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429","message":"request failed"}}}`)
	upstreamErr := openAIImagesUpstreamErrorFromSSEPayload(payload)
	require.NotNil(t, upstreamErr)

	err := gateway.handleOpenAIImagesOAuthResponseError(
		context.Background(), nil, account, "gpt-image-2", "https://example.invalid/v1/responses",
		&http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}, 0, upstreamErr,
	)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.True(t, failoverErr.CindyBalanceInsufficient)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.Zero(t, repo.markCalls)
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIGatewayCindyIndefiniteBlockDominatesLaterCooldown(t *testing.T) {
	gateway := &OpenAIGatewayService{}
	account := newCindyRateLimitAccount(8454, true)

	gateway.BlockAccountScheduling(account, time.Time{}, "cindy_balance_insufficient")
	gateway.BlockAccountScheduling(account, time.Now().Add(time.Hour), "429")

	value, ok := gateway.openaiAccountRuntimeBlockUntil.Load(account.ID)
	require.True(t, ok)
	until, ok := value.(time.Time)
	require.True(t, ok)
	require.True(t, until.IsZero(), "a finite cooldown must never weaken the Cindy fail-closed sentinel")
	require.True(t, gateway.isOpenAIAccountRuntimeBlocked(account))
}

func TestAccountTestServiceCindyBalanceDoesNotStartBackgroundProbe(t *testing.T) {
	repo := &cindyRateLimitAccountRepoStub{}
	rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	gateway := &OpenAIGatewayService{rateLimitService: rateLimitService}
	rateLimitService.SetAccountRuntimeBlocker(gateway)
	accountTest := &AccountTestService{
		accountRepo:          repo,
		openAIGatewayService: gateway,
	}
	account := newCindyRateLimitAccount(8453, true)

	require.True(t, accountTest.markCindyBalanceInsufficientFromTest(
		context.Background(), account, http.StatusOK,
		[]byte(`{"type":"error","error":{"type":"budget_exceeded","code":"429"}}`),
	))
	require.Zero(t, repo.markCalls)
	require.Nil(t, account.CindyBalanceInsufficientAt)
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
}

func TestAccountTestServiceAmbiguousCindyEventDoesNotStartBackgroundProbe(t *testing.T) {
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newCindyBalanceProbeResponse(http.StatusOK, "application/json", `{}`),
	}}
	gateway := &OpenAIGatewayService{httpUpstream: upstream}
	accountTest := &AccountTestService{openAIGatewayService: gateway}
	account := newCindyRateLimitAccount(8456, true)
	payload := []byte(`{"type":"response.failed","response":{"error":{"message":"request failed"}}}`)

	require.False(t, accountTest.markCindyBalanceInsufficientFromTest(
		context.Background(), account, http.StatusOK, payload,
	))
	require.Empty(t, upstream.bodies)
}

func TestAccountTestServiceCindyChatStreamDefersInBandMarkerUntilConfirmation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/admin/accounts/test", nil)
	repo := &cindyRateLimitAccountRepoStub{}
	rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	gateway := &OpenAIGatewayService{rateLimitService: rateLimitService}
	rateLimitService.SetAccountRuntimeBlocker(gateway)
	accountTest := &AccountTestService{accountRepo: repo, openAIGatewayService: gateway}
	account := newCindyRateLimitAccount(8455, true)
	body := "data: {\"type\":\"error\",\"error\":{\"type\":\"budget_exceeded\",\"code\":\"429\",\"message\":\"request failed\"}}\n\n"

	err := accountTest.processOpenAIChatCompletionsStream(c, c.Request.Context(), account, strings.NewReader(body))

	require.Error(t, err)
	require.Zero(t, repo.markCalls)
	require.Nil(t, account.CindyBalanceInsufficientAt)
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIGatewayCindyHTTP429PrecedesErrorPolicies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name   string
		handle func(*OpenAIGatewayService, context.Context, *http.Response, *gin.Context, *Account) error
	}{
		{
			name: "responses",
			handle: func(gateway *OpenAIGatewayService, ctx context.Context, resp *http.Response, c *gin.Context, account *Account) error {
				_, err := gateway.handleErrorResponse(ctx, resp, c, account, nil, "openai/gpt-5.6-luna")
				return err
			},
		},
		{
			name: "images",
			handle: func(gateway *OpenAIGatewayService, ctx context.Context, resp *http.Response, c *gin.Context, account *Account) error {
				_, err := gateway.handleOpenAIImagesErrorResponse(ctx, resp, c, account, "openai/gpt-image-2")
				return err
			},
		},
		{
			name: "chat_compat",
			handle: func(gateway *OpenAIGatewayService, _ context.Context, resp *http.Response, c *gin.Context, account *Account) error {
				_, err := gateway.handleCompatErrorResponse(resp, c, account, writeChatCompletionsError, "openai/gpt-5.6-luna")
				return err
			},
		},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/test", nil)
			repo := &cindyRateLimitAccountRepoStub{}
			rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			gateway := &OpenAIGatewayService{rateLimitService: rateLimitService}
			rateLimitService.SetAccountRuntimeBlocker(gateway)
			account := newCindyRateLimitAccount(int64(8460+index), true)
			account.Credentials["custom_error_codes_enabled"] = true
			account.Credentials["custom_error_codes"] = []any{float64(http.StatusInternalServerError)}
			resp := &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(exactCindyBudgetExceededBody)),
			}

			err := tt.handle(gateway, context.Background(), resp, c, account)

			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.True(t, failoverErr.CindyBalanceInsufficient)
			require.False(t, failoverErr.RetryableOnSameAccount)
			require.False(t, c.Writer.Written(), "classification must happen before response rewriting")
			require.Zero(t, repo.markCalls)
			require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
		})
	}
}

func TestOpenAIGatewayCindyAlphaSearchBalancePrecedesHealthSuppression(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "http_429", status: http.StatusTooManyRequests, body: exactCindyBudgetExceededBody},
		{name: "http_200_response_failed", status: http.StatusOK, body: "event: response.failed\n" +
			`data: {"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429"}}}` + "\n\n"},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestBody := []byte(`{"id":"search-session","model":"gpt-5.6-sol","commands":{"search_query":[{"q":"news"}]}}`)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search", bytes.NewReader(requestBody))
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: tt.status,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(tt.body)),
			}}
			settings := &SettingService{}
			settings.openAIRefusalRecoveryCache.Store(&cachedOpenAIRefusalRecoveryRuntime{
				runtime:   OpenAIRefusalRecoveryRuntime{APIKeyAlphaSearchResponsesBridge: true},
				expiresAt: time.Now().Add(time.Minute).UnixNano(),
			})
			repo := &cindyRateLimitAccountRepoStub{}
			rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			gateway := &OpenAIGatewayService{
				cfg:              &config.Config{},
				httpUpstream:     upstream,
				settingService:   settings,
				rateLimitService: rateLimitService,
			}
			rateLimitService.SetAccountRuntimeBlocker(gateway)
			account := newCindyRateLimitAccount(int64(8475+index), true)
			account.Platform = PlatformCindy
			account.WirePlatform = WirePlatformOpenAI
			account.ProviderProfile = ProviderProfileCindyLaxaV1
			account.Concurrency = 1
			account.Extra = map[string]any{"openai_alpha_search_mode": OpenAIAlphaSearchModeResponsesWebSearch}

			result, err := gateway.ForwardAlphaSearch(context.Background(), c, account, requestBody)

			require.Nil(t, result)
			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.True(t, failoverErr.CindyBalanceInsufficient)
			require.NotContains(t, string(failoverErr.ResponseBody), "budget_exceeded")
			require.NotContains(t, string(failoverErr.ResponseBody), "fixture-account")
			require.False(t, failoverErr.SuppressAccountHealthPenalty,
				"budget exhaustion must not be rewritten as a health-suppressed search capability failure")
			require.False(t, failoverErr.RetryableOnSameAccount)
			require.False(t, c.Writer.Written())
			require.Zero(t, repo.markCalls)
			require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
		})
	}
}

func TestOpenAIGatewayCindyStreamFailoverIsNeverSameAccountRetryable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	repo := &cindyRateLimitAccountRepoStub{}
	rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	gateway := &OpenAIGatewayService{rateLimitService: rateLimitService}
	rateLimitService.SetAccountRuntimeBlocker(gateway)
	account := newCindyRateLimitAccount(8485, true)
	account.Credentials["pool_mode_retry_status_codes"] = []any{float64(http.StatusTooManyRequests)}
	payload := []byte(`{"type":"response.failed","response":{"model":"openai/gpt-5.6-luna","error":{"type":"budget_exceeded","code":"429","message":"request failed"}}}`)

	failoverErr := gateway.newOpenAIStreamFailoverError(c, account, false, "request-id", payload, "request failed", http.StatusOK)

	require.True(t, failoverErr.CindyBalanceInsufficient)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.NotContains(t, string(failoverErr.ResponseBody), "budget_exceeded")
	require.Zero(t, repo.markCalls)
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIGatewayCindyPassthroughStreamBalanceFailsOverBeforeWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for index, tc := range []struct {
		name    string
		payload string
	}{
		{
			name: "response_failed",
			payload: "event: response.created\n" +
				`data: {"type":"response.created","response":{"id":"resp_1"}}` + "\n\n" +
				"event: response.failed\n" +
				`data: {"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429","message":"sensitive upstream detail"}}}` + "\n\n",
		},
		{
			name: "bare_error",
			payload: "event: error\n" +
				`data: {"type":"error","error":{"type":"budget_exceeded","code":"429","message":"sensitive upstream detail"}}` + "\n\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			repo := &cindyRateLimitAccountRepoStub{}
			rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			gateway := &OpenAIGatewayService{rateLimitService: rateLimitService}
			rateLimitService.SetAccountRuntimeBlocker(gateway)
			account := newCindyRateLimitAccount(int64(8487+index), true)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(tc.payload)),
			}

			_, err := gateway.handleStreamingResponsePassthrough(
				context.Background(), resp, c, account, time.Now(), "gpt-5.6-luna", "openai/gpt-5.6-luna",
			)

			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.True(t, failoverErr.CindyBalanceInsufficient)
			require.False(t, failoverErr.RetryableOnSameAccount)
			require.NotContains(t, string(failoverErr.ResponseBody), "budget_exceeded")
			require.False(t, c.Writer.Written(), "preamble events must remain buffered so the handler can switch accounts")
			require.Empty(t, recorder.Body.String())
			require.Zero(t, repo.markCalls)
			require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
		})
	}
}

func TestOpenAIGatewayCindyPassthroughStreamBalanceAfterOutputDropsRawPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	repo := &cindyRateLimitAccountRepoStub{}
	rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	gateway := &OpenAIGatewayService{rateLimitService: rateLimitService}
	rateLimitService.SetAccountRuntimeBlocker(gateway)
	account := newCindyRateLimitAccount(8489, true)
	payload := "event: response.output_text.delta\n" +
		`data: {"type":"response.output_text.delta","delta":"ok"}` + "\n\n" +
		"event: response.failed\n" +
		`data: {"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429","message":"sensitive upstream detail"}}}` + "\n\n"
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(payload)),
	}

	_, err := gateway.handleStreamingResponsePassthrough(
		context.Background(), resp, c, account, time.Now(), "gpt-5.6-luna", "openai/gpt-5.6-luna",
	)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.CindyBalanceInsufficient)
	require.True(t, c.Writer.Written())
	require.Contains(t, recorder.Body.String(), `"delta":"ok"`)
	require.NotContains(t, recorder.Body.String(), "budget_exceeded")
	require.NotContains(t, recorder.Body.String(), "sensitive upstream detail")
	require.NotContains(t, string(failoverErr.ResponseBody), "budget_exceeded")
	require.Zero(t, repo.markCalls)
}

func TestOpenAIGatewayCindyMainResponsesBalancePrecedesPassthroughAndWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for index, payload := range []string{
		`{"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429","message":"sensitive upstream detail"}}}`,
		`{"type":"error","error":{"type":"budget_exceeded","code":"429","message":"sensitive upstream detail"}}`,
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		bindPassthroughRule(c, "openai", []string{"budget_exceeded"}, http.StatusTeapot)
		repo := &cindyRateLimitAccountRepoStub{}
		rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		gateway := &OpenAIGatewayService{
			cfg:              &config.Config{},
			rateLimitService: rateLimitService,
			toolCorrector:    NewCodexToolCorrector(),
		}
		rateLimitService.SetAccountRuntimeBlocker(gateway)
		account := newCindyRateLimitAccount(int64(8510+index), true)
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: " + payload + "\n\n")),
		}

		_, err := gateway.handleStreamingResponseWithReasoning(
			context.Background(), resp, c, account, time.Now(),
			"gpt-5.6-luna", "openai/gpt-5.6-luna", "",
		)

		var failoverErr *UpstreamFailoverError
		require.ErrorAs(t, err, &failoverErr)
		require.True(t, failoverErr.CindyBalanceInsufficient)
		require.False(t, failoverErr.RetryableOnSameAccount)
		require.NotContains(t, string(failoverErr.ResponseBody), "budget_exceeded")
		require.False(t, c.Writer.Written(), "Cindy classification must win before the passthrough rule commits JSON")
		require.Empty(t, recorder.Body.String())
		require.Zero(t, repo.markCalls)
	}
}

func TestOpenAIGatewayCindyMainResponsesBalanceAfterOutputDropsRawPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	repo := &cindyRateLimitAccountRepoStub{}
	rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	gateway := &OpenAIGatewayService{
		cfg:              &config.Config{},
		rateLimitService: rateLimitService,
		toolCorrector:    NewCodexToolCorrector(),
	}
	rateLimitService.SetAccountRuntimeBlocker(gateway)
	account := newCindyRateLimitAccount(8512, true)
	stream := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"ok"}`,
		``,
		`data: {"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429","message":"sensitive upstream detail"}}}`,
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(stream)),
	}

	_, err := gateway.handleStreamingResponse(
		context.Background(), resp, c, account, time.Now(), "gpt-5.6-luna", "openai/gpt-5.6-luna",
	)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.CindyBalanceInsufficient)
	require.Contains(t, recorder.Body.String(), `"delta":"ok"`)
	require.NotContains(t, recorder.Body.String(), "budget_exceeded")
	require.NotContains(t, recorder.Body.String(), "sensitive upstream detail")
	require.Zero(t, repo.markCalls)
}

func TestOpenAIGatewayCindyConvertedMessagesBalanceAfterOutputDropsRawPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	repo := &cindyRateLimitAccountRepoStub{}
	rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	gateway := &OpenAIGatewayService{
		cfg:              &config.Config{},
		rateLimitService: rateLimitService,
		toolCorrector:    NewCodexToolCorrector(),
	}
	rateLimitService.SetAccountRuntimeBlocker(gateway)
	account := newCindyRateLimitAccount(8513, true)
	stream := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_messages","model":"openai/gpt-5.6-luna","status":"in_progress","output":[]}}`,
		``,
		`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"partial"}`,
		``,
		`data: {"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429","message":"sensitive upstream detail"}}}`,
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(stream)),
	}

	_, err := gateway.handleAnthropicStreamingResponse(
		resp, c, account, "gpt-5.6-luna", "gpt-5.6-luna", "openai/gpt-5.6-luna", time.Now(),
	)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.CindyBalanceInsufficient)
	require.True(t, c.Writer.Written(), "converted Anthropic output prevents replaying the turn on another account")
	require.Contains(t, recorder.Body.String(), `"text":"partial"`)
	require.NotContains(t, recorder.Body.String(), "budget_exceeded")
	require.NotContains(t, recorder.Body.String(), "sensitive upstream detail")
	require.NotContains(t, string(failoverErr.ResponseBody), "budget_exceeded")
	require.Zero(t, repo.markCalls)
}

func TestOpenAIGatewayCindyImagesStreamBalanceIsSanitizedBeforeAndAfterOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for index, tc := range []struct {
		name       string
		prefix     string
		wantOutput bool
	}{
		{name: "before_output"},
		{
			name: "after_partial_image",
			prefix: "data: " +
				`{"type":"response.image_generation_call.partial_image","partial_image_b64":"aGVsbG8=","partial_image_index":0}` + "\n\n",
			wantOutput: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
			repo := &cindyRateLimitAccountRepoStub{}
			rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			gateway := &OpenAIGatewayService{cfg: &config.Config{}, rateLimitService: rateLimitService}
			rateLimitService.SetAccountRuntimeBlocker(gateway)
			account := newCindyRateLimitAccount(int64(8514+index), true)
			failed := `{"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429","message":"sensitive upstream detail"}}}`
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(tc.prefix + "data: " + failed + "\n\n")),
			}

			_, _, _, _, err := gateway.handleOpenAIImagesOAuthStreamingResponse(
				resp, c, account, time.Now(), "b64_json", "image_generation", "openai/gpt-image-2",
			)

			var failoverErr *UpstreamFailoverError
			if tc.wantOutput {
				require.Error(t, err)
				require.False(t, errors.As(err, &failoverErr), "a partial image makes account replay unsafe")
				require.Contains(t, recorder.Body.String(), "partial_image")
				require.Contains(t, recorder.Body.String(), "Temporary upstream failure")
			} else {
				require.ErrorAs(t, err, &failoverErr)
				require.True(t, failoverErr.CindyBalanceInsufficient)
				require.False(t, failoverErr.RetryableOnSameAccount)
				require.Empty(t, recorder.Body.String())
			}
			require.NotContains(t, recorder.Body.String(), "budget_exceeded")
			require.NotContains(t, recorder.Body.String(), "sensitive upstream detail")
			require.Zero(t, repo.markCalls)
		})
	}
}

func TestOpenAIGatewayCindyChatBridgeDefersBareErrorMarkerUntilConfirmation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requestBody := []byte(`{"model":"gpt-5.6-luna","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(requestBody))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader("event: error\n" +
			`data: {"type":"error","error":{"type":"budget_exceeded","code":"429","message":"request failed"}}` + "\n\n")),
	}}
	repo := &cindyRateLimitAccountRepoStub{}
	rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	gateway := &OpenAIGatewayService{
		cfg:              &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		httpUpstream:     upstream,
		rateLimitService: rateLimitService,
	}
	rateLimitService.SetAccountRuntimeBlocker(gateway)
	account := newCindyRateLimitAccount(8486, true)
	account.Concurrency = 1

	result, err := gateway.ForwardAsChatCompletions(
		context.Background(), c, account, requestBody, "gpt-5.6-luna", "gpt-5.6-luna",
	)

	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.CindyBalanceInsufficient)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.False(t, c.Writer.Written())
	require.Zero(t, repo.markCalls)
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIGatewayCindyNonStreamingResponsesFailureFailsOverBeforeWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name   string
		handle func(*OpenAIGatewayService, context.Context, *http.Response, *gin.Context, *Account) error
	}{
		{
			name: "responses",
			handle: func(gateway *OpenAIGatewayService, ctx context.Context, resp *http.Response, c *gin.Context, account *Account) error {
				_, err := gateway.handleNonStreamingResponse(ctx, resp, c, account, "gpt-5.6-luna", "openai/gpt-5.6-luna")
				return err
			},
		},
		{
			name: "passthrough",
			handle: func(gateway *OpenAIGatewayService, ctx context.Context, resp *http.Response, c *gin.Context, account *Account) error {
				_, err := gateway.handleNonStreamingResponsePassthrough(ctx, resp, c, "gpt-5.6-luna", "openai/gpt-5.6-luna", account)
				return err
			},
		},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			payload := "event: response.failed\n" +
				`data: {"type":"response.failed","response":{"error":{"type":"budget_exceeded","code":"429","message":"request failed"}}}` + "\n\n"
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(payload)),
			}
			repo := &cindyRateLimitAccountRepoStub{}
			rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			gateway := &OpenAIGatewayService{rateLimitService: rateLimitService}
			rateLimitService.SetAccountRuntimeBlocker(gateway)
			account := newCindyRateLimitAccount(int64(8490+index), true)

			err := tt.handle(gateway, context.Background(), resp, c, account)

			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.True(t, failoverErr.CindyBalanceInsufficient)
			require.False(t, failoverErr.RetryableOnSameAccount)
			require.False(t, c.Writer.Written())
			require.Zero(t, repo.markCalls)
			require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
		})
	}
}

func TestOpenAIGatewayCindyNonStreamingPassthroughClassifiesJSONAndSSEError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for index, tc := range []struct {
		name        string
		contentType string
		body        string
	}{
		{
			name:        "json_error_event",
			contentType: "application/json",
			body:        `{"type":"error","error":{"type":"budget_exceeded","code":"429","message":"sensitive upstream detail"}}`,
		},
		{
			name:        "sse_error_event",
			contentType: "text/event-stream",
			body: "event: error\n" +
				`data: {"type":"error","error":{"type":"budget_exceeded","code":"429","message":"sensitive upstream detail"}}` + "\n\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			repo := &cindyRateLimitAccountRepoStub{}
			rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			gateway := &OpenAIGatewayService{rateLimitService: rateLimitService}
			rateLimitService.SetAccountRuntimeBlocker(gateway)
			account := newCindyRateLimitAccount(int64(8495+index), true)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{tc.contentType}},
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			}

			_, err := gateway.handleNonStreamingResponsePassthrough(
				context.Background(), resp, c, "gpt-5.6-luna", "openai/gpt-5.6-luna", account,
			)

			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.True(t, failoverErr.CindyBalanceInsufficient)
			require.False(t, failoverErr.RetryableOnSameAccount)
			require.NotContains(t, string(failoverErr.ResponseBody), "budget_exceeded")
			require.False(t, c.Writer.Written())
			require.Empty(t, recorder.Body.String())
			require.Zero(t, repo.markCalls)
			require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
		})
	}
}

func TestOpenAIGatewayCindy402DoesNotPersistOrBlockAccount(t *testing.T) {
	repo := &cindyRateLimitAccountRepoStub{}
	rateLimitService := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	gateway := &OpenAIGatewayService{rateLimitService: rateLimitService}
	rateLimitService.SetAccountRuntimeBlocker(gateway)
	account := newCindyRateLimitAccount(8402, false)

	shouldDisable := gateway.handleOpenAIAccountUpstreamError(
		context.Background(), account, http.StatusPaymentRequired, http.Header{},
		[]byte(`{"error":{"type":"budget_exceeded","code":"429"}}`), "gpt-5.6-sol",
	)

	require.False(t, shouldDisable)
	require.Zero(t, repo.markCalls)
	require.Zero(t, repo.setErrorCalls)
	require.Nil(t, account.CindyBalanceInsufficientAt)
	require.False(t, gateway.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.6-sol"))
}
