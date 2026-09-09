//go:build unit

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func capabilityGuardedRequest(t *testing.T, account *Account) AccountCapabilityCreateRequest {
	t.Helper()
	fingerprint, err := AccountCapabilityFingerprint(account)
	require.NoError(t, err)
	request := capabilityJobsRequest()
	request.OnlyUntested = true
	request.ExpectedConfigFingerprints = map[int64]string{account.ID: fingerprint}
	return request
}

func TestAccountCapabilityGuardedCreateFreezesPlanFingerprintAndDeduplicates(t *testing.T) {
	account := capabilityJobsAccount()
	accounts := &capabilityJobsAccountsStub{accounts: []*Account{account}}
	repo := &capabilityJobsRepoStub{}
	svc := NewAccountCapabilityService(repo, accounts, nil)
	request := capabilityGuardedRequest(t, account)
	run, replayed, err := svc.Create(context.Background(), 1, "guarded-plan", request)
	require.NoError(t, err)
	require.False(t, replayed)
	require.True(t, repo.run.OnlyUntested)
	require.Equal(t, 1, run.TargetCount)
	require.Len(t, repo.items, 1)
	require.Equal(t, request.ExpectedConfigFingerprints[31], repo.items[0].ConfigFingerprint)
	require.Equal(t, []string{"public-a", "public-b"}, repo.items[0].Aliases)
	_, replayed, err = svc.Create(context.Background(), 1, "guarded-plan", request)
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, 1, accounts.reads, "idempotent recovery must not create new work")
}

func TestAccountCapabilityGuardedCreateRejectsPlanDriftBeforeQueue(t *testing.T) {
	account := capabilityJobsAccount()
	request := capabilityGuardedRequest(t, account)
	account.Credentials["base_url"] = "https://changed.example.invalid"
	repo := &capabilityJobsRepoStub{}
	svc := NewAccountCapabilityService(repo, &capabilityJobsAccountsStub{accounts: []*Account{account}}, nil)
	_, _, err := svc.Create(context.Background(), 1, "changed-plan", request)
	require.ErrorIs(t, err, ErrAccountCapabilityScope)
	require.Nil(t, repo.run)
	require.Empty(t, repo.items)
}

func TestAccountCapabilityGuardedCreateRejectsHiddenExtraProbes(t *testing.T) {
	tests := map[string]func(*AccountCapabilityCreateRequest){
		"missing fingerprint":          func(r *AccountCapabilityCreateRequest) { r.ExpectedConfigFingerprints = nil },
		"invalid fingerprint":          func(r *AccountCapabilityCreateRequest) { r.ExpectedConfigFingerprints[31] = strings.Repeat("z", 64) },
		"foreign fingerprint":          func(r *AccountCapabilityCreateRequest) { r.ExpectedConfigFingerprints[99] = strings.Repeat("a", 64) },
		"tools are not basic":          func(r *AccountCapabilityCreateRequest) { r.Items[0].Profile = "tool_roundtrip" },
		"metadata is not basic":        func(r *AccountCapabilityCreateRequest) { r.Items[0].Protocol = "responses_input_tokens" },
		"websocket is not automatic":   func(r *AccountCapabilityCreateRequest) { r.Items[0].Protocol = "responses_websocket" },
		"two protocols for same model": func(r *AccountCapabilityCreateRequest) { r.Items[1].Protocol = "chat_completions" },
		"discovery is separate":        func(r *AccountCapabilityCreateRequest) { r.Kind = "discover"; r.Items = nil },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := capabilityGuardedRequest(t, capabilityJobsAccount())
			mutate(&request)
			_, err := normalizeCapabilityRequest(request)
			require.ErrorIs(t, err, ErrAccountCapabilityInvalid)
		})
	}
}

func TestAccountCapabilityGuardedCreateRejectsUnusableManagedProtocol(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic, PlatformGemini} {
		t.Run(platform, func(t *testing.T) {
			account := capabilityJobsAccount()
			account.Platform = platform
			request := capabilityGuardedRequest(t, account)
			if platform == PlatformOpenAI {
				for i := range request.Items {
					request.Items[i].Protocol = AccountCapabilityProtocolMessages
				}
			}
			repo := &capabilityJobsRepoStub{}
			svc := NewAccountCapabilityService(repo, &capabilityJobsAccountsStub{accounts: []*Account{account}}, nil)
			_, _, err := svc.Create(context.Background(), 1, "incompatible-plan", request)
			require.ErrorIs(t, err, ErrAccountCapabilityInvalid)
			require.Nil(t, repo.run, "an unusable organizer request must not enter the paid queue")
		})
	}
}

func TestAccountCapabilityJobsPartialDirectoryPreservesSuccessfulObservation(t *testing.T) {
	account := capabilityJobsAccount()
	fingerprint, err := AccountCapabilityFingerprint(account)
	require.NoError(t, err)
	repo := &capabilityJobsRepoStub{canDispatch: true}
	executor := &capabilityJobsExecutorStub{discoveryStatus: "partial"}
	svc := NewAccountCapabilityService(repo, &capabilityJobsAccountsStub{accounts: []*Account{account}}, executor)
	svc.executeItem(context.Background(), &AccountCapabilityItem{ID: 1, RunID: 1, Kind: AccountCapabilityKindDiscover,
		AccountID: account.ID, FolderID: *account.ManagementFolderID, ConfigFingerprint: fingerprint})
	require.Equal(t, 1, executor.calls)
	require.Equal(t, "succeeded", repo.completedStatus, "a successful but partial directory is not a credential failure")
	require.Contains(t, string(repo.completedResult), `"status":"partial"`)
	require.Equal(t, 1, repo.completedCount)
}

type capabilityReceiptRepo struct {
	AccountCapabilityRepository
	actor int64
	key   string
	calls int
	run   *AccountCapabilityRun
}

func (r *capabilityReceiptRepo) FindIdempotent(_ context.Context, actor int64, key string) (*AccountCapabilityRun, error) {
	r.actor, r.key, r.calls = actor, key, r.calls+1
	if r.run == nil {
		return nil, ErrAccountCapabilityNotFound
	}
	return r.run, nil
}

func TestAccountCapabilityCreationReceiptIsActorScopedAndReadOnly(t *testing.T) {
	repo := &capabilityReceiptRepo{}
	svc := NewAccountCapabilityService(repo, nil, nil)
	_, err := svc.FindCreationReceipt(context.Background(), 7, " saved-create-key ")
	require.ErrorIs(t, err, ErrAccountCapabilityNotFound)
	require.Equal(t, int64(7), repo.actor)
	require.Equal(t, "saved-create-key", repo.key)
	repo.run = &AccountCapabilityRun{ID: 42, CreatedBy: 7, Status: "running"}
	run, err := svc.FindCreationReceipt(context.Background(), 7, "saved-create-key")
	require.NoError(t, err)
	require.Equal(t, int64(42), run.ID)
	require.Equal(t, 2, repo.calls)
	_, err = svc.FindCreationReceipt(context.Background(), 0, "saved-create-key")
	require.ErrorIs(t, err, ErrAccountCapabilityInvalid)
	_, err = svc.FindCreationReceipt(context.Background(), 7, " ")
	require.ErrorIs(t, err, ErrAccountCapabilityIdempotencyRequired)
	require.Equal(t, 2, repo.calls)
}
