//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountIsSchedulable_QuotaExceeded(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name    string
		account *Account
		want    bool
	}{
		{
			name: "Cindy balance marker overrides active schedulable status",
			account: &Account{
				Platform:                   PlatformCindy,
				Credentials:                map[string]any{"base_url": "https://api.laxarouter.ai"},
				Status:                     StatusActive,
				Schedulable:                true,
				Type:                       AccountTypeAPIKey,
				CindyBalanceInsufficientAt: &now,
			},
			want: false,
		},
		{
			name: "ordinary API key ignores residual Cindy terminal markers",
			account: &Account{
				Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Status: StatusActive, Schedulable: true,
				Credentials:                map[string]any{"base_url": "https://api.openai.com"},
				CindyBalanceInsufficientAt: &now, CindyBannedAt: &now,
			},
			want: true,
		},
		{
			name: "apikey daily quota exceeded",
			account: &Account{
				Status:      StatusActive,
				Schedulable: true,
				Type:        AccountTypeAPIKey,
				Extra: map[string]any{
					"quota_daily_limit": 10.0,
					"quota_daily_used":  10.0,
					"quota_daily_start": now.Add(-1 * time.Hour).Format(time.RFC3339),
				},
			},
			want: false,
		},
		{
			name: "apikey weekly quota exceeded",
			account: &Account{
				Status:      StatusActive,
				Schedulable: true,
				Type:        AccountTypeAPIKey,
				Extra: map[string]any{
					"quota_weekly_limit": 50.0,
					"quota_weekly_used":  50.0,
					"quota_weekly_start": now.Add(-2 * 24 * time.Hour).Format(time.RFC3339),
				},
			},
			want: false,
		},
		{
			name: "apikey total quota exceeded",
			account: &Account{
				Status:      StatusActive,
				Schedulable: true,
				Type:        AccountTypeAPIKey,
				Extra: map[string]any{
					"quota_limit": 100.0,
					"quota_used":  100.0,
				},
			},
			want: false,
		},
		{
			name: "apikey quota not exceeded",
			account: &Account{
				Status:      StatusActive,
				Schedulable: true,
				Type:        AccountTypeAPIKey,
				Extra: map[string]any{
					"quota_daily_limit": 10.0,
					"quota_daily_used":  5.0,
					"quota_daily_start": now.Add(-1 * time.Hour).Format(time.RFC3339),
				},
			},
			want: true,
		},
		{
			name: "apikey expired daily period restores schedulable",
			account: &Account{
				Status:      StatusActive,
				Schedulable: true,
				Type:        AccountTypeAPIKey,
				Extra: map[string]any{
					"quota_daily_limit": 10.0,
					"quota_daily_used":  10.0,
					"quota_daily_start": now.Add(-25 * time.Hour).Format(time.RFC3339),
				},
			},
			want: true,
		},
		{
			name: "oauth ignores quota exceeded",
			account: &Account{
				Status:      StatusActive,
				Schedulable: true,
				Type:        AccountTypeOAuth,
				Extra: map[string]any{
					"quota_daily_limit": 10.0,
					"quota_daily_used":  10.0,
					"quota_daily_start": now.Add(-1 * time.Hour).Format(time.RFC3339),
				},
			},
			want: true,
		},
		{
			name: "bedrock quota exceeded",
			account: &Account{
				Status:      StatusActive,
				Schedulable: true,
				Type:        AccountTypeBedrock,
				Extra: map[string]any{
					"quota_limit": 200.0,
					"quota_used":  200.0,
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.account.IsSchedulable())
		})
	}
}
