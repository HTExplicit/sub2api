//go:build unit

package service

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type cindyHealthAccountRepoStub struct {
	AccountRepository
	account *Account
}

func (r *cindyHealthAccountRepoStub) GetByID(context.Context, int64) (*Account, error) {
	clone := *r.account
	clone.Credentials = maps.Clone(r.account.Credentials)
	return &clone, nil
}

type cindyHealthIdentityRepoStub struct {
	AccountCredentialIdentityRepository
	mu       sync.Mutex
	identity AccountCredentialIdentity
}

func (r *cindyHealthIdentityRepoStub) GetActiveByAccountID(context.Context, int64) (*AccountCredentialIdentity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	identity := r.identity
	return &identity, nil
}

func (r *cindyHealthIdentityRepoStub) rotate(generation int64, fingerprint string) {
	r.mu.Lock()
	r.identity.Generation = generation
	r.identity.Fingerprint = fingerprint
	r.mu.Unlock()
}

type cindyHealthRepositoryStub struct {
	mu             sync.Mutex
	begun          []CindyHealthEpisode
	finalized      []CindyHealthFinalization
	persisted      []CindyHealthEpisode
	recovered      *CindyHealthEpisode
	persistErrors  []error
	persistApplied []bool
}

func (r *cindyHealthRepositoryStub) BeginCindyHealthEpisode(
	_ context.Context,
	episode CindyHealthEpisode,
	_ string,
	_ time.Time,
	_ time.Time,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.begun = append(r.begun, episode)
	return true, nil
}

func (r *cindyHealthRepositoryStub) PersistCindyTerminalState(
	_ context.Context,
	episode CindyHealthEpisode,
	finalization CindyHealthFinalization,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.persistErrors) > 0 {
		err := r.persistErrors[0]
		r.persistErrors = r.persistErrors[1:]
		if err != nil {
			return false, err
		}
	}
	if len(r.persistApplied) > 0 {
		applied := r.persistApplied[0]
		r.persistApplied = r.persistApplied[1:]
		if !applied {
			return false, nil
		}
	}
	r.finalized = append(r.finalized, finalization)
	r.persisted = append(r.persisted, episode)
	return true, nil
}

func (r *cindyHealthRepositoryStub) terminalEpisodes() []CindyHealthEpisode {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]CindyHealthEpisode(nil), r.persisted...)
}

func (r *cindyHealthRepositoryStub) RecoverTransientCindyHealth(
	context.Context,
	int64,
	int64,
	time.Time,
) (*CindyHealthEpisode, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.recovered == nil {
		return nil, false, nil
	}
	episode := *r.recovered
	return &episode, true, nil
}

func (r *cindyHealthRepositoryStub) snapshot() ([]CindyHealthEpisode, []CindyHealthFinalization) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]CindyHealthEpisode(nil), r.begun...), append([]CindyHealthFinalization(nil), r.finalized...)
}

type cindyHealthEpisodeStoreStub struct {
	mu      sync.Mutex
	claimed []CindyHealthEpisode
	cleared []CindyHealthEpisode
	pending map[int64]CindyHealthEpisode
}

type cindyPendingGatewayCacheStub struct {
	GatewayCache
	*cindyHealthEpisodeStoreStub
}

func (s *cindyHealthEpisodeStoreStub) ClaimCindyHealthEpisode(_ context.Context, episode CindyHealthEpisode, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		s.pending = make(map[int64]CindyHealthEpisode)
	}
	if current, ok := s.pending[episode.AccountID]; ok && current.Generation >= episode.Generation {
		return false, nil
	}
	s.pending[episode.AccountID] = episode
	s.claimed = append(s.claimed, episode)
	return true, nil
}

func (s *cindyHealthEpisodeStoreStub) ClearCindyHealthEpisodeIfMatch(_ context.Context, episode CindyHealthEpisode) error {
	s.mu.Lock()
	s.cleared = append(s.cleared, episode)
	if current, ok := s.pending[episode.AccountID]; ok && current.Generation == episode.Generation && current.EpisodeID == episode.EpisodeID {
		delete(s.pending, episode.AccountID)
	}
	s.mu.Unlock()
	return nil
}

func (s *cindyHealthEpisodeStoreStub) ListCindyHealthEpisodes(_ context.Context, _ int) ([]CindyHealthEpisode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CindyHealthEpisode, 0, len(s.pending))
	for _, episode := range s.pending {
		out = append(out, episode)
	}
	return out, nil
}

func (s *cindyHealthEpisodeStoreStub) GetCindyHealthEpisodes(_ context.Context, accountID int64) ([]CindyHealthEpisode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if episode, ok := s.pending[accountID]; ok {
		return []CindyHealthEpisode{episode}, nil
	}
	return []CindyHealthEpisode{}, nil
}

type cindyHealthRuntimeStub struct {
	mu      sync.Mutex
	blocks  []time.Time
	cleared []int64
}

func (s *cindyHealthRuntimeStub) BlockAccountScheduling(_ *Account, until time.Time, _ string) {
	s.mu.Lock()
	s.blocks = append(s.blocks, until)
	s.mu.Unlock()
}

func (s *cindyHealthRuntimeStub) ClearAccountSchedulingBlock(accountID int64) {
	s.mu.Lock()
	s.cleared = append(s.cleared, accountID)
	s.mu.Unlock()
}

func newHealthTestAccount(t *testing.T, accountID int64, key string) (*Account, AccountCredentialIdentity) {
	t.Helper()
	account := &Account{
		ID: accountID, Platform: PlatformCindy, WirePlatform: WirePlatformOpenAI,
		ProviderProfile: ProviderProfileCindyLaxaV1, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true,
		CindyCredentialGeneration: 7,
		Credentials:               map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": key},
	}
	fingerprint, err := AccountCredentialFingerprint(
		ProviderProfileCindyLaxaV1, AccountTypeAPIKey, "https://api.laxarouter.ai", key,
	)
	require.NoError(t, err)
	return account, AccountCredentialIdentity{
		AccountID: accountID, ProviderProfile: ProviderProfileCindyLaxaV1,
		AuthType: AccountTypeAPIKey, NormalizedBaseURL: "https://api.laxarouter.ai",
		Fingerprint: fingerprint, Generation: 7, Active: true,
	}
}

func newHealthTestService(
	t *testing.T,
	account *Account,
	identity AccountCredentialIdentity,
	repo *cindyHealthRepositoryStub,
	store *cindyHealthEpisodeStoreStub,
	runtime *cindyHealthRuntimeStub,
	probe func(context.Context, *Account, string) cindyBalanceProbeOutcome,
) (*CindyHealthService, *cindyHealthIdentityRepoStub) {
	t.Helper()
	identityRepo := &cindyHealthIdentityRepoStub{identity: identity}
	service := NewCindyHealthService(
		&cindyHealthAccountRepoStub{account: account}, identityRepo, repo, store, runtime,
	)
	t.Cleanup(service.Stop)
	return service, identityRepo
}

func TestClassifyCindyHealthSignalTreatsStrict401AsBannedAndKeeps403Transient(t *testing.T) {
	account, _ := newHealthTestAccount(t, 9101, "key-one")

	require.Equal(t, CindyHealthSignalBanned, ClassifyCindyHealthSignal(
		account, http.StatusUnauthorized, []byte(`{"error":{"type":"token_not_found"}}`),
	))
	require.Equal(t, CindyHealthSignalForbidden, ClassifyCindyHealthSignal(account, http.StatusForbidden, nil))
	require.Equal(t, CindyHealthSignalExactBudget, ClassifyCindyHealthSignal(
		account, http.StatusTooManyRequests, []byte(exactCindyBudgetExceededBody),
	))
}

func TestLegacyCindyCompatibilityDoesNotEnterCanonicalHealth(t *testing.T) {
	legacy := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "not-exposed",
			"base_url": "https://api.laxarouter.ai",
		},
	}
	require.Equal(t, CindyHealthSignalNone, ClassifyCindyHealthSignal(legacy, http.StatusForbidden, nil))
	require.Equal(t, CindyHealthSignalNone, ClassifyCindyHealthSignal(
		legacy, http.StatusTooManyRequests, []byte(exactCindyBudgetExceededBody),
	))
}

func TestStrictCindy401ReachesSharedHealthCoordinatorAcrossHTTPMessagesAndAccountTest(t *testing.T) {
	account, _ := newHealthTestAccount(t, 9106, "key-six")
	recorder := &cindyHealthCoordinatorRecorder{}
	openAI := &OpenAIGatewayService{cindyHealth: recorder}
	native := &GatewayService{cindyHealth: recorder}
	accountTest := &AccountTestService{openAIGatewayService: openAI}

	require.True(t, openAI.handleOpenAIAccountUpstreamError(
		context.Background(), account, http.StatusUnauthorized, nil, []byte(`{"error":{"message":"ignored"}}`),
	))
	require.Equal(t, CindyHealthSignalBanned, native.observeCindyHealthSignal(
		context.Background(), account, http.StatusUnauthorized, []byte(`{"error":{"message":"ignored"}}`),
	))
	require.True(t, accountTest.markCindyBalanceInsufficientFromTest(
		context.Background(), account, http.StatusUnauthorized, []byte(`{"error":{"message":"ignored"}}`),
	))

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	require.Equal(t, []CindyHealthSignal{
		CindyHealthSignalBanned, CindyHealthSignalBanned, CindyHealthSignalBanned,
	}, recorder.signals)
}

func TestProvideCindyHealthServiceWiresGatewayCoordinator(t *testing.T) {
	gateway := &OpenAIGatewayService{}
	nativeGateway := &GatewayService{}

	health := ProvideCindyHealthService(nil, nil, nil, nil, gateway, nativeGateway)
	t.Cleanup(health.Stop)

	require.Same(t, health, gateway.cindyHealth)
	require.Same(t, health, nativeGateway.cindyHealth)
}

func TestCindyHealthExactBudgetPersistsImmediatelyWithoutAutomaticProbes(t *testing.T) {
	account, identity := newHealthTestAccount(t, 9102, "key-two")
	repo := &cindyHealthRepositoryStub{}
	store := &cindyHealthEpisodeStoreStub{}
	runtime := &cindyHealthRuntimeStub{}
	var modelsMu sync.Mutex
	var models []string
	svc, _ := newHealthTestService(t, account, identity, repo, store, runtime, func(_ context.Context, _ *Account, model string) cindyBalanceProbeOutcome {
		modelsMu.Lock()
		models = append(models, model)
		modelsMu.Unlock()
		return cindyBalanceProbeExhausted
	})

	svc.ObserveCindyHealthSignal(context.Background(), account, CindyHealthSignalExactBudget)
	require.Eventually(t, func() bool {
		_, finalized := repo.snapshot()
		return len(finalized) == 1
	}, time.Second, 10*time.Millisecond)

	begun, finalized := repo.snapshot()
	require.Empty(t, begun)
	require.Len(t, finalized, 1)
	require.Equal(t, CindyHealthStatusBalanceInsufficient, finalized[0].Status)
	modelsMu.Lock()
	require.Empty(t, models)
	modelsMu.Unlock()
}

func TestCindyHealthBannedBlocksBeforeGenerationBoundPersistence(t *testing.T) {
	account, identity := newHealthTestAccount(t, 9105, "key-five")
	repo := &cindyHealthRepositoryStub{}
	runtime := &cindyHealthRuntimeStub{}
	svc, _ := newHealthTestService(t, account, identity, repo, &cindyHealthEpisodeStoreStub{}, runtime,
		func(context.Context, *Account, string) cindyBalanceProbeOutcome {
			t.Fatal("strict 401 must not launch an automatic probe")
			return cindyBalanceProbeOther
		})

	svc.ObserveCindyHealthSignal(context.Background(), account, CindyHealthSignalBanned)

	_, finalized := repo.snapshot()
	require.Len(t, finalized, 1)
	require.Equal(t, CindyHealthStatusBanned, finalized[0].Status)
	runtime.mu.Lock()
	require.Len(t, runtime.blocks, 1)
	require.True(t, runtime.blocks[0].IsZero(), "banned must install a permanent runtime block")
	runtime.mu.Unlock()
}

func TestCindyHealthTerminalWriteCarriesCurrentGenerationWithoutProbe(t *testing.T) {
	account, identity := newHealthTestAccount(t, 9103, "old-key")
	repo := &cindyHealthRepositoryStub{}
	store := &cindyHealthEpisodeStoreStub{}
	runtime := &cindyHealthRuntimeStub{}
	probeCalls := 0
	svc, _ := newHealthTestService(t, account, identity, repo, store, runtime, func(context.Context, *Account, string) cindyBalanceProbeOutcome {
		probeCalls++
		return cindyBalanceProbeExhausted
	})

	svc.ObserveCindyHealthSignal(context.Background(), account, CindyHealthSignalExactBudget)

	persisted := repo.terminalEpisodes()
	require.Len(t, persisted, 1)
	require.Equal(t, account.ID, persisted[0].AccountID)
	require.Equal(t, identity.Generation, persisted[0].Generation)
	require.NotEmpty(t, persisted[0].EpisodeID)
	require.Zero(t, probeCalls)
}

func TestCindyHealthRejectsABAStaleResponseBeforeRuntimeBlock(t *testing.T) {
	account, identity := newHealthTestAccount(t, 9107, "same-key")
	account.CindyCredentialGeneration = 4
	identity.Generation = 6
	repo := &cindyHealthRepositoryStub{}
	runtime := &cindyHealthRuntimeStub{}
	svc, _ := newHealthTestService(t, account, identity, repo, &cindyHealthEpisodeStoreStub{}, runtime, nil)

	svc.ObserveCindyHealthSignal(context.Background(), account, CindyHealthSignalBanned)

	require.Empty(t, repo.terminalEpisodes())
	runtime.mu.Lock()
	require.Empty(t, runtime.blocks)
	runtime.mu.Unlock()
}

func TestCindyHealthTerminalPersistenceFailureRetainsPendingAndRetries(t *testing.T) {
	account, identity := newHealthTestAccount(t, 9108, "retry-key")
	repo := &cindyHealthRepositoryStub{persistErrors: []error{errors.New("db unavailable"), errors.New("db still unavailable"), nil}}
	store := &cindyHealthEpisodeStoreStub{}
	runtime := &cindyHealthRuntimeStub{}
	svc, _ := newHealthTestService(t, account, identity, repo, store, runtime, nil)

	svc.ObserveCindyHealthSignal(context.Background(), account, CindyHealthSignalBanned)
	store.mu.Lock()
	require.Len(t, store.pending, 1)
	store.mu.Unlock()

	for attempts := 0; attempts < 3 && len(repo.terminalEpisodes()) == 0; attempts++ {
		svc.retryPendingTerminals()
	}
	require.Len(t, repo.terminalEpisodes(), 1)
	store.mu.Lock()
	require.Empty(t, store.pending)
	store.mu.Unlock()
}

func TestCindyHealthRestartRecoversDurablePendingWithoutAutomaticProbe(t *testing.T) {
	account, identity := newHealthTestAccount(t, 9109, "restart-key")
	store := &cindyHealthEpisodeStoreStub{pending: map[int64]CindyHealthEpisode{
		account.ID: {
			AccountID: account.ID, Generation: identity.Generation, EpisodeID: "restart-episode",
			Fingerprint: identity.Fingerprint, Status: CindyHealthStatusBanned,
			Evidence: CindyHealthEvidenceUnauthorized, ObservedAt: time.Now().UTC(),
		},
	}}
	repo := &cindyHealthRepositoryStub{}
	runtime := &cindyHealthRuntimeStub{}
	svc, _ := newHealthTestService(t, account, identity, repo, store, runtime, nil)

	svc.retryPendingTerminals()

	require.Len(t, repo.terminalEpisodes(), 1)
	store.mu.Lock()
	require.Empty(t, store.pending)
	store.mu.Unlock()
}

func TestCindyHealthAppliedFalseRetainsPendingUntilAuthoritativeRetry(t *testing.T) {
	account, identity := newHealthTestAccount(t, 9110, "cas-retry-key")
	repo := &cindyHealthRepositoryStub{persistApplied: []bool{false, true}}
	store := &cindyHealthEpisodeStoreStub{}
	runtime := &cindyHealthRuntimeStub{}
	svc, _ := newHealthTestService(t, account, identity, repo, store, runtime, nil)

	svc.ObserveCindyHealthSignal(context.Background(), account, CindyHealthSignalExactBudget)
	for attempts := 0; attempts < 3 && len(repo.terminalEpisodes()) == 0; attempts++ {
		svc.retryPendingTerminals()
	}

	require.Len(t, repo.terminalEpisodes(), 1)
	store.mu.Lock()
	require.Empty(t, store.pending)
	store.mu.Unlock()
}

func TestCindyHealthClaimFalseRestoresExistingGenerationScopedBlock(t *testing.T) {
	account, identity := newHealthTestAccount(t, 9111, "existing-key")
	existing := CindyHealthEpisode{
		AccountID: account.ID, Generation: identity.Generation, EpisodeID: "existing-episode",
		Fingerprint: identity.Fingerprint, Status: CindyHealthStatusBanned,
		Evidence: CindyHealthEvidenceUnauthorized, ObservedAt: time.Now().UTC(),
	}
	store := &cindyHealthEpisodeStoreStub{pending: map[int64]CindyHealthEpisode{account.ID: existing}}
	gateway := &OpenAIGatewayService{}
	svc := &CindyHealthService{
		accountRepo:  &cindyHealthAccountRepoStub{account: account},
		identityRepo: &cindyHealthIdentityRepoStub{identity: identity},
		healthRepo:   &cindyHealthRepositoryStub{}, episodeStore: store, runtime: gateway,
		now: time.Now, ctx: context.Background(), retryWake: make(chan struct{}, 1),
	}

	svc.ObserveCindyHealthSignal(context.Background(), account, CindyHealthSignalBanned)

	require.True(t, gateway.isOpenAIAccountRuntimeBlockedContext(context.Background(), account))
}

func TestCindyTerminalPendingHotPathBlocksBeforeRetryHydrationAndClearsStale(t *testing.T) {
	account, identity := newHealthTestAccount(t, 9112, "hotpath-key")
	current := CindyHealthEpisode{
		AccountID: account.ID, Generation: identity.Generation, EpisodeID: "hotpath-current",
		Fingerprint: identity.Fingerprint, Status: CindyHealthStatusBanned,
		Evidence: CindyHealthEvidenceUnauthorized, ObservedAt: time.Now().UTC(),
	}
	store := &cindyHealthEpisodeStoreStub{pending: map[int64]CindyHealthEpisode{account.ID: current}}
	gateway := &OpenAIGatewayService{cache: &cindyPendingGatewayCacheStub{cindyHealthEpisodeStoreStub: store}}

	require.True(t, gateway.isOpenAIAccountRequestRuntimeBlockedContext(context.Background(), account, "gpt-5.6-sol"))

	gateway.ClearAccountSchedulingBlock(account.ID)
	stale := current
	stale.Generation--
	stale.EpisodeID = "hotpath-stale"
	store.mu.Lock()
	store.pending[account.ID] = stale
	store.mu.Unlock()
	require.False(t, gateway.isOpenAIAccountRequestRuntimeBlockedContext(context.Background(), account, "gpt-5.6-sol"))
	store.mu.Lock()
	require.Empty(t, store.pending)
	store.mu.Unlock()
}

func TestCindyHealth403OnlyCreatesFiniteTransientQuarantine(t *testing.T) {
	account, identity := newHealthTestAccount(t, 9104, "key-four")
	repo := &cindyHealthRepositoryStub{}
	store := &cindyHealthEpisodeStoreStub{}
	runtime := &cindyHealthRuntimeStub{}
	probeCalls := 0
	svc, _ := newHealthTestService(t, account, identity, repo, store, runtime, func(context.Context, *Account, string) cindyBalanceProbeOutcome {
		probeCalls++
		return cindyBalanceProbeExhausted
	})

	svc.ObserveCindyHealthSignal(context.Background(), account, CindyHealthSignalForbidden)
	begun, finalized := repo.snapshot()
	require.Len(t, begun, 1)
	require.Empty(t, finalized)
	require.Zero(t, probeCalls)
	runtime.mu.Lock()
	require.Len(t, runtime.blocks, 1)
	require.False(t, runtime.blocks[0].IsZero(), "403 must never create an indefinite block")
	runtime.mu.Unlock()
}

func TestCindyHealthSuccessClearsOnlyMatchingTransientEpisode(t *testing.T) {
	account, identity := newHealthTestAccount(t, 9105, "key-five")
	episode := CindyHealthEpisode{AccountID: account.ID, Generation: identity.Generation, EpisodeID: "episode-five"}
	repo := &cindyHealthRepositoryStub{recovered: &episode}
	store := &cindyHealthEpisodeStoreStub{}
	runtime := &cindyHealthRuntimeStub{}
	svc, _ := newHealthTestService(t, account, identity, repo, store, runtime, func(context.Context, *Account, string) cindyBalanceProbeOutcome {
		return cindyBalanceProbeOther
	})

	svc.ObserveCindyHealthSuccess(context.Background(), account)

	store.mu.Lock()
	require.Equal(t, []CindyHealthEpisode{episode}, store.cleared)
	store.mu.Unlock()
	runtime.mu.Lock()
	require.Equal(t, []int64{account.ID}, runtime.cleared)
	runtime.mu.Unlock()
}
