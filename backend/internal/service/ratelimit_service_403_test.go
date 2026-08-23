//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type runtimeBlockRecorder struct {
	accounts   []*Account
	until      []time.Time
	reasons    []string
	clearedIDs []int64
}

func (r *runtimeBlockRecorder) BlockAccountScheduling(account *Account, until time.Time, reason string) {
	r.accounts = append(r.accounts, account)
	r.until = append(r.until, until)
	r.reasons = append(r.reasons, reason)
}

func (r *runtimeBlockRecorder) ClearAccountSchedulingBlock(accountID int64) {
	r.clearedIDs = append(r.clearedIDs, accountID)
}

func TestRateLimitService_HandleUpstreamError_OpenAI403FirstHitTempUnschedulable(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	counter := &openAI403CounterCacheStub{counts: []int64{1}}
	blocker := &runtimeBlockRecorder{}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	service.SetOpenAI403CounterCache(counter)
	service.SetAccountRuntimeBlocker(blocker)
	account := &Account{
		ID:       301,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
	}

	shouldDisable := service.HandleUpstreamError(
		context.Background(),
		account,
		http.StatusForbidden,
		http.Header{},
		[]byte(`{"error":{"message":"temporary edge rejection"}}`),
	)

	require.True(t, shouldDisable)
	require.Equal(t, 0, repo.setErrorCalls)
	require.Equal(t, 1, repo.tempCalls)
	require.Contains(t, repo.lastTempReason, "temporary edge rejection")
	require.Contains(t, repo.lastTempReason, "(1/3)")
	require.Len(t, blocker.accounts, 1)
	require.Equal(t, account.ID, blocker.accounts[0].ID)
	require.Equal(t, "openai_403_temp", blocker.reasons[0])
	require.True(t, blocker.until[0].After(time.Now()))
}

func TestRateLimitService_HandleUpstreamError_OpenAI403ThresholdDisables(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	counter := &openAI403CounterCacheStub{counts: []int64{3}}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	service.SetOpenAI403CounterCache(counter)
	account := &Account{
		ID:       302,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
	}

	shouldDisable := service.HandleUpstreamError(
		context.Background(),
		account,
		http.StatusForbidden,
		http.Header{},
		[]byte(`{"error":{"message":"workspace forbidden by policy"}}`),
	)

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Equal(t, 0, repo.tempCalls)
	require.Contains(t, repo.lastErrorMsg, "workspace forbidden by policy")
	require.Contains(t, repo.lastErrorMsg, "consecutive_403=3/3")
}

func TestOpenAIGatewayService_Cindy403UsesTransientHealthCoordinatorEvenInPoolMode(t *testing.T) {
	for _, poolMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "pool"}[poolMode], func(t *testing.T) {
			repo := &rateLimitAccountRepoStub{}
			counter := &countingOpenAI403CounterCache{openAI403CounterCacheStub: openAI403CounterCacheStub{counts: []int64{1}}}
			rateLimit := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			rateLimit.SetOpenAI403CounterCache(counter)
			gateway := &OpenAIGatewayService{rateLimitService: rateLimit}
			health := &cindyHealthCoordinatorRecorder{}
			gateway.SetCindyHealthCoordinator(health)
			account := newFirstClassCindyRateLimitAccount(303, poolMode)

			shouldDisable := gateway.handleOpenAIAccountUpstreamError(
				context.Background(), account, http.StatusForbidden, http.Header{},
				[]byte(`{"error":{"message":"account access forbidden"}}`), "gpt-5.4",
			)

			require.False(t, shouldDisable)
			require.Zero(t, repo.tempCalls)
			require.Zero(t, repo.setErrorCalls)
			require.Zero(t, counter.increments)
			require.Equal(t, []CindyHealthSignal{CindyHealthSignalForbidden}, health.signals)
			require.Equal(t, []int64{account.ID}, health.accounts)
		})
	}
}

func TestOpenAIGatewayService_RepeatedCindy403NeverUsesPermanentDisable(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	counter := &countingOpenAI403CounterCache{openAI403CounterCacheStub: openAI403CounterCacheStub{counts: []int64{openAI403DisableThreshold}}}
	rateLimit := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	rateLimit.SetOpenAI403CounterCache(counter)
	gateway := &OpenAIGatewayService{rateLimitService: rateLimit}
	health := &cindyHealthCoordinatorRecorder{}
	gateway.SetCindyHealthCoordinator(health)
	account := newFirstClassCindyRateLimitAccount(304, true)

	for range 2 {
		shouldDisable := gateway.handleOpenAIAccountUpstreamError(
			context.Background(), account, http.StatusForbidden, http.Header{}, []byte("Forbidden"), "gpt-5.4",
		)
		require.False(t, shouldDisable)
	}

	require.Zero(t, repo.setErrorCalls)
	require.Zero(t, repo.tempCalls)
	require.Zero(t, counter.increments)
	require.Equal(t, []CindyHealthSignal{CindyHealthSignalForbidden, CindyHealthSignalForbidden}, health.signals)
}

func TestOpenAIGatewayService_Cindy403HTMLAndCyberDoNotPenalize(t *testing.T) {
	for name, tc := range map[string]struct {
		body       []byte
		wantSignal bool
	}{
		"html":  {body: []byte(openAI403HTMLBody), wantSignal: true},
		"cyber": {body: []byte(`{"error":{"code":"cyber_policy","message":"blocked"}}`)},
	} {
		t.Run(name, func(t *testing.T) {
			repo := &rateLimitAccountRepoStub{}
			counter := &countingOpenAI403CounterCache{openAI403CounterCacheStub: openAI403CounterCacheStub{counts: []int64{1}}}
			blocker := &runtimeBlockRecorder{}
			rateLimit := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			rateLimit.SetOpenAI403CounterCache(counter)
			rateLimit.SetAccountRuntimeBlocker(blocker)
			gateway := &OpenAIGatewayService{rateLimitService: rateLimit}
			health := &cindyHealthCoordinatorRecorder{}
			gateway.SetCindyHealthCoordinator(health)
			account := newFirstClassCindyRateLimitAccount(305, true)

			shouldDisable := gateway.handleOpenAIAccountUpstreamError(
				context.Background(), account, http.StatusForbidden, http.Header{}, tc.body, "gpt-5.4",
			)

			require.False(t, shouldDisable)
			require.Zero(t, counter.increments)
			require.Zero(t, repo.tempCalls)
			require.Zero(t, repo.setErrorCalls)
			require.Empty(t, blocker.accounts)
			if tc.wantSignal {
				require.Equal(t, []CindyHealthSignal{CindyHealthSignalForbidden}, health.signals)
			} else {
				require.Empty(t, health.signals)
			}
		})
	}
}

func TestRateLimitServiceCindy403NeverPersistsPermanentError(t *testing.T) {
	legacy := newCindyRateLimitAccount(307, false)
	legacy.Platform = PlatformOpenAI
	legacy.WirePlatform = ""
	legacy.ProviderProfile = ""
	for _, test := range []struct {
		name    string
		account *Account
	}{
		{name: "canonical", account: newFirstClassCindyRateLimitAccount(306, false)},
		{name: "legacy", account: legacy},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &rateLimitAccountRepoStub{}
			counter := &countingOpenAI403CounterCache{openAI403CounterCacheStub: openAI403CounterCacheStub{counts: []int64{openAI403DisableThreshold}}}
			service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			service.SetOpenAI403CounterCache(counter)

			for range 2 {
				require.True(t, service.HandleUpstreamError(
					context.Background(), test.account, http.StatusForbidden, http.Header{},
					[]byte(`{"error":{"message":"temporary forbidden"}}`),
				))
			}

			require.Equal(t, 2, repo.tempCalls)
			require.Zero(t, repo.setErrorCalls)
			require.Zero(t, counter.increments)
		})
	}
}
