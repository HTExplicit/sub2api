//go:build unit

package service

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type cindyRateLimitAccountRepoStub struct {
	rateLimitAccountRepoStub
	mu          sync.Mutex
	markCalls   int
	markChanged int
	marked      bool
}

func (r *cindyRateLimitAccountRepoStub) MarkCindyBalanceInsufficient(context.Context, int64, time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markCalls++
	if r.marked {
		return false, nil
	}
	r.marked = true
	r.markChanged++
	return true, nil
}

func (r *cindyRateLimitAccountRepoStub) ClearCindyBalanceInsufficient(context.Context, int64) (bool, error) {
	return false, nil
}

func (r *cindyRateLimitAccountRepoStub) PreviewCindyInsufficientDeletion(context.Context) (*CindyInsufficientDeletePreview, error) {
	return &CindyInsufficientDeletePreview{}, nil
}

func (r *cindyRateLimitAccountRepoStub) DeleteCindyInsufficient(context.Context, int, string) (*CindyInsufficientDeleteResult, error) {
	return &CindyInsufficientDeleteResult{}, nil
}

func newCindyRateLimitAccount(id int64, poolMode bool) *Account {
	credentials := cindyCredentials()
	credentials["pool_mode"] = poolMode
	return &Account{
		ID:          id,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: credentials,
	}
}

func TestRateLimitServiceCindy402MarksBeforePoolModeSkip(t *testing.T) {
	for _, poolMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "pool"}[poolMode], func(t *testing.T) {
			repo := &cindyRateLimitAccountRepoStub{}
			blocker := &runtimeBlockRecorder{}
			svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			svc.SetAccountRuntimeBlocker(blocker)
			account := newCindyRateLimitAccount(8101, poolMode)

			shouldDisable := svc.HandleUpstreamError(context.Background(), account, http.StatusPaymentRequired, http.Header{}, nil)

			require.True(t, shouldDisable)
			require.Equal(t, 1, repo.markCalls)
			require.Equal(t, 1, repo.markChanged)
			require.Zero(t, repo.setErrorCalls, "Cindy 402 must preserve the account's manual status")
			require.NotNil(t, account.CindyBalanceInsufficientAt)
			require.Len(t, blocker.accounts, 1)
			require.True(t, blocker.until[0].IsZero(), "Cindy balance block must not expire automatically")
			require.Equal(t, "cindy_balance_insufficient", blocker.reasons[0])
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
}

func TestRateLimitServiceCindy402ConcurrentMarkIsIdempotent(t *testing.T) {
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
			results <- svc.HandleUpstreamError(context.Background(), account, http.StatusPaymentRequired, http.Header{}, nil)
		}()
	}
	wg.Wait()
	close(results)
	for shouldDisable := range results {
		require.True(t, shouldDisable)
	}

	require.Equal(t, requests, repo.markCalls)
	require.Equal(t, 1, repo.markChanged)
}
