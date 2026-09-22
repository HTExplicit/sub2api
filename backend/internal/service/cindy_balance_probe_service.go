package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"

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
	if err := requireCindyBalanceProbePolicy(ctx); err != nil {
		return nil, err
	}
	scope, err := captureCindyProbeOrigin(ctx, scope, rateRPS, 0, "")
	if err != nil {
		return nil, err
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
	if err := requireCindyBalanceProbePolicy(ctx); err != nil {
		return nil, err
	}
	if err := validateCindyBalanceProbeRate(rateRPS); err != nil {
		return nil, err
	}
	scope = CanonicalizeCindyBalanceProbeScope(scope)
	var err error
	scope, err = captureCindyProbeOrigin(ctx, scope, rateRPS, expectedCount, expectedFingerprint)
	if err != nil {
		return nil, err
	}
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
	// Fail closed before touching persistence: a disabled or unavailable probe
	// policy must not load, claim or rebind anything. Scoped jobs re-check the
	// policy again below inside the rebound origin context.
	if err := requireCindyBalanceProbePolicy(ctx); err != nil {
		return nil, err
	}
	stored, err := s.repo.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, ErrCindyBalanceProbeNotFound
	}
	if stored.Scope.Origin != nil {
		bound, release, err := rebindCindyProbeOrigin(ctx, stored.Scope)
		if err != nil {
			return nil, err
		}
		defer release()
		ctx = bound
		if err := requireCindyBalanceProbePolicy(ctx); err != nil {
			return nil, err
		}
		repository, ok := s.repo.(CindyScopedProbeResumeRepository)
		if !ok {
			return nil, ErrAccountViewUnavailable
		}
		next, ok := ctx.Value(cindyProbeOriginContextKey{}).(*CindyBalanceProbeOrigin)
		if !ok || next == nil {
			return nil, ErrAccountViewUnavailable
		}
		job, err := repository.ResumeScoped(ctx, jobID, stored.Scope.Origin, next)
		if err == nil {
			s.notify()
		}
		return job, err
	}
	if err := requireCindyBalanceProbePolicy(ctx); err != nil {
		return nil, err
	}
	job, err := s.repo.Resume(ctx, jobID)
	if err == nil {
		s.notify()
	}
	return job, err
}

func (s *CindyBalanceProbeService) Cancel(ctx context.Context, jobID int64) (*CindyBalanceProbeJob, error) {
	job, err := s.repo.Cancel(ctx, jobID)
	if err == nil {
		if job != nil && job.Scope.Origin != nil {
			// Host-only progress finalization must not wait for a now-disabled
			// provider/view to claim the canceled job. No account health is written.
			finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			_, finishErr := s.repo.FinishIfDone(finishCtx, jobID, "")
			cancel()
			if finishErr != nil {
				return nil, finishErr
			}
			job, err = s.repo.GetJob(ctx, jobID)
		}
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
	for {
		if s.ctx.Err() != nil {
			return
		}
		now := s.now().UTC()
		if _, err := cindyBalanceProbePlan(s.ctx); err != nil {
			s.wait(cindyBalanceProbePollInterval)
			continue
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
	var jobCtx context.Context
	var cancel context.CancelFunc
	var err error
	if job.Scope.Origin != nil {
		jobCtx, cancel, err = bindCindyProbeOrigin(s.ctx, job.Scope)
	} else {
		jobCtx, cancel, err = bindProcessExtensionContext(s.ctx, PlatformCindy, AccountTypeAPIKey,
			extensionv1.Invocation{Capability: extensionv1.CapabilityProvider, Operation: "cindy.probe.plan"})
	}
	if err != nil {
		return
	}
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
					lostOnce.Do(func() { close(lostLease); cancel() })
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
	if ctx.Err() != nil {
		return false
	}
	if origin, bound := ctx.Value(cindyProbeOriginContextKey{}).(*CindyBalanceProbeOrigin); bound {
		if reservation == nil || !slices.Contains(origin.FrozenAccountIDs, reservation.AccountID) || ValidateAccountViewTargets(ctx, []int64{reservation.AccountID}) != nil {
			return false
		}
		view, _ := AccountViewFromContext(ctx)
		if view == nil || view.manager.ValidateResourceAccounts(ctx, extensionv1.CapabilityProvider, []int64{reservation.AccountID}, false) != nil {
			return false
		}
	}
	account, eligible := s.loadReservationAccount(ctx, reservation)
	if !eligible || account.ID <= 0 {
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
	// Creating a job only admitted its then-current selection. Bind the actual
	// reserved account again before any IO, retaining one policy lifetime for
	// its plan, HTTP request and completion decision.
	bound, release, err := bindProcessExtensionContext(ctx, PlatformCindy, AccountTypeAPIKey,
		extensionv1.Invocation{Capability: extensionv1.CapabilityProvider, Operation: "cindy.probe.plan", AccountID: account.ID})
	if err != nil {
		if ctx.Err() == nil && errors.Is(err, ErrExtensionOperationDisabled) {
			// A known admission rejection invalidates the original selection,
			// not the account's health. Finish via the existing claim CAS so this
			// reservation is never retried or reported as an upstream failure.
			slog.Info("cindy_balance_probe_scope_skipped", "job_id", reservation.JobID, "account_id", account.ID, "stage", reservation.Stage)
			return s.completeStage(ctx, reservation, account, leaseToken, "stale", "skipped_stale", false)
		}
		return false
	}
	defer release()
	ctx = bound
	plan, err := cindyBalanceProbePlanForAccount(ctx, account.ID)
	if err != nil {
		return false
	}
	model := plan.Models[0]
	if reservation.Stage == "terra" {
		model = plan.Models[1]
	}
	probeCtx, cancel := context.WithTimeout(ctx, cindyBalanceProbeTimeout)
	outcome := s.gateway.probeCindyBalanceModel(probeCtx, account, model)
	cancel()
	if ctx.Err() != nil {
		return false
	}
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
	decision, err := cindyBalanceProbeDecision(ctx, reservation.AccountID, reservation.Stage, reservation.WasMarked, outcome)
	if err != nil {
		return false
	}
	switch decision.Action {
	case "recover":
		return s.finalizeRecovery(ctx, reservation, account, leaseToken)
	case "exhausted":
		return s.finalizeExhausted(ctx, reservation, account, leaseToken)
	case "complete":
		return s.completeStage(ctx, reservation, account, leaseToken, decision.Outcome, decision.State, decision.NetworkFailure)
	default:
		return false
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
				clearBase, release := detachPluginPolicyContext(ctx)
				defer release()
				clearCtx, cancel := context.WithTimeout(clearBase, 3*time.Second)
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
	var state string
	var err error
	var committed *CindyHealthEpisode
	_, scoped := CindyBalanceProbeOriginOwner(ctx)
	if scoped {
		repository, ok := s.repo.(CindyScopedProbeTerminalRepository)
		if !ok {
			return false
		}
		state, committed, err = repository.FinalizeScopedExhausted(ctx, reservation, leaseToken, s.now().UTC(), cindyBalanceProbeConfirmationWindow)
		if err == nil && (state == "exhausted" || state == "already_marked") && (committed == nil || !committed.terminalValid() || committed.AccountID != reservation.AccountID) {
			return false
		}
	} else {
		state, err = s.repo.FinalizeExhausted(ctx, reservation, leaseToken, s.now().UTC(), cindyBalanceProbeConfirmationWindow)
	}
	if err == nil && (state == "exhausted" || state == "already_marked") {
		// The DB marker and item outcome committed atomically. Reuse the shared
		// health coordinator so the diagnostic terminal block is owned by the
		// current credential generation/fingerprint episode, just like a request
		// signal. Do not fall back to the legacy fingerprint-only block.
		if s.gateway != nil && s.gateway.cindyHealth != nil {
			if scoped {
				if projector, ok := s.gateway.cindyHealth.(CindyCommittedProbeHealthProjector); ok {
					projector.ApplyCommittedProbeTerminal(ctx, account, *committed)
				}
			} else {
				s.gateway.cindyHealth.ObserveCindyHealthSignal(ctx, account, CindyHealthSignalExactBudget)
			}
		}
		if s.gateway != nil {
			store, ok := s.gateway.cindyBalancePendingStore()
			if !ok {
				return true
			}
			clearBase, release := detachPluginPolicyContext(ctx)
			defer release()
			clearCtx, clearCancel := context.WithTimeout(clearBase, 3*time.Second)
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

type cindyProbeOperationKeyContextKey struct{}
type cindyProbeOriginContextKey struct{}

func WithCindyProbeOperationKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, cindyProbeOperationKeyContextKey{}, strings.TrimSpace(key))
}

func captureCindyProbeOrigin(ctx context.Context, scope CindyBalanceProbeScope, rate float64, expectedCount int, fingerprint string) (CindyBalanceProbeScope, error) {
	scope.Origin = nil // Origin is never accepted from an HTTP/plugin scope copy.
	view, bound := AccountViewFromContext(ctx)
	if !bound {
		return scope, nil
	}
	if err := ValidateAccountViewFresh(ctx); err != nil {
		return scope, err
	}
	primary, ok := PluginExecutionFromContext(ctx)
	if !ok {
		return scope, ErrAccountViewUnavailable
	}
	installation, err := view.manager.repo.GetByID(ctx, primary.ID)
	if err != nil || installation == nil || installation.RuntimeGeneration != primary.Generation || installation.State != PluginStateEnabled {
		return scope, ErrAccountViewUnavailable
	}
	metadata, err := stampAccountJobView(ctx, nil)
	if err != nil {
		return scope, err
	}
	snapshot, err := AccountJobViewExecution(metadata)
	if err != nil || snapshot == nil {
		return scope, ErrAccountViewUnavailable
	}
	key, _ := ctx.Value(cindyProbeOperationKeyContextKey{}).(string)
	if key != "" && !regexp.MustCompile(`^[a-zA-Z0-9:_-]{1,160}$`).MatchString(key) {
		return scope, ErrAccountViewInvalid
	}
	request, _ := json.Marshal(struct {
		Scope         CindyBalanceProbeScope
		View          extensionv1.AccountViewIdentityV1
		QueryDigest   string
		Owner         int64
		Package       string
		Rate          float64
		ExpectedCount int
		Fingerprint   string
	}{CanonicalizeCindyBalanceProbeScope(scope), snapshot.AccountViewIdentityV1, snapshot.NormalizedQueryDigest, primary.ID, installation.PackageSHA256, rate, expectedCount, strings.TrimSpace(fingerprint)})
	digest := sha256.Sum256(request)
	scope.Origin = &CindyBalanceProbeOrigin{Version: 1, PluginID: primary.ID, PluginKey: installation.PluginKey, PackageSHA256: installation.PackageSHA256, RuntimeGeneration: primary.Generation, View: *snapshot, OperationKey: key, RequestDigest: hex.EncodeToString(digest[:])}
	return scope, nil
}

func (m *PluginManager) BindCindyBalanceProbeOrigin(ctx context.Context, scope CindyBalanceProbeScope) (context.Context, context.CancelFunc, error) {
	return m.bindCindyBalanceProbeOrigin(ctx, scope, false)
}

func (m *PluginManager) RebindCindyBalanceProbeOrigin(ctx context.Context, scope CindyBalanceProbeScope) (context.Context, context.CancelFunc, error) {
	return m.bindCindyBalanceProbeOrigin(ctx, scope, true)
}

func (m *PluginManager) bindCindyBalanceProbeOrigin(ctx context.Context, scope CindyBalanceProbeScope, explicitResume bool) (context.Context, context.CancelFunc, error) {
	origin := scope.Origin
	if origin == nil || origin.Version != 1 || origin.PluginID <= 0 || origin.RuntimeGeneration <= 0 || origin.View.RuntimeGeneration <= 0 || !accountViewDigestPattern.MatchString(origin.PackageSHA256) || !accountViewDigestPattern.MatchString(origin.RequestDigest) || len(origin.FrozenAccountIDs) == 0 {
		return nil, nil, ErrAccountViewUnavailable
	}
	for _, id := range origin.FrozenAccountIDs {
		if id <= 0 {
			return nil, nil, ErrAccountViewScope
		}
	}
	if existing, bound := AccountViewFromContext(ctx); bound && existing.Request.AccountViewIdentityV1 != origin.View.AccountViewIdentityV1 {
		return nil, nil, ErrAccountViewUnavailable
	}
	viewCtx, releaseView, err := m.BindAccountViewRequest(ctx, extensionv1.AccountViewContextV1{AccountViewIdentityV1: origin.View.AccountViewIdentityV1})
	if err != nil {
		return nil, nil, err
	}
	view, _ := AccountViewFromContext(viewCtx)
	if !explicitResume && view.Execution.Generation != origin.View.RuntimeGeneration {
		releaseView()
		return nil, nil, ErrAccountViewUnavailable
	}
	primary, releasePrimary, err := m.BindResourceContext(viewCtx, origin.PluginID, origin.PackageSHA256, extensionv1.ResourceGrant{Name: "cindy.probe.create", Capability: extensionv1.CapabilityProvider, Permission: "admin"})
	if err != nil {
		releaseView()
		return nil, nil, err
	}
	release := func() { releasePrimary(); releaseView() }
	execution, _ := PluginExecutionFromContext(primary)
	current, err := m.repo.GetByID(primary, origin.PluginID)
	if err != nil || current == nil || current.PluginKey != origin.PluginKey || (!explicitResume && execution.Generation != origin.RuntimeGeneration) {
		release()
		return nil, nil, ErrAccountViewUnavailable
	}
	if err := m.ValidateResourceAccounts(primary, extensionv1.CapabilityProvider, nil, scope.Mode != "selected", PlatformCindy, AccountTypeAPIKey); err != nil {
		release()
		return nil, nil, err
	}
	next := *origin
	if explicitResume {
		if err := ValidateAccountViewTargets(primary, origin.FrozenAccountIDs); err != nil {
			release()
			return nil, nil, err
		}
		if err := m.ValidateResourceAccounts(primary, extensionv1.CapabilityProvider, origin.FrozenAccountIDs, false); err != nil {
			release()
			return nil, nil, err
		}
		next.RuntimeGeneration, next.View.RuntimeGeneration, next.View.PolicyRevision = execution.Generation, view.Execution.Generation, view.installation.Revision
	}
	return context.WithValue(primary, cindyProbeOriginContextKey{}, &next), release, nil
}

func bindCindyProbeOrigin(ctx context.Context, scope CindyBalanceProbeScope) (context.Context, context.CancelFunc, error) {
	provider := processExtensionOperations.Load()
	if provider == nil {
		return nil, nil, ErrAccountViewUnavailable
	}
	binder, ok := provider.invoker.(interface {
		BindCindyBalanceProbeOrigin(context.Context, CindyBalanceProbeScope) (context.Context, context.CancelFunc, error)
	})
	if !ok {
		return nil, nil, ErrAccountViewUnavailable
	}
	return binder.BindCindyBalanceProbeOrigin(ctx, scope)
}

func rebindCindyProbeOrigin(ctx context.Context, scope CindyBalanceProbeScope) (context.Context, context.CancelFunc, error) {
	provider := processExtensionOperations.Load()
	if provider == nil {
		return nil, nil, ErrAccountViewUnavailable
	}
	binder, ok := provider.invoker.(interface {
		RebindCindyBalanceProbeOrigin(context.Context, CindyBalanceProbeScope) (context.Context, context.CancelFunc, error)
	})
	if !ok {
		return nil, nil, ErrAccountViewUnavailable
	}
	return binder.RebindCindyBalanceProbeOrigin(ctx, scope)
}

func CindyBalanceProbeOriginOwner(ctx context.Context) (string, bool) {
	origin, ok := ctx.Value(cindyProbeOriginContextKey{}).(*CindyBalanceProbeOrigin)
	if !ok || origin == nil {
		return "", false
	}
	return origin.PluginKey, true
}

func ValidateCindyBalanceProbeOriginContext(ctx context.Context, origin *CindyBalanceProbeOrigin) error {
	if origin == nil {
		return nil
	}
	view, bound := AccountViewFromContext(ctx)
	primary, primaryBound := PluginExecutionFromContext(ctx)
	if !bound || !primaryBound || origin.Version != 1 || view.Request.AccountViewIdentityV1 != origin.View.AccountViewIdentityV1 || view.Execution.Generation != origin.View.RuntimeGeneration || primary.ID != origin.PluginID || primary.Generation != origin.RuntimeGeneration {
		return ErrAccountViewUnavailable
	}
	return ValidateAccountViewFresh(ctx)
}
