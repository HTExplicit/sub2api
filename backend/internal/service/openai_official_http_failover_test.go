package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOpenAIOfficialHTTPFailoverRequiresMarkerAndOrdinaryAPIKeyIdentity(t *testing.T) {
	marked := WithOpenAIOfficialHTTPFailover(context.Background())
	managed := WithManagedModelRequest(marked, &ManagedModelRequest{GroupID: 23})
	ordinary := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	tests := []struct {
		name    string
		ctx     context.Context
		account *Account
		want    bool
	}{
		{name: "ordinary API key", ctx: marked, account: ordinary, want: true},
		{name: "pool API key", ctx: marked, account: openAIHealthPoolAccount(), want: true},
		{name: "missing marker", ctx: context.Background(), account: ordinary},
		{name: "nil context", account: ordinary},
		{name: "nil account", ctx: marked},
		{name: "managed publication", ctx: managed, account: ordinary},
		{name: "canonical Cindy", ctx: marked, account: &Account{Platform: PlatformCindy, Type: AccountTypeAPIKey, Credentials: cindyCredentials()}},
		{name: "legacy OpenAI Laxa", ctx: marked, account: &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: cindyCredentials()}},
		{name: "OpenAI Cindy profile", ctx: marked, account: &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, ProviderProfile: ProviderProfileCindyLaxaV1}},
		{name: "OAuth", ctx: marked, account: &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}},
		{name: "non OpenAI", ctx: marked, account: &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, IsOpenAIOfficialHTTPFailover(tt.ctx, tt.account))
		})
	}
}

func TestOpenAIOfficialHTTPSuccessResetsOnlyTheSuccessfulModelStreak(t *testing.T) {
	tests := []struct {
		name     string
		failures int
		cooldown time.Duration
	}{
		{name: "first failure without cooldown", failures: 1},
		{name: "second failure with ten seconds", failures: 2, cooldown: 10 * time.Second},
		{name: "third failure with forty five seconds", failures: 3, cooldown: 45 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			state := newOpenAIAccountModelTransientState(16)
			svc := &OpenAIGatewayService{openaiModelTransient: state}
			// Keep active cooldowns unambiguously in the future without sleeping.
			at := time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC)
			var decision openAIAccountModelTransientDecision
			for i := 0; i < tt.failures; i++ {
				decision = state.recordFailure(account.ID, "gpt-6-astra", at.Add(time.Duration(i)*time.Millisecond))
			}
			require.Equal(t, tt.failures, decision.FailureStreak)
			require.Equal(t, tt.cooldown, decision.Cooldown)
			state.recordFailure(account.ID, "deepseek-v4-pro", at)
			state.recordFailure(43, "gpt-6-astra", at)

			svc.ReportOpenAIOfficialHTTPAccountScheduleResult(WithOpenAIOfficialHTTPFailover(context.Background()), account, "gpt-6-astra", true, nil, nil)

			successKey, ok := openAIAccountModelTransientKey(account.ID, "gpt-6-astra")
			require.True(t, ok)
			otherModelKey, ok := openAIAccountModelTransientKey(account.ID, "deepseek-v4-pro")
			require.True(t, ok)
			otherAccountKey, ok := openAIAccountModelTransientKey(43, "gpt-6-astra")
			require.True(t, ok)
			state.mu.Lock()
			_, successfulModelRemains := state.entries[successKey]
			otherModel, otherModelRemains := state.entries[otherModelKey]
			otherAccount, otherAccountRemains := state.entries[otherAccountKey]
			state.mu.Unlock()
			require.False(t, successfulModelRemains, "a successful official HTTP attempt resets even an active model cooldown")
			require.True(t, otherModelRemains)
			require.Equal(t, 1, otherModel.failureStreak)
			require.True(t, otherAccountRemains)
			require.Equal(t, 1, otherAccount.failureStreak)

			next := state.recordFailure(account.ID, "gpt-6-astra", at.Add(time.Second))
			require.Equal(t, 1, next.FailureStreak)
			require.Zero(t, next.Cooldown)
		})
	}
}

func TestOpenAIOfficialHTTPHealthFailureCountsOnceAndPersistsOnlyOnTrip(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		tripped bool
	}{
		{name: "disabled", tripped: true},
		{name: "enabled below threshold", enabled: true},
		{name: "enabled threshold reached", enabled: true, tripped: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := json.Marshal(OpenAIAPIKeyHealthBreakerSettings{Enabled: tt.enabled, WindowMinutes: 1, FailureThreshold: 3, CooldownMinutes: 5})
			require.NoError(t, err)
			settings := NewSettingService(&openAIAPIKeyHealthSettingRepo{value: string(encoded)}, &config.Config{})
			cache := &openAIAPIKeyHealthCacheStub{tripped: tt.tripped}
			repo := &openAIAPIKeyHealthAccountRepo{}
			blocker := &openAIAPIKeyHealthRuntimeBlocker{}
			rateLimit := NewRateLimitService(repo, nil, &config.Config{}, nil, cache)
			rateLimit.SetSettingService(settings)
			rateLimit.SetOpenAIAPIKeyHealthCache(cache)
			rateLimit.SetAccountRuntimeBlocker(blocker)
			svc := &OpenAIGatewayService{rateLimitService: rateLimit}
			account := openAIHealthPoolAccount()
			observedErr := &UpstreamFailoverError{StatusCode: http.StatusBadGateway}

			tripped := svc.ReportOpenAIOfficialHTTPAccountScheduleResult(WithOpenAIOfficialHTTPFailover(context.Background()), account, "gpt-6-astra", false, nil, observedErr)

			wantTripped := tt.enabled && tt.tripped
			require.Equal(t, wantTripped, tripped)
			wantRecordCalls := 0
			if tt.enabled {
				wantRecordCalls = 1
			}
			wantSetCalls := 0
			if wantTripped {
				wantSetCalls = 1
			}
			require.Equal(t, wantRecordCalls, cache.recordCalls, "one failed attempt must not count through both old and official health reporters")
			require.Equal(t, wantSetCalls, repo.setCalls)
			require.Equal(t, wantSetCalls, cache.setCalls)
			require.Equal(t, wantSetCalls, blocker.calls)
			if wantTripped {
				require.NotNil(t, account.TempUnschedulableUntil)
				require.Contains(t, repo.reason, openAIAPIKeyHealthBreakerReason)
			} else {
				require.Nil(t, account.TempUnschedulableUntil)
			}
		})
	}
}
