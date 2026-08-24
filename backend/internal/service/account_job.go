package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
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
	AccountJobKindDuplicateReview        = "account_duplicate_review"
	AccountJobKindDuplicateMerge         = "account_duplicate_merge"
	AccountJobKindCindyConfirmedCleanup  = "cindy_confirmed_cleanup"
	AccountJobKindCindyBannedCleanup     = "cindy_banned_cleanup"

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
	AccountJobResultTTL   = 30 * 24 * time.Hour
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
	ID                int64           `json:"id"`
	CreatedBy         int64           `json:"created_by"`
	Kind              string          `json:"kind"`
	IdempotencyKey    string          `json:"-"`
	RequestHash       string          `json:"-"`
	Status            string          `json:"status"`
	Metadata          json.RawMessage `json:"metadata"`
	TargetCount       int             `json:"target_count"`
	ProcessedCount    int             `json:"processed_count"`
	SucceededCount    int             `json:"succeeded_count"`
	FailedCount       int             `json:"failed_count"`
	CanceledCount     int             `json:"canceled_count"`
	CancelRequestedAt *time.Time      `json:"cancel_requested_at,omitempty"`
	ErrorCode         string          `json:"error_code,omitempty"`
	ErrorMessage      string          `json:"error_message,omitempty"`
	RetryOfJobID      *int64          `json:"retry_of_job_id,omitempty"`
	Attempt           int             `json:"attempt"`
	StartedAt         *time.Time      `json:"started_at,omitempty"`
	FinishedAt        *time.Time      `json:"finished_at,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
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

// AccountJobCindyMutationRunner supplies the one ordinary PostgreSQL
// transaction used by strict Cindy job items. The callback performs the
// existing account/group/taxonomy mutation through a transaction-aware context;
// the runner then binds credential generation and resets Cindy health before
// committing.
type AccountJobCindyMutationRunner interface {
	Run(context.Context, int64, func(context.Context) (*Account, error)) (*Account, error)
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
	existing, err := s.repo.FindIdempotent(ctx, createdBy, kind, idempotencyKey)
	if err == nil {
		if existing.RequestHash != requestHash {
			return nil, false, ErrAccountJobIdempotencyConflict
		}
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

func (s *AccountJobService) Get(ctx context.Context, jobID int64) (*AccountJob, error) {
	return s.repo.Get(ctx, jobID)
}

func (s *AccountJobService) List(ctx context.Context, createdBy int64, kind, status string, page, pageSize int) (*AccountJobList, error) {
	return s.repo.List(ctx, createdBy, kind, status, page, pageSize)
}

func (s *AccountJobService) ListItems(ctx context.Context, jobID int64, status string, page, pageSize int) (*AccountJobItemList, error) {
	return s.repo.ListItems(ctx, jobID, status, page, pageSize)
}

func (s *AccountJobService) Cancel(ctx context.Context, jobID, createdBy int64) (*AccountJob, error) {
	return s.repo.Cancel(ctx, jobID, createdBy)
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
	if old == nil || len(seeds) == 0 {
		return nil, false, ErrAccountJobNotRetryable
	}
	if cipher == "" || time.Now().UTC().After(expires) {
		return nil, false, ErrAccountJobPayloadExpired
	}
	if _, err = s.encryptor.Decrypt(cipher); err != nil {
		return nil, false, ErrAccountJobPayloadExpired
	}
	hashInput, _ := json.Marshal(struct {
		RetryOfJobID int64 `json:"retry_of_job_id"`
	}{RetryOfJobID: old.ID})
	hash := sha256.Sum256(hashInput)
	requestHash := hex.EncodeToString(hash[:])
	if existing, findErr := s.repo.FindIdempotent(ctx, createdBy, old.Kind, idempotencyKey); findErr == nil {
		if existing.RequestHash != requestHash {
			return nil, false, ErrAccountJobIdempotencyConflict
		}
		return existing, true, nil
	} else if !errors.Is(findErr, ErrAccountJobNotFound) {
		return nil, false, findErr
	}
	metadata, _ := json.Marshal(map[string]any{"retry_of_job_id": old.ID, "failed_item_count": len(seeds)})
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
}

func NewAccountJobRuntime(jobs *AccountJobService, executor AccountJobExecutor) *AccountJobRuntime {
	return &AccountJobRuntime{jobs: jobs, executor: executor}
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
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), 10*time.Second)
	defer cancel()
	_, _ = r.jobs.repo.Finish(finishCtx, job.ID, code, message)
	return true
}

func (r *AccountJobRuntime) execute(job *AccountJob) (string, string) {
	ciphertext, expires, err := r.jobs.repo.Payload(r.ctx, job.ID)
	if err != nil || ciphertext == "" || time.Now().UTC().After(expires) {
		return normalizeAccountJobFailure("payload_expired")
	}
	plaintext, err := r.jobs.encryptor.Decrypt(ciphertext)
	if err != nil || !json.Valid([]byte(plaintext)) {
		return normalizeAccountJobFailure("payload_unavailable")
	}
	payload := json.RawMessage(plaintext)
	for {
		canceled, cancelErr := r.jobs.repo.CancelRequested(r.ctx, job.ID)
		if cancelErr != nil {
			return normalizeAccountJobFailure("cancel_check_failed")
		}
		if canceled {
			return "", ""
		}
		items, reserveErr := r.jobs.repo.ReservePendingItems(r.ctx, job.ID, AccountJobBatchSize)
		if reserveErr != nil {
			return normalizeAccountJobFailure("item_reservation_failed")
		}
		if len(items) == 0 {
			return "", ""
		}
		for index, item := range items {
			canceled, cancelErr = r.jobs.repo.CancelRequested(r.ctx, job.ID)
			if cancelErr != nil {
				return normalizeAccountJobFailure("cancel_check_failed")
			}
			if canceled {
				remaining := make([]AccountJobExecutionResult, 0, len(items)-index)
				for _, pending := range items[index:] {
					remaining = append(remaining, AccountJobExecutionResult{ItemID: pending.ID, Status: AccountJobItemStatusCanceled})
				}
				_ = r.jobs.repo.CompleteItems(r.ctx, job.ID, remaining)
				return "", ""
			}
			result := AccountJobExecutionResult{ItemID: item.ID, Status: AccountJobItemStatusFailed,
				ErrorCode: "execution_failed", ErrorMessage: "account job item failed"}
			if r.executor != nil {
				results, executeErr := r.executor.ExecuteAccountJob(r.ctx, job, payload, []AccountJobItem{item})
				if executeErr == nil && len(results) == 1 && results[0].ItemID == item.ID {
					result = results[0]
				}
			}
			if err := ValidateAccountJobMetadata(result.Metadata); err != nil {
				result = AccountJobExecutionResult{ItemID: item.ID, Status: AccountJobItemStatusFailed,
					ErrorCode: "result_redacted", ErrorMessage: "account job result was rejected"}
			}
			if result.Status == AccountJobItemStatusFailed {
				result.ErrorCode, result.ErrorMessage = normalizeAccountJobFailure(result.ErrorCode)
			} else {
				result.ErrorCode, result.ErrorMessage = "", ""
			}
			if completeErr := r.jobs.repo.CompleteItems(r.ctx, job.ID, []AccountJobExecutionResult{result}); completeErr != nil {
				return normalizeAccountJobFailure("item_completion_failed")
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
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	_ = r.jobs.repo.ExpirePayloads(ctx, now)
	_ = r.jobs.repo.Prune(ctx, now.Add(-AccountJobResultTTL))
}

func validAccountJobKind(kind string) bool {
	switch kind {
	case AccountJobKindImportData, AccountJobKindImportCodex, AccountJobKindBatchCreate,
		AccountJobKindBulkUpdate, AccountJobKindBulkTaxonomy, AccountJobKindBatchDelete,
		AccountJobKindBatchClearError, AccountJobKindBatchRefresh, AccountJobKindBatchRefreshTier,
		AccountJobKindBatchUpdateCredentials, AccountJobKindDuplicateReview,
		AccountJobKindDuplicateMerge, AccountJobKindCindyConfirmedCleanup, AccountJobKindCindyBannedCleanup:
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

func NormalizeAccountJobFailure(code string) (string, string) {
	messages := map[string]string{
		"payload_expired":                    "account job payload expired",
		"payload_unavailable":                "account job payload is unavailable",
		"cancel_check_failed":                "account job cancellation state is unavailable",
		"item_reservation_failed":            "account job items could not be reserved",
		"item_completion_failed":             "account job item result could not be persisted",
		"result_redacted":                    "account job result was rejected",
		"execution_failed":                   "account job item failed",
		"cindy_cleanup_target_changed":       "matching Cindy accounts changed; reload and confirm again",
		"cindy_cleanup_failed":               "Cindy account cleanup failed",
		"account_import_payload_invalid":     "account import item is invalid",
		"account_import_identity_conflict":   "account identity matches multiple existing accounts",
		"cindy_import_target_group_required": "one explicit target group is required for Cindy imports",
		"cindy_import_target_group_invalid":  "target group is not a strict Cindy group",
		"cindy_import_api_key_invalid":       "Cindy API key is required",
		"cindy_import_credential_conflict":   "credential is duplicated in the submitted import",
		"cindy_import_device_conflict":       "device identity belongs to another Cindy credential",
		"cindy_import_device_invalid":        "Cindy device identity is invalid",
		"account_import_execution_failed":    "account import item could not be applied",
	}
	code = strings.TrimSpace(code)
	if message, ok := messages[code]; ok {
		return code, message
	}
	return "execution_failed", messages["execution_failed"]
}

func normalizeAccountJobFailure(code string) (string, string) {
	return NormalizeAccountJobFailure(code)
}
