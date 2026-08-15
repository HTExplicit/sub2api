package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/tidwall/gjson"
)

var (
	ErrCindyAccountRequired           = infraerrors.BadRequest("CINDY_ACCOUNT_REQUIRED", "account is not a Cindy API key account")
	ErrCindyInsufficientDeleteChanged = infraerrors.Conflict(
		"CINDY_INSUFFICIENT_DELETE_CHANGED",
		"matching Cindy accounts changed; reload and confirm again",
	)
)

type CindyInsufficientDeletePreview struct {
	Count       int    `json:"count"`
	Fingerprint string `json:"fingerprint"`
}

type CindyInsufficientDeleteResult struct {
	DeletedCount      int     `json:"deleted_count"`
	DeletedAccountIDs []int64 `json:"-"`
}

// CindyBalanceSignal identifies the exact structured Cindy budget signal that
// was observed. The transport shape is part of the contract: an HTTP error is
// not interchangeable with an in-band Responses/WebSocket terminal event.
type CindyBalanceSignal uint8

const (
	CindyBalanceSignalNone CindyBalanceSignal = iota
	CindyBalanceSignalHTTP429
	CindyBalanceSignalResponseFailed
	CindyBalanceSignalErrorEvent
)

// CindyBalanceAccountRepository is intentionally separate from AccountRepository
// so focused gateway test doubles do not need to implement administrative cleanup.
type CindyBalanceAccountRepository interface {
	MarkCindyBalanceInsufficient(ctx context.Context, accountID int64, observedAt time.Time) (bool, error)
	ClearCindyBalanceInsufficient(ctx context.Context, accountID int64) (bool, error)
	PreviewCindyInsufficientDeletion(ctx context.Context) (*CindyInsufficientDeletePreview, error)
	DeleteCindyInsufficient(ctx context.Context, expectedCount int, fingerprint string) (*CindyInsufficientDeleteResult, error)
}

// CindyBalancePendingStore preserves an exact Cindy budget signal while the
// database marker is being committed. Production implements this with Redis
// and no TTL so a process restart cannot make an exhausted account schedulable.
// It is intentionally separate from GatewayCache to keep lightweight tests and
// alternate cache adapters source-compatible.
type CindyBalancePendingStore interface {
	MarkCindyBalancePending(ctx context.Context, accountID int64) error
	HasCindyBalancePendingBatch(ctx context.Context, accountIDs []int64) (map[int64]bool, error)
	ClearCindyBalancePending(ctx context.Context, accountID int64) error
}

type cindyBalancePendingSnapshotState uint8

const (
	cindyBalancePendingSnapshotClear cindyBalancePendingSnapshotState = iota + 1
	cindyBalancePendingSnapshotBlocked
	cindyBalancePendingSnapshotReadFailed
)

type cindyBalancePendingSnapshotContextKey struct{}

// cindyBalancePendingSnapshot is request-scoped. Negative results must never
// escape the request because that could hide a marker created immediately
// afterwards by another request/process.
type cindyBalancePendingSnapshot struct {
	mu     sync.RWMutex
	states map[int64]cindyBalancePendingSnapshotState
}

func ensureCindyBalancePendingSnapshotContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if snapshot, _ := ctx.Value(cindyBalancePendingSnapshotContextKey{}).(*cindyBalancePendingSnapshot); snapshot != nil {
		return ctx
	}
	return context.WithValue(ctx, cindyBalancePendingSnapshotContextKey{}, &cindyBalancePendingSnapshot{
		states: make(map[int64]cindyBalancePendingSnapshotState),
	})
}

func cindyBalancePendingSnapshotFromContext(ctx context.Context) *cindyBalancePendingSnapshot {
	if ctx == nil {
		return nil
	}
	snapshot, _ := ctx.Value(cindyBalancePendingSnapshotContextKey{}).(*cindyBalancePendingSnapshot)
	return snapshot
}

func (s *cindyBalancePendingSnapshot) state(accountID int64) (cindyBalancePendingSnapshotState, bool) {
	if s == nil {
		return 0, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.states[accountID]
	return state, ok
}

func (s *cindyBalancePendingSnapshot) unknownStrictCindyAccountIDs(accounts []Account) []int64 {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]int64, 0, len(accounts))
	seen := make(map[int64]struct{}, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if account.ID <= 0 || !IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) {
			continue
		}
		if _, loaded := s.states[account.ID]; loaded {
			continue
		}
		if _, duplicate := seen[account.ID]; duplicate {
			continue
		}
		seen[account.ID] = struct{}{}
		ids = append(ids, account.ID)
	}
	return ids
}

func (s *cindyBalancePendingSnapshot) record(accountIDs []int64, pending map[int64]bool, readErr error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, accountID := range accountIDs {
		state := cindyBalancePendingSnapshotClear
		if readErr != nil {
			state = cindyBalancePendingSnapshotReadFailed
		} else if pending[accountID] {
			state = cindyBalancePendingSnapshotBlocked
		}
		s.states[accountID] = state
	}
}

type cindyBalancePersistTask struct {
	accountID  int64
	observedAt time.Time
	generation uint64
	attempt    int
	nextAt     time.Time
	operation  sync.Mutex
}

var cindyBalancePersistenceBackoffs = [...]time.Duration{
	time.Second,
	5 * time.Second,
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
}

func (s *RateLimitService) scheduleCindyBalancePersistenceRetry(account *Account, observedAt time.Time) {
	if s == nil || account == nil || account.ID <= 0 {
		return
	}
	s.cindyBalancePersistMu.Lock()
	if s.cindyBalancePersistTasks == nil {
		s.cindyBalancePersistTasks = make(map[int64]*cindyBalancePersistTask)
		s.cindyBalancePersistWake = make(chan struct{}, 1)
	}
	if s.cindyBalancePersistEpochs == nil {
		s.cindyBalancePersistEpochs = make(map[int64]uint64)
	}
	if _, exists := s.cindyBalancePersistTasks[account.ID]; !exists {
		generation := s.cindyBalancePersistEpochs[account.ID] + 1
		s.cindyBalancePersistEpochs[account.ID] = generation
		s.cindyBalancePersistTasks[account.ID] = &cindyBalancePersistTask{
			accountID:  account.ID,
			observedAt: observedAt,
			generation: generation,
			nextAt:     time.Now().Add(cindyBalancePersistenceBackoffs[0]),
		}
	}
	start := !s.cindyBalancePersistRunning
	if start {
		s.cindyBalancePersistRunning = true
	}
	wake := s.cindyBalancePersistWake
	s.cindyBalancePersistMu.Unlock()
	select {
	case wake <- struct{}{}:
	default:
	}
	if start {
		go s.runCindyBalancePersistenceRetries()
	}
}

func (s *RateLimitService) runCindyBalancePersistenceRetries() {
	for {
		s.cindyBalancePersistMu.Lock()
		var next *cindyBalancePersistTask
		for _, task := range s.cindyBalancePersistTasks {
			if next == nil || task.nextAt.Before(next.nextAt) {
				next = task
			}
		}
		if next == nil {
			s.cindyBalancePersistRunning = false
			s.cindyBalancePersistMu.Unlock()
			return
		}
		wait := time.Until(next.nextAt)
		wake := s.cindyBalancePersistWake
		s.cindyBalancePersistMu.Unlock()

		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-wake:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				continue
			}
		}

		s.cindyBalancePersistMu.Lock()
		task := s.cindyBalancePersistTasks[next.accountID]
		if task == nil || task != next ||
			s.cindyBalancePersistEpochs[task.accountID] != task.generation ||
			time.Now().Before(task.nextAt) {
			s.cindyBalancePersistMu.Unlock()
			continue
		}
		s.cindyBalancePersistMu.Unlock()
		s.runCindyBalancePersistenceTask(task)
	}
}

func (s *RateLimitService) isCurrentCindyBalancePersistenceTask(task *cindyBalancePersistTask) bool {
	if s == nil || task == nil {
		return false
	}
	s.cindyBalancePersistMu.Lock()
	defer s.cindyBalancePersistMu.Unlock()
	return s.cindyBalancePersistTasks[task.accountID] == task &&
		s.cindyBalancePersistEpochs[task.accountID] == task.generation
}

// runCindyBalancePersistenceTask serializes retry side effects with explicit
// admin recovery. The generation check rejects a queued/extracted stale task;
// the operation lock is the barrier that makes recovery wait for a task which
// already entered Redis/DB I/O before clearing the durable and local state.
func (s *RateLimitService) runCindyBalancePersistenceTask(task *cindyBalancePersistTask) {
	if s == nil || task == nil {
		return
	}
	task.operation.Lock()
	defer task.operation.Unlock()
	if !s.isCurrentCindyBalancePersistenceTask(task) {
		return
	}

	// Re-establish the durable guard on every retry. This also repairs a
	// transient Redis failure from the original request before touching DB.
	if store := s.cindyBalancePendingStore; store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := store.MarkCindyBalancePending(ctx, task.accountID)
		cancel()
		if err != nil {
			slog.Error("cindy_balance_pending_retry_failed", "account_id", task.accountID, "error", err)
		}
	}
	if !s.isCurrentCindyBalancePersistenceTask(task) {
		return
	}

	repo, ok := s.accountRepo.(CindyBalanceAccountRepository)
	if !ok {
		s.rescheduleCindyBalancePersistenceRetry(task, "repository unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	changed, err := repo.MarkCindyBalanceInsufficient(ctx, task.accountID, task.observedAt)
	cancel()
	if err != nil {
		s.rescheduleCindyBalancePersistenceRetry(task, err.Error())
		return
	}
	if !s.isCurrentCindyBalancePersistenceTask(task) {
		// Recovery waits on operation before clearing DB/pending, so it owns the
		// final state after a write that raced with cancellation.
		return
	}
	if err := s.clearCindyBalancePending(context.Background(), task.accountID); err != nil {
		slog.Error("cindy_balance_pending_clear_after_retry_failed", "account_id", task.accountID, "error", err)
	}
	s.cindyBalancePersistMu.Lock()
	if s.cindyBalancePersistTasks[task.accountID] == task &&
		s.cindyBalancePersistEpochs[task.accountID] == task.generation {
		delete(s.cindyBalancePersistTasks, task.accountID)
	}
	s.cindyBalancePersistMu.Unlock()
	if changed {
		slog.Warn("cindy_balance_insufficient_marked_after_retry", "account_id", task.accountID)
	}
}

// CancelCindyBalancePersistenceRetry is the linearization point for explicit
// recovery. Incrementing the epoch is a tombstone for queued/extracted work;
// waiting on operation guarantees any already-started writes finish before the
// caller clears the DB marker, durable pending marker, and local block.
func (s *RateLimitService) CancelCindyBalancePersistenceRetry(accountID int64) {
	if s == nil || accountID <= 0 {
		return
	}
	s.cindyBalancePersistMu.Lock()
	if s.cindyBalancePersistEpochs == nil {
		s.cindyBalancePersistEpochs = make(map[int64]uint64)
	}
	s.cindyBalancePersistEpochs[accountID]++
	task := s.cindyBalancePersistTasks[accountID]
	delete(s.cindyBalancePersistTasks, accountID)
	wake := s.cindyBalancePersistWake
	s.cindyBalancePersistMu.Unlock()
	if wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	if task != nil {
		task.operation.Lock()
		task.operation.Unlock()
	}
}

func (s *RateLimitService) rescheduleCindyBalancePersistenceRetry(task *cindyBalancePersistTask, reason string) {
	if s == nil || task == nil {
		return
	}
	s.cindyBalancePersistMu.Lock()
	current := s.cindyBalancePersistTasks[task.accountID]
	if current == task && s.cindyBalancePersistEpochs[task.accountID] == task.generation {
		current.attempt++
		index := current.attempt
		if index >= len(cindyBalancePersistenceBackoffs) {
			index = len(cindyBalancePersistenceBackoffs) - 1
		}
		current.nextAt = time.Now().Add(cindyBalancePersistenceBackoffs[index])
	}
	s.cindyBalancePersistMu.Unlock()
	slog.Warn("cindy_balance_insufficient_retry_failed", "account_id", task.accountID, "reason", reason)
}

// IsCindyBalanceInsufficientResponse trusts only Cindy's structured budget
// fields. Generic 402, ordinary 429, and message text retain their normal path.
func IsCindyBalanceInsufficientResponse(account *Account, statusCode int, responseBody []byte) bool {
	return ClassifyCindyBalanceInsufficient(account, statusCode, responseBody) == CindyBalanceSignalHTTP429
}

func newCindyBalanceTerminalFailover(headers http.Header) *UpstreamFailoverError {
	return &UpstreamFailoverError{
		StatusCode:               http.StatusTooManyRequests,
		ResponseBody:             append([]byte(nil), openAITransportFailoverBody...),
		ResponseHeaders:          headers.Clone(),
		RetryableOnSameAccount:   false,
		Scope:                    GatewayFailureScopeAccount,
		NextAccountAction:        NextAccountRetry,
		CindyBalanceInsufficient: true,
	}
}

func (s *OpenAIGatewayService) cindyBalanceTerminalFailover(
	ctx context.Context,
	account *Account,
	headers http.Header,
	payload []byte,
	canonicalModel ...string,
) (*UpstreamFailoverError, bool) {
	if s == nil || !s.handleCindyBalanceTerminalEvent(ctx, account, headers, payload, canonicalModel...) {
		return nil, false
	}
	return newCindyBalanceTerminalFailover(headers), true
}

// HTTP terminal events are valid only on an exact 200 response. WebSocket
// callers use cindyBalanceTerminalFailover directly because they do not have
// an HTTP response status; HTTP callers must preserve the observed status here
// so a 201/202 body cannot masquerade as an in-band budget event.
func (s *OpenAIGatewayService) cindyBalanceHTTPResponseTerminalFailover(
	ctx context.Context,
	account *Account,
	statusCode int,
	headers http.Header,
	payload []byte,
	canonicalModel ...string,
) (*UpstreamFailoverError, bool) {
	if statusCode != http.StatusOK {
		return nil, false
	}
	return s.cindyBalanceTerminalFailover(ctx, account, headers, payload, canonicalModel...)
}

func (s *OpenAIGatewayService) handleCindyBalanceHTTPResponseTerminalEvent(
	ctx context.Context,
	account *Account,
	statusCode int,
	headers http.Header,
	payload []byte,
	canonicalModel ...string,
) bool {
	if statusCode != http.StatusOK {
		return false
	}
	return s.handleCindyBalanceTerminalEvent(ctx, account, headers, payload, canonicalModel...)
}

// ClassifyCindyBalanceInsufficient accepts only the three observed structured
// shapes. It deliberately rejects message text, numeric codes, generic 402s,
// malformed JSON, and the same payload on a non-Cindy account.
func ClassifyCindyBalanceInsufficient(account *Account, statusCode int, payload []byte) CindyBalanceSignal {
	if !CindyBalanceDetectionFeatureEnabled() || account == nil || !IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) ||
		!gjson.ValidBytes(payload) {
		return CindyBalanceSignalNone
	}

	if statusCode == http.StatusTooManyRequests && cindyBudgetErrorAtPath(payload, "error") {
		return CindyBalanceSignalHTTP429
	}

	eventType := gjson.GetBytes(payload, "type")
	if eventType.Type == gjson.String &&
		(eventType.Str == "response.failed" || eventType.Str == "error") &&
		statusCode != http.StatusOK {
		return CindyBalanceSignalNone
	}

	switch {
	case eventType.Type == gjson.String && eventType.Str == "response.failed" &&
		cindyBudgetErrorAtPath(payload, "response.error"):
		return CindyBalanceSignalResponseFailed
	case eventType.Type == gjson.String && eventType.Str == "error" &&
		cindyBudgetErrorAtPath(payload, "error"):
		return CindyBalanceSignalErrorEvent
	default:
		return CindyBalanceSignalNone
	}
}

func cindyBudgetErrorAtPath(payload []byte, path string) bool {
	errorType := gjson.GetBytes(payload, path+".type")
	errorCode := gjson.GetBytes(payload, path+".code")
	return errorType.Type == gjson.String && errorType.Str == "budget_exceeded" &&
		errorCode.Type == gjson.String && errorCode.Str == strconv.Itoa(http.StatusTooManyRequests)
}

func cindyBalanceReplayBufferEnabled(account *Account) bool {
	return CindyBalanceDetectionFeatureEnabled() && account != nil &&
		IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials)
}

// IsAmbiguousCindyBalanceTerminalEvent reports an in-band Cindy terminal error
// whose structured type/code pair is incomplete. Complete non-budget pairs are
// treated as conclusive other errors and never trigger a paid recheck.
func IsAmbiguousCindyBalanceTerminalEvent(account *Account, payload []byte) bool {
	if !CindyBalanceDetectionFeatureEnabled() || account == nil || !IsCindyAPIKeyAccount(account.Platform, account.Type, account.Credentials) ||
		!gjson.ValidBytes(payload) ||
		ClassifyCindyBalanceInsufficient(account, http.StatusOK, payload) != CindyBalanceSignalNone {
		return false
	}
	eventType := gjson.GetBytes(payload, "type")
	if eventType.Type != gjson.String {
		return false
	}
	path := ""
	switch eventType.Str {
	case "response.failed":
		path = "response.error"
	case "error":
		path = "error"
	default:
		return false
	}
	errorType := gjson.GetBytes(payload, path+".type")
	errorCode := gjson.GetBytes(payload, path+".code")
	return errorType.Type != gjson.String || strings.TrimSpace(errorType.Str) == "" ||
		errorCode.Type != gjson.String || strings.TrimSpace(errorCode.Str) == ""
}

func CindyInsufficientAccountFingerprint(accountIDs []int64) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("cindy-insufficient:v1\n"))
	for _, accountID := range accountIDs {
		_, _ = hash.Write([]byte(strconv.FormatInt(accountID, 10)))
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func NormalizeCindyInsufficientFingerprint(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
