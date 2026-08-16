package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type cindyBalanceProbeRecoveryRepositoryStub struct {
	CindyBalanceProbeRepository
	recovered bool
	err       error
	calls     int
}

type cindyBalanceProbeFinalizeRepositoryStub struct {
	CindyBalanceProbeRepository
	state          string
	err            error
	calls          int
	beforeFinalize func()
}

type cindyBalanceProbePendingStoreStub struct {
	*stubGatewayCache
	pending    map[int64]string
	clearCalls int
	clearErr   error
}

type cindyBalanceProbeDispatchRepositoryStub struct {
	CindyBalanceProbeRepository
	ready             bool
	validateCalls     int
	completeCalls     int
	validatedLease    string
	validatedItemID   int64
	validatedJobEpoch int
	validatedAccount  *Account
	databaseUpdatedAt time.Time
}

type cindyBalanceProbeClaimEpochRepositoryStub struct {
	CindyBalanceProbeRepository
	cancel      context.CancelFunc
	claimTokens []string
}

type cindyBalanceProbeCreateRepositoryStub struct {
	CindyBalanceProbeRepository
	scope               CindyBalanceProbeScope
	rateRPS             float64
	expectedCount       int
	expectedFingerprint string
	createCalls         int
	err                 error
}

func (s *cindyBalanceProbeCreateRepositoryStub) CreateJob(
	_ context.Context,
	_ *int64,
	scope CindyBalanceProbeScope,
	rateRPS float64,
	expectedCount int,
	expectedFingerprint string,
) (*CindyBalanceProbeJob, error) {
	s.createCalls++
	s.scope = scope
	s.rateRPS = rateRPS
	s.expectedCount = expectedCount
	s.expectedFingerprint = expectedFingerprint
	return nil, s.err
}

func (s *cindyBalanceProbeClaimEpochRepositoryStub) PruneFinished(context.Context, time.Time) error {
	return nil
}

func (s *cindyBalanceProbeClaimEpochRepositoryStub) ClaimJob(
	_ context.Context,
	leaseToken string,
	_ time.Time,
) (*CindyBalanceProbeJob, error) {
	s.claimTokens = append(s.claimTokens, leaseToken)
	if len(s.claimTokens) == 1 {
		return &CindyBalanceProbeJob{ID: 1}, nil
	}
	s.cancel()
	return nil, nil
}

func (s *cindyBalanceProbeClaimEpochRepositoryStub) RecoverInterruptedItems(context.Context, int64, string) error {
	return nil
}

func (s *cindyBalanceProbeClaimEpochRepositoryStub) ReserveNext(
	context.Context,
	int64,
	string,
	time.Time,
	time.Time,
) (*CindyBalanceProbeReservation, time.Duration, error) {
	return nil, 0, nil
}

func (s *cindyBalanceProbeClaimEpochRepositoryStub) FinishIfDone(context.Context, int64, string) (bool, error) {
	return true, nil
}

func (s *cindyBalanceProbeDispatchRepositoryStub) ValidateReservationForSend(
	_ context.Context,
	reservation *CindyBalanceProbeReservation,
	account *Account,
	leaseToken string,
) (bool, error) {
	s.validateCalls++
	s.validatedLease = leaseToken
	s.validatedItemID = reservation.ItemID
	s.validatedJobEpoch = reservation.JobRequestCount
	s.validatedAccount = account
	if !s.databaseUpdatedAt.IsZero() && !s.databaseUpdatedAt.Equal(account.UpdatedAt) {
		return false, nil
	}
	return s.ready, nil
}

func (s *cindyBalanceProbeDispatchRepositoryStub) CompleteStage(
	context.Context,
	*CindyBalanceProbeReservation,
	string,
	string,
	string,
	bool,
) (bool, error) {
	s.completeCalls++
	return true, nil
}

type cindyBalanceProbeAccountRepositoryStub struct {
	AccountRepository
	account  *Account
	afterGet func()
}

func (s *cindyBalanceProbeAccountRepositoryStub) GetByID(context.Context, int64) (*Account, error) {
	if s.afterGet != nil {
		s.afterGet()
	}
	return s.account, nil
}

func (s *cindyBalanceProbeRecoveryRepositoryStub) FinalizeRecovery(context.Context, *CindyBalanceProbeReservation, string, time.Time) (bool, error) {
	s.calls++
	return s.recovered, s.err
}

func (s *cindyBalanceProbeFinalizeRepositoryStub) FinalizeExhausted(
	context.Context,
	*CindyBalanceProbeReservation,
	string,
	time.Time,
	time.Duration,
) (string, error) {
	s.calls++
	if s.beforeFinalize != nil {
		s.beforeFinalize()
	}
	return s.state, s.err
}

func newCindyBalanceProbePendingStoreStub() *cindyBalanceProbePendingStoreStub {
	return &cindyBalanceProbePendingStoreStub{
		stubGatewayCache: &stubGatewayCache{},
		pending:          make(map[int64]string),
	}
}

func (s *cindyBalanceProbePendingStoreStub) GetCindyBalancePendingFingerprint(_ context.Context, accountID int64) (string, error) {
	return s.pending[accountID], nil
}

func (s *cindyBalanceProbePendingStoreStub) HasCindyBalancePendingBatch(_ context.Context, accountIDs []int64) (map[int64]bool, error) {
	result := make(map[int64]bool, len(accountIDs))
	for _, accountID := range accountIDs {
		if s.pending[accountID] != "" {
			result[accountID] = true
		}
	}
	return result, nil
}

func (s *cindyBalanceProbePendingStoreStub) ClearCindyBalancePending(_ context.Context, accountID int64) error {
	s.clearCalls++
	if s.clearErr != nil {
		return s.clearErr
	}
	delete(s.pending, accountID)
	return nil
}

func (s *cindyBalanceProbePendingStoreStub) ClearCindyBalancePendingIfFingerprintMatches(
	_ context.Context,
	accountID int64,
	fingerprint string,
) error {
	s.clearCalls++
	if s.clearErr != nil {
		return s.clearErr
	}
	if s.pending[accountID] == fingerprint {
		delete(s.pending, accountID)
	}
	return nil
}

func TestCindyBalanceProbeFinalizeExhaustedBlocksOnlyAfterDatabaseCommit(t *testing.T) {
	for _, tc := range []struct {
		name       string
		state      string
		err        error
		cancel     bool
		wantReturn bool
		wantBlock  bool
		wantClear  int
	}{
		{name: "new marker committed", state: "exhausted", wantReturn: true, wantBlock: true, wantClear: 1},
		{name: "existing marker confirmed", state: "already_marked", wantReturn: true, wantBlock: true, wantClear: 1},
		{name: "database failure", err: errors.New("database unavailable")},
		{name: "canceled lease has no side effects", cancel: true, wantReturn: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := newCindyRateLimitAccount(23001, true)
			fingerprint, err := CindyAccountIdentityFingerprint(account.Platform, account.Type, account.Credentials)
			require.NoError(t, err)
			reservation := &CindyBalanceProbeReservation{
				JobID: 41, ItemID: 42, AccountID: account.ID, IdentityFingerprint: fingerprint,
			}
			store := newCindyBalanceProbePendingStoreStub()
			gateway := &OpenAIGatewayService{cache: store}
			rateLimit := &RateLimitService{runtimeBlocker: gateway}
			repo := &cindyBalanceProbeFinalizeRepositoryStub{state: tc.state, err: tc.err}
			repo.beforeFinalize = func() {
				_, blocked := gateway.openaiAccountRuntimeBlockUntil.Load(account.ID)
				require.False(t, blocked, "the account must remain unblocked until the lease-guarded DB transaction commits")
			}
			svc := &CindyBalanceProbeService{
				repo: repo, gateway: gateway, rateLimit: rateLimit,
				now: func() time.Time { return time.Date(2026, 8, 16, 8, 0, 0, 0, time.UTC) },
			}
			ctx := context.Background()
			if tc.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			require.Equal(t, tc.wantReturn, svc.finalizeExhausted(ctx, reservation, account, "lease-epoch"))
			require.Equal(t, 1, repo.calls)
			require.Equal(t, tc.wantClear, store.clearCalls)
			require.Equal(t, tc.wantBlock, gateway.isOpenAIAccountRuntimeBlocked(account))
		})
	}
}

func TestCindyBalanceProbeRecoveryClearFailureCannotReMarkFromOrdinaryRequest(t *testing.T) {
	account := newCindyRateLimitAccount(23002, true)
	fingerprint, err := CindyAccountIdentityFingerprint(account.Platform, account.Type, account.Credentials)
	require.NoError(t, err)
	store := newCindyBalanceProbePendingStoreStub()
	store.pending[account.ID] = fingerprint
	store.clearErr = errors.New("redis unavailable")
	markRepo := &cindyRateLimitAccountRepoStub{}
	gateway := &OpenAIGatewayService{cache: store}
	rateLimit := &RateLimitService{accountRepo: markRepo, runtimeBlocker: gateway}
	gateway.rateLimitService = rateLimit
	gateway.BlockAccountScheduling(account, time.Time{}, "cindy_balance_insufficient")
	probeRepo := &cindyBalanceProbeRecoveryRepositoryStub{recovered: true}
	svc := &CindyBalanceProbeService{
		repo: probeRepo, gateway: gateway, rateLimit: rateLimit, now: time.Now,
	}
	reservation := &CindyBalanceProbeReservation{
		JobID: 43, ItemID: 44, AccountID: account.ID, IdentityFingerprint: fingerprint,
	}

	require.True(t, svc.finalizeRecovery(context.Background(), reservation, "lease-epoch"))
	require.Equal(t, 1, probeRepo.calls)
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(account), "the committed DB recovery owns scheduling even when cache cleanup fails")
	require.False(t, gateway.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.6-luna"))
	require.NotEmpty(t, store.pending[account.ID], "the fixture must retain the failed cache cleanup evidence")
	markRepo.mu.Lock()
	require.Zero(t, markRepo.markCalls, "ordinary requests must never persist a marker from stale cache state")
	markRepo.mu.Unlock()
}

func TestCindyBalanceProbeUsesFreshTokenForEveryClaim(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	repo := &cindyBalanceProbeClaimEpochRepositoryStub{cancel: cancel}
	svc := &CindyBalanceProbeService{
		ctx:    ctx,
		cancel: cancel,
		repo:   repo,
		wake:   make(chan struct{}, 1),
		now:    time.Now,
	}
	done := make(chan struct{})
	svc.wg.Add(1)
	go func() {
		svc.run()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("balance probe worker did not stop")
	}
	require.Len(t, repo.claimTokens, 2)
	require.NotEmpty(t, repo.claimTokens[0])
	require.NotEmpty(t, repo.claimTokens[1])
	require.NotEqual(t, repo.claimTokens[0], repo.claimTokens[1])
}

func TestCindyBalanceProbeReservationCASFailureStopsBeforeProbe(t *testing.T) {
	updatedAt := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	account := &Account{
		ID: 13, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, UpdatedAt: updatedAt,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://api.laxarouter.ai",
		},
	}
	fingerprint, err := CindyAccountIdentityFingerprint(account.Platform, account.Type, account.Credentials)
	require.NoError(t, err)
	repo := &cindyBalanceProbeDispatchRepositoryStub{ready: false}
	svc := &CindyBalanceProbeService{
		repo: repo,
		accountRepo: &cindyBalanceProbeAccountRepositoryStub{
			account: account,
		},
		// A nil gateway makes any accidental probe attempt fail the test by panic.
		gateway: nil,
	}
	reservation := &CindyBalanceProbeReservation{
		JobID: 7, ItemID: 11, AccountID: account.ID, Stage: "luna",
		LeaseToken: "lease-epoch-1", JobRequestCount: 4, RequestCount: 1,
		IdentityFingerprint: fingerprint, AccountUpdatedAt: updatedAt,
	}

	require.NotPanics(t, func() {
		require.False(t, svc.executeReservation(context.Background(), reservation, reservation.LeaseToken))
	})
	require.Equal(t, 1, repo.validateCalls)
	require.Equal(t, reservation.LeaseToken, repo.validatedLease)
	require.Equal(t, reservation.ItemID, repo.validatedItemID)
	require.Equal(t, reservation.JobRequestCount, repo.validatedJobEpoch)
	require.Same(t, account, repo.validatedAccount)
	require.Zero(t, repo.completeCalls, "lost claims must be recovered by the next epoch")
}

func TestCindyBalanceProbeAccountUpdateAfterLoadStopsBeforeProbe(t *testing.T) {
	loadedAt := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	account := &Account{
		ID: 13, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, UpdatedAt: loadedAt,
		Credentials: map[string]any{
			"api_key":  "sk-old-generation",
			"base_url": "https://api.laxarouter.ai",
		},
	}
	fingerprint, err := CindyAccountIdentityFingerprint(account.Platform, account.Type, account.Credentials)
	require.NoError(t, err)
	repo := &cindyBalanceProbeDispatchRepositoryStub{ready: true, databaseUpdatedAt: loadedAt}
	accountRepo := &cindyBalanceProbeAccountRepositoryStub{
		account: account,
		afterGet: func() {
			// Simulate a credential/status generation update committed after the
			// service loaded its account object but before the dispatch CAS.
			repo.databaseUpdatedAt = loadedAt.Add(time.Second)
		},
	}
	svc := &CindyBalanceProbeService{repo: repo, accountRepo: accountRepo, gateway: nil}
	reservation := &CindyBalanceProbeReservation{
		JobID: 7, ItemID: 11, AccountID: account.ID, Stage: "luna",
		LeaseToken: "lease-epoch-1", JobRequestCount: 4, RequestCount: 1,
		IdentityFingerprint: fingerprint, AccountUpdatedAt: loadedAt,
	}

	require.NotPanics(t, func() {
		require.False(t, svc.executeReservation(context.Background(), reservation, reservation.LeaseToken))
	})
	require.Equal(t, 1, repo.validateCalls)
	require.Same(t, account, repo.validatedAccount)
	require.Zero(t, repo.completeCalls)
}

func TestCindyBalanceProbeServiceCreateJobPropagatesAtomicCandidateDrift(t *testing.T) {
	fingerprint := strings.Repeat("a", 64)
	scope := CindyBalanceProbeScope{
		Mode: "selected",
		Filters: AccountConsoleFilters{
			CindyOnly:  true,
			AccountIDs: []int64{19, 17, 19},
		},
	}
	canonicalScope := CindyBalanceProbeScope{
		Mode: "selected", AccountIDs: []int64{17, 19},
		Filters: AccountConsoleFilters{CindyOnly: true},
	}
	repo := &cindyBalanceProbeCreateRepositoryStub{err: ErrCindyBalanceProbeChanged}
	svc := NewCindyBalanceProbeService(repo, nil, nil, nil)
	t.Cleanup(svc.Stop)

	job, err := svc.CreateJob(context.Background(), nil, scope, 0.5, 1, fingerprint)

	require.Nil(t, job)
	require.ErrorIs(t, err, ErrCindyBalanceProbeChanged)
	require.Equal(t, 1, repo.createCalls)
	require.Equal(t, canonicalScope, repo.scope)
	require.Equal(t, 0.5, repo.rateRPS)
	require.Equal(t, 1, repo.expectedCount)
	require.Equal(t, fingerprint, repo.expectedFingerprint)
}

func TestCindyBalanceProbeSelectedScopeCanonicalJSONAndFiltering(t *testing.T) {
	legacyJSON := []byte(`{"mode":"selected","filters":{"account_ids":[19,17,19],"cindy_only":true}}`)
	scope := DecodeCindyBalanceProbeScope(legacyJSON)

	require.Equal(t, []int64{17, 19}, scope.AccountIDs)
	require.Empty(t, scope.Filters.AccountIDs)
	require.JSONEq(t,
		`{"mode":"selected","account_ids":[17,19],"filters":{"cindy_only":true}}`,
		string(EncodeCindyBalanceProbeScope(scope)),
	)

	account17 := newCindyRateLimitAccount(17, false)
	account17.UpdatedAt = time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	account20 := newCindyRateLimitAccount(20, false)
	account20.UpdatedAt = account17.UpdatedAt
	preview, err := BuildCindyBalanceProbePreviewFromSnapshot(
		scope,
		[]Account{*account17, *account20},
		0.5,
		account17.UpdatedAt,
	)
	require.NoError(t, err)
	require.Equal(t, 1, preview.CandidateCount)
	require.Equal(t, int64(17), preview.Candidates[0].AccountID)
	require.Equal(t, []int64{17, 19}, preview.Scope.AccountIDs)
	require.Empty(t, preview.Scope.Filters.AccountIDs)
}
