package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	CindyHealthStatusQuarantined         = "quarantined"
	CindyHealthStatusConfirmedExhausted  = "confirmed_exhausted"
	CindyHealthStatusBalanceInsufficient = CindyHealthStatusConfirmedExhausted
	CindyHealthStatusBanned              = "banned"

	CindyHealthEvidenceExactBudget  = "exact_budget"
	CindyHealthEvidenceForbidden    = "http_403"
	CindyHealthEvidenceUnauthorized = "http_401"

	cindyHealthForbiddenBackoff = 2 * time.Minute
	cindyHealthStateTimeout     = 3 * time.Second
	cindyHealthRetryInterval    = time.Second
)

type CindyHealthSignal uint8

const (
	CindyHealthSignalNone CindyHealthSignal = iota
	CindyHealthSignalExactBudget
	CindyHealthSignalForbidden
	CindyHealthSignalBanned
)

func (s CindyHealthSignal) evidence() string {
	switch s {
	case CindyHealthSignalExactBudget:
		return CindyHealthEvidenceExactBudget
	case CindyHealthSignalForbidden:
		return CindyHealthEvidenceForbidden
	case CindyHealthSignalBanned:
		return CindyHealthEvidenceUnauthorized
	default:
		return ""
	}
}

func ClassifyCindyHealthSignal(account *Account, statusCode int, body []byte) CindyHealthSignal {
	if account == nil || !hasCanonicalCindyProviderIdentity(account) {
		return CindyHealthSignalNone
	}
	if statusCode == http.StatusUnauthorized {
		return CindyHealthSignalBanned
	}
	if !CindyBalanceDetectionFeatureEnabled() {
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
	AccountID   int64
	Generation  int64
	EpisodeID   string
	Fingerprint string
	Status      string
	Evidence    string
	ObservedAt  time.Time
}

func (e CindyHealthEpisode) valid() bool {
	return e.AccountID > 0 && e.Generation > 0 && strings.TrimSpace(e.EpisodeID) != "" && len(e.EpisodeID) <= 64
}

func (e CindyHealthEpisode) terminalValid() bool {
	return e.valid() && NormalizeCindyCredentialsFingerprint(e.Fingerprint) != "" &&
		(e.Status == CindyHealthStatusBanned || e.Status == CindyHealthStatusBalanceInsufficient) &&
		strings.TrimSpace(e.Evidence) != "" && !e.ObservedAt.IsZero()
}

type CindyHealthFinalization struct {
	Status          string
	Evidence        string
	ObservedAt      time.Time
	QuarantineUntil *time.Time
}

type CindyHealthRepository interface {
	BeginCindyHealthEpisode(ctx context.Context, episode CindyHealthEpisode, evidence string, observedAt, quarantineUntil time.Time) (bool, error)
	PersistCindyTerminalState(ctx context.Context, episode CindyHealthEpisode, finalization CindyHealthFinalization) (bool, error)
	RecoverTransientCindyHealth(ctx context.Context, accountID, generation int64, observedAt time.Time) (*CindyHealthEpisode, bool, error)
}

type CindyHealthEpisodeStore interface {
	ClaimCindyHealthEpisode(ctx context.Context, episode CindyHealthEpisode, ttl time.Duration) (bool, error)
	ClearCindyHealthEpisodeIfMatch(ctx context.Context, episode CindyHealthEpisode) error
	GetCindyHealthEpisodes(ctx context.Context, accountID int64) ([]CindyHealthEpisode, error)
	ListCindyHealthEpisodes(ctx context.Context, limit int) ([]CindyHealthEpisode, error)
}

type CindyHealthRuntimeBlocker interface {
	BlockAccountScheduling(account *Account, until time.Time, reason string)
	ClearAccountSchedulingBlock(accountID int64)
}

type CindyHealthEpisodeRuntimeBlocker interface {
	BlockCindyHealthEpisode(account *Account, episode CindyHealthEpisode, reason string) bool
	ClearCindyHealthEpisodeBlock(episode CindyHealthEpisode)
}

type CindyHealthCoordinator interface {
	ObserveCindyHealthSignal(ctx context.Context, account *Account, signal CindyHealthSignal)
	ObserveCindyHealthSuccess(ctx context.Context, account *Account)
}

type CindyHealthEpisodeAuthority interface {
	ResolveCindyHealthEpisode(ctx context.Context, episode CindyHealthEpisode) (*Account, bool, error)
}

type CindyHealthService struct {
	accountRepo  AccountRepository
	identityRepo AccountCredentialIdentityRepository
	healthRepo   CindyHealthRepository
	episodeStore CindyHealthEpisodeStore
	runtime      CindyHealthRuntimeBlocker
	now          func() time.Time

	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	retryWake     chan struct{}
	retryInterval time.Duration
}

func NewCindyHealthService(
	accountRepo AccountRepository,
	identityRepo AccountCredentialIdentityRepository,
	healthRepo CindyHealthRepository,
	episodeStore CindyHealthEpisodeStore,
	runtime CindyHealthRuntimeBlocker,
) *CindyHealthService {
	ctx, cancel := context.WithCancel(context.Background())
	svc := &CindyHealthService{
		accountRepo: accountRepo, identityRepo: identityRepo, healthRepo: healthRepo,
		episodeStore: episodeStore, runtime: runtime, now: time.Now,
		ctx: ctx, cancel: cancel, retryWake: make(chan struct{}, 1), retryInterval: cindyHealthRetryInterval,
	}
	if episodeStore != nil {
		svc.wg.Add(1)
		go svc.runTerminalRetryLoop()
	}
	return svc
}

func (s *CindyHealthService) Stop() {
	if s == nil {
		return
	}
	s.cancel()
	s.wg.Wait()
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
	if account.CindyCredentialGeneration != identity.Generation {
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

func (s *CindyHealthService) ResolveCindyHealthEpisode(ctx context.Context, episode CindyHealthEpisode) (*Account, bool, error) {
	if s == nil || s.accountRepo == nil || s.identityRepo == nil || !episode.terminalValid() {
		return nil, false, errors.New("cindy health authority is unavailable")
	}
	account, err := s.accountRepo.GetByID(ctx, episode.AccountID)
	if err != nil {
		return nil, false, err
	}
	identity, err := s.identityRepo.GetActiveByAccountID(ctx, episode.AccountID)
	if err != nil || identity == nil || !identity.Active || identity.Generation <= 0 {
		if err == nil {
			err = errors.New("active Cindy credential identity is unavailable")
		}
		return nil, false, err
	}
	if !hasCanonicalCindyProviderIdentity(account) || account.CindyCredentialGeneration != identity.Generation {
		return nil, false, errors.New("Cindy credential generation projection is unavailable")
	}
	return account, identity.Generation == episode.Generation && identity.Fingerprint == episode.Fingerprint, nil
}

func (s *CindyHealthService) ObserveCindyHealthSignal(ctx context.Context, account *Account, signal CindyHealthSignal) {
	if s == nil || s.healthRepo == nil || s.runtime == nil || signal == CindyHealthSignalNone ||
		!hasCanonicalCindyProviderIdentity(account) {
		return
	}
	now := s.now().UTC()
	stateCtx, cancel := s.stateContext(ctx)
	defer cancel()
	identity, ok := s.currentIdentity(stateCtx, account)
	if !ok {
		return
	}
	episode := CindyHealthEpisode{
		AccountID: account.ID, Generation: identity.Generation, EpisodeID: uuid.NewString(),
		Fingerprint: identity.Fingerprint, Evidence: signal.evidence(), ObservedAt: now,
	}
	if signal == CindyHealthSignalBanned || signal == CindyHealthSignalExactBudget {
		status := CindyHealthStatusBanned
		reason := "cindy_banned"
		if signal == CindyHealthSignalExactBudget {
			status = CindyHealthStatusBalanceInsufficient
			reason = "cindy_balance_insufficient"
		}
		episode.Status = status
		if s.episodeStore != nil {
			claimed, claimErr := s.episodeStore.ClaimCindyHealthEpisode(stateCtx, episode, 0)
			if claimErr != nil {
				slog.Error("cindy_terminal_pending_claim_failed", "account_id", account.ID, "generation", identity.Generation, "error", claimErr)
			} else if !claimed {
				s.restoreCurrentPendingBlock(stateCtx, account)
				s.wakeTerminalRetry()
				return
			}
		}
		if episodeRuntime, supported := s.runtime.(CindyHealthEpisodeRuntimeBlocker); supported {
			if !episodeRuntime.BlockCindyHealthEpisode(account, episode, reason) {
				return
			}
		} else {
			s.runtime.BlockAccountScheduling(account, time.Time{}, reason)
		}
		applied, err := s.healthRepo.PersistCindyTerminalState(stateCtx, episode, CindyHealthFinalization{
			Status: status, Evidence: signal.evidence(), ObservedAt: now,
		})
		if err != nil || !applied {
			slog.Error("cindy_terminal_persist_failed", "account_id", account.ID, "generation", identity.Generation, "applied", applied, "error", err)
			s.wakeTerminalRetry()
			return
		}
		if s.episodeStore != nil {
			if clearErr := s.episodeStore.ClearCindyHealthEpisodeIfMatch(stateCtx, episode); clearErr != nil {
				slog.Warn("cindy_terminal_pending_clear_failed", "account_id", account.ID, "generation", identity.Generation, "error", clearErr)
			}
		}
		if applied {
			if signal == CindyHealthSignalBanned {
				account.CindyBannedAt = &now
			} else {
				account.CindyBalanceInsufficientAt = &now
			}
		}
		return
	}
	until := now.Add(cindyHealthForbiddenBackoff)
	if signal == CindyHealthSignalForbidden {
		s.runtime.BlockAccountScheduling(account, until, "cindy_health_quarantine")
		_, _ = s.healthRepo.BeginCindyHealthEpisode(stateCtx, episode, signal.evidence(), now, until)
		return
	}
}

func (s *CindyHealthService) restoreCurrentPendingBlock(ctx context.Context, account *Account) bool {
	if s == nil || s.episodeStore == nil || s.runtime == nil || account == nil {
		return false
	}
	episodes, err := s.episodeStore.GetCindyHealthEpisodes(ctx, account.ID)
	if err != nil {
		slog.Error("cindy_terminal_pending_get_failed", "account_id", account.ID, "error", err)
		return false
	}
	for _, episode := range episodes {
		if !episode.terminalValid() || episode.Generation != account.CindyCredentialGeneration {
			continue
		}
		fingerprint, fingerprintErr := AccountCredentialFingerprint(
			ProviderProfileCindyLaxaV1, AccountTypeAPIKey, "https://api.laxarouter.ai", account.GetCredential("api_key"),
		)
		if fingerprintErr != nil || fingerprint != episode.Fingerprint {
			continue
		}
		reason := "cindy_banned"
		if episode.Status == CindyHealthStatusBalanceInsufficient {
			reason = "cindy_balance_insufficient"
		}
		if episodeRuntime, ok := s.runtime.(CindyHealthEpisodeRuntimeBlocker); ok {
			return episodeRuntime.BlockCindyHealthEpisode(account, episode, reason)
		}
		s.runtime.BlockAccountScheduling(account, time.Time{}, reason)
		return true
	}
	return false
}

func (s *CindyHealthService) wakeTerminalRetry() {
	if s == nil || s.retryWake == nil {
		return
	}
	select {
	case s.retryWake <- struct{}{}:
	default:
	}
}

func (s *CindyHealthService) runTerminalRetryLoop() {
	defer s.wg.Done()
	s.retryPendingTerminals()
	ticker := time.NewTicker(s.retryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.retryPendingTerminals()
		case <-s.retryWake:
			s.retryPendingTerminals()
		}
	}
}

func (s *CindyHealthService) retryPendingTerminals() {
	if s == nil || s.episodeStore == nil || s.accountRepo == nil || s.healthRepo == nil || s.runtime == nil {
		return
	}
	listCtx, cancel := context.WithTimeout(s.ctx, cindyHealthStateTimeout)
	episodes, err := s.episodeStore.ListCindyHealthEpisodes(listCtx, 100)
	cancel()
	if err != nil {
		slog.Error("cindy_terminal_pending_list_failed", "error", err)
		return
	}
	for _, episode := range episodes {
		if !episode.terminalValid() {
			continue
		}
		ctx, episodeCancel := context.WithTimeout(s.ctx, cindyHealthStateTimeout)
		account, getErr := s.accountRepo.GetByID(ctx, episode.AccountID)
		if getErr != nil || account == nil {
			if errors.Is(getErr, ErrAccountNotFound) {
				_ = s.episodeStore.ClearCindyHealthEpisodeIfMatch(ctx, episode)
			}
			slog.Warn("cindy_terminal_pending_account_load_failed", "account_id", episode.AccountID, "error", getErr)
			episodeCancel()
			continue
		}
		identity, current := s.currentIdentity(ctx, account)
		if !current || identity.Generation != episode.Generation || identity.Fingerprint != episode.Fingerprint {
			if episodeRuntime, ok := s.runtime.(CindyHealthEpisodeRuntimeBlocker); ok {
				episodeRuntime.ClearCindyHealthEpisodeBlock(episode)
			}
			_ = s.episodeStore.ClearCindyHealthEpisodeIfMatch(ctx, episode)
			episodeCancel()
			continue
		}
		reason := "cindy_banned"
		if episode.Status == CindyHealthStatusBalanceInsufficient {
			reason = "cindy_balance_insufficient"
		}
		if episodeRuntime, ok := s.runtime.(CindyHealthEpisodeRuntimeBlocker); ok &&
			!episodeRuntime.BlockCindyHealthEpisode(account, episode, reason) {
			episodeCancel()
			continue
		}
		applied, persistErr := s.healthRepo.PersistCindyTerminalState(ctx, episode, CindyHealthFinalization{
			Status: episode.Status, Evidence: episode.Evidence, ObservedAt: episode.ObservedAt,
		})
		if persistErr != nil || !applied {
			slog.Error("cindy_terminal_retry_failed", "account_id", episode.AccountID, "generation", episode.Generation, "applied", applied, "error", persistErr)
			episodeCancel()
			continue
		}
		if clearErr := s.episodeStore.ClearCindyHealthEpisodeIfMatch(ctx, episode); clearErr != nil {
			slog.Warn("cindy_terminal_pending_clear_failed", "account_id", episode.AccountID, "generation", episode.Generation, "error", clearErr)
		}
		episodeCancel()
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
