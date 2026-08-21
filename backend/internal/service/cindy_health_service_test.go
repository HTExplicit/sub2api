//go:build unit

package service

import (
	"context"
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
	mu        sync.Mutex
	begun     []CindyHealthEpisode
	finalized []CindyHealthFinalization
	recovered *CindyHealthEpisode
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

func (r *cindyHealthRepositoryStub) FinalizeCindyHealthEpisode(
	_ context.Context,
	episode CindyHealthEpisode,
	finalization CindyHealthFinalization,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finalized = append(r.finalized, finalization)
	return true, nil
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
}

func (s *cindyHealthEpisodeStoreStub) ClaimCindyHealthEpisode(_ context.Context, episode CindyHealthEpisode, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimed = append(s.claimed, episode)
	return true, nil
}

func (s *cindyHealthEpisodeStoreStub) ClearCindyHealthEpisodeIfMatch(_ context.Context, episode CindyHealthEpisode) error {
	s.mu.Lock()
	s.cleared = append(s.cleared, episode)
	s.mu.Unlock()
	return nil
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
		Credentials: map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": key},
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
		&cindyHealthAccountRepoStub{account: account}, identityRepo, repo, store, runtime, probe,
	)
	t.Cleanup(service.Stop)
	return service, identityRepo
}

func TestClassifyCindyHealthSignalRejects401AndKeeps403Transient(t *testing.T) {
	account, _ := newHealthTestAccount(t, 9101, "key-one")

	require.Equal(t, CindyHealthSignalNone, ClassifyCindyHealthSignal(
		account, http.StatusUnauthorized, []byte(`{"error":{"type":"token_not_found"}}`),
	))
	require.Equal(t, CindyHealthSignalForbidden, ClassifyCindyHealthSignal(account, http.StatusForbidden, nil))
	require.Equal(t, CindyHealthSignalExactBudget, ClassifyCindyHealthSignal(
		account, http.StatusTooManyRequests, []byte(exactCindyBudgetExceededBody),
	))
}

func TestProvideCindyHealthServiceWiresGatewayCoordinator(t *testing.T) {
	gateway := &OpenAIGatewayService{}

	health := ProvideCindyHealthService(nil, nil, nil, nil, gateway)
	t.Cleanup(health.Stop)

	require.Same(t, health, gateway.cindyHealth)
}

func TestCindyHealthExactBudgetRequiresLunaAndTerraForSameGeneration(t *testing.T) {
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
	require.Len(t, begun, 1)
	require.Equal(t, int64(7), begun[0].Generation)
	require.Len(t, finalized, 1)
	require.Equal(t, CindyHealthStatusConfirmedExhausted, finalized[0].Status)
	modelsMu.Lock()
	require.Equal(t, []string{"openai/gpt-5.6-luna", "openai/gpt-5.6-terra"}, models)
	modelsMu.Unlock()
}

func TestCindyHealthGenerationRotationDiscardsOldProbeResult(t *testing.T) {
	account, identity := newHealthTestAccount(t, 9103, "old-key")
	repo := &cindyHealthRepositoryStub{}
	store := &cindyHealthEpisodeStoreStub{}
	runtime := &cindyHealthRuntimeStub{}
	var identityRepo *cindyHealthIdentityRepoStub
	probeCalls := 0
	svc, identityRepo := newHealthTestService(t, account, identity, repo, store, runtime, func(context.Context, *Account, string) cindyBalanceProbeOutcome {
		probeCalls++
		if probeCalls == 1 {
			identityRepo.rotate(8, "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
		}
		return cindyBalanceProbeExhausted
	})

	svc.ObserveCindyHealthSignal(context.Background(), account, CindyHealthSignalExactBudget)
	require.Eventually(t, func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return len(store.cleared) == 1
	}, time.Second, 10*time.Millisecond)

	_, finalized := repo.snapshot()
	require.Empty(t, finalized)
	require.Equal(t, 1, probeCalls, "rotation between Luna and Terra must stop the old generation")
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
