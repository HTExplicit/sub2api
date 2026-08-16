//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type cindyBalancePendingStoreStub struct {
	*stubGatewayCache
	mu           sync.Mutex
	pending      map[int64]bool
	fingerprints map[int64]string
	hasErr       error
	getErr       error
	clearErr     error
	hasCalls     int
	getCalls     int
	clearCalls   int
	lastBatch    int
}

func newCindyBalancePendingStoreStub() *cindyBalancePendingStoreStub {
	return &cindyBalancePendingStoreStub{
		stubGatewayCache: &stubGatewayCache{},
		pending:          make(map[int64]bool),
		fingerprints:     make(map[int64]string),
	}
}

func (s *cindyBalancePendingStoreStub) GetCindyBalancePendingFingerprint(_ context.Context, accountID int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCalls++
	return s.fingerprints[accountID], s.getErr
}

func (s *cindyBalancePendingStoreStub) HasCindyBalancePendingBatch(_ context.Context, accountIDs []int64) (map[int64]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hasCalls++
	s.lastBatch = len(accountIDs)
	if s.hasErr != nil {
		return nil, s.hasErr
	}
	result := make(map[int64]bool, len(accountIDs))
	for _, accountID := range accountIDs {
		if s.pending[accountID] {
			result[accountID] = true
		}
	}
	return result, nil
}

func (s *cindyBalancePendingStoreStub) ClearCindyBalancePending(_ context.Context, accountID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearCalls++
	if s.clearErr != nil {
		return s.clearErr
	}
	delete(s.pending, accountID)
	delete(s.fingerprints, accountID)
	return nil
}

func (s *cindyBalancePendingStoreStub) ClearCindyBalancePendingIfFingerprintMatches(
	_ context.Context,
	accountID int64,
	credentialsFingerprint string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearCalls++
	if s.clearErr != nil {
		return s.clearErr
	}
	if s.fingerprints[accountID] == credentialsFingerprint {
		delete(s.pending, accountID)
		delete(s.fingerprints, accountID)
	}
	return nil
}

func (s *cindyBalancePendingStoreStub) isPending(accountID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending[accountID]
}

func TestCindyBalancePendingWithoutDatabaseMarkerIsCleanupOnly(t *testing.T) {
	for _, tc := range []struct {
		name     string
		clearErr error
	}{
		{name: "matching legacy marker is cleared"},
		{name: "cleanup failure remains schedulable", clearErr: errors.New("redis unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newCindyBalancePendingStoreStub()
			store.clearErr = tc.clearErr
			account := newCindyRateLimitAccount(99101, true)
			fingerprint, err := CindyAccountIdentityFingerprint(account.Platform, account.Type, account.Credentials)
			require.NoError(t, err)
			store.pending[account.ID] = true
			store.fingerprints[account.ID] = fingerprint
			gateway := &OpenAIGatewayService{cache: store}

			require.False(t, gateway.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.6-luna"))
			require.Nil(t, account.CindyBalanceInsufficientAt)
			require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
			require.Equal(t, tc.clearErr != nil, store.isPending(account.ID))
			store.mu.Lock()
			require.Equal(t, 1, store.hasCalls)
			require.Equal(t, 1, store.getCalls)
			require.Equal(t, 1, store.clearCalls)
			store.mu.Unlock()
		})
	}
}

func TestCindyBalancePendingReadErrorsUseDatabaseMarkerOnly(t *testing.T) {
	t.Run("batch read", func(t *testing.T) {
		store := newCindyBalancePendingStoreStub()
		store.hasErr = errors.New("redis unavailable")
		gateway := &OpenAIGatewayService{cache: store}
		unmarked := newCindyRateLimitAccount(99103, true)
		require.False(t, gateway.isOpenAIAccountRequestRuntimeBlocked(unmarked, "gpt-5.6-luna"))

		marked := newCindyRateLimitAccount(99104, true)
		markedAt := time.Now().UTC()
		marked.CindyBalanceInsufficientAt = &markedAt
		require.True(t, gateway.isOpenAIAccountRequestRuntimeBlocked(marked, "gpt-5.6-luna"))
		store.mu.Lock()
		require.Equal(t, 1, store.hasCalls, "DB-marked accounts must not query legacy Redis state")
		store.mu.Unlock()
	})

	t.Run("fingerprint read", func(t *testing.T) {
		store := newCindyBalancePendingStoreStub()
		store.getErr = errors.New("redis unavailable")
		account := newCindyRateLimitAccount(99105, true)
		fingerprint, err := CindyAccountIdentityFingerprint(account.Platform, account.Type, account.Credentials)
		require.NoError(t, err)
		store.pending[account.ID] = true
		store.fingerprints[account.ID] = fingerprint
		gateway := &OpenAIGatewayService{cache: store}

		require.False(t, gateway.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.6-luna"))
		require.True(t, store.isPending(account.ID))
		require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account))
	})
}

func TestCindyBalancePendingFromRotatedCredentialDoesNotBlockCurrentKey(t *testing.T) {
	store := newCindyBalancePendingStoreStub()
	account := newCindyRateLimitAccount(99106, true)
	oldCredentials := cindyCredentials()
	oldCredentials["api_key"] = "old-fixture-key"
	oldFingerprint, err := CindyCredentialsFingerprint(oldCredentials)
	require.NoError(t, err)
	account.Credentials["api_key"] = "new-fixture-key"
	store.pending[account.ID] = true
	store.fingerprints[account.ID] = oldFingerprint
	gateway := &OpenAIGatewayService{cache: store}

	require.False(t, gateway.isCindyBalancePendingBlocked(context.Background(), account))
	require.False(t, store.isPending(account.ID))
}

func TestCindyBalancePendingSnapshotBatchesLegacyCleanupHints(t *testing.T) {
	store := newCindyBalancePendingStoreStub()
	gateway := &OpenAIGatewayService{cache: store}
	accounts := make([]Account, 2000)
	for i := range accounts {
		accounts[i] = *newCindyRateLimitAccount(int64(100000+i), true)
	}
	store.pending[accounts[777].ID] = true
	fingerprint, err := CindyCredentialsFingerprint(accounts[777].Credentials)
	require.NoError(t, err)
	store.fingerprints[accounts[777].ID] = fingerprint

	ctx := gateway.withCindyBalancePendingSnapshot(context.Background(), accounts)
	for i := range accounts {
		require.False(t, gateway.isOpenAIAccountRequestRuntimeBlockedContext(ctx, &accounts[i], "gpt-5.6-luna"))
	}
	require.False(t, store.isPending(accounts[777].ID))
	store.mu.Lock()
	require.Equal(t, 1, store.hasCalls)
	require.Equal(t, 2000, store.lastBatch)
	store.mu.Unlock()
}

func TestAdminCindyBalanceRecoveryIgnoresLegacyPendingCleanupFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		clearErr error
	}{
		{name: "cleanup succeeds"},
		{name: "cleanup failure", clearErr: errors.New("redis unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			markedAt := time.Now().UTC()
			repo := &cindyBalanceAdminRepoStub{
				accountRepoStubForClearAccountError: accountRepoStubForClearAccountError{
					account: &Account{
						ID:                         99107,
						Platform:                   PlatformOpenAI,
						Type:                       AccountTypeAPIKey,
						Status:                     StatusActive,
						Schedulable:                true,
						Credentials:                cindyCredentials(),
						CindyBalanceInsufficientAt: &markedAt,
					},
				},
			}
			store := newCindyBalancePendingStoreStub()
			store.pending[repo.account.ID] = true
			store.clearErr = tc.clearErr
			gateway := &OpenAIGatewayService{cache: store}
			gateway.BlockAccountScheduling(repo.account, time.Time{}, "cindy_balance_insufficient")
			svc := &adminServiceImpl{accountRepo: repo, runtimeBlocker: gateway}

			updated, err := svc.ClearCindyBalanceInsufficient(context.Background(), repo.account.ID)

			require.NoError(t, err)
			require.Nil(t, updated.CindyBalanceInsufficientAt)
			require.False(t, gateway.isOpenAIAccountRuntimeBlocked(updated))
			require.Equal(t, tc.clearErr != nil, store.isPending(repo.account.ID))
		})
	}
}
