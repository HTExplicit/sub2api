package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCindyTerminalSchedulingIsolation(t *testing.T) {
	now := time.Now()
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic, PlatformGemini, PlatformCindy} {
		t.Run(platform, func(t *testing.T) {
			account := &Account{
				Platform: platform, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
				Credentials:                map[string]any{"base_url": "https://api.laxarouter.ai"},
				CindyBalanceInsufficientAt: &now, CindyBannedAt: &now,
			}
			want := platform != PlatformCindy
			require.Equal(t, want, account.IsSchedulable())
			require.Equal(t, want, accountConsoleStatus(account, now) == StatusActive)
			account.Schedulable = false
			require.False(t, account.IsSchedulable(), "administrator pause remains authoritative")
		})
	}
}
