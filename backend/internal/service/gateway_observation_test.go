//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type accountObservationRepo struct {
	AccountRepository
	errorCalls   atomic.Int64
	tempCalls    atomic.Int64
	rateCalls    atomic.Int64
	modelCalls   atomic.Int64
	extraCalls   atomic.Int64
	clearedCalls atomic.Int64
	extraWritten chan struct{}
}

func (r *accountObservationRepo) SetError(context.Context, int64, string) error {
	r.errorCalls.Add(1)
	return nil
}

func (r *accountObservationRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.tempCalls.Add(1)
	return nil
}

func (r *accountObservationRepo) SetRateLimited(context.Context, int64, time.Time) error {
	r.rateCalls.Add(1)
	return nil
}

func (r *accountObservationRepo) SetModelRateLimit(context.Context, int64, string, time.Time, ...string) error {
	r.modelCalls.Add(1)
	return nil
}

func (r *accountObservationRepo) UpdateExtra(context.Context, int64, map[string]any) error {
	r.extraCalls.Add(1)
	if r.extraWritten != nil {
		r.extraWritten <- struct{}{}
	}
	return nil
}

func (r *accountObservationRepo) ClearRateLimitIfObserved(context.Context, int64, time.Time, time.Time) (bool, error) {
	r.clearedCalls.Add(1)
	return true, nil
}

func requireNoAccountObservationWrites(t *testing.T, r *accountObservationRepo) {
	t.Helper()
	require.Zero(t, r.errorCalls.Load())
	require.Zero(t, r.tempCalls.Load())
	require.Zero(t, r.rateCalls.Load())
	require.Zero(t, r.modelCalls.Load())
	require.Zero(t, r.extraCalls.Load())
	require.Zero(t, r.clearedCalls.Load())
}

func TestAccountObservationBusinessForwardPreservesErrorsWithoutPublishingState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"http_auth", http.StatusUnauthorized, `{"error":{"message":"synthetic token expired"}}`},
		{"http_budget", http.StatusTooManyRequests, `{"error":{"type":"budget_exceeded","message":"synthetic budget exhausted"}}`},
		{"stream_budget", http.StatusOK, "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_observation_error\",\"status\":\"failed\",\"error\":{\"type\":\"budget_exceeded\",\"message\":\"synthetic budget exhausted\"}}}\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, observation := range []bool{true, false} {
				mode := "normal"
				if observation {
					mode = "observation"
				}
				t.Run(mode, func(t *testing.T) {
					setup := newAstraOAuthSetup(t, false)
					repo := &accountObservationRepo{}
					setup.svc.accountRepo = repo
					setup.svc.rateLimitService = NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
					setup.svc.rateLimitService.SetAccountRuntimeBlocker(setup.svc)
					setup.account.Credentials["refresh_token"] = "synthetic-refresh-token"
					setup.upstream.resp = &http.Response{
						StatusCode: tc.status,
						Header:     http.Header{"Content-Type": {"text/event-stream"}},
						Body:       io.NopCloser(strings.NewReader(tc.body)),
					}
					ctx := context.Background()
					if observation {
						ctx = WithAccountObservation(ctx)
					}
					setup.c.Request = setup.c.Request.WithContext(ctx)
					_, err := setup.svc.Forward(ctx, setup.c, setup.account, astraRequestBody("gpt-6-astra", true, "", "high"))
					require.Error(t, err)
					require.Len(t, setup.upstream.requests, 1, "observation must send to this account exactly once")
					var failure *UpstreamFailoverError
					require.ErrorAs(t, err, &failure)
					require.Contains(t, string(failure.ResponseBody), "synthetic")
					if observation {
						requireNoAccountObservationWrites(t, repo)
						require.False(t, setup.svc.isOpenAIAccountRuntimeBlocked(setup.account))
					} else {
						require.Positive(t, repo.errorCalls.Load()+repo.tempCalls.Load(), "ordinary forwarding retains its account-state contract")
					}
				})
			}
		})
	}
}

func TestAccountObservationSuccessSuppressesBackgroundQuotaAndProxyHealth(t *testing.T) {
	setup := newAstraOAuthSetup(t, false)
	repo := &accountObservationRepo{extraWritten: make(chan struct{}, 1)}
	setup.svc.accountRepo = repo
	setup.upstream.resp = &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":                   {"text/event-stream"},
			"X-Codex-Primary-Used-Percent":   {"50"},
			"X-Codex-Primary-Window-Minutes": {"300"},
		},
		Body: io.NopCloser(strings.NewReader(codexCompletedSSE(`{"id":"resp_observation_success","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"synthetic html"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`))),
	}
	proxyID := int64(71)
	setup.account.ProxyID = &proxyID
	setup.svc.openaiProxyStreamCircuit = newOpenAIProxyStreamCircuit(openAIProxyStreamCircuitSettings{
		failureThreshold: 1, failureWindow: time.Minute, quarantineTTL: time.Hour, maxEntries: 2,
	})
	setup.svc.openaiProxyStreamCircuit.recordFailure(proxyID, time.Now())
	ctx := WithAccountObservation(context.Background())
	setup.c.Request = setup.c.Request.WithContext(ctx)
	result, err := setup.svc.Forward(ctx, setup.c, setup.account, astraRequestBody("gpt-6-astra", true, "", "high"))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, setup.rec.Body.String(), "synthetic html")
	requireNoAccountObservationWrites(t, repo)
	require.Nil(t, setup.svc.codexSnapshotThrottle, "suppression happens before throttle state and goroutine creation")
	require.True(t, setup.svc.openaiProxyStreamCircuit.isBlocked(proxyID, time.Now()), "a diagnostic success cannot recover the business proxy")

	// Normal success remains an explicit positive control for the detached write.
	snapshot := ParseCodexRateLimitHeaders(setup.upstream.resp.Header)
	require.NotNil(t, snapshot)
	setup.svc.updateCodexUsageSnapshot(context.Background(), setup.account.ID, snapshot)
	select {
	case <-repo.extraWritten:
	case <-time.After(time.Second):
		t.Fatal("ordinary success did not publish its quota snapshot")
	}
	require.EqualValues(t, 1, repo.extraCalls.Load())
	setup.svc.clearOpenAIProxyStreamDisconnectInContext(context.Background(), setup.account)
	require.False(t, setup.svc.openaiProxyStreamCircuit.isBlocked(proxyID, time.Now()))
}

func TestAccountObservationTokenRefreshPersistsCredentialsAcrossProviders(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic, PlatformGemini, PlatformGrok, PlatformAntigravity} {
		t.Run(platform, func(t *testing.T) {
			account := &Account{ID: 91, Platform: platform, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"access_token": "old-token", "refresh_token": "old-refresh", "expires_at": time.Now().Add(-time.Minute).Format(time.RFC3339)},
			}
			repo := &refreshAPIAccountRepo{account: account}
			executor := &refreshAPIExecutorStub{needsRefresh: true, credentials: map[string]any{
				"access_token": "new-token", "refresh_token": "new-refresh", "expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
			}}
			api := NewOAuthRefreshAPI(repo, nil)
			var token string
			var err error
			ctx := WithAccountObservation(context.Background())
			switch platform {
			case PlatformOpenAI:
				provider := NewOpenAITokenProvider(repo, nil, nil)
				provider.SetRefreshAPI(api, executor)
				token, err = provider.GetAccessToken(ctx, account)
			case PlatformAnthropic:
				provider := NewClaudeTokenProvider(repo, nil, nil)
				provider.SetRefreshAPI(api, executor)
				token, err = provider.GetAccessToken(ctx, account)
			case PlatformGemini:
				provider := NewGeminiTokenProvider(repo, nil, nil)
				provider.SetRefreshAPI(api, executor)
				token, err = provider.GetAccessToken(ctx, account)
			case PlatformGrok:
				provider := NewGrokTokenProvider(repo, nil)
				provider.SetRefreshAPI(api, executor)
				token, err = provider.GetAccessToken(ctx, account)
			case PlatformAntigravity:
				provider := NewAntigravityTokenProvider(repo, nil, nil)
				provider.SetRefreshAPI(api, executor)
				token, err = provider.GetAccessToken(ctx, account)
			}
			require.NoError(t, err)
			require.Equal(t, "new-token", token)
			require.Equal(t, 1, executor.refreshCalls)
			require.Equal(t, 1, repo.updateCredentialsCalls)
			require.Equal(t, "new-refresh", repo.account.GetCredential("refresh_token"))
			require.Equal(t, StatusActive, repo.account.Status)
		})
	}
}

type accountObservationTempCache struct {
	TempUnschedCache
	setCalls []int64
}

func (c *accountObservationTempCache) SetTempUnsched(_ context.Context, id int64, _ *TempUnschedState) error {
	c.setCalls = append(c.setCalls, id)
	return nil
}

func TestAccountObservationProviderFailuresSkipDetachedStateWrites(t *testing.T) {
	ctx := WithAccountObservation(context.Background())
	repo := &rateLimitAccountRepoStub{}
	openAI := NewOpenAITokenProvider(repo, nil, nil)
	blocker := &runtimeBlockRecorder{}
	openAI.SetAccountRuntimeBlocker(blocker)
	_, err := openAI.GetAccessToken(ctx, &Account{ID: 92, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "expired", "expires_at": time.Now().Add(-time.Minute).Format(time.RFC3339)},
	})
	require.ErrorContains(t, err, "refresh_token is missing")
	require.Zero(t, repo.setErrorCalls)
	require.Empty(t, blocker.accounts)

	account := &Account{ID: 93, Platform: PlatformAntigravity, Type: AccountTypeOAuth, Status: StatusActive,
		Credentials: map[string]any{"access_token": "expired", "refresh_token": "synthetic-refresh", "expires_at": time.Now().Add(-time.Minute).Format(time.RFC3339)},
	}
	refreshRepo := &refreshAPIAccountRepo{account: account}
	executor := &refreshAPIExecutorStub{needsRefresh: true, err: errors.New("synthetic refresh failure")}
	provider := NewAntigravityTokenProvider(repo, nil, nil)
	provider.SetRefreshAPI(NewOAuthRefreshAPI(refreshRepo, nil), executor)
	cache := &accountObservationTempCache{}
	provider.SetTempUnschedCache(cache)
	_, err = provider.GetAccessToken(ctx, account)
	require.Error(t, err)
	require.Zero(t, repo.tempCalls)
	require.Empty(t, cache.setCalls)
	_, err = provider.GetAccessToken(context.Background(), account)
	require.Error(t, err)
	require.Equal(t, 1, repo.tempCalls)
	require.Len(t, cache.setCalls, 1, "ordinary refresh failure must retain its detached DB and cache writes")
}

type accountObservationBillingRepo struct {
	UsageBillingRepository
	calls int
}

func (r *accountObservationBillingRepo) Apply(context.Context, *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	r.calls++
	return &UsageBillingApplyResult{Applied: false}, nil
}

type accountObservationLogRepo struct {
	UsageLogRepository
	calls int
}

func (r *accountObservationLogRepo) Create(context.Context, *UsageLog) (bool, error) {
	r.calls++
	return true, nil
}

func TestAccountObservationQueuedUsagePreservesMarkerAndDoesNotWrite(t *testing.T) {
	store := &quotaActivityMemoryStore{}
	activity := NewQuotaActivityService(store)
	ctx, finish := activity.Attach(WithAccountObservation(context.WithValue(context.Background(), ctxkey.AccountID, int64(95))))
	ObserveQuotaAccount(ctx, 95)
	var ran bool
	task, discard := TrackQuotaUsageTask(ctx, func(workerCtx context.Context) {
		ran = true
		require.True(t, IsAccountObservation(workerCtx))
		require.NoError(t, (&GatewayService{}).RecordUsage(workerCtx, nil))
		require.NoError(t, (&OpenAIGatewayService{}).RecordUsage(workerCtx, nil))
		MarkQuotaLogPersisted(workerCtx, 95)
	})
	finish()
	task(context.Background())
	discard()
	require.True(t, ran)
	stamp, known := activity.Read(context.Background(), 95)
	require.True(t, known)
	require.Equal(t, QuotaActivityStamp{}, stamp)

	billing := &accountObservationBillingRepo{}
	deferred := &DeferredService{}
	params := &postUsageBillingParams{User: &User{ID: 1}, APIKey: &APIKey{ID: 2}, Account: &Account{ID: 95, Type: AccountTypeAPIKey}, Cost: &CostBreakdown{ActualCost: 1}}
	applied, err := applyUsageBilling(CopyAccountObservationContext(ctx, context.Background()), "synthetic-bill", nil, params, &billingDeps{deferredService: deferred}, billing)
	require.NoError(t, err)
	require.False(t, applied)
	require.Zero(t, billing.calls)
	_, scheduled := deferred.lastUsedUpdates.Load(int64(95))
	require.False(t, scheduled)
	_, err = applyUsageBilling(context.Background(), "synthetic-bill", nil, params, &billingDeps{deferredService: deferred}, billing)
	require.NoError(t, err)
	require.Equal(t, 1, billing.calls)
	_, scheduled = deferred.lastUsedUpdates.Load(int64(95))
	require.True(t, scheduled)

	logs := &accountObservationLogRepo{}
	require.False(t, writeUsageLogBestEffort(ctx, logs, &UsageLog{AccountID: 95}, "observation-test"))
	require.Zero(t, logs.calls)
	require.True(t, writeUsageLogBestEffort(context.Background(), logs, &UsageLog{AccountID: 95}, "observation-test"))
	require.Equal(t, 1, logs.calls)
}

func TestAccountObservationTimeoutRecoveryAndAntigravityRetryBoundaries(t *testing.T) {
	ctx := WithAccountObservation(context.Background())
	repo := &accountObservationRepo{}
	rateLimit := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 96, Platform: PlatformAntigravity, Type: AccountTypeOAuth,
		Credentials: map[string]any{
			"temp_unschedulable_enabled": true,
			"temp_unschedulable_rules": []any{map[string]any{
				"error_code":       float64(http.StatusInternalServerError),
				"keywords":         []any{"synthetic failure"},
				"duration_minutes": float64(1),
			}},
		},
	}
	require.Equal(t, ErrorPolicyTempUnscheduled, rateLimit.CheckErrorPolicy(ctx, account, http.StatusInternalServerError, []byte(`{"error":{"message":"synthetic failure"}}`), "synthetic-model"), "request-local policy matching survives observation isolation")
	require.False(t, rateLimit.HandleStreamTimeout(ctx, account, "synthetic-model"))
	result, err := rateLimit.RecoverAccountState(ctx, account.ID, AccountRecoveryOptions{InvalidateToken: true})
	require.NoError(t, err)
	require.Equal(t, &SuccessfulTestRecoveryResult{}, result)
	require.NoError(t, rateLimit.ClearRateLimit(ctx, account.ID))
	require.NoError(t, rateLimit.ClearTempUnschedulable(ctx, account.ID))
	tempUnscheduleAccountForTransportError(ctx, repo, account, "synthetic transport failure")
	tempUnscheduleGoogleConfigError(ctx, repo, account.ID, "observation-test")
	tempUnscheduleEmptyResponse(ctx, repo, account.ID, "observation-test")
	require.False(t, setModelRateLimitByModelName(ctx, repo, account.ID, "synthetic-model", "observation-test", 429, time.Now().Add(time.Hour), true))
	internalCounter := &mockInternal500Cache{incrementCount: 3}
	antigravity := &AntigravityGatewayService{accountRepo: repo, internal500Cache: internalCounter}
	antigravity.handleInternal500RetryExhausted(ctx, "observation-test", account)
	antigravity.resetInternal500Counter(ctx, "observation-test", account.ID)
	antigravity.setCreditsExhausted(ctx, account)
	require.Empty(t, internalCounter.incrementCalls)
	require.Empty(t, internalCounter.resetCalls)
	requireNoAccountObservationWrites(t, repo)
	antigravity.handleInternal500RetryExhausted(context.Background(), "observation-test", account)
	require.EqualValues(t, 1, repo.errorCalls.Load())
}

func TestAccountObservationWebSocketFailureCallbacksDoNotPublishCooldown(t *testing.T) {
	ctx := WithAccountObservation(context.Background())
	repo := &accountObservationRepo{}
	gateway := &OpenAIGatewayService{accountRepo: repo, cfg: &config.Config{}}
	gateway.rateLimitService = NewRateLimitService(repo, nil, gateway.cfg, nil, nil)
	gateway.rateLimitService.SetAccountRuntimeBlocker(gateway)
	account := &Account{ID: 97, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	payload := []byte(`{"type":"error","error":{"code":"rate_limit_exceeded","type":"rate_limit_error","message":"synthetic rate limit exceeded"}}`)
	require.True(t, gateway.handleOpenAIWSFailureAccountSideEffects(ctx, account, "gpt-6.1-sol", nil, payload))
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
	gateway.persistOpenAIWSRateLimitSignal(ctx, account, nil, payload, "rate_limit_exceeded", "rate_limit_error", "synthetic rate limit exceeded", "gpt-6.1-sol")
	requireNoAccountObservationWrites(t, repo)
	require.True(t, gateway.handleOpenAIWSFailureAccountSideEffects(context.Background(), account, "gpt-6.1-sol", nil, payload))
	require.True(t, gateway.isOpenAIAccountRuntimeBlocked(account), "ordinary WS failure retains its retry-exhausted cooldown")
}

type accountObservationBlockingUpstream struct {
	entered chan context.Context
}

func (u *accountObservationBlockingUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.entered <- req.Context()
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func (u *accountObservationBlockingUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func TestAccountObservationCancellationReachesBusinessUpstream(t *testing.T) {
	setup := newAstraOAuthSetup(t, false)
	upstream := &accountObservationBlockingUpstream{entered: make(chan context.Context, 1)}
	setup.svc.httpUpstream = upstream
	ctx, cancel := context.WithCancel(WithAccountObservation(context.Background()))
	defer cancel()
	setup.c.Request = setup.c.Request.WithContext(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := setup.svc.Forward(ctx, setup.c, setup.account, astraRequestBody("gpt-6-astra", true, "", "high"))
		done <- err
	}()
	select {
	case actualCtx := <-upstream.entered:
		require.True(t, IsAccountObservation(actualCtx))
	case <-time.After(time.Second):
		t.Fatal("business sender did not reach local fake upstream")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("observation request detached its cancellation")
	}

	ordinary, ordinaryCancel := context.WithCancel(context.Background())
	detached, release := detachStreamUpstreamContext(ordinary, true)
	defer release()
	ordinaryCancel()
	require.NoError(t, detached.Err(), "ordinary stream keeps the existing detached usage-collection contract")
}
