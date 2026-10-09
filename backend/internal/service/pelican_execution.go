package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

const (
	PelicanExecutionConcurrency            = 10
	PelicanDefaultGenerationTimeoutSeconds = 600
	PelicanMinGenerationTimeoutSeconds     = 60
	PelicanMaxGenerationTimeoutSeconds     = 1800
	PelicanExecutionModeAccount            = "account"
	PelicanExecutionModeLegacyCache        = "legacy_cache"
)

// Every manual Pelican runner in this process shares the same outbound capacity.
// Preparation and generation acquire it only while an actual model request is
// in flight. A waiting preparation therefore cannot retain a target's permit
// while it waits for a source account or the remaining global permits.
var sharedPelicanExecution = newPelicanExecutionCoordinator(PelicanExecutionConcurrency)

type pelicanExecutionContextKey struct{}
type pelicanExecutionLeaseContextKey struct{}

type pelicanExecutionWaiter struct {
	accountID int64
	ready     chan struct{}
	granted   bool
}

type pelicanExecutionCoordinator struct {
	mu       sync.Mutex
	limit    int
	active   int
	accounts map[int64]bool
	waiting  []*pelicanExecutionWaiter
}

func newPelicanExecutionCoordinator(limit int) *pelicanExecutionCoordinator {
	return &pelicanExecutionCoordinator{limit: limit, accounts: make(map[int64]bool)}
}

// scheduleLocked grants account and global capacity atomically. No waiter holds
// one resource while waiting for the other, and each account keeps FIFO order.
func (c *pelicanExecutionCoordinator) scheduleLocked() {
	for i := 0; i < len(c.waiting) && c.active < c.limit; {
		waiter := c.waiting[i]
		if c.accounts[waiter.accountID] {
			i++
			continue
		}
		c.waiting = append(c.waiting[:i], c.waiting[i+1:]...)
		c.accounts[waiter.accountID] = true
		c.active++
		waiter.granted = true
		close(waiter.ready)
	}
}

func (c *pelicanExecutionCoordinator) acquire(ctx context.Context, accountID int64) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	waiter := &pelicanExecutionWaiter{accountID: accountID, ready: make(chan struct{})}
	c.mu.Lock()
	c.waiting = append(c.waiting, waiter)
	c.scheduleLocked()
	c.mu.Unlock()
	select {
	case <-waiter.ready:
	case <-ctx.Done():
	}
	c.mu.Lock()
	if err := ctx.Err(); err != nil {
		if waiter.granted {
			delete(c.accounts, accountID)
			c.active--
		} else {
			for i, pending := range c.waiting {
				if pending == waiter {
					c.waiting = append(c.waiting[:i], c.waiting[i+1:]...)
					break
				}
			}
		}
		c.scheduleLocked()
		c.mu.Unlock()
		return nil, err
	}
	c.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			delete(c.accounts, accountID)
			c.active--
			c.scheduleLocked()
			c.mu.Unlock()
		})
	}, nil
}

type pelicanExecutionLease struct {
	accountID int64
	active    atomic.Bool
}

// PelicanInvocation records the resolved request at the actual sending boundary.
// It deliberately contains no group/client simulation settings or credentials.
type PelicanInvocation struct {
	Platform      string
	Model         string
	Effort        string
	Endpoint      string
	Protocol      string
	Transport     string
	BorrowApplied bool
}

type pelicanExecutionSnapshot struct {
	invocation            PelicanInvocation
	generationStartedAt   *time.Time
	queueDurationMS       int64
	preparationDurationMS int64
	generationDurationMS  int64
	generationTimedOut    bool
}

type pelicanExecutionState struct {
	admission       context.Context
	mu              sync.Mutex
	coordinator     *pelicanExecutionCoordinator
	parent          context.Context
	operationCancel context.CancelCauseFunc
	now             func() time.Time
	withTimeout     func(context.Context, time.Duration) (context.Context, context.CancelFunc)
	budget          time.Duration
	preparationAt   time.Time
	preparationWait time.Duration
	queueDuration   time.Duration
	generationAt    time.Time
	finishedAt      time.Time
	invocation      PelicanInvocation
	budgetCtx       context.Context
	budgetCancel    context.CancelFunc
	budgetStopLink  func() bool
	requestCancels  []context.CancelFunc
	onPhase         func(string, pelicanExecutionSnapshot)
}

func newPelicanExecutionState(parent context.Context, coordinator *pelicanExecutionCoordinator, budget time.Duration, now func() time.Time) *pelicanExecutionState {
	operationCtx, operationCancel := context.WithCancelCause(parent)
	return &pelicanExecutionState{admission: parent, parent: operationCtx, operationCancel: operationCancel, coordinator: coordinator, budget: budget, now: now,
		withTimeout: context.WithTimeout}
}

func pelicanExecutionFromContext(ctx context.Context) *pelicanExecutionState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(pelicanExecutionContextKey{}).(*pelicanExecutionState)
	return state
}

// CancelPelicanExecution stops the whole fixed-account operation when a shared
// sender loses its distributed account slot. The batch remains able to persist
// the observation. Outside a Pelican operation this helper has no side effects.
func CancelPelicanExecution(ctx context.Context, cause error) {
	if state := pelicanExecutionFromContext(ctx); state != nil {
		state.operationCancel(cause)
	}
}

// AcquirePelicanExecution coordinates an actual model request, including a
// nested borrow source/target probe, with all other manual Pelican requests.
// Call it after route preparation and retain its lease until the response body
// has been consumed/closed. Nested wrappers for the same request/account reuse
// the lease; a leased context must not be shared by parallel model requests.
// Ordinary business requests have no Pelican state and retain their usual path.
func AcquirePelicanExecution(ctx context.Context, accountID int64) (context.Context, func(), error) {
	state := pelicanExecutionFromContext(ctx)
	if state == nil {
		return ctx, func() {}, nil
	}
	if err := ctx.Err(); err != nil {
		return ctx, nil, err
	}
	if state.admission != nil {
		if err := state.admission.Err(); err != nil {
			return ctx, nil, err
		}
	}
	if lease, _ := ctx.Value(pelicanExecutionLeaseContextKey{}).(*pelicanExecutionLease); lease != nil && lease.active.Load() {
		if lease.accountID == accountID {
			return ctx, func() {}, nil
		}
		// Route preparation belongs before the final request lease. Refusing an
		// incorrectly nested account prevents every global permit being held by
		// parents waiting for child requests, rather than silently deadlocking.
		return ctx, nil, errors.New("pelican route preparation must finish before acquiring another account's request lease")
	}
	finishWait := BeginPelicanQueueWait(ctx)
	release, err := state.coordinator.acquire(ctx, accountID)
	finishWait()
	if err != nil {
		return ctx, nil, err
	}
	// A task cancellation marks its parent before walking the per-target
	// children. A released slot can wake a sibling during that walk, while the
	// sibling's own Err is still nil. Do not admit that cancelled task.
	if state.admission != nil {
		if err := state.admission.Err(); err != nil {
			release()
			return ctx, nil, err
		}
	}

	lease := &pelicanExecutionLease{accountID: accountID}
	lease.active.Store(true)
	return context.WithValue(ctx, pelicanExecutionLeaseContextKey{}, lease), func() {
		if lease.active.Swap(false) {
			release()
		}
	}, nil
}

// BeginPelicanQueueWait accounts for ordinary business-account capacity waits
// using the same clock as the global/account coordinator. Before the first
// question is sent, waiting time is excluded from route preparation. Once the
// generation clock starts, retry waits remain inside its original budget.
func BeginPelicanQueueWait(ctx context.Context) func() {
	state := pelicanExecutionFromContext(ctx)
	if state == nil {
		return func() {}
	}
	started := state.now()
	var once sync.Once
	return func() {
		once.Do(func() {
			finished := state.now()
			state.mu.Lock()
			if state.generationAt.IsZero() {
				waited := nonnegativePelicanDuration(finished.Sub(started))
				state.queueDuration += waited
				if !state.preparationAt.IsZero() {
					state.preparationWait += waited
				}
			}
			state.mu.Unlock()
		})
	}
}

// BeginPelicanPreparation marks account/credential/route preparation. Borrow
// preparation keeps its own existing budgets; none of this starts generation.
func BeginPelicanPreparation(ctx context.Context) {
	state := pelicanExecutionFromContext(ctx)
	if state == nil {
		return
	}
	state.mu.Lock()
	if !state.preparationAt.IsZero() {
		state.mu.Unlock()
		return
	}
	state.preparationAt = state.now().UTC()
	callback, snapshot := state.onPhase, state.snapshotLocked(state.preparationAt)
	state.mu.Unlock()
	if callback != nil {
		callback("preparing", snapshot)
	}
}

// BeginPelicanGeneration starts the one generation clock immediately before the
// first actual question request, after route and outbound capacity preparation.
// Repeated same-account compatibility sends inherit the first clock. It returns
// a context retaining the current sender's values and cancellation boundaries.
func BeginPelicanGeneration(ctx context.Context) context.Context {
	state := pelicanExecutionFromContext(ctx)
	if state == nil {
		return ctx
	}
	state.mu.Lock()
	first := state.generationAt.IsZero()
	if first {
		state.generationAt = state.now().UTC()
		state.budgetCtx, state.budgetCancel = state.withTimeout(state.parent, state.budget)
		state.budgetStopLink = context.AfterFunc(state.budgetCtx, func() {
			if errors.Is(context.Cause(state.budgetCtx), context.DeadlineExceeded) {
				state.operationCancel(context.DeadlineExceeded)
			}
		})
	}
	budgetCtx := state.budgetCtx
	deadline, hasDeadline := budgetCtx.Deadline()
	var generationCtx context.Context
	var cancel context.CancelFunc
	if hasDeadline {
		generationCtx, cancel = context.WithDeadline(ctx, deadline)
	} else {
		generationCtx, cancel = context.WithCancel(ctx)
	}
	stopLink := context.AfterFunc(budgetCtx, cancel)
	state.requestCancels = append(state.requestCancels, func() { stopLink(); cancel() })
	callback, snapshot := state.onPhase, state.snapshotLocked(state.generationAt)
	state.mu.Unlock()
	if budgetCtx.Err() != nil {
		cancel()
	}
	if first && callback != nil {
		callback("generating", snapshot)
	}
	return generationCtx
}

func RecordPelicanInvocation(ctx context.Context, invocation PelicanInvocation) {
	if state := pelicanExecutionFromContext(ctx); state != nil {
		state.mu.Lock()
		state.invocation = invocation
		state.mu.Unlock()
	}
}

func nonnegativePelicanDuration(value time.Duration) time.Duration {
	if value < 0 {
		return 0
	}
	return value
}

func (s *pelicanExecutionState) snapshotLocked(now time.Time) pelicanExecutionSnapshot {
	snapshot := pelicanExecutionSnapshot{invocation: s.invocation, queueDurationMS: s.queueDuration.Milliseconds()}
	endPreparation := now
	if !s.generationAt.IsZero() {
		started := s.generationAt
		snapshot.generationStartedAt = &started
		endPreparation = started
		snapshot.generationDurationMS = nonnegativePelicanDuration(now.Sub(started)).Milliseconds()
		snapshot.generationTimedOut = errors.Is(context.Cause(s.budgetCtx), context.DeadlineExceeded)
	}
	if !s.preparationAt.IsZero() {
		snapshot.preparationDurationMS = nonnegativePelicanDuration(endPreparation.Sub(s.preparationAt) - s.preparationWait).Milliseconds()
	}
	return snapshot
}

func (s *pelicanExecutionState) finish() pelicanExecutionSnapshot {
	s.mu.Lock()
	s.finishedAt = s.now().UTC()
	snapshot := s.snapshotLocked(s.finishedAt)
	cancels := s.requestCancels
	budgetCancel := s.budgetCancel
	budgetStopLink := s.budgetStopLink
	s.requestCancels = nil
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	if budgetCancel != nil {
		budgetCancel()
	}
	if budgetStopLink != nil {
		budgetStopLink()
	}
	s.operationCancel(context.Canceled)
	return snapshot
}

func applyPelicanExecutionSnapshot(result *CodexGatewayBorrowTestResult, snapshot pelicanExecutionSnapshot) {
	if snapshot.invocation.Platform != "" {
		result.Platform = snapshot.invocation.Platform
	}
	if snapshot.invocation.Model != "" {
		result.UpstreamModel = snapshot.invocation.Model
		result.Effort = snapshot.invocation.Effort
	}
	result.ActualEndpoint = snapshot.invocation.Endpoint
	result.ActualProtocol = snapshot.invocation.Protocol
	result.ActualTransport = snapshot.invocation.Transport
	result.BorrowApplied = snapshot.invocation.BorrowApplied
	result.GenerationStartedAt = snapshot.generationStartedAt
	result.QueueDurationMS = snapshot.queueDurationMS
	result.PreparationDurationMS = snapshot.preparationDurationMS
	result.GenerationDurationMS = snapshot.generationDurationMS
}
