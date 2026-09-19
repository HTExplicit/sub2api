package service

import (
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
