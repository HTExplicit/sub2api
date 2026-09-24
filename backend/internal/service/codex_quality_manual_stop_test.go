package service

import (
	"context"
	"net/http"
	"strings"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type qualityManualStopDirectory struct {
	*routingHostDirectoryFixture
	scopeReads int
}

func (d *qualityManualStopDirectory) PrepareCodexRoutingScope(context.Context, int64, string) (extensionv1.CodexRoutingScope, error) {
	d.scopeReads++
	return d.scope, nil
}

type qualityManualStopConcurrency struct {
	ConcurrencyCache
	acquisitions int
}

func (c *qualityManualStopConcurrency) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	c.acquisitions++
	return true, nil
}

func (*qualityManualStopConcurrency) ReleaseAccountSlot(context.Context, int64, string) error {
	return nil
}

func TestCodexQualityManualStopRequiredBeforeCreateRenewAndSend(t *testing.T) {
	ctx := context.Background()
	host, baseDirectory, store := routingHostFixture()
	directory := &qualityManualStopDirectory{routingHostDirectoryFixture: baseDirectory}
	host.directory = directory
	manager := nativeRoutingFixtureRuntime(host, store)

	group, proxy := int64(53), int64(34)
	account := &Account{ID: 16380, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: false, Concurrency: 1, GroupIDs: []int64{group}, ProxyID: &proxy, Credentials: map[string]any{"plan_type": "pro", "chatgpt_account_id": "quality-account"}}
	directory.account = *extensionAccount(account)
	directory.scope.AccountID, directory.scope.Identity = account.ID, CodexTicketAccountIdentity(account)
	slots := &qualityManualStopConcurrency{}
	s := &OpenAIGatewayService{nativeCodexRuntime: manager, accountRepo: &routingAccountRepositoryFixture{account: account}, concurrencyService: &ConcurrencyService{cache: slots}}
	key := &APIKey{ID: 101, UserID: 9, GroupID: &group, Status: StatusAPIKeyActive}
	lookup := func(context.Context, int64) (*APIKey, error) { return key, nil }
	request := CodexQualityCreateRequest{RunID: uuid.NewString(), APIKeyID: key.ID, PromptSHA256: codexQualityHash("question"), MaxSends: 6, TTLSeconds: 7200}
	created, err := s.CreateCodexQualityRun(ctx, key.UserID, account.ID, key, request)
	require.NoError(t, err, "the stopped account remains eligible for its bounded diagnostic run")
	require.NotEmpty(t, created.Grant)
	require.False(t, account.Schedulable, "the eligibility copy must not enable the persisted account")
	run, revision, err := readCodexQualityRun(ctx, store, request.RunID)
	require.NoError(t, err)

	// Simulate an administrator turning ordinary scheduling back on while the
	// existing diagnostic run and its downstream grant remain unexpired.
	account.Schedulable = true
	otherRequest := request
	otherRequest.RunID = uuid.NewString()
	_, err = s.CreateCodexQualityRun(ctx, key.UserID, account.ID, key, otherRequest)
	require.ErrorIs(t, err, ErrCodexQualityUnavailable)
	missing, missingRevision, err := readCodexQualityRun(ctx, store, otherRequest.RunID)
	require.NoError(t, err)
	require.Empty(t, missing.RunID)
	require.Zero(t, missingRevision, "a scheduled account must not obtain a new run or grant")

	scopeReads := directory.scopeReads
	_, err = s.RenewCodexQualityRoute(ctx, key.UserID, account.ID, request.RunID, uuid.NewString(), lookup)
	require.ErrorIs(t, err, ErrCodexQualityUnavailable)
	require.Equal(t, scopeReads, directory.scopeReads, "reject before route probing, not at a later missing dependency")
	require.Zero(t, slots.acquisitions)
	require.Zero(t, directory.requests)

	runtime, err := s.codexQualityRuntime()
	require.NoError(t, err)
	for _, stage := range []string{"acquire", "verify", "business"} {
		t.Run(stage, func(t *testing.T) {
			execution := &codexQualityExecution{runtime: runtime, runID: run.RunID, grantDigest: run.GrantDigest, accountID: account.ID, stage: stage, trialID: uuid.NewString(), operationID: uuid.NewString(), keyLookup: lookup}
			sendCtx := context.WithValue(ctx, codexQualityExecutionKey{}, execution)
			req, err := http.NewRequestWithContext(sendCtx, http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"model":"gpt-6-astra","reasoning":{"effort":"high"}}`))
			require.NoError(t, err)
			require.ErrorIs(t, reserveCodexQualitySend(req, account, nil), ErrCodexQualityUnavailable)
			require.Equal(t, scopeReads, directory.scopeReads, "the fresh manual-stop guard must reject before qualification checks")
			saved, savedRevision, err := readCodexQualityRun(ctx, store, run.RunID)
			require.NoError(t, err)
			require.Equal(t, revision, savedRevision)
			require.Zero(t, saved.UsedSends)
			require.Empty(t, saved.Attempts)
			require.Zero(t, directory.requests)
			require.True(t, account.Schedulable, "rejection must not flip the administrator's setting back off")
		})
	}
}
