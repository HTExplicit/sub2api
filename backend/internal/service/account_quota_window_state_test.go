package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountQuotaWindowsActualPeriod(t *testing.T) {
	now := time.Date(2026, 9, 19, 7, 0, 0, 0, time.UTC)
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
		"codex_primary_used_percent": 100.0, "codex_primary_window_minutes": 43200,
		"codex_primary_reset_after_seconds": 2 * 86400, "codex_usage_updated_at": now.Add(-time.Hour).Format(time.RFC3339),
		"codex_secondary_used_percent": 0.0, "codex_secondary_window_minutes": 0,
	}}
	state := account.QuotaState(now)
	require.True(t, state.Blocked)
	require.Len(t, state.Windows, 1)
	require.Equal(t, 43200, state.Windows[0].WindowMinutes)
	require.Equal(t, now.Add(47*time.Hour), *state.Until)
	require.Equal(t, state.Until.Add(-30*24*time.Hour), *state.Windows[0].StatsStart())
	// A stale observation with a known future reset still blocks.
	require.True(t, account.QuotaState(now.Add(46*time.Hour)).Blocked)
	require.False(t, account.QuotaState(now.Add(48*time.Hour)).Blocked)
	account.Type = AccountTypeAPIKey
	require.Nil(t, account.QuotaState(now))
}

func TestQuotaWindowObservationsKeepIndependentDeadlinesAndClearRemovedSlots(t *testing.T) {
	now := time.Date(2026, 9, 19, 7, 0, 0, 0, time.UTC)
	primaryUsed, secondaryUsed := 10.0, 100.0
	short, long, hour := 300, 43200, 3600
	extra := buildCodexUsageExtraUpdates(&OpenAICodexUsageSnapshot{PrimaryUsedPercent: &primaryUsed, PrimaryWindowMinutes: &short, PrimaryResetAfterSeconds: &hour, SecondaryUsedPercent: &secondaryUsed, SecondaryWindowMinutes: &long, SecondaryResetAfterSeconds: &hour}, now)
	later := now.Add(30 * time.Minute)
	for key, value := range buildCodexUsageExtraUpdates(&OpenAICodexUsageSnapshot{PrimaryUsedPercent: &primaryUsed, PrimaryWindowMinutes: &short}, later) {
		extra[key] = value
	}
	windows := OpenAIQuotaWindows(extra, later)
	require.Len(t, windows, 2)
	require.Equal(t, now.Add(time.Hour), *windows[1].ResetsAt, "the untouched window keeps its original deadline")
	require.Equal(t, now, *windows[1].ObservedAt)
	account := &Account{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra}
	require.True(t, account.QuotaState(later).Blocked)
	require.False(t, account.QuotaState(now.Add(2*time.Hour)).Blocked)
	absolute := now.Add(30 * 24 * time.Hour).Unix()
	updates := buildCodexRateLimitExtraUpdates(&OpenAIRateLimit{PrimaryWindow: &OpenAIRateLimitWindow{UsedPercent: 30, LimitWindowSeconds: 30 * 86400, ResetAt: absolute, ResetAfterSeconds: 999}}, later)
	for key, value := range updates {
		extra[key] = value
	}
	windows = OpenAIQuotaWindows(extra, later)
	require.Len(t, windows, 1, "an absent secondary slot must not revive old state")
	require.Equal(t, 43200, windows[0].WindowMinutes)
	require.Equal(t, time.Unix(absolute, 0).UTC(), *windows[0].ResetsAt)
	require.Nil(t, openAIThresholdCandidate(extra, "7d", later), "a monthly window is not a weekly pause policy")
}

func TestQuotaRecoveryCannotReactivateOldUnknownErrorOrClearOtherState(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	account := &Account{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: "disabled", Schedulable: false, ErrorMessage: "operator note", Extra: map[string]any{}}
	err := persistOpenAIQuotaClassification(context.Background(), nil, account, openAIOAuth429Classification{Disposition: openAIOAuth429QuotaReset}, now)
	require.NoError(t, err)
	require.True(t, account.QuotaState(now).Blocked)
	for key, value := range buildCodexRateLimitExtraUpdates(&OpenAIRateLimit{PrimaryWindow: &OpenAIRateLimitWindow{UsedPercent: 0, LimitWindowSeconds: 18000, ResetAt: now.Add(time.Hour).Unix()}}, now.Add(time.Minute)) {
		account.Extra[key] = value
	}
	require.False(t, account.QuotaState(now.Add(2*time.Minute)).Blocked)
	require.False(t, account.QuotaState(now.Add(2*time.Hour)).Blocked, "expiry cannot resurrect a superseded unknown deadline")
	require.Equal(t, "disabled", account.Status)
	require.False(t, account.Schedulable)
	require.Equal(t, "operator note", account.ErrorMessage)
}

func TestOAuthQuota429UsesRecoverableQuotaSource(t *testing.T) {
	account := &Account{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{}}
	header := http.Header{}
	header.Set("x-codex-primary-used-percent", "100")
	header.Set("x-codex-primary-window-minutes", "10080")
	header.Set("x-codex-primary-reset-after-seconds", "3600")
	gateway := &OpenAIGatewayService{}
	gateway.markOpenAIOAuth429RateLimited(context.Background(), account, header, nil)
	require.True(t, gateway.isOpenAIAccountRuntimeBlocked(account))
	require.Nil(t, account.RateLimitResetAt)
	_, runtimeCooldown := gateway.openaiAccountRuntimeBlockUntil.Load(account.ID)
	require.False(t, runtimeCooldown, "hard quota must not leave an unrelated runtime cooldown")
	state := account.QuotaState(time.Now())
	require.NotNil(t, state.Until)
}

func TestAccountQuotaWindowsMultipleAndUnknown(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	a := &Account{Platform: PlatformOpenAI, Type: AccountTypeSetupToken, Extra: map[string]any{
		"codex_5h_used_percent": 100.0, "codex_5h_reset_at": now.Add(time.Hour).Format(time.RFC3339),
		"codex_7d_used_percent": 100.0, "codex_7d_reset_at": now.Add(3 * time.Hour).Format(time.RFC3339),
	}}
	require.Equal(t, now.Add(3*time.Hour), *a.QuotaState(now).Until)
	delete(a.Extra, "codex_7d_reset_at")
	require.True(t, a.QuotaState(now).Blocked)
	require.Nil(t, a.QuotaState(now).Until)
	a.Extra["codex_7d_used_percent"] = 50.0
	require.Equal(t, now.Add(time.Hour), *a.QuotaState(now).Until)
	require.False(t, a.QuotaState(now.Add(2*time.Hour)).Blocked)
}

func TestQuotaQueryFlagsRemainEvidenceWhenWindowDetailsAreMissing(t *testing.T) {
	now := time.Now().UTC()
	account := &Account{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: buildCodexRateLimitExtraUpdates(&OpenAIRateLimit{LimitReached: true}, now)}
	state := account.QuotaState(now)
	require.NotNil(t, state)
	require.True(t, state.Blocked)
	require.Nil(t, state.Until)
	require.NoError(t, persistOpenAIQuotaClassification(context.Background(), nil, account, openAIOAuth429Classification{Disposition: openAIOAuth429QuotaReset}, now))
	for key, value := range buildCodexRateLimitExtraUpdates(&OpenAIRateLimit{Allowed: true}, now.Add(time.Minute)) {
		account.Extra[key] = value
	}
	state = account.QuotaState(now.Add(time.Minute))
	require.True(t, state == nil || !state.Blocked, "an explicit recovered query can supersede the old quota error without inventing a window")
}
