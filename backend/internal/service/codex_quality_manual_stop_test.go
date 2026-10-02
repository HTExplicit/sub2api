package service

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

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

func TestCodexQualityManualStopRequiredBeforeCreateAndSend(t *testing.T) {
	ctx := context.Background()
	s, account, store, key := qualityCreateFixture()
	slots := &qualityManualStopConcurrency{}
	s.concurrencyService = &ConcurrencyService{cache: slots}
	lookup := func(context.Context, int64) (*APIKey, error) { return key, nil }
	request := CodexQualityCreateRequest{RunID: uuid.NewString(), APIKeyID: key.ID, PromptSHA256: codexQualityHash("question"), Model: "gpt-6-astra", ReasoningEffort: "high", MaxSends: 6, TTLSeconds: 7200}
	created, err := s.CreateCodexQualityRun(ctx, key.UserID, account.ID, key, request)
	require.NoError(t, err, "the stopped account remains eligible for its bounded diagnostic run")
	require.NotEmpty(t, created.Grant)
	require.False(t, account.Schedulable, "the eligibility copy must not enable the persisted account")
	run, revision, err := readCodexQualityRun(ctx, store, request.RunID)
	require.NoError(t, err)
	// Any administrator reads the run; only its creator closes it.
	view, err := s.ReadCodexQualityRun(ctx, key.UserID+1, account.ID, request.RunID)
	require.NoError(t, err)
	require.Equal(t, key.UserID, view.ActorID)
	_, err = s.CloseCodexQualityRun(ctx, key.UserID+1, account.ID, request.RunID)
	require.ErrorContains(t, err, "only its creator can close it")

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

	runtime, err := s.codexQualityRuntime()
	require.NoError(t, err)
	execution := &codexQualityExecution{runtime: runtime, runID: run.RunID, grantDigest: run.GrantDigest, accountID: account.ID, trialID: uuid.NewString(), keyLookup: lookup}
	sendCtx := context.WithValue(ctx, codexQualityExecutionKey{}, execution)
	_, err = s.SelectCodexQualityAccount(sendCtx)
	require.ErrorIs(t, err, ErrCodexQualityUnavailable)
	require.Zero(t, slots.acquisitions, "the private selection rejects before taking a concurrency slot")
	req, err := http.NewRequestWithContext(sendCtx, http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"model":"gpt-6-astra","reasoning":{"effort":"high"}}`))
	require.NoError(t, err)
	require.ErrorIs(t, reserveCodexQualitySend(req, account), ErrCodexQualityUnavailable)
	saved, savedRevision, err := readCodexQualityRun(ctx, store, run.RunID)
	require.NoError(t, err)
	require.Equal(t, revision, savedRevision)
	require.Zero(t, saved.UsedSends)
	require.Empty(t, saved.Attempts)
	require.True(t, account.Schedulable, "rejection must not flip the administrator's setting back off")
}
