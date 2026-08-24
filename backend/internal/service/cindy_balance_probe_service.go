package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// CindyBalanceProbeService executes only jobs explicitly created by an
// administrator. It never discovers accounts or creates periodic work.
type CindyBalanceProbeService struct {
	repo        CindyBalanceProbeRepository
	accountRepo AccountRepository
	gateway     *OpenAIGatewayService
	rateLimit   *RateLimitService

	ctx       context.Context
	cancel    context.CancelFunc
	wake      chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
	wg        sync.WaitGroup
	now       func() time.Time
}

func NewCindyBalanceProbeService(
	repo CindyBalanceProbeRepository,
	accountRepo AccountRepository,
	gateway *OpenAIGatewayService,
	rateLimit *RateLimitService,
) *CindyBalanceProbeService {
	ctx, cancel := context.WithCancel(context.Background())
	return &CindyBalanceProbeService{
		repo: repo, accountRepo: accountRepo, gateway: gateway, rateLimit: rateLimit,
		ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), now: time.Now,
	}
}

func (s *CindyBalanceProbeService) Start() {
	if s == nil || s.repo == nil || s.accountRepo == nil || s.gateway == nil || s.rateLimit == nil {
		return
	}
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go s.run()
	})
}

func (s *CindyBalanceProbeService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		s.cancel()
		s.wg.Wait()
	})
}

func (s *CindyBalanceProbeService) Preview(
	ctx context.Context,
	scope CindyBalanceProbeScope,
	rateRPS float64,
) (*CindyBalanceProbePreview, error) {
	if s == nil || s.repo == nil {
		return nil, ErrCindyBalanceProbeChanged
	}
	return s.repo.Preview(ctx, CanonicalizeCindyBalanceProbeScope(scope), rateRPS)
}

func (s *CindyBalanceProbeService) CreateJob(
	ctx context.Context,
	requestedBy *int64,
	scope CindyBalanceProbeScope,
	rateRPS float64,
	expectedCount int,
	expectedFingerprint string,
) (*CindyBalanceProbeJob, error) {
	if s == nil || s.repo == nil || expectedCount < 0 || strings.TrimSpace(expectedFingerprint) == "" {
		return nil, ErrCindyBalanceProbeChanged
	}
	if rateRPS == 0 {
		rateRPS = CindyBalanceProbeDefaultRateRPS
	}
	if err := validateCindyBalanceProbeRate(rateRPS); err != nil {
		return nil, err
	}
	scope = CanonicalizeCindyBalanceProbeScope(scope)
	job, err := s.repo.CreateJob(
		ctx,
		requestedBy,
		scope,
		rateRPS,
		expectedCount,
		strings.TrimSpace(expectedFingerprint),
	)
	if err == nil {
		s.notify()
	}
	return job, err
}

func (s *CindyBalanceProbeService) GetJob(ctx context.Context, jobID int64) (*CindyBalanceProbeJob, error) {
	return s.repo.GetJob(ctx, jobID)
}

// ListJobs returns the most recently created probe jobs.
func (s *CindyBalanceProbeService) ListJobs(ctx context.Context, limit int) (*CindyBalanceProbeJobList, error) {
	return s.repo.ListJobs(ctx, limit)
}

func (s *CindyBalanceProbeService) ListItems(ctx context.Context, jobID int64, state string, page, pageSize int) (*CindyBalanceProbePage, error) {
	return s.repo.ListItems(ctx, jobID, state, page, pageSize)
}

func (s *CindyBalanceProbeService) SetRate(ctx context.Context, jobID int64, rateRPS float64) (*CindyBalanceProbeJob, error) {
	if err := validateCindyBalanceProbeRate(rateRPS); err != nil {
		return nil, err
	}
	job, err := s.repo.SetRate(ctx, jobID, rateRPS)
	if err == nil {
		s.notify()
	}
	return job, err
}

func (s *CindyBalanceProbeService) Pause(ctx context.Context, jobID int64) (*CindyBalanceProbeJob, error) {
	job, err := s.repo.Pause(ctx, jobID)
	if err == nil {
		s.notify()
	}
	return job, err
}

func (s *CindyBalanceProbeService) Resume(ctx context.Context, jobID int64) (*CindyBalanceProbeJob, error) {
	job, err := s.repo.Resume(ctx, jobID)
	if err == nil {
		s.notify()
	}
	return job, err
}

func (s *CindyBalanceProbeService) Cancel(ctx context.Context, jobID int64) (*CindyBalanceProbeJob, error) {
	job, err := s.repo.Cancel(ctx, jobID)
	if err == nil {
		s.notify()
	}
	return job, err
}

func (s *CindyBalanceProbeService) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *CindyBalanceProbeService) run() {
	defer s.wg.Done()
	lastPrune := time.Time{}
	for {
		if s.ctx.Err() != nil {
			return
		}
		now := s.now().UTC()
		if lastPrune.IsZero() || now.Sub(lastPrune) >= 24*time.Hour {
			if err := s.repo.PruneFinished(s.ctx, now.Add(-cindyBalanceProbeHistoryRetention)); err != nil {
				slog.Error("cindy_balance_probe_prune_failed", "error", err)
			}
			lastPrune = now
		}
		// A lease token is a claim epoch, not a long-lived worker identity. Never
		// let a reservation from an earlier claim regain authority after reclaim.
		leaseToken := uuid.NewString()
		job, err := s.repo.ClaimJob(s.ctx, leaseToken, now.Add(cindyBalanceProbeLeaseDuration))
		if err != nil {
			slog.Error("cindy_balance_probe_claim_failed", "error", err)
			s.wait(cindyBalanceProbePollInterval)
			continue
		}
		if job == nil {
			s.wait(cindyBalanceProbePollInterval)
			continue
		}
		s.processJob(job, leaseToken)
	}
}

func (s *CindyBalanceProbeService) wait(delay time.Duration) bool {
	if delay <= 0 {
		delay = 10 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-s.ctx.Done():
		return false
	case <-s.wake:
		return true
	case <-timer.C:
		return true
	}
}

func (s *CindyBalanceProbeService) processJob(job *CindyBalanceProbeJob, leaseToken string) {
	if job == nil {
		return
	}
	jobCtx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	lostLease := make(chan struct{})
	var lostOnce sync.Once
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(cindyBalanceProbeHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-jobCtx.Done():
				return
			case <-ticker.C:
				ok, err := s.repo.Heartbeat(jobCtx, job.ID, leaseToken, s.now().UTC().Add(cindyBalanceProbeLeaseDuration))
				if err != nil {
					slog.Error("cindy_balance_probe_heartbeat_failed", "job_id", job.ID, "error", err)
					continue
				}
				if !ok {
					lostOnce.Do(func() { close(lostLease) })
					return
				}
			}
		}
	}()
	defer func() {
		cancel()
		<-heartbeatDone
	}()

	if err := s.repo.RecoverInterruptedItems(jobCtx, job.ID, leaseToken); err != nil {
		slog.Error("cindy_balance_probe_recover_interrupted_failed", "job_id", job.ID, "error", err)
		return
	}
	for jobCtx.Err() == nil {
		select {
		case <-lostLease:
			return
		default:
		}
		now := s.now().UTC()
		reservation, delay, err := s.repo.ReserveNext(jobCtx, job.ID, leaseToken, now, now.Add(-cindyBalanceProbeConfirmationWindow))
		if err != nil {
			slog.Error("cindy_balance_probe_reserve_failed", "job_id", job.ID, "error", err)
			return
		}
		if delay > 0 {
			if !s.waitJob(jobCtx, lostLease, delay) {
				return
			}
			continue
		}
		if reservation == nil {
			done, finishErr := s.repo.FinishIfDone(jobCtx, job.ID, leaseToken)
			if finishErr != nil {
				slog.Error("cindy_balance_probe_finish_failed", "job_id", job.ID, "error", finishErr)
				return
			}
			if done {
				return
			}
			if !s.waitJob(jobCtx, lostLease, 100*time.Millisecond) {
				return
			}
			continue
		}
		if !s.executeReservation(jobCtx, reservation, leaseToken) {
			return
		}
	}
}

func (s *CindyBalanceProbeService) waitJob(ctx context.Context, lostLease <-chan struct{}, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-lostLease:
		return false
	case <-s.wake:
		return true
	case <-timer.C:
		return true
	}
}

func (s *CindyBalanceProbeService) executeReservation(ctx context.Context, reservation *CindyBalanceProbeReservation, leaseToken string) bool {
	account, eligible := s.loadReservationAccount(ctx, reservation)
	if !eligible {
		keepRunning, applied, err := s.repo.CompleteStage(ctx, reservation, account, leaseToken, "stale", "skipped_stale", false)
		if err != nil {
			slog.Error("cindy_balance_probe_stale_finalize_failed", "job_id", reservation.JobID, "error", err)
		} else if !applied {
			slog.Warn("cindy_balance_probe_stale_finalize_authority_rejected", "stage", reservation.Stage)
		}
		return err == nil && applied && keepRunning
	}
	ready, err := s.repo.ValidateReservationForSend(ctx, reservation, account, leaseToken)
	if err != nil {
		slog.Error("cindy_balance_probe_dispatch_validate_failed", "job_id", reservation.JobID, "error", err)
		return false
	}
	if !ready {
		// The old claim must not classify or release the reservation. A new claim
		// epoch conservatively recovers the pre-send reservation as unknown.
		return false
	}
	model := cindyBalanceProbeModels[0]
	if reservation.Stage == "terra" {
		model = cindyBalanceProbeModels[1]
	}
	probeCtx, cancel := context.WithTimeout(ctx, cindyBalanceProbeTimeout)
	outcome := s.gateway.probeCindyBalanceModel(probeCtx, account, model)
	cancel()
	return s.completeReservation(ctx, reservation, account, leaseToken, outcome)
}

func (s *CindyBalanceProbeService) loadReservationAccount(ctx context.Context, reservation *CindyBalanceProbeReservation) (*Account, bool) {
	if reservation == nil {
		return nil, false
	}
	account, err := s.accountRepo.GetByID(ctx, reservation.AccountID)
	if err != nil || !CindyBalanceProbeReservationMatchesAccount(reservation, account) {
		return account, false
	}
	return account, true
}

func (s *CindyBalanceProbeService) completeReservation(
	ctx context.Context,
	reservation *CindyBalanceProbeReservation,
	account *Account,
	leaseToken string,
	outcome cindyBalanceProbeOutcome,
) bool {
	switch outcome {
	case cindyBalanceProbeSuccess:
		if reservation.Stage == "luna" && reservation.WasMarked {
			return s.finalizeRecovery(ctx, reservation, account, leaseToken)
		}
		state := "healthy"
		if reservation.Stage == "terra" {
			state = "inconclusive"
		}
		return s.completeStage(ctx, reservation, account, leaseToken, "success", state, false)
	case cindyBalanceProbeExhausted:
		if reservation.Stage == "luna" {
			if reservation.WasMarked {
				return s.completeStage(ctx, reservation, account, leaseToken, "exact", "still_exhausted", false)
			}
			return s.completeStage(ctx, reservation, account, leaseToken, "exact", "luna_exact", false)
		}
		return s.finalizeExhausted(ctx, reservation, account, leaseToken)
	case cindyBalanceProbeNetworkFailure:
		return s.completeStage(ctx, reservation, account, leaseToken, "network_error", "inconclusive", true)
	case cindyBalanceProbeServerFailure:
		return s.completeStage(ctx, reservation, account, leaseToken, "server_error", "inconclusive", true)
	default:
		return s.completeStage(ctx, reservation, account, leaseToken, "other_error", "inconclusive", false)
	}
}

func (s *CindyBalanceProbeService) completeStage(
	ctx context.Context,
	reservation *CindyBalanceProbeReservation,
	accountSnapshot *Account,
	leaseToken, outcome, state string,
	networkFailure bool,
) bool {
	keepRunning, applied, err := s.repo.CompleteStage(ctx, reservation, accountSnapshot, leaseToken, outcome, state, networkFailure)
	if err != nil {
		slog.Error("cindy_balance_probe_complete_failed", "job_id", reservation.JobID, "error", err)
		return false
	}
	if !applied {
		slog.Warn("cindy_balance_probe_complete_authority_rejected",
			"stage", reservation.Stage,
			"outcome", outcome,
			"target_state", state,
		)
		return false
	}
	return keepRunning
}

func (s *CindyBalanceProbeService) finalizeRecovery(
	ctx context.Context,
	reservation *CindyBalanceProbeReservation,
	accountSnapshot *Account,
	leaseToken string,
) bool {
	var captured *CindyHealthEpisode
	if s.gateway != nil {
		var captureErr error
		captured, captureErr = s.gateway.GetCindyHealthTerminalPending(ctx, reservation.AccountID, CindyHealthStatusBalanceInsufficient)
		if captureErr != nil {
			slog.Error("cindy_balance_probe_recovery_terminal_pending_capture_failed", "job_id", reservation.JobID, "error", captureErr)
			return false
		}
	}
	recovered, err := s.repo.FinalizeRecovery(ctx, reservation, accountSnapshot, leaseToken, s.now().UTC())
	if err != nil {
		slog.Error("cindy_balance_probe_recovery_failed", "job_id", reservation.JobID, "error", err)
		return false
	}
	if recovered {
		if s.gateway != nil {
			if store, ok := s.gateway.cindyBalancePendingStore(); ok {
				clearCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
				if clearErr := store.ClearCindyBalancePendingIfFingerprintMatches(clearCtx, reservation.AccountID, reservation.IdentityFingerprint); clearErr != nil {
					slog.Error("cindy_balance_probe_recovery_pending_clear_failed", "job_id", reservation.JobID, "error", clearErr)
				}
				cancel()
			}
			if captured != nil {
				cleared, clearErr := s.gateway.ClearCindyHealthTerminalPendingIfMatch(ctx, *captured)
				if clearErr != nil {
					slog.Error("cindy_balance_probe_recovery_terminal_pending_clear_failed", "job_id", reservation.JobID, "error", clearErr)
				}
				if clearErr == nil && cleared {
					s.gateway.ClearCindyHealthEpisodeBlock(*captured)
				}
			}
		}
	}
	return true
}

func (s *CindyBalanceProbeService) finalizeExhausted(
	ctx context.Context,
	reservation *CindyBalanceProbeReservation,
	account *Account,
	leaseToken string,
) bool {
	if account == nil {
		return s.completeStage(ctx, reservation, nil, leaseToken, "stale", "skipped_stale", false)
	}
	state, err := s.repo.FinalizeExhausted(
		ctx,
		reservation,
		leaseToken,
		s.now().UTC(),
		cindyBalanceProbeConfirmationWindow,
	)
	if err == nil && (state == "exhausted" || state == "already_marked") {
		// The DB marker and item outcome committed atomically. Block locally only
		// after that commit; scheduler outbox propagation supplies the durable
		// cross-process invalidation.
		if s.rateLimit != nil {
			s.rateLimit.blockCindyBalanceRuntime(account)
		}
		if s.gateway != nil {
			store, ok := s.gateway.cindyBalancePendingStore()
			if !ok {
				return true
			}
			clearCtx, clearCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			clearErr := store.ClearCindyBalancePendingIfFingerprintMatches(clearCtx, account.ID, reservation.IdentityFingerprint)
			clearCancel()
			if clearErr != nil {
				slog.Error("cindy_balance_probe_stale_pending_clear_after_mark_failed", "job_id", reservation.JobID, "error", clearErr)
			}
		}
		return true
	}
	if err != nil {
		slog.Error("cindy_balance_probe_mark_failed", "job_id", reservation.JobID, "error", err)
		return false
	}
	if state == "skipped_stale" || state == "" {
		return true
	}
	return !errors.Is(ctx.Err(), context.Canceled)
}
