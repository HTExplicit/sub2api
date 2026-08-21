package service

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	CindyHealthStatusHealthy            = "healthy"
	CindyHealthStatusQuarantined        = "quarantined"
	CindyHealthStatusConfirmedExhausted = "confirmed_exhausted"

	CindyHealthEvidenceExactBudget  = "exact_budget"
	CindyHealthEvidenceDualExact    = "dual_exact_budget"
	CindyHealthEvidenceForbidden    = "http_403"
	CindyHealthEvidenceRecovered    = "valid_success"
	CindyHealthEvidenceInconclusive = "inconclusive"

	cindyHealthForbiddenBackoff = 2 * time.Minute
	cindyHealthConfirmBackoff   = 5 * time.Minute
	cindyHealthMixedBackoff     = 15 * time.Minute
	cindyHealthEpisodeTTL       = 5 * time.Minute
	cindyHealthProbeTimeout     = 20 * time.Second
	cindyHealthStateTimeout     = 3 * time.Second
	cindyHealthProbeConcurrency = 2
)

type CindyHealthSignal uint8

const (
	CindyHealthSignalNone CindyHealthSignal = iota
	CindyHealthSignalExactBudget
	CindyHealthSignalForbidden
)

func (s CindyHealthSignal) evidence() string {
	switch s {
	case CindyHealthSignalExactBudget:
		return CindyHealthEvidenceExactBudget
	case CindyHealthSignalForbidden:
		return CindyHealthEvidenceForbidden
	default:
		return ""
	}
}

// ClassifyCindyHealthSignal deliberately has no 401 branch. A first-class
// invalid-credential state requires an observed provider field contract.
func ClassifyCindyHealthSignal(account *Account, statusCode int, body []byte) CindyHealthSignal {
	if account == nil || !hasCanonicalCindyProviderIdentity(account) || !CindyBalanceDetectionFeatureEnabled() {
		return CindyHealthSignalNone
	}
	if ClassifyCindyBalanceInsufficient(account, statusCode, body) != CindyBalanceSignalNone {
		return CindyHealthSignalExactBudget
	}
	if statusCode == http.StatusForbidden {
		return CindyHealthSignalForbidden
	}
	return CindyHealthSignalNone
}

type CindyHealthEpisode struct {
	AccountID  int64
	Generation int64
	EpisodeID  string
}

func (e CindyHealthEpisode) valid() bool {
	return e.AccountID > 0 && e.Generation > 0 && strings.TrimSpace(e.EpisodeID) != "" && len(e.EpisodeID) <= 64
}

type CindyHealthFinalization struct {
	Status          string
	Evidence        string
	ObservedAt      time.Time
	QuarantineUntil *time.Time
}

type CindyHealthRepository interface {
	BeginCindyHealthEpisode(ctx context.Context, episode CindyHealthEpisode, evidence string, observedAt, quarantineUntil time.Time) (bool, error)
	FinalizeCindyHealthEpisode(ctx context.Context, episode CindyHealthEpisode, finalization CindyHealthFinalization) (bool, error)
	RecoverTransientCindyHealth(ctx context.Context, accountID, generation int64, observedAt time.Time) (*CindyHealthEpisode, bool, error)
}

type CindyHealthEpisodeStore interface {
	ClaimCindyHealthEpisode(ctx context.Context, episode CindyHealthEpisode, ttl time.Duration) (bool, error)
	ClearCindyHealthEpisodeIfMatch(ctx context.Context, episode CindyHealthEpisode) error
}

type CindyHealthRuntimeBlocker interface {
	BlockAccountScheduling(account *Account, until time.Time, reason string)
	ClearAccountSchedulingBlock(accountID int64)
}

type CindyHealthCoordinator interface {
	ObserveCindyHealthSignal(ctx context.Context, account *Account, signal CindyHealthSignal)
	ObserveCindyHealthSuccess(ctx context.Context, account *Account)
}

type CindyHealthService struct {
	accountRepo  AccountRepository
	identityRepo AccountCredentialIdentityRepository
	healthRepo   CindyHealthRepository
	episodeStore CindyHealthEpisodeStore
	runtime      CindyHealthRuntimeBlocker
	probe        func(context.Context, *Account, string) cindyBalanceProbeOutcome
	now          func() time.Time

	ctx      context.Context
	cancel   context.CancelFunc
	slots    chan struct{}
	launchMu sync.Mutex
	stopped  bool
	wg       sync.WaitGroup
}

func NewCindyHealthService(
	accountRepo AccountRepository,
	identityRepo AccountCredentialIdentityRepository,
	healthRepo CindyHealthRepository,
	episodeStore CindyHealthEpisodeStore,
	runtime CindyHealthRuntimeBlocker,
	probe func(context.Context, *Account, string) cindyBalanceProbeOutcome,
) *CindyHealthService {
	ctx, cancel := context.WithCancel(context.Background())
	return &CindyHealthService{
		accountRepo: accountRepo, identityRepo: identityRepo, healthRepo: healthRepo,
		episodeStore: episodeStore, runtime: runtime, probe: probe, now: time.Now,
		ctx: ctx, cancel: cancel, slots: make(chan struct{}, cindyHealthProbeConcurrency),
	}
}

func (s *CindyHealthService) Stop() {
	if s == nil {
		return
	}
	s.launchMu.Lock()
	if !s.stopped {
		s.stopped = true
		s.cancel()
	}
	s.launchMu.Unlock()
	s.wg.Wait()
}

func (s *CindyHealthService) launch(fn func()) bool {
	s.launchMu.Lock()
	defer s.launchMu.Unlock()
	if s.stopped {
		return false
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
			fn()
		case <-s.ctx.Done():
		}
	}()
	return true
}

func (s *CindyHealthService) stateContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, cindyHealthStateTimeout)
}

func (s *CindyHealthService) currentIdentity(ctx context.Context, account *Account) (*AccountCredentialIdentity, bool) {
	if s == nil || s.identityRepo == nil || !hasCanonicalCindyProviderIdentity(account) {
		return nil, false
	}
	identity, err := s.identityRepo.GetActiveByAccountID(ctx, account.ID)
	if err != nil || identity == nil || !identity.Active || identity.Generation <= 0 ||
		identity.ProviderProfile != ProviderProfileCindyLaxaV1 || identity.AuthType != AccountTypeAPIKey {
		return nil, false
	}
	normalizedURL, err := NormalizeCredentialIdentityBaseURL(ProviderProfileCindyLaxaV1, account.GetCredential("base_url"))
	if err != nil || normalizedURL != identity.NormalizedBaseURL {
		return nil, false
	}
	fingerprint, err := AccountCredentialFingerprint(
		ProviderProfileCindyLaxaV1, AccountTypeAPIKey, normalizedURL, account.GetCredential("api_key"),
	)
	if err != nil || fingerprint != identity.Fingerprint {
		return nil, false
	}
	return identity, true
}

func (s *CindyHealthService) ObserveCindyHealthSignal(ctx context.Context, account *Account, signal CindyHealthSignal) {
	if s == nil || s.healthRepo == nil || s.runtime == nil || signal == CindyHealthSignalNone ||
		!hasCanonicalCindyProviderIdentity(account) {
		return
	}
	now := s.now().UTC()
	if account.CindyBalanceInsufficientAt != nil {
		s.runtime.BlockAccountScheduling(account, time.Time{}, "cindy_balance_insufficient")
		return
	}
	backoff := cindyHealthForbiddenBackoff
	if signal == CindyHealthSignalExactBudget {
		backoff = cindyHealthConfirmBackoff
	}
	until := now.Add(backoff)
	s.runtime.BlockAccountScheduling(account, until, "cindy_health_quarantine")

	stateCtx, cancel := s.stateContext(ctx)
	defer cancel()
	identity, ok := s.currentIdentity(stateCtx, account)
	if !ok {
		return
	}
	episode := CindyHealthEpisode{AccountID: account.ID, Generation: identity.Generation, EpisodeID: uuid.NewString()}
	if signal == CindyHealthSignalForbidden {
		_, _ = s.healthRepo.BeginCindyHealthEpisode(stateCtx, episode, signal.evidence(), now, until)
		return
	}
	if s.episodeStore == nil || s.probe == nil {
		return
	}
	claimed, err := s.episodeStore.ClaimCindyHealthEpisode(stateCtx, episode, cindyHealthEpisodeTTL)
	if err != nil || !claimed {
		return
	}
	begun, err := s.healthRepo.BeginCindyHealthEpisode(stateCtx, episode, signal.evidence(), now, until)
	if err != nil || !begun {
		_ = s.episodeStore.ClearCindyHealthEpisodeIfMatch(stateCtx, episode)
		return
	}
	if !s.launch(func() { s.confirmExactEpisode(episode) }) {
		_ = s.episodeStore.ClearCindyHealthEpisodeIfMatch(stateCtx, episode)
	}
}

func (s *CindyHealthService) loadEpisodeAccount(ctx context.Context, episode CindyHealthEpisode) (*Account, bool) {
	if s == nil || s.accountRepo == nil || !episode.valid() {
		return nil, false
	}
	account, err := s.accountRepo.GetByID(ctx, episode.AccountID)
	if err != nil || account == nil {
		return nil, false
	}
	identity, ok := s.currentIdentity(ctx, account)
	return account, ok && identity.Generation == episode.Generation
}

func (s *CindyHealthService) confirmExactEpisode(episode CindyHealthEpisode) {
	defer func() {
		clearCtx, cancel := context.WithTimeout(context.Background(), cindyHealthStateTimeout)
		defer cancel()
		_ = s.episodeStore.ClearCindyHealthEpisodeIfMatch(clearCtx, episode)
	}()

	account, ok := s.loadEpisodeAccount(s.ctx, episode)
	if !ok {
		return
	}
	outcomes := [2]cindyBalanceProbeOutcome{}
	for index, model := range cindyBalanceProbeModels {
		probeCtx, cancel := context.WithTimeout(s.ctx, cindyHealthProbeTimeout)
		outcomes[index] = s.probe(probeCtx, account, model)
		cancel()
		if s.ctx.Err() != nil {
			return
		}
		account, ok = s.loadEpisodeAccount(s.ctx, episode)
		if !ok {
			return
		}
	}

	now := s.now().UTC()
	finalization := CindyHealthFinalization{
		Status: CindyHealthStatusQuarantined, Evidence: CindyHealthEvidenceInconclusive,
		ObservedAt: now,
	}
	until := now.Add(cindyHealthMixedBackoff)
	finalization.QuarantineUntil = &until
	if outcomes[0] == cindyBalanceProbeSuccess || outcomes[1] == cindyBalanceProbeSuccess {
		finalization.Status = CindyHealthStatusHealthy
		finalization.Evidence = CindyHealthEvidenceRecovered
		finalization.QuarantineUntil = nil
	} else if outcomes[0] == cindyBalanceProbeExhausted && outcomes[1] == cindyBalanceProbeExhausted {
		finalization.Status = CindyHealthStatusConfirmedExhausted
		finalization.Evidence = CindyHealthEvidenceDualExact
		finalization.QuarantineUntil = nil
	}
	stateCtx, cancel := context.WithTimeout(s.ctx, cindyHealthStateTimeout)
	defer cancel()
	if _, ok = s.loadEpisodeAccount(stateCtx, episode); !ok {
		return
	}
	applied, err := s.healthRepo.FinalizeCindyHealthEpisode(stateCtx, episode, finalization)
	if err != nil || !applied {
		return
	}
	switch finalization.Status {
	case CindyHealthStatusHealthy:
		s.runtime.ClearAccountSchedulingBlock(episode.AccountID)
	case CindyHealthStatusConfirmedExhausted:
		s.runtime.BlockAccountScheduling(account, time.Time{}, "cindy_balance_insufficient")
	default:
		s.runtime.BlockAccountScheduling(account, until, "cindy_health_quarantine")
	}
}

func (s *CindyHealthService) ObserveCindyHealthSuccess(ctx context.Context, account *Account) {
	if s == nil || s.healthRepo == nil || s.runtime == nil || !hasCanonicalCindyProviderIdentity(account) {
		return
	}
	stateCtx, cancel := s.stateContext(ctx)
	defer cancel()
	identity, ok := s.currentIdentity(stateCtx, account)
	if !ok {
		return
	}
	episode, recovered, err := s.healthRepo.RecoverTransientCindyHealth(stateCtx, account.ID, identity.Generation, s.now().UTC())
	if err != nil || !recovered {
		return
	}
	if episode != nil && s.episodeStore != nil {
		_ = s.episodeStore.ClearCindyHealthEpisodeIfMatch(stateCtx, *episode)
	}
	s.runtime.ClearAccountSchedulingBlock(account.ID)
}

var _ CindyHealthCoordinator = (*CindyHealthService)(nil)
