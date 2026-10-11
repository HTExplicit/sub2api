package service

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"
)

// Request-owned recovery never writes downstream headers or starts a business
// generation. Every shared preparation remains bounded; only waiting is open-ended.
type CodexGatewayBorrowRecovery struct {
	Phase             string     `json:"phase"`
	StartedAt         time.Time  `json:"started_at"`
	NextAttemptAt     *time.Time `json:"next_attempt_at,omitempty"`
	Rounds            uint64     `json:"rounds"`
	SourceRequests    uint64     `json:"source_requests"`
	ProbeRequests     uint64     `json:"probe_requests"`
	SharedPreparation bool       `json:"shared_preparation,omitempty"`
	LastStage         string     `json:"last_stage,omitempty"`
	LastReason        string     `json:"last_reason,omitempty"`
	LastError         string     `json:"last_error,omitempty"`
}

func (t *codexBorrowUsageTracker) recovering(phase string, next *time.Time, err error) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished || t.row.Dispatched {
		return
	}
	r := CodexGatewayBorrowRecovery{StartedAt: time.Now()}
	if t.row.Recovery != nil {
		r = *t.row.Recovery
	}
	r.Phase, r.NextAttemptAt = phase, next
	if phase == "searching" {
		r.Rounds++
	}
	if err != nil {
		r.LastStage, r.LastReason = codexBorrowFailureStage(err)
		r.LastError = err.Error()
	}
	t.row.Recovery = &r
	t.publish(0, 0, 0)
	log.Printf("[codex_borrow_recovery] request_id=%s account_id=%d model=%s phase=%s round=%d reason=%s next_attempt_at=%v source_requests=%d probe_requests=%d", t.row.GatewayRequestID, t.key.account, t.key.model, phase, r.Rounds, r.LastReason, next, r.SourceRequests, r.ProbeRequests)
}

func (t *codexBorrowUsageTracker) recoveryObservation(source bool, attempts int) {
	if t == nil || attempts == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.row.Recovery == nil {
		return
	}
	r := *t.row.Recovery
	if source {
		r.SourceRequests += uint64(attempts)
	} else {
		r.ProbeRequests += uint64(attempts)
	}
	t.row.Recovery = &r
	t.publish(0, 0, 0)
	log.Printf("[codex_borrow_recovery_observation] request_id=%s account_id=%d model=%s source=%t attempts=%d", t.row.GatewayRequestID, t.key.account, t.key.model, source, attempts)
}

func (t *codexBorrowUsageTracker) sharedRecoveryPreparation() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished || t.row.Recovery == nil {
		return
	}
	r := *t.row.Recovery
	r.SharedPreparation = true
	t.row.Recovery = &r
	t.publish(0, 0, 0)
}

func borrowRecoveryRetryable(err error) bool {
	if errors.Is(err, ErrCodexGatewayBorrowChanged) || errors.Is(err, context.Canceled) {
		return false
	}
	var failure *CodexGatewayBorrowFailure
	if errors.As(err, &failure) {
		if v := failure.Verification; v != nil && (v.Reason == "account_unavailable" || v.MintStatus == 401 || v.ContinueStatus == 401) {
			return false
		}
		switch failure.Reason {
		case "account_unavailable", "account_inactive", "model_not_allowed", "model_mapping_mismatch", "configuration_changed", "node_proxy_repository_unavailable":
			return false
		}
		return true
	}
	return errors.Is(err, ErrCodexGatewayBorrowUnavailable) || errors.Is(err, context.DeadlineExceeded)
}

// Compute the next actual eligible node, rather than spinning every 15 seconds
// through an hour-long rejection. Source/node and target/gateway quiet windows
// are combined; a global source-search pause is a lower bound for all nodes.
func (s *CodexGatewayBorrowService) nextBorrowRecoveryAttempt(ctx context.Context, accountID int64, model string, err error) time.Time {
	now := time.Now()
	fallback := now.Add(codexGatewayBorrowFailureWait)
	cfg := s.ConfigSnapshot()
	nodes, loadErr := s.borrowNodes(ctx, cfg.NodeProxyIDs)
	if loadErr != nil || len(nodes) == 0 {
		return fallback
	}
	cache := s.rotation.cache(model)
	s.rotation.mu.Lock()
	global := s.rotation.retry[codexGatewayBorrowTargetKey{accountID, model}]
	s.rotation.mu.Unlock()
	if until := cache.sourceSearchPausedUntil(accountID, now); until.After(global) {
		global = until
	}
	var earliest time.Time
	for _, node := range nodes {
		until := cache.blockedUntil(accountID, node.identity, "", now)
		if sourceUntil := s.rotation.sourceCache.sourcesBlockedUntil(cfg.SourceAccountIDs, node.identity, now); sourceUntil.After(until) {
			until = sourceUntil
		}
		if global.After(until) {
			until = global
		}
		if !now.Before(until) {
			return fallback
		}
		if earliest.IsZero() || until.Before(earliest) {
			earliest = until
		}
	}
	if earliest.IsZero() {
		return fallback
	}
	return earliest
}

func (s *CodexGatewayBorrowService) recoveryAccount(ctx context.Context, id int64, model string) (*Account, *http.Request, error) {
	a, template, _, err := s.accountTemplate(ctx, id, model)
	if err != nil {
		return nil, nil, &CodexGatewayBorrowFailure{Cause: err, Stage: "recovery", Reason: "account_unavailable"}
	}
	return a, template, nil
}

func (s *CodexGatewayBorrowService) waitBorrowRecovery(ctx context.Context, next time.Time, accountID int64) error {
	timer := time.NewTimer(time.Until(next))
	defer timer.Stop()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case <-ticker.C:
			a, err := s.accounts.GetByID(ctx, accountID)
			if err != nil || !codexGatewayBorrowAccountSupported(a) || a.Status != StatusActive {
				return &CodexGatewayBorrowFailure{Cause: ErrCodexGatewayBorrowUnavailable, Stage: "recovery", Reason: "account_inactive"}
			}
		}
	}
}

func (s *CodexGatewayBorrowService) recoverQualifiedHTTP(ctx context.Context, req *http.Request, account *Account, model, proxy string, revision uint64, revisionCtx context.Context) (*http.Request, *CodexGatewayBorrowApplication, error) {
	caller := ctx
	ctx, cancel := context.WithCancel(ctx)
	stopRevision := context.AfterFunc(revisionCtx, cancel)
	defer stopRevision()
	defer cancel()
	tracker := codexBorrowUsageFromContext(ctx)
	originalAccount := req.Header.Get("ChatGPT-Account-ID")
	wire := req.Clone(ctx) // Shares Body/GetBody; the pending generation is never read or replayed.
	for {
		if err := ctx.Err(); err != nil {
			if revisionCtx.Err() != nil {
				return nil, nil, ErrCodexGatewayBorrowChanged
			}
			return nil, nil, err
		}
		s.mu.Lock()
		current := s.revision == revision && s.config.Enabled && s.config.WaitForQualifiedRoute && s.config.RotateNodes
		s.mu.Unlock()
		if !current {
			return nil, nil, ErrCodexGatewayBorrowChanged
		}
		checkCtx, cancelCheck := context.WithTimeout(ctx, codexGatewayBorrowStoreTimeout)
		fresh, auth, err := s.recoveryAccount(checkCtx, account.ID, model)
		cancelCheck()
		if err != nil {
			return nil, nil, err
		}
		if auth.Header.Get("ChatGPT-Account-ID") != originalAccount {
			return nil, nil, ErrCodexGatewayBorrowChanged
		}
		wire.Header.Set("Authorization", auth.Header.Get("Authorization"))
		tracker.recovering("searching", nil, nil)
		roundCtx, cancelRound := context.WithTimeout(ctx, 180*time.Second)
		prepared, app, err := s.Apply(wire.WithContext(roundCtx), fresh, model, proxy, nil, false)
		cancelRound()
		if err == nil && app != nil && app.Applied {
			checkCtx, cancelCheck := context.WithTimeout(ctx, codexGatewayBorrowStoreTimeout)
			latest, latestAuth, checkErr := s.recoveryAccount(checkCtx, account.ID, model)
			cancelCheck()
			if checkErr != nil {
				return nil, nil, checkErr
			}
			if account.Schedulable && !latest.Schedulable {
				return nil, nil, &CodexGatewayBorrowFailure{Cause: ErrCodexGatewayBorrowUnavailable, Stage: "recovery", Reason: "account_inactive"}
			}
			if latestAuth.Header.Get("ChatGPT-Account-ID") != originalAccount || latestAuth.Header.Get("X-OpenAI-Fedramp") != wire.Header.Get("X-OpenAI-Fedramp") {
				return nil, nil, ErrCodexGatewayBorrowChanged
			}
			if latestAuth.Header.Get("Authorization") != wire.Header.Get("Authorization") {
				continue
			}
			tracker.recovering("recovered", nil, nil)
			return prepared.WithContext(codexGatewayBorrowRecoveryContext{Context: caller, values: prepared.Context()}), app, nil
		}
		if err == nil {
			err = ErrCodexGatewayBorrowChanged
		}
		if ctx.Err() != nil {
			continue
		}
		if errors.Is(err, ErrCodexGatewayBorrowChanged) {
			// Revision cancellation stops above. Lease expiry or a changed node
			// within a live configuration requires a new qualified route.
			err = &CodexGatewayBorrowFailure{Cause: ErrCodexGatewayBorrowUnavailable, Stage: "recovery", Reason: "route_changed"}
		}
		if !borrowRecoveryRetryable(err) {
			return nil, nil, err
		}
		next := s.nextBorrowRecoveryAttempt(ctx, account.ID, model, err)
		tracker.recovering("cooling", &next, err)
		if err := s.waitBorrowRecovery(ctx, next, account.ID); err != nil {
			if revisionCtx.Err() != nil {
				return nil, nil, ErrCodexGatewayBorrowChanged
			}
			return nil, nil, err
		}
	}
}
