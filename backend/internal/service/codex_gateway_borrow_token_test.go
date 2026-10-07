//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type borrowTokenObservationRepository struct {
	AccountRepository
	errorWrites int
}

func (r *borrowTokenObservationRepository) SetError(context.Context, int64, string) error {
	r.errorWrites++
	return nil
}

type borrowTokenObservationBlocker struct{ calls int }

func (b *borrowTokenObservationBlocker) BlockAccountScheduling(*Account, time.Time, string) {
	b.calls++
}

func (b *borrowTokenObservationBlocker) ClearAccountSchedulingBlock(int64) {
	b.calls++
}

func TestCodexGatewayBorrowTokenObservationKeepsBusinessProtection(t *testing.T) {
	for _, observation := range []bool{false, true} {
		name := "ordinary_business"
		if observation {
			name = "borrow_observation"
		}
		t.Run(name, func(t *testing.T) {
			repo := &borrowTokenObservationRepository{}
			blocker := &borrowTokenObservationBlocker{}
			provider := NewOpenAITokenProvider(repo, nil, nil)
			provider.SetAccountRuntimeBlocker(blocker)
			account := &Account{ID: 73, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
				Credentials: map[string]any{
					"access_token": "synthetic-expired-token",
					"expires_at":   time.Now().Add(-time.Hour).Format(time.RFC3339),
				}}
			ctx := context.Background()
			if observation {
				ctx = WithCodexGatewayBorrowObservation(ctx)
			}
			token, err := provider.GetAccessToken(ctx, account)
			require.Empty(t, token)
			require.ErrorContains(t, err, "refresh_token is missing")
			writes := 1
			if observation {
				writes = 0
			}
			require.Equal(t, writes, repo.errorWrites)
			require.Equal(t, writes, blocker.calls)
			require.Equal(t, StatusActive, account.Status)
		})
	}
}
