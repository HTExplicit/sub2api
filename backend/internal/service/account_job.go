package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	AccountJobKindImportData             = "account_import"
	AccountJobKindImportCodex            = "account_import_codex"
	AccountJobKindBatchCreate            = "account_batch_create"
	AccountJobKindBulkUpdate             = "account_bulk_update"
	AccountJobKindBulkTaxonomy           = "account_bulk_taxonomy"
	AccountJobKindBatchDelete            = "account_batch_delete"
	AccountJobKindBatchClearError        = "account_batch_clear_error"
	AccountJobKindBatchRefresh           = "account_batch_refresh"
	AccountJobKindBatchRefreshTier       = "account_batch_refresh_tier"
	AccountJobKindBatchUpdateCredentials = "account_batch_update_credentials"

	AccountJobStatusPending            = "pending"
	AccountJobStatusRunning            = "running"
	AccountJobStatusSucceeded          = "succeeded"
	AccountJobStatusPartiallySucceeded = "partially_succeeded"
	AccountJobStatusFailed             = "failed"
	AccountJobStatusCanceled           = "canceled"

	AccountJobItemStatusPending   = "pending"
	AccountJobItemStatusRunning   = "running"
	AccountJobItemStatusSucceeded = "succeeded"
	AccountJobItemStatusFailed    = "failed"
	AccountJobItemStatusCanceled  = "canceled"

	AccountJobBatchSize   = 100
	AccountJobPayloadTTL  = 24 * time.Hour
	AccountJobResultTTL   = 24 * time.Hour
	accountJobWorkerCount = 2
)

var (
	ErrAccountJobNotFound            = errors.New("account job not found")
	ErrAccountJobIdempotencyRequired = errors.New("Idempotency-Key is required")
	ErrAccountJobIdempotencyConflict = errors.New("Idempotency-Key was reused with a different request")
	ErrAccountJobBatchTooLarge       = errors.New("account job contains more than 100 items")
	ErrAccountJobPayloadExpired      = errors.New("account job payload expired")
	ErrAccountJobInvalidMetadata     = errors.New("account job metadata contains credential material")
	ErrAccountJobNotRetryable        = errors.New("account job has no failed items to retry")
)

type AccountJob struct {
	ID                     int64           `json:"id"`
	CreatedBy              int64           `json:"created_by"`
	Kind                   string          `json:"kind"`
	IdempotencyKey         string          `json:"-"`
	RequestHash            string          `json:"-"`
	Status                 string          `json:"status"`
	Metadata               json.RawMessage `json:"metadata"`
	TargetCount            int             `json:"target_count"`
	ProcessedCount         int             `json:"processed_count"`
	SucceededCount         int             `json:"succeeded_count"`
	FailedCount            int             `json:"failed_count"`
	CanceledCount          int             `json:"canceled_count"`
	CancelRequestedAt      *time.Time      `json:"cancel_requested_at,omitempty"`
	ErrorCode              string          `json:"error_code,omitempty"`
	ErrorMessage           string          `json:"error_message,omitempty"`
	RetryOfJobID           *int64          `json:"retry_of_job_id,omitempty"`
	Attempt                int             `json:"attempt"`
	StartedAt              *time.Time      `json:"started_at,omitempty"`
	FinishedAt             *time.Time      `json:"finished_at,omitempty"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
	RetryEligible          bool            `json:"retry_eligible"`
	RetryUnavailableReason string          `json:"retry_unavailable_reason,omitempty"`
}

type AccountJobItem struct {
	ID              int64           `json:"id"`
	JobID           int64           `json:"job_id"`
	Ordinal         int             `json:"ordinal"`
	Action          string          `json:"action,omitempty"`
	TargetAccountID *int64          `json:"target_account_id,omitempty"`
	Status          string          `json:"status"`
	Metadata        json.RawMessage `json:"metadata"`
	ErrorCode       string          `json:"error_code,omitempty"`
	ErrorMessage    string          `json:"error_message,omitempty"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type AccountJobItemSeed struct {
	Ordinal         int
	Action          string
	TargetAccountID *int64
	Metadata        json.RawMessage
}

type CreateAccountJobParams struct {
	CreatedBy      int64
	Kind           string
	IdempotencyKey string
	RequestHash    string
	PayloadCipher  string
	PayloadExpires time.Time
	Metadata       json.RawMessage
	Items          []AccountJobItemSeed
	RetryOfJobID   *int64
	Attempt        int
}

type AccountJobList struct {
	Items []AccountJob `json:"items"`
	Total int64        `json:"total"`
	Page  int          `json:"page"`
	Size  int          `json:"page_size"`
}

type AccountJobItemList struct {
	Items []AccountJobItem `json:"items"`
	Total int64            `json:"total"`
	Page  int              `json:"page"`
	Size  int              `json:"page_size"`
}

type AccountJobExecutionResult struct {
	ItemID       int64
	Status       string
	Metadata     json.RawMessage
	ErrorCode    string
	ErrorMessage string
}

type AccountJobRepository interface {
	ResultAccountIDs(context.Context, int64) ([]int64, error)
	Create(context.Context, CreateAccountJobParams) (*AccountJob, bool, error)
	FindIdempotent(context.Context, int64, string, string) (*AccountJob, error)
	Get(context.Context, int64) (*AccountJob, error)
	List(context.Context, int64, string, string, int, int) (*AccountJobList, error)
	ListItems(context.Context, int64, string, int, int) (*AccountJobItemList, error)
	MarkInterrupted(context.Context) error
	Claim(context.Context) (*AccountJob, error)
	Payload(context.Context, int64) (string, time.Time, error)
	ReservePendingItems(context.Context, int64, int) ([]AccountJobItem, error)
	CancelRequested(context.Context, int64) (bool, error)
	CompleteItems(context.Context, int64, []AccountJobExecutionResult) error
	Finish(context.Context, int64, string, string) (*AccountJob, error)
	Cancel(context.Context, int64, int64) (*AccountJob, error)
	FailedItemSeeds(context.Context, int64, int64) (*AccountJob, []AccountJobItemSeed, string, time.Time, error)
	ExpirePayloads(context.Context, time.Time) error
	Prune(context.Context, time.Time) error
}

type AccountJobExecutor interface {
	ExecuteAccountJob(context.Context, *AccountJob, json.RawMessage, []AccountJobItem) ([]AccountJobExecutionResult, error)
}

// AccountJobPreparingExecutor builds request-scoped state once for a claimed
// job. The returned context is reused for every item and discarded when the
// job finishes, is canceled, or the process stops.
type AccountJobPreparingExecutor interface {
	PrepareAccountJob(context.Context, *AccountJob, json.RawMessage) (context.Context, func(), error)
}

type AccountJobService struct {
	repo      AccountJobRepository
	encryptor SecretEncryptor
}

func NewAccountJobService(repo AccountJobRepository, encryptor SecretEncryptor) *AccountJobService {
	return &AccountJobService{repo: repo, encryptor: encryptor}
}

func (s *AccountJobService) Submit(ctx context.Context, createdBy int64, kind, idempotencyKey string, payload, metadata json.RawMessage, items []AccountJobItemSeed) (*AccountJob, bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	kind = strings.TrimSpace(kind)
	if idempotencyKey == "" || len(idempotencyKey) > 255 {
		return nil, false, ErrAccountJobIdempotencyRequired
	}
	if createdBy <= 0 || !validAccountJobKind(kind) || !json.Valid(payload) || len(items) == 0 {
		return nil, false, errors.New("invalid account job submission")
	}
	if err := ValidateAccountJobMetadata(metadata); err != nil {
		return nil, false, err
	}
	if _, err := AccountJobPluginExecution(metadata); err != nil {
		return nil, false, err
	}
	if err := validateRecordedAccountJobPayload(metadata, payload); err != nil {
		return nil, false, err
	}

	for index := range items {
		if items[index].Ordinal <= 0 {
			items[index].Ordinal = index + 1
		}
		if err := ValidateAccountJobMetadata(items[index].Metadata); err != nil {
			return nil, false, err
		}
		items[index].Metadata = normalizeAccountJobMetadata(items[index].Metadata)
	}
	hash := sha256.Sum256(payload)
	requestHash := hex.EncodeToString(hash[:])
	existing, err := s.findMatchingSubmission(ctx, createdBy, kind, idempotencyKey, requestHash, metadata)
	if err == nil {
		return existing, true, nil
	}
	if !errors.Is(err, ErrAccountJobNotFound) {
		return nil, false, err
	}

	ciphertext, err := s.encryptor.Encrypt(string(payload))
	if err != nil {
		return nil, false, err
	}
	return s.repo.Create(ctx, CreateAccountJobParams{
		CreatedBy: createdBy, Kind: kind, IdempotencyKey: idempotencyKey,
		RequestHash: requestHash, PayloadCipher: ciphertext,
		PayloadExpires: time.Now().UTC().Add(AccountJobPayloadTTL),
		Metadata:       normalizeAccountJobMetadata(metadata), Items: items, Attempt: 1,
	})
}

// ReplaySubmission checks a previously submitted request before a caller resolves
// mutable targets. It never creates a job, touches its payload lifetime, or binds
// a plugin: a plugin owner must already be authorized in the caller's context.
func (s *AccountJobService) ReplaySubmission(ctx context.Context, createdBy int64, kind, idempotencyKey string, payload json.RawMessage) (*AccountJob, bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	kind = strings.TrimSpace(kind)

	if idempotencyKey == "" || len(idempotencyKey) > 255 {
		return nil, false, ErrAccountJobIdempotencyRequired
	}
	if createdBy <= 0 || !validAccountJobKind(kind) || !json.Valid(payload) {
		return nil, false, errors.New("invalid account job submission")
	}
	var metadata json.RawMessage
	if err := validateRecordedAccountJobPayload(metadata, payload); err != nil {
		return nil, false, err
	}

	hash := sha256.Sum256(payload)
	existing, err := s.findMatchingSubmission(ctx, createdBy, kind, idempotencyKey, hex.EncodeToString(hash[:]), metadata)
	if errors.Is(err, ErrAccountJobNotFound) {
		return nil, false, nil
	}
	return existing, err == nil, err
}

func (s *AccountJobService) findMatchingSubmission(ctx context.Context, createdBy int64, kind, idempotencyKey, requestHash string, metadata json.RawMessage) (*AccountJob, error) {
	existing, err := s.repo.FindIdempotent(ctx, createdBy, kind, idempotencyKey)
	if err != nil {
		return nil, err
	}
	oldOwner, _ := AccountJobPluginExecution(existing.Metadata)
	newOwner, _ := AccountJobPluginExecution(metadata)
	if existing.RequestHash != requestHash || oldOwner.ID != newOwner.ID || !AccountJobViewIdentityEqual(existing.Metadata, metadata) {
		return nil, ErrAccountJobIdempotencyConflict
	}
	return existing, nil
}

func (s *AccountJobService) Get(ctx context.Context, jobID int64) (*AccountJob, error) {
	job, err := s.repo.Get(ctx, jobID)
	if err == nil {
		s.decorateRetryEligibility(ctx, job)
	}
	return job, err
}

func (s *AccountJobService) List(ctx context.Context, createdBy int64, kind, status string, page, pageSize int) (*AccountJobList, error) {
	list, err := s.repo.List(ctx, createdBy, kind, status, page, pageSize)
	if err == nil && list != nil {
		for i := range list.Items {
			s.decorateRetryEligibility(ctx, &list.Items[i])
		}
	}
	return list, err
}

func AccountJobHasRetryableFailures(job *AccountJob) bool {
	return job != nil && job.FailedCount > 0 && (job.Status == AccountJobStatusFailed || job.Status == AccountJobStatusPartiallySucceeded || job.Status == AccountJobStatusCanceled)
}

func (s *AccountJobService) decorateRetryEligibility(ctx context.Context, job *AccountJob) {
	if job == nil {
		return
	}
	job.RetryEligible = false
	if !AccountJobHasRetryableFailures(job) {
		return
	}
	if !validAccountJobKind(job.Kind) {
		// A retired kind (route acquisition, renewal stop, legacy extension
		// operation) still lists, but it has no executor to retry with.
		job.RetryUnavailableReason = "kind_unsupported"
		return
	}
	cipher, expires, err := s.repo.Payload(ctx, job.ID)
	if err != nil || cipher == "" || !time.Now().UTC().Before(expires) {
		job.RetryUnavailableReason = "payload_expired"
		return
	}
	job.RetryEligible = true
	job.RetryUnavailableReason = ""
}

// Stop and internal failure take precedence over progress counters. A monitor
// failure after all item writes must never turn the overall task green.
func AccountJobTerminalStatus(cancelRequested bool, errorCode string, succeeded, failed, canceled int) string {
	if cancelRequested {
		return AccountJobStatusCanceled
	}
	if errorCode != "" {
		return AccountJobStatusFailed
	}
	if canceled > 0 {
		return AccountJobStatusCanceled
	}
	if failed == 0 {
		return AccountJobStatusSucceeded
	}
	if succeeded > 0 {
		return AccountJobStatusPartiallySucceeded
	}
	return AccountJobStatusFailed
}

func (s *AccountJobService) ListItems(ctx context.Context, jobID int64, status string, page, pageSize int) (*AccountJobItemList, error) {
	return s.repo.ListItems(ctx, jobID, status, page, pageSize)
}

func (s *AccountJobService) ResultAccountIDs(ctx context.Context, jobID int64) ([]int64, error) {
	if _, err := s.repo.Get(ctx, jobID); err != nil {
		return nil, err
	}
	return s.repo.ResultAccountIDs(ctx, jobID)
}

func (s *AccountJobService) Cancel(ctx context.Context, jobID, createdBy int64) (*AccountJob, error) {
	job, err := s.repo.Cancel(ctx, jobID, createdBy)
	if err == nil {
		s.decorateRetryEligibility(ctx, job)
	}
	return job, err
}

func (s *AccountJobService) RetryFailed(ctx context.Context, jobID, createdBy int64, idempotencyKey string) (*AccountJob, bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" || len(idempotencyKey) > 255 {
		return nil, false, ErrAccountJobIdempotencyRequired
	}
	old, seeds, cipher, expires, err := s.repo.FailedItemSeeds(ctx, jobID, createdBy)
	if err != nil {
		return nil, false, err
	}
	if old == nil || len(seeds) == 0 || !validAccountJobKind(old.Kind) {
		return nil, false, ErrAccountJobNotRetryable
	}

	if cipher == "" || time.Now().UTC().After(expires) {
		return nil, false, ErrAccountJobPayloadExpired
	}
	plaintext, err := s.encryptor.Decrypt(cipher)
	if err != nil {
		return nil, false, ErrAccountJobPayloadExpired
	}
	ctx, releaseView, err := bindRecordedAccountJobView(ctx, old.Metadata, json.RawMessage(plaintext), true)
	if err != nil {
		return nil, false, err
	}
	defer releaseView()
	hashInput, _ := json.Marshal(struct {
		RetryOfJobID int64 `json:"retry_of_job_id"`
	}{RetryOfJobID: old.ID})
	hash := sha256.Sum256(hashInput)
	requestHash := hex.EncodeToString(hash[:])
	if existing, findErr := s.repo.FindIdempotent(ctx, createdBy, old.Kind, idempotencyKey); findErr == nil {
		oldOwner, _ := AccountJobPluginExecution(old.Metadata)
		retryOwner, _ := AccountJobPluginExecution(existing.Metadata)
		if existing.RequestHash != requestHash || oldOwner.ID != retryOwner.ID || !AccountJobViewIdentityEqual(old.Metadata, existing.Metadata) {
			return nil, false, ErrAccountJobIdempotencyConflict
		}
		return existing, true, nil
	} else if !errors.Is(findErr, ErrAccountJobNotFound) {
		return nil, false, findErr
	}
	fields := map[string]json.RawMessage{}
	if len(old.Metadata) > 0 && json.Unmarshal(old.Metadata, &fields) != nil {
		return nil, false, ErrAccountJobInvalidMetadata
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	fields["retry_of_job_id"], _ = json.Marshal(old.ID)
	fields["failed_item_count"], _ = json.Marshal(len(seeds))
	fields["target_count"], _ = json.Marshal(len(seeds))
	// The retired source identity remains historical evidence. Retry preserves
	// the original encrypted request, expiry and failed target/action records.
	metadata, _ := json.Marshal(fields)

	return s.repo.Create(ctx, CreateAccountJobParams{
		CreatedBy: createdBy, Kind: old.Kind, IdempotencyKey: idempotencyKey,
		RequestHash: requestHash, PayloadCipher: cipher, PayloadExpires: expires,
		Metadata: metadata, Items: seeds, RetryOfJobID: &old.ID, Attempt: old.Attempt + 1,
	})
}

type AccountJobRuntime struct {
	jobs     *AccountJobService
	executor AccountJobExecutor
	ctx      context.Context
	cancel   context.CancelFunc
	stopOnce sync.Once
	wg       sync.WaitGroup

	finishRetryDelay time.Duration
	unfinishedMu     sync.Mutex
	unfinished       map[int64]accountJobFinishResult
}

// accountJobFinishResult is a job result whose Finish write failed; the
// cleanup loop keeps writing it while the runtime runs.
type accountJobFinishResult struct {
	code    string
	message string
}

func NewAccountJobRuntime(jobs *AccountJobService, executor AccountJobExecutor) *AccountJobRuntime {
	return &AccountJobRuntime{jobs: jobs, executor: executor, finishRetryDelay: 500 * time.Millisecond}
}

func (r *AccountJobRuntime) Start(parent context.Context) error {
	if r == nil || r.jobs == nil || r.jobs.repo == nil {
		return errors.New("account job runtime is unavailable")
	}
	if err := r.jobs.repo.MarkInterrupted(parent); err != nil {
		return err
	}
	r.ctx, r.cancel = context.WithCancel(context.WithoutCancel(parent))
	for range accountJobWorkerCount {
		r.wg.Add(1)
		go r.worker()
	}
	r.wg.Add(1)
	go r.cleanupWorker()
	return nil
}

func (r *AccountJobRuntime) Stop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() {
		if r.cancel != nil {
			r.cancel()
		}
	})
	r.wg.Wait()
}

func (r *AccountJobRuntime) worker() {
	defer r.wg.Done()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if r.runOne() {
			continue
		}
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *AccountJobRuntime) runOne() bool {
	job, err := r.jobs.repo.Claim(r.ctx)
	if err != nil || job == nil {
		return false
	}
	code, message := r.execute(job)
	r.finish(job.ID, code, message)
	return true
}

const (
	accountJobFinishAttempts      = 3
	accountJobFallbackMessageRune = 500
)

// finish stores the job result. A failed write is logged and retried with a
// shortened storable copy of the message, so the text of a result can never
// keep a job running. A result that still cannot be written is retried by the
// cleanup loop while the runtime runs; after a restart the job is marked
// interrupted.
func (r *AccountJobRuntime) finish(jobID int64, code, message string) {
	delay := r.finishRetryDelay
	for attempt := 1; ; attempt++ {
		if r.tryFinish(jobID, code, message, attempt) {
			return
		}
		message = accountJobFallbackMessage(message)
		if attempt >= accountJobFinishAttempts || r.ctx.Err() != nil {
			r.unfinishedMu.Lock()
			if r.unfinished == nil {
				r.unfinished = make(map[int64]accountJobFinishResult)
			}
			r.unfinished[jobID] = accountJobFinishResult{code: code, message: message}
			r.unfinishedMu.Unlock()
			return
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-r.ctx.Done():
			timer.Stop()
		}
		delay *= 2
	}
}

func (r *AccountJobRuntime) tryFinish(jobID int64, code, message string, attempt int) bool {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), 10*time.Second)
	defer cancel()
	_, err := r.jobs.repo.Finish(ctx, jobID, code, message)
	if err == nil || errors.Is(err, ErrAccountJobNotFound) {
		return true
	}
	slog.Error("account_job.finish_failed", "job_id", jobID, "attempt", attempt, "error_code", code, "error", err.Error())
	return false
}

// retryUnfinished writes the results whose Finish failed earlier.
func (r *AccountJobRuntime) retryUnfinished() {
	r.unfinishedMu.Lock()
	pending := make(map[int64]accountJobFinishResult, len(r.unfinished))
	for jobID, result := range r.unfinished {
		pending[jobID] = result
	}
	r.unfinishedMu.Unlock()
	for jobID, result := range pending {
		if r.tryFinish(jobID, result.code, result.message, 0) {
			r.unfinishedMu.Lock()
			delete(r.unfinished, jobID)
			r.unfinishedMu.Unlock()
		}
	}
}

// accountJobFallbackMessage keeps the start of a message in a form that any
// error_message column accepts.
func accountJobFallbackMessage(message string) string {
	message = StorableAccountJobText(message)
	if utf8.RuneCountInString(message) <= accountJobFallbackMessageRune {
		return message
	}
	runes := []rune(message)
	return string(runes[:accountJobFallbackMessageRune]) + "..."
}

func (r *AccountJobRuntime) execute(job *AccountJob) (string, string) {
	ciphertext, expires, err := r.jobs.repo.Payload(r.ctx, job.ID)
	switch {
	case err != nil:
		return accountJobRuntimeFailure(AccountJobCodePayloadExpired, err)
	case ciphertext == "":
		return accountJobRuntimeFailure(AccountJobCodePayloadExpired, nil)
	case time.Now().UTC().After(expires):
		return accountJobRuntimeFailure(AccountJobCodePayloadExpired, fmt.Errorf("account job payload expired at %s", expires.UTC().Format(time.RFC3339)))
	}
	plaintext, err := r.jobs.encryptor.Decrypt(ciphertext)
	if err == nil && !json.Valid([]byte(plaintext)) {
		err = errors.New("decrypted account job payload is not valid JSON")
	}
	if err != nil {
		return accountJobRuntimeFailure(AccountJobCodePayloadUnavailable, err)
	}
	payload := json.RawMessage(plaintext)
	canceled, cancelErr := r.jobs.repo.CancelRequested(r.ctx, job.ID)
	if cancelErr != nil {
		return accountJobRuntimeFailure(AccountJobCodeCancelCheckFailed, cancelErr)
	}
	if canceled {
		return "", ""
	}
	executionCtx := r.ctx
	cleanup := func() {}
	if preparer, ok := r.executor.(AccountJobPreparingExecutor); ok {
		preparedCtx, preparedCleanup, prepareErr := preparer.PrepareAccountJob(r.ctx, job, payload)
		if prepareErr != nil {
			return accountJobRuntimeFailure("preparation_failed", prepareErr)
		}
		if preparedCtx != nil {
			executionCtx = preparedCtx
		}
		if preparedCleanup != nil {
			cleanup = preparedCleanup
		}
	}
	defer cleanup()

	for {
		if executionCtx.Err() != nil {
			return "", ""
		}
		canceled, cancelErr := r.jobs.repo.CancelRequested(r.ctx, job.ID)
		if cancelErr != nil {
			return accountJobRuntimeFailure(AccountJobCodeCancelCheckFailed, cancelErr)
		}
		if canceled {
			return "", ""
		}
		items, reserveErr := r.jobs.repo.ReservePendingItems(r.ctx, job.ID, AccountJobBatchSize)
		if reserveErr != nil {
			return accountJobRuntimeFailure(AccountJobCodeReservationFailed, reserveErr)
		}
		if len(items) == 0 {
			return "", ""
		}
		for index, item := range items {
			if executionCtx.Err() != nil {
				return "", ""
			}
			canceled, cancelErr = r.jobs.repo.CancelRequested(r.ctx, job.ID)
			if cancelErr != nil {
				return accountJobRuntimeFailure(AccountJobCodeCancelCheckFailed, cancelErr)
			}
			if canceled {
				remaining := make([]AccountJobExecutionResult, 0, len(items)-index)
				for _, pending := range items[index:] {
					remaining = append(remaining, AccountJobExecutionResult{ItemID: pending.ID, Status: AccountJobItemStatusCanceled})
				}
				_ = r.jobs.repo.CompleteItems(r.ctx, job.ID, remaining)
				return "", ""
			}
			result := r.executeItem(executionCtx, job, payload, item)
			if completeErr := r.jobs.repo.CompleteItems(r.ctx, job.ID, []AccountJobExecutionResult{result}); completeErr != nil {
				return accountJobRuntimeFailure(AccountJobCodeCompletionFailed, completeErr)
			}
		}
	}
}

func (r *AccountJobRuntime) cleanupWorker() {
	defer r.wg.Done()
	r.cleanup(time.Now().UTC())
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case now := <-ticker.C:
			r.cleanup(now.UTC())
		}
	}
}

func (r *AccountJobRuntime) cleanup(now time.Time) {
	r.retryUnfinished()
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	_ = r.jobs.repo.ExpirePayloads(ctx, now)
	_ = r.jobs.repo.Prune(ctx, now.Add(-AccountJobResultTTL))
}

// executeItem stores what the executor reported. A failed item keeps its code
// and verbatim error text (the catalog sentence only fills a missing message),
// metadata is stored as returned, and a reported status is never flipped.
func (r *AccountJobRuntime) executeItem(ctx context.Context, job *AccountJob, payload json.RawMessage, item AccountJobItem) AccountJobExecutionResult {
	result := AccountJobExecutionResult{ItemID: item.ID, Status: AccountJobItemStatusFailed, ErrorCode: AccountJobCodeExecutionFailed}
	if r.executor == nil {
		result.ErrorMessage = "account job executor is unavailable"
	} else {
		results, err := r.executor.ExecuteAccountJob(ctx, job, payload, []AccountJobItem{item})
		switch {
		case err != nil:
			result.ErrorMessage = err.Error()
		case len(results) != 1 || results[0].ItemID != item.ID:
			result.ErrorMessage = fmt.Sprintf("account job executor returned %d results for item %d", len(results), item.ID)
		default:
			result = results[0]
		}
	}
	result.Metadata = StorableAccountJobMetadata(result.Metadata)
	switch result.Status {
	case AccountJobItemStatusSucceeded, AccountJobItemStatusCanceled:
	case AccountJobItemStatusFailed:
		result.ErrorCode, result.ErrorMessage = AccountJobFailure(result.ErrorCode, result.ErrorMessage)
	default:
		if strings.TrimSpace(result.ErrorMessage) == "" {
			result.ErrorMessage = fmt.Sprintf("account job executor returned item status %q", result.Status)
		}
		result.Status = AccountJobItemStatusFailed
		result.ErrorCode, result.ErrorMessage = AccountJobFailure(result.ErrorCode, result.ErrorMessage)
	}
	return result
}

// StorableAccountJobText returns text that a PostgreSQL text column accepts:
// invalid UTF-8 and NUL characters become U+FFFD, everything else is kept.
func StorableAccountJobText(text string) string {
	if utf8.ValidString(text) && !strings.ContainsRune(text, 0) {
		return text
	}
	return strings.ReplaceAll(strings.ToValidUTF8(text, "\uFFFD"), "\x00", "\uFFFD")
}

// StorableAccountJobMetadata keeps executor metadata and returns it in a form
// the jsonb columns accept. Values stay as reported; only invalid UTF-8, NUL
// characters (jsonb rejects \u0000) and unpaired surrogates become U+FFFD.
// Bytes that are not JSON are kept as text under metadata_raw, and a JSON
// value that is not an object under metadata_value.
func StorableAccountJobMetadata(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return raw
	}
	if !json.Valid(trimmed) {
		wrapped, _ := json.Marshal(map[string]string{"metadata_raw": StorableAccountJobText(string(raw))})
		return wrapped
	}
	if trimmed[0] == '{' && utf8.Valid(trimmed) && bytes.IndexByte(trimmed, 0) < 0 && !bytes.Contains(trimmed, []byte(`\u`)) {
		return raw
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		wrapped, _ := json.Marshal(map[string]string{"metadata_raw": StorableAccountJobText(string(raw))})
		return wrapped
	}
	value = storableAccountJobValue(value)
	if _, ok := value.(map[string]any); !ok {
		value = map[string]any{"metadata_value": value}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		wrapped, _ := json.Marshal(map[string]string{"metadata_raw": StorableAccountJobText(string(raw))})
		return wrapped
	}
	return encoded
}

func storableAccountJobValue(value any) any {
	switch typed := value.(type) {
	case string:
		return StorableAccountJobText(typed)
	case []any:
		for index := range typed {
			typed[index] = storableAccountJobValue(typed[index])
		}
		return typed
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[StorableAccountJobText(key)] = storableAccountJobValue(item)
		}
		return out
	default:
		return value
	}
}

// accountJobRuntimeFailure keeps the underlying error text of a job-level
// failure; the catalog sentence is used only when there is none.
func accountJobRuntimeFailure(code string, err error) (string, string) {
	message := ""
	if err != nil {
		message = err.Error()
	}
	return AccountJobFailure(code, message)
}

func validAccountJobKind(kind string) bool {
	switch kind {
	case AccountJobKindImportData, AccountJobKindImportCodex, AccountJobKindBatchCreate,
		AccountJobKindBulkUpdate, AccountJobKindBulkTaxonomy, AccountJobKindBatchDelete,
		AccountJobKindBatchClearError, AccountJobKindBatchRefresh, AccountJobKindBatchRefreshTier,
		AccountJobKindBatchUpdateCredentials:
		return true
	default:
		return false
	}
}

func ValidateAccountJobMetadata(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	if accountJobMetadataContainsSecret(value) {
		return ErrAccountJobInvalidMetadata
	}
	return nil
}

func accountJobMetadataContainsSecret(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"), " ", "_"))
			for _, forbidden := range []string{"api_key", "apikey", "access_token", "refresh_token", "id_token", "password", "secret", "cookie", "authorization", "credential", "credentials"} {
				if normalized == forbidden || strings.HasSuffix(normalized, "_"+forbidden) {
					return true
				}
			}
			if accountJobMetadataContainsSecret(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if accountJobMetadataContainsSecret(child) {
				return true
			}
		}
	}
	return false
}

func normalizeAccountJobMetadata(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`{}`)
	}
	return append(json.RawMessage(nil), raw...)
}

// NormalizeAccountJobFailure keeps code and supplies the catalog sentence for
// a failure without underlying error text; see AccountJobFailure.
func NormalizeAccountJobFailure(code string) (string, string) {
	return NormalizeAccountBusinessFailure(code)
}
