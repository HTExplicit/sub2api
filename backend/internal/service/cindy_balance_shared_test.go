package service

import (
	"context"
	"strings"
	"sync"
	"time"
)

const exactCindyBudgetExceededBody = `{"error":{"message":"ExceededBudget: User=aigw:v1:cindy:fixture-account over budget. Spend=3.0533505, Budget=3.0","type":"budget_exceeded","param":null,"code":"429"}}`

// cindyRateLimitAccountRepoStub is shared by default and unit-tagged transport
// tests. Embedding the production interface keeps the fixture focused on the
// balance methods exercised by these tests.
type cindyRateLimitAccountRepoStub struct {
	AccountRepository
	mu            sync.Mutex
	markCalls     int
	markChanged   int
	marked        bool
	markErr       error
	markFailures  int
	setErrorCalls int
}

type cindyHealthCoordinatorRecorder struct {
	mu        sync.Mutex
	signals   []CindyHealthSignal
	accounts  []int64
	successes []int64
}

// cindyBalanceRecoveryInterleavingCache is shared by default and unit-tagged
// recovery tests so both build modes exercise the same episode-CAS fixture.
type cindyBalanceRecoveryInterleavingCache struct {
	*stubGatewayCache
	terminalPending  *CindyHealthEpisode
	afterGet         func()
	afterClear       func()
	legacyClearErr   error
	legacyClearCalls int
	legacyCleared    bool
	legacyPending    bool
}

func (c *cindyBalanceRecoveryInterleavingCache) GetCindyBalancePendingFingerprint(_ context.Context, _ int64) (string, error) {
	if c.legacyPending {
		return strings.Repeat("f", 64), nil
	}
	return "", nil
}

func (c *cindyBalanceRecoveryInterleavingCache) HasCindyBalancePendingBatch(_ context.Context, accountIDs []int64) (map[int64]bool, error) {
	result := make(map[int64]bool)
	if c.legacyPending {
		for _, accountID := range accountIDs {
			result[accountID] = true
		}
	}
	return result, nil
}

func (c *cindyBalanceRecoveryInterleavingCache) ClearCindyBalancePending(_ context.Context, _ int64) error {
	c.legacyClearCalls++
	if c.legacyClearErr != nil {
		return c.legacyClearErr
	}
	c.legacyCleared = true
	c.legacyPending = false
	return nil
}

func (c *cindyBalanceRecoveryInterleavingCache) ClearCindyBalancePendingIfFingerprintMatches(
	ctx context.Context,
	accountID int64,
	_ string,
) error {
	return c.ClearCindyBalancePending(ctx, accountID)
}

func (c *cindyBalanceRecoveryInterleavingCache) GetCindyHealthTerminalPending(
	_ context.Context,
	_ int64,
	_ string,
) (*CindyHealthEpisode, error) {
	var captured *CindyHealthEpisode
	if c.terminalPending != nil {
		episode := *c.terminalPending
		captured = &episode
	}
	if c.afterGet != nil {
		afterGet := c.afterGet
		c.afterGet = nil
		afterGet()
	}
	return captured, nil
}

func (c *cindyBalanceRecoveryInterleavingCache) ClearCindyHealthTerminalPendingIfMatch(
	_ context.Context,
	episode CindyHealthEpisode,
) (bool, error) {
	current := c.terminalPending
	if current == nil || current.AccountID != episode.AccountID || current.Generation != episode.Generation ||
		current.EpisodeID != episode.EpisodeID || current.Fingerprint != episode.Fingerprint || current.Status != episode.Status {
		return false, nil
	}
	c.terminalPending = nil
	if c.afterClear != nil {
		c.afterClear()
	}
	return true, nil
}

func (r *cindyHealthCoordinatorRecorder) ObserveCindyHealthSignal(_ context.Context, account *Account, signal CindyHealthSignal) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.signals = append(r.signals, signal)
	if account != nil {
		r.accounts = append(r.accounts, account.ID)
	}
}

func (r *cindyHealthCoordinatorRecorder) ObserveCindyHealthSuccess(_ context.Context, account *Account) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if account != nil {
		r.successes = append(r.successes, account.ID)
	}
}

func (r *cindyRateLimitAccountRepoStub) MarkCindyBalanceInsufficient(context.Context, int64, time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markCalls++
	if r.markErr != nil && r.markFailures != 0 {
		if r.markFailures > 0 {
			r.markFailures--
		}
		return false, r.markErr
	}
	if r.marked {
		return false, nil
	}
	r.marked = true
	r.markChanged++
	return true, nil
}

func (r *cindyRateLimitAccountRepoStub) MarkCindyBalanceInsufficientIfCredentialsMatch(
	ctx context.Context,
	accountID int64,
	observedAt time.Time,
	_ string,
) (bool, bool, error) {
	changed, err := r.MarkCindyBalanceInsufficient(ctx, accountID, observedAt)
	return changed, true, err
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

func (r *cindyRateLimitAccountRepoStub) SetError(context.Context, int64, string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setErrorCalls++
	return nil
}

func newCindyRateLimitAccount(id int64, poolMode bool) *Account {
	credentials := cindyCredentials()
	credentials["pool_mode"] = poolMode
	return &Account{
		ID:              id,
		Platform:        PlatformCindy,
		WirePlatform:    WirePlatformOpenAI,
		ProviderProfile: ProviderProfileCindyLaxaV1,
		Type:            AccountTypeAPIKey,
		Status:          StatusActive,
		Schedulable:     true,
		Credentials:     credentials,
	}
}

func newFirstClassCindyRateLimitAccount(id int64, poolMode bool) *Account {
	return newCindyRateLimitAccount(id, poolMode)
}
