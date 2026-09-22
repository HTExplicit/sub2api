//go:build unit

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

type viewCountingExecutor struct{ calls int }

func TestAccountViewGenericRPCJobsAreUnsupported(t *testing.T) {
	manager, plugins, _, request := newViewFixture(t)
	view, release, err := manager.BindAccountViewRequest(context.Background(), request)
	require.NoError(t, err)
	defer release()
	repo := newAccountJobTestRepo()
	jobs := NewAccountJobService(repo, accountJobTestCipher{})
	id := int64(2)
	seeds := []AccountJobItemSeed{{Ordinal: 1, TargetAccountID: &id}}
	payload := json.RawMessage(`{"plugin_id":2,"operation":"fixture","items":{}}`)
	_, _, err = jobs.Submit(view, 42, AccountJobKindExtensionOperation, "view-rpc", payload, nil, seeds)
	require.ErrorIs(t, err, ErrAccountViewUnsupportedOperation)
	_, _, err = jobs.ReplaySubmission(view, 42, AccountJobKindExtensionOperation, "view-rpc", payload)
	require.ErrorIs(t, err, ErrAccountViewUnsupportedOperation)
	metadata, err := stampAccountJobView(view, nil)
	require.NoError(t, err)
	executor := NewPluginJobExecutor(manager, nil)
	_, _, err = executor.PrepareAccountJob(context.Background(), &AccountJob{Kind: AccountJobKindExtensionOperation, Metadata: metadata}, payload)
	require.ErrorIs(t, err, ErrAccountViewUnsupportedOperation)
	_, err = executor.ExecuteAccountJob(view, &AccountJob{Kind: AccountJobKindExtensionOperation, Metadata: metadata}, payload, []AccountJobItem{{ID: 1, TargetAccountID: &id}})
	require.ErrorIs(t, err, ErrAccountViewUnsupportedOperation)

	background := WithPluginExecution(context.Background(), plugins.installations[2])
	job, _, err := jobs.Submit(background, 42, AccountJobKindExtensionOperation, "background-rpc", payload, nil, seeds)
	require.NoError(t, err)
	storedView, err := AccountJobViewExecution(job.Metadata)
	require.NoError(t, err)
	require.Nil(t, storedView)
	require.Equal(t, string(payload), mustViewPayload(t, jobs, repo, job.ID))
}

func (e *viewCountingExecutor) ExecuteAccountJob(_ context.Context, _ *AccountJob, _ json.RawMessage, items []AccountJobItem) ([]AccountJobExecutionResult, error) {
	e.calls++
	return []AccountJobExecutionResult{{ItemID: items[0].ID, Status: AccountJobItemStatusSucceeded}}, nil
}

type viewCancelJobRepo struct {
	*accountJobTestRepo
	cancels int
}

func (r *viewCancelJobRepo) Cancel(ctx context.Context, id, actor int64) (*AccountJob, error) {
	r.cancels++
	return r.accountJobTestRepo.Get(ctx, id)
}

type viewCancelExecutor struct{ cancel func() }

func (e *viewCancelExecutor) ExecuteAccountJob(ctx context.Context, _ *AccountJob, _ json.RawMessage, _ []AccountJobItem) ([]AccountJobExecutionResult, error) {
	e.cancel()
	return nil, ctx.Err()
}

func TestAccountViewNativeJobCancellationKeepsHostProgress(t *testing.T) {
	manager, _, _, request := newViewFixture(t)
	ctx, release, err := manager.BindAccountViewRequest(context.Background(), request)
	require.NoError(t, err)
	defer release()
	repo := &viewCancelJobRepo{accountJobTestRepo: newAccountJobTestRepo()}
	jobs := NewAccountJobService(repo, accountJobTestCipher{})
	id := int64(2)
	job, _, err := jobs.Submit(ctx, 42, AccountJobKindBatchDelete, "view-core-cancel", json.RawMessage(`{"account_ids":[2]}`), nil, []AccountJobItemSeed{{Ordinal: 1, TargetAccountID: &id}})
	require.NoError(t, err)
	primary, _ := AccountJobPluginExecution(job.Metadata)
	require.Zero(t, primary.ID, "native core action ownership remains native")
	executor := NewPluginJobExecutor(manager, &viewCancelExecutor{cancel: func() { manager.extensions.Load().runtimes[1].beginDrain() }})
	runtime := &AccountJobRuntime{jobs: jobs, executor: executor, ctx: context.Background()}
	runtime.execute(job)
	require.Equal(t, 1, repo.cancels, "origin-only cancellation still settles the host job")
}

func TestAccountViewReplayIdentityAndFrozenRetry(t *testing.T) {
	manager, plugins, directory, request := newViewFixture(t)
	request.Query.Search = "captured-private-search"
	view, releaseView, err := manager.BindAccountViewRequest(context.Background(), request)
	require.NoError(t, err)
	defer releaseView()
	ctx, release, err := manager.BindResourceContext(view, 2, plugins.installations[2].PackageSHA256, plugins.installations[2].Manifest.Resources[0])
	require.NoError(t, err)
	defer release()
	ctx = manager.WithResourcePolicy(ctx, extensionv1.ResourceDescriptor{ResourceGrant: plugins.installations[2].Manifest.Resources[0]})
	repo := newAccountJobTestRepo()
	jobs := NewAccountJobService(repo, accountJobTestCipher{})
	id := int64(2)
	payload := json.RawMessage(`{"name":"renamed","filters":{"search":"captured-private-search"}}`)
	job, replayed, err := jobs.Submit(ctx, 42, AccountJobKindBulkUpdate, "view-key", payload, json.RawMessage(`{"plugin_id":2}`), []AccountJobItemSeed{{Ordinal: 1, TargetAccountID: &id}})
	require.NoError(t, err)
	require.False(t, replayed)
	owner, err := AccountJobPluginExecution(job.Metadata)
	require.NoError(t, err)
	require.EqualValues(t, 2, owner.ID)
	origin, err := AccountJobViewExecution(job.Metadata)
	require.NoError(t, err)
	require.EqualValues(t, 1, origin.PluginID)
	require.NotContains(t, string(job.Metadata), "captured-private-search")
	account := directory.accounts[2]
	delete(directory.accounts, 2)
	directory.reads = 0
	old, replayed, err := jobs.ReplaySubmission(ctx, 42, AccountJobKindBulkUpdate, "view-key", payload)
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, job.ID, old.ID)
	require.Zero(t, directory.reads, "replay must precede mutable account selection")
	directory.accounts[2] = account
	other := request
	other.PresetID = "insufficient"
	otherCtx, otherRelease, err := manager.BindAccountViewRequest(context.Background(), other)
	require.NoError(t, err)
	defer otherRelease()
	otherCtx = WithPluginExecution(otherCtx, plugins.installations[2])
	_, _, err = jobs.ReplaySubmission(otherCtx, 42, AccountJobKindBulkUpdate, "view-key", payload)
	require.ErrorIs(t, err, ErrAccountJobIdempotencyConflict)
	beforePayload := repo.payloads[job.ID]
	repo.items[job.ID][0].Status = AccountJobItemStatusFailed
	previous := processExtensionOperations.Load()
	processExtensionOperations.Store(&extensionOperationProvider{invoker: manager})
	t.Cleanup(func() { processExtensionOperations.Store(previous) })
	plugins.installations[1].RuntimeGeneration++
	applyViewFixture(t, manager, plugins)
	retried, replayed, err := jobs.RetryFailed(context.Background(), job.ID, 42, "retry-view-key")
	require.NoError(t, err)
	require.False(t, replayed)
	retryOrigin, err := AccountJobViewExecution(retried.Metadata)
	require.NoError(t, err)
	require.Equal(t, origin.RuntimeGeneration+1, retryOrigin.RuntimeGeneration)
	require.Equal(t, beforePayload, repo.payloads[retried.ID], "retry preserves original encrypted request and expiry")
	require.EqualValues(t, 2, *repo.items[retried.ID][0].TargetAccountID)
	_, _, err = manager.BindAccountJobView(context.Background(), job.Metadata, json.RawMessage(mustViewPayload(t, jobs, repo, job.ID)), false)
	require.Error(t, err, "queued work cannot silently adopt a different generation")
}

func mustViewPayload(t *testing.T, jobs *AccountJobService, repo *accountJobTestRepo, id int64) string {
	t.Helper()
	plain, err := jobs.encryptor.Decrypt(repo.payloads[id].cipher)
	require.NoError(t, err)
	return plain
}

func TestAccountViewCoreJobHashAndTTLUnchanged(t *testing.T) {
	repo := newAccountJobTestRepo()
	jobs := NewAccountJobService(repo, accountJobTestCipher{})
	payload := json.RawMessage("{ \"account_ids\" : [2] }")
	id := int64(2)
	job, _, err := jobs.Submit(context.Background(), 42, AccountJobKindBatchDelete, "core-key", payload, nil, []AccountJobItemSeed{{Ordinal: 1, TargetAccountID: &id}})
	require.NoError(t, err)
	digest := sha256.Sum256(payload)
	require.Equal(t, hex.EncodeToString(digest[:]), job.RequestHash)
	require.Equal(t, string(payload), mustViewPayload(t, jobs, repo, job.ID))
	view, err := AccountJobViewExecution(job.Metadata)
	require.NoError(t, err)
	require.Nil(t, view)
	require.Equal(t, 24*time.Hour, AccountJobPayloadTTL)
	require.WithinDuration(t, time.Now().UTC().Add(24*time.Hour), repo.payloads[job.ID].expiresAt, time.Second)
}

func TestCindyCleanupBindAndExecuteAdmission(t *testing.T) {
	manager, plugins, _, request := newViewFixture(t)
	request.PresetID = "banned"
	request.Query.Search = "nothing matches"
	view, releaseView, err := manager.BindAccountViewRequest(context.Background(), request)
	require.NoError(t, err)
	defer releaseView()
	ctx, release, err := manager.BindAccountJobExecution(view, 1, 3, AccountJobKindCindyConfirmedCleanup)
	require.NoError(t, err)
	defer release()
	metadata, err := stampAccountJobPlugin(nil, PluginExecution{1, 3})
	require.NoError(t, err)
	metadata, err = stampAccountJobView(ctx, metadata)
	require.NoError(t, err)
	core := &viewCountingExecutor{}
	executor := NewPluginJobExecutor(manager, core)
	job := &AccountJob{Kind: AccountJobKindCindyConfirmedCleanup, Metadata: metadata}
	_, err = executor.ExecuteAccountJob(ctx, job, json.RawMessage(`{"expected_count":3,"fingerprint":"fixed"}`), []AccountJobItem{{ID: 1}})
	require.NoError(t, err)
	require.Equal(t, 1, core.calls, "ordinal cleanup ignores active preset/search; it checks the fixed full domain")
	plugins.installations[1].Bindings[0].RolloutPercent = 99
	applyViewFixture(t, manager, plugins)
	_, _, err = manager.BindAccountJobExecution(context.Background(), 1, 3, AccountJobKindCindyConfirmedCleanup)
	require.Error(t, err)
	_, err = executor.ExecuteAccountJob(ctx, job, json.RawMessage(`{}`), []AccountJobItem{{ID: 2}})
	require.Error(t, err)
	require.Equal(t, 1, core.calls)
}

func TestLegacyCindyCleanupExecutionOwner(t *testing.T) {
	manager, plugins, _, _ := newViewFixture(t)
	core := &viewCountingExecutor{}
	executor := NewPluginJobExecutor(manager, core)
	job := &AccountJob{Kind: AccountJobKindCindyBannedCleanup}
	ctx, release, err := executor.PrepareAccountJob(context.Background(), job, json.RawMessage(`{"expected_count":1,"fingerprint":"fixed"}`))
	require.NoError(t, err)
	owner, bound := PluginExecutionFromContext(ctx)
	require.True(t, bound)
	require.EqualValues(t, 1, owner.ID)
	_, err = executor.ExecuteAccountJob(ctx, job, json.RawMessage(`{}`), []AccountJobItem{{ID: 1}})
	require.NoError(t, err)
	release()
	require.Empty(t, job.Metadata, "old records must not be rewritten")
	plugins.installations[1].PluginKey = "another.provider"
	applyViewFixture(t, manager, plugins)
	_, _, err = executor.PrepareAccountJob(context.Background(), job, json.RawMessage(`{}`))
	require.Error(t, err)
}

func TestAccountViewDuplicateJobTargetsDoNotLoseLosers(t *testing.T) {
	ids, err := accountViewJobTargets(AccountJobKindDuplicateReview, json.RawMessage(`{"account_ids":[2,3]}`), []*int64{nil})
	require.NoError(t, err)
	require.Equal(t, []int64{2, 3}, ids)
	id := int64(2)
	ids, err = accountViewJobTargets(AccountJobKindDuplicateMerge, json.RawMessage(`{"survivor_account_id":2,"loser_account_ids":[3]}`), []*int64{&id})
	require.NoError(t, err)
	require.Equal(t, []int64{2, 3}, ids)
	_, err = accountViewJobTargets(AccountJobKindDuplicateReview, json.RawMessage(`{}`), []*int64{nil})
	require.Error(t, err)
	_, err = accountViewJobTargets(AccountJobKindDuplicateMerge, json.RawMessage(`{"survivor_account_id":2,"loser_account_ids":[-1]}`), []*int64{&id})
	require.Error(t, err)
}
