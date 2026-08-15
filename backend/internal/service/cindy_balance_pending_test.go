//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type cindyBalancePendingStoreStub struct {
	*stubGatewayCache
	mu        sync.Mutex
	pending   map[int64]bool
	markErr   error
	hasErr    error
	hasErrs   []error
	clearErr  error
	hasCalls  int
	lastBatch int
}

type cindyPendingOrderRepo struct {
	cindyRateLimitAccountRepoStub
	store         *cindyBalancePendingStoreStub
	pendingAtMark bool
}

type cindyRecoveryRetryRepo struct {
	accountRepoStubForClearAccountError

	mu           sync.Mutex
	markCalls    int
	clearCalls   int
	firstMarkErr error
	retryStarted chan struct{}
	retryRelease chan struct{}
}

func (r *cindyRecoveryRetryRepo) GetByID(_ context.Context, _ int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	account := *r.account
	return &account, nil
}

func (r *cindyRecoveryRetryRepo) MarkCindyBalanceInsufficient(_ context.Context, _ int64, observedAt time.Time) (bool, error) {
	r.mu.Lock()
	r.markCalls++
	call := r.markCalls
	if call == 1 && r.firstMarkErr != nil {
		err := r.firstMarkErr
		r.mu.Unlock()
		return false, err
	}
	started := r.retryStarted
	release := r.retryRelease
	r.mu.Unlock()

	if call == 2 && started != nil {
		started <- struct{}{}
		<-release
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	changed := r.account.CindyBalanceInsufficientAt == nil
	markedAt := observedAt
	r.account.CindyBalanceInsufficientAt = &markedAt
	return changed, nil
}

func (r *cindyRecoveryRetryRepo) ClearCindyBalanceInsufficient(_ context.Context, _ int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearCalls++
	changed := r.account.CindyBalanceInsufficientAt != nil
	r.account.CindyBalanceInsufficientAt = nil
	return changed, nil
}

func (r *cindyRecoveryRetryRepo) PreviewCindyInsufficientDeletion(context.Context) (*CindyInsufficientDeletePreview, error) {
	return &CindyInsufficientDeletePreview{}, nil
}

func (r *cindyRecoveryRetryRepo) DeleteCindyInsufficient(context.Context, int, string) (*CindyInsufficientDeleteResult, error) {
	return &CindyInsufficientDeleteResult{}, nil
}

func (r *cindyRecoveryRetryRepo) state() (markCalls, clearCalls int, marked bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.markCalls, r.clearCalls, r.account.CindyBalanceInsufficientAt != nil
}

func (r *cindyPendingOrderRepo) MarkCindyBalanceInsufficient(ctx context.Context, accountID int64, observedAt time.Time) (bool, error) {
	r.pendingAtMark = r.store.isPending(accountID)
	return r.cindyRateLimitAccountRepoStub.MarkCindyBalanceInsufficient(ctx, accountID, observedAt)
}

func newCindyBalancePendingStoreStub() *cindyBalancePendingStoreStub {
	return &cindyBalancePendingStoreStub{
		stubGatewayCache: &stubGatewayCache{},
		pending:          make(map[int64]bool),
	}
}

func (s *cindyBalancePendingStoreStub) MarkCindyBalancePending(_ context.Context, accountID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.markErr != nil {
		return s.markErr
	}
	s.pending[accountID] = true
	return nil
}

func (s *cindyBalancePendingStoreStub) HasCindyBalancePendingBatch(_ context.Context, accountIDs []int64) (map[int64]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hasCalls++
	s.lastBatch = len(accountIDs)
	if len(s.hasErrs) > 0 {
		err := s.hasErrs[0]
		s.hasErrs = s.hasErrs[1:]
		if err != nil {
			return nil, err
		}
	}
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
	if s.clearErr != nil {
		return s.clearErr
	}
	delete(s.pending, accountID)
	return nil
}

func (s *cindyBalancePendingStoreStub) isPending(accountID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending[accountID]
}

func TestCindyBalancePendingSurvivesGatewayServiceRebuild(t *testing.T) {
	store := newCindyBalancePendingStoreStub()
	repo := &cindyRateLimitAccountRepoStub{markErr: errors.New("database unavailable"), markFailures: -1}
	rateLimit := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	rateLimit.SetCindyBalancePendingStore(store)
	first := &OpenAIGatewayService{cache: store, rateLimitService: rateLimit}
	rateLimit.SetAccountRuntimeBlocker(first)
	account := newCindyRateLimitAccount(99101, true)

	require.True(t, first.handleOpenAIAccountUpstreamError(
		context.Background(), account, http.StatusTooManyRequests, http.Header{},
		[]byte(exactCindyBudgetExceededBody), "gpt-5.6-luna",
	))
	require.True(t, store.isPending(account.ID))

	// This instance has no local sentinel and models a fresh process. The shared
	// pending marker alone must keep the account out of scheduling.
	rebuilt := &OpenAIGatewayService{cache: store}
	require.True(t, rebuilt.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.6-luna"))
}

func TestCindyBalanceDatabaseSuccessClearsPendingMarker(t *testing.T) {
	store := newCindyBalancePendingStoreStub()
	repo := &cindyRateLimitAccountRepoStub{}
	rateLimit := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	rateLimit.SetCindyBalancePendingStore(store)
	gateway := &OpenAIGatewayService{cache: store, rateLimitService: rateLimit}
	rateLimit.SetAccountRuntimeBlocker(gateway)
	account := newCindyRateLimitAccount(99102, true)

	require.True(t, gateway.handleOpenAIAccountUpstreamError(
		context.Background(), account, http.StatusTooManyRequests, http.Header{},
		[]byte(exactCindyBudgetExceededBody), "gpt-5.6-luna",
	))
	require.NotNil(t, account.CindyBalanceInsufficientAt)
	require.False(t, store.isPending(account.ID))
}

func TestCindyBalancePendingIsDurableBeforeDatabaseWrite(t *testing.T) {
	store := newCindyBalancePendingStoreStub()
	repo := &cindyPendingOrderRepo{store: store}
	rateLimit := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	rateLimit.SetCindyBalancePendingStore(store)
	gateway := &OpenAIGatewayService{cache: store, rateLimitService: rateLimit}
	rateLimit.SetAccountRuntimeBlocker(gateway)
	account := newCindyRateLimitAccount(99107, true)

	require.True(t, gateway.handleOpenAIAccountUpstreamError(
		context.Background(), account, http.StatusTooManyRequests, http.Header{},
		[]byte(exactCindyBudgetExceededBody), "gpt-5.6-luna",
	))
	require.True(t, repo.pendingAtMark)
	require.False(t, store.isPending(account.ID), "DB success must clear the transitional marker")
}

func TestCindyBalancePendingReadErrorFailsClosedOnlyForStrictCindy(t *testing.T) {
	store := newCindyBalancePendingStoreStub()
	store.hasErr = errors.New("redis unavailable")
	gateway := &OpenAIGatewayService{cache: store}

	strictCindy := newCindyRateLimitAccount(99103, true)
	require.True(t, gateway.isOpenAIAccountRequestRuntimeBlocked(strictCindy, "gpt-5.6-luna"))

	nonCindy := &Account{
		ID:          99104,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"base_url": "https://api.openai.com"},
	}
	require.False(t, gateway.isOpenAIAccountRequestRuntimeBlocked(nonCindy, "gpt-5.6-luna"))
	store.mu.Lock()
	require.Equal(t, 1, store.hasCalls, "non-Cindy accounts must not query the pending store")
	store.mu.Unlock()
}

func TestCindyBalancePendingTwoThousandCandidatesUseOneBatchSnapshot(t *testing.T) {
	store := newCindyBalancePendingStoreStub()
	gateway := &OpenAIGatewayService{cache: store}
	accounts := make([]Account, 2000)
	for i := range accounts {
		accounts[i] = *newCindyRateLimitAccount(int64(100000+i), true)
	}
	store.pending[accounts[777].ID] = true

	ctx := gateway.withCindyBalancePendingSnapshot(context.Background(), accounts)
	blocked := 0
	for i := range accounts {
		requestedModel := "gpt-5.6-luna"
		if gateway.isOpenAIAccountRequestRuntimeBlockedContext(ctx, &accounts[i], requestedModel) {
			blocked++
		}
		// Mirrors the advanced scheduler compatibility recheck. It must reuse
		// the same request snapshot rather than querying Redis again.
		if gateway.isOpenAIAccountRequestRuntimeBlockedContext(ctx, &accounts[i], requestedModel) {
			_ = true
		}
	}

	require.Equal(t, 1, blocked)
	store.mu.Lock()
	require.Equal(t, 1, store.hasCalls)
	require.Equal(t, 2000, store.lastBatch)
	store.mu.Unlock()
}

func TestLegacyStickyPendingFallbackUsesOneFailClosedSnapshot(t *testing.T) {
	store := newCindyBalancePendingStoreStub()
	store.hasErrs = []error{errors.New("redis unavailable"), nil}
	sticky := newCindyRateLimitAccount(135001, true)
	sticky.Priority = 0
	fallback := newCindyRateLimitAccount(135002, true)
	fallback.Priority = 1
	sessionHash := "cindy-pending-first-read-fails"
	store.sessionBindings = map[string]int64{"openai:" + sessionHash: sticky.ID}
	gateway := &OpenAIGatewayService{
		accountRepo: stubOpenAIAccountRepo{accounts: []Account{*sticky, *fallback}},
		cache:       store,
	}

	selected, err := gateway.SelectAccountForModelWithExclusions(
		context.Background(), nil, sessionHash, "gpt-5.6-luna", nil,
	)

	require.Error(t, err)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, selected)
	store.mu.Lock()
	require.Equal(t, 1, store.hasCalls)
	require.Equal(t, 2, store.lastBatch)
	store.mu.Unlock()
}

func TestAdvancedSchedulerTwoThousandCindyCandidatesUsesOnePendingBatch(t *testing.T) {
	store := newCindyBalancePendingStoreStub()
	accounts := make([]Account, 2000)
	for i := range accounts {
		accounts[i] = *newCindyRateLimitAccount(int64(140000+i), true)
		accounts[i].Concurrency = 1
	}
	store.pending[accounts[777].ID] = true
	svc := &OpenAIGatewayService{
		accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:       store,
		cfg:         &config.Config{},
	}
	scheduler := &defaultOpenAIAccountScheduler{service: svc}

	selection, candidateCount, _, _, err := scheduler.selectByLoadBalance(context.Background(), OpenAIAccountScheduleRequest{
		Platform:       PlatformOpenAI,
		RequestedModel: "gpt-5.6-luna",
	})

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotEqual(t, accounts[777].ID, selection.Account.ID)
	require.Equal(t, 1999, candidateCount)
	store.mu.Lock()
	require.Equal(t, 1, store.hasCalls)
	require.Equal(t, 2000, store.lastBatch)
	store.mu.Unlock()
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestAdvancedSchedulerPendingBatchFailureClosesEntireCindyPool(t *testing.T) {
	store := newCindyBalancePendingStoreStub()
	store.hasErr = errors.New("redis unavailable")
	accounts := make([]Account, 32)
	for i := range accounts {
		accounts[i] = *newCindyRateLimitAccount(int64(150000+i), true)
		accounts[i].Concurrency = 1
	}
	svc := &OpenAIGatewayService{
		accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:       store,
		cfg:         &config.Config{},
	}
	scheduler := &defaultOpenAIAccountScheduler{service: svc}

	selection, _, _, _, err := scheduler.selectByLoadBalance(context.Background(), OpenAIAccountScheduleRequest{
		Platform:       PlatformOpenAI,
		RequestedModel: "gpt-5.6-luna",
	})

	require.Error(t, err)
	require.Nil(t, selection)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	store.mu.Lock()
	require.Equal(t, 1, store.hasCalls)
	require.Equal(t, len(accounts), store.lastBatch)
	store.mu.Unlock()
}

func TestCindyBalancePendingBatchFailureClosesEntireStrictPool(t *testing.T) {
	store := newCindyBalancePendingStoreStub()
	store.hasErr = errors.New("redis unavailable")
	gateway := &OpenAIGatewayService{cache: store}
	accounts := []Account{
		*newCindyRateLimitAccount(120001, true),
		*newCindyRateLimitAccount(120002, true),
		{
			ID:          120003,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Schedulable: true,
			Credentials: map[string]any{"base_url": "https://api.openai.com"},
		},
	}

	ctx := gateway.withCindyBalancePendingSnapshot(context.Background(), accounts)
	require.True(t, gateway.isOpenAIAccountRequestRuntimeBlockedContext(ctx, &accounts[0], "gpt-5.6-luna"))
	require.True(t, gateway.isOpenAIAccountRequestRuntimeBlockedContext(ctx, &accounts[1], "gpt-5.6-luna"))
	require.False(t, gateway.isOpenAIAccountRequestRuntimeBlockedContext(ctx, &accounts[2], "gpt-5.6-luna"))
	store.mu.Lock()
	require.Equal(t, 1, store.hasCalls)
	require.Equal(t, 2, store.lastBatch)
	store.mu.Unlock()
}

func BenchmarkCindyBalancePendingSnapshotTwoThousandCandidates(b *testing.B) {
	store := newCindyBalancePendingStoreStub()
	gateway := &OpenAIGatewayService{cache: store}
	accounts := make([]Account, 2000)
	for i := range accounts {
		accounts[i] = *newCindyRateLimitAccount(int64(130000+i), true)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := gateway.withCindyBalancePendingSnapshot(context.Background(), accounts)
		for index := range accounts {
			_ = gateway.isOpenAIAccountRequestRuntimeBlockedContext(ctx, &accounts[index], "gpt-5.6-luna")
		}
	}
}

func TestCindyBalanceBothDurableWritesFailKeepsLocalSentinel(t *testing.T) {
	store := newCindyBalancePendingStoreStub()
	store.markErr = errors.New("redis unavailable")
	repo := &cindyRateLimitAccountRepoStub{markErr: errors.New("database unavailable"), markFailures: -1}
	rateLimit := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	rateLimit.SetCindyBalancePendingStore(store)
	gateway := &OpenAIGatewayService{cache: store, rateLimitService: rateLimit}
	rateLimit.SetAccountRuntimeBlocker(gateway)
	account := newCindyRateLimitAccount(99105, true)

	require.True(t, gateway.handleOpenAIAccountUpstreamError(
		context.Background(), account, http.StatusTooManyRequests, http.Header{},
		[]byte(exactCindyBudgetExceededBody), "gpt-5.6-luna",
	))
	value, ok := gateway.openaiAccountRuntimeBlockUntil.Load(account.ID)
	require.True(t, ok)
	until, ok := value.(time.Time)
	require.True(t, ok)
	require.True(t, until.IsZero())
	require.True(t, gateway.isOpenAIAccountRuntimeBlocked(account))
}

func TestAdminCindyBalanceRecoveryClearsDurablePendingBeforeLocalBlock(t *testing.T) {
	markedAt := time.Now().UTC()
	repo := &cindyBalanceAdminRepoStub{
		accountRepoStubForClearAccountError: accountRepoStubForClearAccountError{
			account: &Account{
				ID:                         99106,
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
	store.pending[99106] = true
	gateway := &OpenAIGatewayService{cache: store}
	gateway.BlockAccountScheduling(repo.account, time.Time{}, "cindy_balance_insufficient")
	svc := &adminServiceImpl{accountRepo: repo, runtimeBlocker: gateway}

	updated, err := svc.ClearCindyBalanceInsufficient(context.Background(), 99106)

	require.NoError(t, err)
	require.Nil(t, updated.CindyBalanceInsufficientAt)
	require.False(t, store.isPending(99106))
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(updated))
}

func newCindyRecoveryRetryFixture(t *testing.T, accountID int64) (*cindyRecoveryRetryRepo, *cindyBalancePendingStoreStub, *RateLimitService, *OpenAIGatewayService) {
	t.Helper()
	repo := &cindyRecoveryRetryRepo{
		accountRepoStubForClearAccountError: accountRepoStubForClearAccountError{
			account: newCindyRateLimitAccount(accountID, true),
		},
		firstMarkErr: errors.New("database unavailable"),
	}
	store := newCindyBalancePendingStoreStub()
	rateLimit := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	rateLimit.SetCindyBalancePendingStore(store)
	gateway := &OpenAIGatewayService{cache: store, rateLimitService: rateLimit}
	rateLimit.SetAccountRuntimeBlocker(gateway)
	account, err := repo.GetByID(context.Background(), accountID)
	require.NoError(t, err)
	require.True(t, gateway.handleOpenAIAccountUpstreamError(
		context.Background(), account, http.StatusTooManyRequests, http.Header{},
		[]byte(exactCindyBudgetExceededBody), "gpt-5.6-luna",
	))
	require.True(t, store.isPending(accountID))
	require.True(t, gateway.isOpenAIAccountRuntimeBlocked(account))

	// Keep the background worker asleep; tests explicitly advance the captured
	// task to cover queued and already-extracted retry states deterministically.
	rateLimit.cindyBalancePersistMu.Lock()
	task := rateLimit.cindyBalancePersistTasks[accountID]
	if task != nil {
		task.nextAt = time.Now().Add(time.Hour)
	}
	wake := rateLimit.cindyBalancePersistWake
	rateLimit.cindyBalancePersistMu.Unlock()
	require.NotNil(t, task)
	if wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	return repo, store, rateLimit, gateway
}

func capturedCindyRecoveryTask(t *testing.T, rateLimit *RateLimitService, accountID int64) *cindyBalancePersistTask {
	t.Helper()
	rateLimit.cindyBalancePersistMu.Lock()
	defer rateLimit.cindyBalancePersistMu.Unlock()
	task := rateLimit.cindyBalancePersistTasks[accountID]
	require.NotNil(t, task)
	return task
}

func TestAdminRecoveryCancelsQueuedCindyBalancePersistence(t *testing.T) {
	const accountID = int64(99108)
	repo, store, rateLimit, gateway := newCindyRecoveryRetryFixture(t, accountID)
	task := capturedCindyRecoveryTask(t, rateLimit, accountID)
	admin := &adminServiceImpl{accountRepo: repo, runtimeBlocker: gateway}

	updated, err := admin.ClearCindyBalanceInsufficient(context.Background(), accountID)
	require.NoError(t, err)

	// Simulate the worker advancing a pointer it extracted before recovery. The
	// generation tombstone must make this stale task a no-op.
	rateLimit.runCindyBalancePersistenceTask(task)

	markCalls, clearCalls, marked := repo.state()
	require.Equal(t, 1, markCalls, "stale queued retry must not reach the database")
	require.Equal(t, 1, clearCalls)
	require.False(t, marked)
	require.Nil(t, updated.CindyBalanceInsufficientAt)
	require.False(t, store.isPending(accountID))
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(updated))
	rateLimit.cindyBalancePersistMu.Lock()
	require.Nil(t, rateLimit.cindyBalancePersistTasks[accountID])
	require.Greater(t, rateLimit.cindyBalancePersistEpochs[accountID], task.generation)
	rateLimit.cindyBalancePersistMu.Unlock()
}

func TestAdminRecoveryWaitsForInFlightCindyBalancePersistence(t *testing.T) {
	const accountID = int64(99109)
	repo, store, rateLimit, gateway := newCindyRecoveryRetryFixture(t, accountID)
	repo.retryStarted = make(chan struct{}, 1)
	repo.retryRelease = make(chan struct{})
	task := capturedCindyRecoveryTask(t, rateLimit, accountID)
	retryDone := make(chan struct{})
	go func() {
		defer close(retryDone)
		rateLimit.runCindyBalancePersistenceTask(task)
	}()
	select {
	case <-repo.retryStarted:
	case <-time.After(time.Second):
		t.Fatal("persistence retry did not enter the database write")
	}

	admin := &adminServiceImpl{accountRepo: repo, runtimeBlocker: gateway}
	type recoveryResult struct {
		account *Account
		err     error
	}
	recoveryDone := make(chan recoveryResult, 1)
	go func() {
		account, err := admin.ClearCindyBalanceInsufficient(context.Background(), accountID)
		recoveryDone <- recoveryResult{account: account, err: err}
	}()
	select {
	case result := <-recoveryDone:
		t.Fatalf("recovery returned before in-flight persistence drained: %v", result.err)
	case <-time.After(50 * time.Millisecond):
	}

	close(repo.retryRelease)
	select {
	case <-retryDone:
	case <-time.After(time.Second):
		t.Fatal("in-flight persistence did not exit")
	}
	var result recoveryResult
	select {
	case result = <-recoveryDone:
	case <-time.After(time.Second):
		t.Fatal("recovery did not finish after persistence drained")
	}
	require.NoError(t, result.err)
	require.NotNil(t, result.account)

	markCalls, clearCalls, marked := repo.state()
	require.Equal(t, 2, markCalls, "the in-flight write may finish, then recovery must clear it")
	require.Equal(t, 1, clearCalls)
	require.False(t, marked)
	require.Nil(t, result.account.CindyBalanceInsufficientAt)
	require.False(t, store.isPending(accountID))
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(result.account))
}
