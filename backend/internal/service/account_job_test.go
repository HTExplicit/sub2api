//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type accountJobTestCipher struct{}

func (accountJobTestCipher) Encrypt(value string) (string, error) { return "cipher:" + value, nil }
func (accountJobTestCipher) Decrypt(value string) (string, error) {
	if len(value) < len("cipher:") || value[:len("cipher:")] != "cipher:" {
		return "", errors.New("invalid ciphertext")
	}
	return value[len("cipher:"):], nil
}

type accountJobTestRepo struct {
	mu sync.Mutex

	jobs        map[int64]*AccountJob
	items       map[int64][]AccountJobItem
	payloads    map[int64]accountJobTestPayload
	nextID      int64
	interrupted bool
	claimCalls  int
	claimLimit  int
}

type accountJobTestPayload struct {
	cipher    string
	expiresAt time.Time
}

func newAccountJobTestRepo() *accountJobTestRepo {
	return &accountJobTestRepo{
		jobs: make(map[int64]*AccountJob), items: make(map[int64][]AccountJobItem),
		payloads: make(map[int64]accountJobTestPayload), nextID: 1,
	}
}

func cloneAccountJob(job *AccountJob) *AccountJob {
	if job == nil {
		return nil
	}
	copy := *job
	return &copy
}

func (r *accountJobTestRepo) Create(_ context.Context, params CreateAccountJobParams) (*AccountJob, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.jobs {
		if existing.CreatedBy == params.CreatedBy && existing.Kind == params.Kind && existing.IdempotencyKey == params.IdempotencyKey {
			if existing.RequestHash != params.RequestHash {
				return nil, false, ErrAccountJobIdempotencyConflict
			}
			return cloneAccountJob(existing), true, nil
		}
	}
	id := r.nextID
	r.nextID++
	now := time.Now().UTC()
	job := &AccountJob{ID: id, CreatedBy: params.CreatedBy, Kind: params.Kind, IdempotencyKey: params.IdempotencyKey,
		RequestHash: params.RequestHash, Status: AccountJobStatusPending, Metadata: params.Metadata,
		TargetCount: len(params.Items), RetryOfJobID: params.RetryOfJobID, Attempt: params.Attempt,
		CreatedAt: now, UpdatedAt: now}
	r.jobs[id] = job
	r.payloads[id] = accountJobTestPayload{cipher: params.PayloadCipher, expiresAt: params.PayloadExpires}
	items := make([]AccountJobItem, len(params.Items))
	for index, seed := range params.Items {
		items[index] = AccountJobItem{ID: int64(index + 1), JobID: id, Ordinal: seed.Ordinal,
			Action: seed.Action, TargetAccountID: seed.TargetAccountID, Status: AccountJobItemStatusPending,
			Metadata: seed.Metadata}
	}
	r.items[id] = items
	return cloneAccountJob(job), false, nil
}

func (r *accountJobTestRepo) FindIdempotent(_ context.Context, createdBy int64, kind, key string) (*AccountJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, job := range r.jobs {
		if job.CreatedBy == createdBy && job.Kind == kind && job.IdempotencyKey == key {
			return cloneAccountJob(job), nil
		}
	}
	return nil, ErrAccountJobNotFound
}

func (r *accountJobTestRepo) Get(_ context.Context, id int64) (*AccountJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job := r.jobs[id]
	if job == nil {
		return nil, ErrAccountJobNotFound
	}
	return cloneAccountJob(job), nil
}

func (r *accountJobTestRepo) List(context.Context, int64, string, string, int, int) (*AccountJobList, error) {
	return &AccountJobList{}, nil
}

func (r *accountJobTestRepo) ListItems(context.Context, int64, string, int, int) (*AccountJobItemList, error) {
	return &AccountJobItemList{}, nil
}

func (r *accountJobTestRepo) MarkInterrupted(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.interrupted = true
	return nil
}

func (r *accountJobTestRepo) Claim(context.Context) (*AccountJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.claimCalls++
	return nil, nil
}

func (r *accountJobTestRepo) Payload(context.Context, int64) (string, time.Time, error) {
	return "", time.Time{}, ErrAccountJobNotFound
}

func (r *accountJobTestRepo) ReservePendingItems(context.Context, int64, int) ([]AccountJobItem, error) {
	return nil, nil
}

func (r *accountJobTestRepo) CancelRequested(context.Context, int64) (bool, error) { return false, nil }
func (r *accountJobTestRepo) CompleteItems(context.Context, int64, []AccountJobExecutionResult) error {
	return nil
}
func (r *accountJobTestRepo) Finish(context.Context, int64, string, string) (*AccountJob, error) {
	return nil, nil
}
func (r *accountJobTestRepo) Cancel(context.Context, int64, int64) (*AccountJob, error) {
	return nil, nil
}
func (r *accountJobTestRepo) FailedItemSeeds(_ context.Context, jobID, createdBy int64) (*AccountJob, []AccountJobItemSeed, string, time.Time, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job := r.jobs[jobID]
	if job == nil || job.CreatedBy != createdBy {
		return nil, nil, "", time.Time{}, ErrAccountJobNotFound
	}
	seeds := make([]AccountJobItemSeed, 0)
	for _, item := range r.items[jobID] {
		if item.Status == AccountJobItemStatusFailed {
			seeds = append(seeds, AccountJobItemSeed{
				Ordinal: item.Ordinal, Action: item.Action,
				TargetAccountID: item.TargetAccountID, Metadata: item.Metadata,
			})
		}
	}
	if len(seeds) == 0 {
		return nil, nil, "", time.Time{}, ErrAccountJobNotRetryable
	}
	payload := r.payloads[jobID]
	return cloneAccountJob(job), seeds, payload.cipher, payload.expiresAt, nil
}
func (r *accountJobTestRepo) ExpirePayloads(context.Context, time.Time) error { return nil }
func (r *accountJobTestRepo) Prune(context.Context, time.Time) error          { return nil }

func TestAccountJobSubmitRequiresIdempotencyKeyAndEncryptsPayload(t *testing.T) {
	repo := newAccountJobTestRepo()
	jobs := NewAccountJobService(repo, accountJobTestCipher{})
	payload := json.RawMessage(`{"account_ids":[7]}`)
	items := []AccountJobItemSeed{{Ordinal: 1}}

	_, _, err := jobs.Submit(context.Background(), 9, AccountJobKindBatchDelete, "", payload, nil, items)
	require.ErrorIs(t, err, ErrAccountJobIdempotencyRequired)

	job, replayed, err := jobs.Submit(context.Background(), 9, AccountJobKindBatchDelete, "delete-7", payload, nil, items)
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, AccountJobStatusPending, job.Status)
	require.Equal(t, 1, job.TargetCount)
	require.Equal(t, "cipher:"+string(payload), repo.payloads[job.ID].cipher)
	require.WithinDuration(t, time.Now().UTC().Add(AccountJobPayloadTTL), repo.payloads[job.ID].expiresAt, time.Second)
}

func TestAccountJobSubmitRejectsMoreThanOneBatch(t *testing.T) {
	repo := newAccountJobTestRepo()
	jobs := NewAccountJobService(repo, accountJobTestCipher{})
	items := make([]AccountJobItemSeed, AccountJobBatchSize+1)

	_, _, err := jobs.Submit(context.Background(), 9, AccountJobKindBatchDelete, "too-many",
		json.RawMessage(`{"account_ids":[7]}`), nil, items)

	require.ErrorIs(t, err, ErrAccountJobBatchTooLarge)
	require.Empty(t, repo.jobs)
}

func TestAccountJobSubmitReplaysSamePayloadAndRejectsConflict(t *testing.T) {
	repo := newAccountJobTestRepo()
	jobs := NewAccountJobService(repo, accountJobTestCipher{})
	items := []AccountJobItemSeed{{Ordinal: 1}}

	first, replayed, err := jobs.Submit(context.Background(), 9, AccountJobKindBatchDelete, "same-key", json.RawMessage(`{"account_ids":[7]}`), nil, items)
	require.NoError(t, err)
	require.False(t, replayed)

	second, replayed, err := jobs.Submit(context.Background(), 9, AccountJobKindBatchDelete, "same-key", json.RawMessage(`{"account_ids":[7]}`), nil, items)
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, first.ID, second.ID)

	_, _, err = jobs.Submit(context.Background(), 9, AccountJobKindBatchDelete, "same-key", json.RawMessage(`{"account_ids":[8]}`), nil, items)
	require.ErrorIs(t, err, ErrAccountJobIdempotencyConflict)
}

func TestAccountJobRuntimeMarksInterruptedBeforeClaiming(t *testing.T) {
	repo := newAccountJobTestRepo()
	jobs := NewAccountJobService(repo, accountJobTestCipher{})
	runtime := NewAccountJobRuntime(jobs, nil)
	require.NoError(t, runtime.Start(context.Background()))
	t.Cleanup(runtime.Stop)

	require.Eventually(t, func() bool {
		repo.mu.Lock()
		defer repo.mu.Unlock()
		return repo.interrupted && repo.claimCalls > 0
	}, time.Second, 10*time.Millisecond)
}

func TestAccountJobRetryRequiresKeyAndReplaysFailedItems(t *testing.T) {
	repo := newAccountJobTestRepo()
	jobs := NewAccountJobService(repo, accountJobTestCipher{})
	original, _, err := jobs.Submit(context.Background(), 9, AccountJobKindBatchDelete, "original",
		json.RawMessage(`{"account_ids":[7]}`), nil,
		[]AccountJobItemSeed{{Ordinal: 1, Action: "delete"}})
	require.NoError(t, err)
	repo.jobs[original.ID].Status = AccountJobStatusFailed
	repo.items[original.ID][0].Status = AccountJobItemStatusFailed

	_, _, err = jobs.RetryFailed(context.Background(), original.ID, 9, "")
	require.ErrorIs(t, err, ErrAccountJobIdempotencyRequired)

	retry, replayed, err := jobs.RetryFailed(context.Background(), original.ID, 9, "retry-1")
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, original.ID, *retry.RetryOfJobID)
	require.Equal(t, 2, retry.Attempt)
	require.Equal(t, repo.payloads[original.ID].cipher, repo.payloads[retry.ID].cipher)

	replayedRetry, replayed, err := jobs.RetryFailed(context.Background(), original.ID, 9, "retry-1")
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, retry.ID, replayedRetry.ID)
}

func TestAccountJobMetadataRejectsCredentialFields(t *testing.T) {
	require.NoError(t, ValidateAccountJobMetadata(json.RawMessage(`{"account_id":7,"action":"updated"}`)))
	require.ErrorIs(t, ValidateAccountJobMetadata(json.RawMessage(`{"api_key":"secret"}`)), ErrAccountJobInvalidMetadata)
}
