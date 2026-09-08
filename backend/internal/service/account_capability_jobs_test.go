//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type capabilityJobsAccountsStub struct {
	AccountRepository
	accounts []*Account
	reads    int
}

func (s *capabilityJobsAccountsStub) GetByIDs(_ context.Context, _ []int64) ([]*Account, error) {
	s.reads++
	return s.accounts, nil
}
func (s *capabilityJobsAccountsStub) GetByID(_ context.Context, id int64) (*Account, error) {
	for _, account := range s.accounts {
		if account.ID == id {
			return account, nil
		}
	}
	return nil, errors.New("missing")
}

type capabilityJobsRepoStub struct {
	AccountCapabilityRepository
	run                *AccountCapabilityRun
	items              []AccountCapabilityItem
	canDispatch        bool
	dispatches         int
	releases           int
	controls           int
	completedStatus    string
	completedResult    json.RawMessage
	completedCount     int
	completionFailures int
	completionAttempts int
}

func (s *capabilityJobsRepoStub) FindIdempotent(context.Context, int64, string) (*AccountCapabilityRun, error) {
	if s.run == nil {
		return nil, ErrAccountCapabilityNotFound
	}
	return s.run, nil
}
func (s *capabilityJobsRepoStub) Create(_ context.Context, run *AccountCapabilityRun, items []AccountCapabilityItem) (*AccountCapabilityRun, bool, error) {
	run.ID = 1
	s.run = run
	s.items = items
	return run, false, nil
}
func (s *capabilityJobsRepoStub) ScopeItems(context.Context, int64) ([]AccountCapabilityItem, error) {
	return append([]AccountCapabilityItem(nil), s.items...), nil
}
func (s *capabilityJobsRepoStub) Control(context.Context, int64, string) (*AccountCapabilityRun, error) {
	s.controls++
	return s.run, nil
}
func (s *capabilityJobsRepoStub) MarkDispatched(context.Context, int64) (bool, error) {
	s.dispatches++
	return s.canDispatch, nil
}
func (s *capabilityJobsRepoStub) Release(context.Context, int64) error { s.releases++; return nil }
func (s *capabilityJobsRepoStub) Complete(_ context.Context, _ int64, status string, result json.RawMessage, count int) error {
	s.completionAttempts++
	if s.completionFailures > 0 {
		s.completionFailures--
		return errors.New("offline persistence failure")
	}
	s.completedStatus = status
	s.completedResult = result
	s.completedCount = count
	return nil
}

type capabilityJobsExecutorStub struct {
	calls             int
	model             string
	status            string
	deadlineRemaining time.Duration
}

func (s *capabilityJobsExecutorStub) Discover(context.Context, *Account) AccountCapabilityDiscoveryResult {
	s.calls++
	return AccountCapabilityDiscoveryResult{Status: "empty", RequestCount: 1}
}
func (s *capabilityJobsExecutorStub) Probe(ctx context.Context, _ *Account, model, _, _ string) AccountCapabilityProbeResult {
	s.calls++
	s.model = model
	if deadline, ok := ctx.Deadline(); ok {
		s.deadlineRemaining = time.Until(deadline)
	}
	return AccountCapabilityProbeResult{Status: s.status, RequestCount: 1}
}

func capabilityJobsAccount() *Account {
	folder := int64(7)
	return &Account{ID: 31, Name: "offline fixture", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, ManagementFolderID: &folder,
		Credentials: map[string]any{"api_key": "offline-fixture-not-live", "base_url": "https://example.invalid", "model_mapping": map[string]any{"public": "private-target"}}}
}

func capabilityJobsRequest() AccountCapabilityCreateRequest {
	return AccountCapabilityCreateRequest{Kind: "probe", FolderIDs: []int64{8, 7, 7}, AccountIDs: []int64{31}, Items: []AccountCapabilityProbeTarget{
		{AccountID: 31, UpstreamModel: "Real-Model", Protocol: "responses", Aliases: []string{"public-a"}},
		{AccountID: 31, UpstreamModel: "Real-Model", Protocol: "responses", Profile: "text", Aliases: []string{"public-b", "public-a"}},
	}}
}

func TestAccountCapabilityJobsFreezeScopeDeduplicateAliasesAndReplay(t *testing.T) {
	account := capabilityJobsAccount()
	accounts := &capabilityJobsAccountsStub{accounts: []*Account{account}}
	repo := &capabilityJobsRepoStub{}
	svc := NewAccountCapabilityService(repo, accounts, nil)
	run, replayed, err := svc.Create(context.Background(), 1, "stable-key", capabilityJobsRequest())
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, 1, run.TargetCount)
	require.Equal(t, []int64{7, 8}, run.FolderIDs)
	require.Equal(t, int64(7), repo.items[0].FolderID)
	require.Equal(t, []string{"public-a", "public-b"}, repo.items[0].Aliases)
	require.Len(t, repo.items[0].ConfigFingerprint, 64)
	// Replayed submissions do not look up credentials again or create fresh work.
	_, replayed, err = svc.Create(context.Background(), 1, "stable-key", capabilityJobsRequest())
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, 1, accounts.reads)
	changed := capabilityJobsRequest()
	changed.Items[0].UpstreamModel = "different"
	_, _, err = svc.Create(context.Background(), 1, "stable-key", changed)
	require.ErrorIs(t, err, ErrAccountCapabilityIdempotencyConflict)
}

func TestAccountCapabilityJobsRejectOutOfScopeAndUnfrozenTarget(t *testing.T) {
	account := capabilityJobsAccount()
	accounts := &capabilityJobsAccountsStub{accounts: []*Account{account}}
	repo := &capabilityJobsRepoStub{}
	svc := NewAccountCapabilityService(repo, accounts, nil)
	request := capabilityJobsRequest()
	request.FolderIDs = []int64{8}
	_, _, err := svc.Create(context.Background(), 1, "scope", request)
	require.ErrorIs(t, err, ErrAccountCapabilityScope)
	require.Nil(t, repo.run)
	request = capabilityJobsRequest()
	request.Items[0].AccountID = 999
	_, _, err = svc.Create(context.Background(), 1, "target", request)
	require.ErrorIs(t, err, ErrAccountCapabilityInvalid)
	require.Nil(t, repo.run)
}

func TestAccountCapabilityJobsFingerprintPreservesMappingButInvalidatesCredentials(t *testing.T) {
	account := capabilityJobsAccount()
	original, err := AccountCapabilityFingerprint(account)
	require.NoError(t, err)
	account.Credentials["model_mapping"] = map[string]any{"public": "private-target", "s2pub-g23-m123": "Real-Model"}
	account.Schedulable = true
	account.GroupIDs = []int64{23, 100}
	unchanged, err := AccountCapabilityFingerprint(account)
	require.NoError(t, err)
	require.Equal(t, original, unchanged)
	account.Credentials["api_key"] = "rotated-offline-fixture"
	changed, err := AccountCapabilityFingerprint(account)
	require.NoError(t, err)
	require.NotEqual(t, original, changed)
}

func TestAccountCapabilityJobsResumeRejectsChangedSnapshot(t *testing.T) {
	account := capabilityJobsAccount()
	fingerprint, err := AccountCapabilityFingerprint(account)
	require.NoError(t, err)
	repo := &capabilityJobsRepoStub{items: []AccountCapabilityItem{{AccountID: account.ID, FolderID: 7, ConfigFingerprint: fingerprint}}}
	account.Credentials["api_key"] = "rotated-offline-fixture"
	svc := NewAccountCapabilityService(repo, &capabilityJobsAccountsStub{accounts: []*Account{account}}, nil)
	_, err = svc.Control(context.Background(), 1, "resume")
	require.ErrorIs(t, err, ErrAccountCapabilityScope)
	require.Zero(t, repo.controls)
}

func TestAccountCapabilityJobsDispatchGateAndExactTarget(t *testing.T) {
	for _, paused := range []bool{true, false} {
		t.Run(map[bool]string{true: "paused before dispatch", false: "one exact wire attempt"}[paused], func(t *testing.T) {
			account := capabilityJobsAccount()
			fp, err := AccountCapabilityFingerprint(account)
			require.NoError(t, err)
			repo := &capabilityJobsRepoStub{canDispatch: !paused}
			executor := &capabilityJobsExecutorStub{status: "alive"}
			svc := NewAccountCapabilityService(repo, &capabilityJobsAccountsStub{accounts: []*Account{account}}, executor)
			item := &AccountCapabilityItem{ID: 1, Kind: "probe", AccountID: 31, FolderID: 7, ConfigFingerprint: fp, UpstreamModel: "public", Protocol: "responses", Profile: "text"}
			svc.executeItem(context.Background(), item)
			if paused {
				require.Zero(t, executor.calls)
				require.Equal(t, 1, repo.releases)
				return
			}
			require.Equal(t, 1, executor.calls)
			require.Equal(t, "public", executor.model)
			require.Equal(t, "succeeded", repo.completedStatus)
			require.Equal(t, 1, repo.completedCount)
			require.NotContains(t, string(repo.completedResult), "offline-fixture")
		})
	}
}

func TestAccountCapabilityJobsStaleTargetNeverDispatches(t *testing.T) {
	account := capabilityJobsAccount()
	repo := &capabilityJobsRepoStub{canDispatch: true}
	executor := &capabilityJobsExecutorStub{status: "alive"}
	svc := NewAccountCapabilityService(repo, &capabilityJobsAccountsStub{accounts: []*Account{account}}, executor)
	svc.executeItem(context.Background(), &AccountCapabilityItem{ID: 1, Kind: "probe", AccountID: 31, FolderID: 7, ConfigFingerprint: "old", UpstreamModel: "Real-Model", Protocol: "responses", Profile: "text"})
	require.Zero(t, executor.calls)
	require.Zero(t, repo.dispatches)
	require.Zero(t, repo.completedCount)
	require.Equal(t, "stale", repo.completedStatus)
}

func TestAccountCapabilityJobsTerminalClassificationNeverInventsAlive(t *testing.T) {
	for _, test := range []struct{ input, want string }{{"failed", "failed"}, {"uncertain", "indeterminate"}, {"unsupported", "failed"}, {"canceled", "indeterminate"}} {
		t.Run(test.input, func(t *testing.T) {
			account := capabilityJobsAccount()
			fp, err := AccountCapabilityFingerprint(account)
			require.NoError(t, err)
			repo := &capabilityJobsRepoStub{canDispatch: true}
			executor := &capabilityJobsExecutorStub{status: test.input}
			svc := NewAccountCapabilityService(repo, &capabilityJobsAccountsStub{accounts: []*Account{account}}, executor)
			svc.executeItem(context.Background(), &AccountCapabilityItem{ID: 1, Kind: "probe", AccountID: 31, FolderID: 7, ConfigFingerprint: fp, UpstreamModel: "Real-Model", Protocol: "responses", Profile: "text"})
			require.Equal(t, test.want, repo.completedStatus)
			require.Equal(t, 1, executor.calls)
		})
	}
}

func TestAccountCapabilityJobsExplicitWebSocketProfile(t *testing.T) {
	request := capabilityJobsRequest()
	request.Items = request.Items[:1]
	request.Items[0].Protocol = "responses_websocket"
	request.Items[0].Profile = "tool_roundtrip"
	normalized, err := normalizeCapabilityRequest(request)
	require.NoError(t, err)
	require.Equal(t, "responses_websocket", normalized.Items[0].Protocol)
}

func TestAccountCapabilityJobsToolRoundtripAllowsTwoBoundedRequests(t *testing.T) {
	account := capabilityJobsAccount()
	fp, err := AccountCapabilityFingerprint(account)
	require.NoError(t, err)
	repo := &capabilityJobsRepoStub{canDispatch: true}
	executor := &capabilityJobsExecutorStub{status: "alive"}
	svc := NewAccountCapabilityService(repo, &capabilityJobsAccountsStub{accounts: []*Account{account}}, executor)
	svc.executeItem(context.Background(), &AccountCapabilityItem{ID: 1, Kind: "probe", AccountID: 31, FolderID: 7, ConfigFingerprint: fp, UpstreamModel: "Real-Model", Protocol: "responses", Profile: "tool_roundtrip"})
	require.Greater(t, executor.deadlineRemaining, 110*time.Second)
	require.LessOrEqual(t, executor.deadlineRemaining, 120*time.Second)
	request := capabilityJobsRequest()
	request.Items = request.Items[:1]
	request.Items[0].Protocol = "messages_count_tokens"
	request.Items[0].Profile = "tool_roundtrip"
	_, err = normalizeCapabilityRequest(request)
	require.ErrorIs(t, err, ErrAccountCapabilityInvalid)
}

func TestAccountCapabilityJobsPersistenceRetryNeverResendsUpstream(t *testing.T) {
	for _, failures := range []int{2, 3} {
		account := capabilityJobsAccount()
		fp, err := AccountCapabilityFingerprint(account)
		require.NoError(t, err)
		repo := &capabilityJobsRepoStub{canDispatch: true, completionFailures: failures}
		executor := &capabilityJobsExecutorStub{status: "alive"}
		svc := NewAccountCapabilityService(repo, &capabilityJobsAccountsStub{accounts: []*Account{account}}, executor)
		svc.executeItem(context.Background(), &AccountCapabilityItem{ID: 1, RunID: 9, Kind: "probe", AccountID: 31, FolderID: 7, ConfigFingerprint: fp, UpstreamModel: "Real-Model", Protocol: "responses", Profile: "text"})
		require.Equal(t, 1, executor.calls)
		require.Equal(t, 3, repo.completionAttempts)
		if failures == 3 {
			require.Equal(t, 1, repo.controls)
		} else {
			require.Zero(t, repo.controls)
			require.Equal(t, "succeeded", repo.completedStatus)
		}
	}
}
