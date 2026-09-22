//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type usageCommitObserverLogRepo struct {
	UsageLogRepository
	create func(context.Context, *UsageLog) (bool, error)
}

func (r *usageCommitObserverLogRepo) Create(ctx context.Context, log *UsageLog) (bool, error) {
	return r.create(ctx, log)
}

type usageCommitObserverBestEffortRepo struct {
	*usageCommitObserverLogRepo
	createBestEffort func(context.Context, *UsageLog) error
}

func (r *usageCommitObserverBestEffortRepo) CreateBestEffort(ctx context.Context, log *UsageLog) error {
	return r.createBestEffort(ctx, log)
}

func usageCommitObserverRecordUsageForTest(gateway string, repo UsageLogRepository, billingRepo UsageBillingRepository, observer UsageCommitObserver) func(context.Context) error {
	apiKey := &APIKey{ID: 501}
	user := &User{ID: 601}
	account := &Account{ID: 701}
	if gateway == "openai" {
		svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(repo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
		svc.SetUsageCommitObserver(observer)
		return func(ctx context.Context) error {
			return svc.RecordUsage(ctx, &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{
					RequestID: "observer-persisted-log",
					Usage:     OpenAIUsage{InputTokens: 8, OutputTokens: 4},
					Model:     "gpt-5.1",
					Duration:  time.Second,
				},
				APIKey: apiKey, User: user, Account: account,
			})
		}
	}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(repo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
	svc.SetUsageCommitObserver(observer)
	return func(ctx context.Context) error {
		return svc.RecordUsage(ctx, &RecordUsageInput{
			Result: &ForwardResult{
				RequestID: "observer-persisted-log",
				Usage:     ClaudeUsage{InputTokens: 8, OutputTokens: 4},
				Model:     "claude-sonnet-4",
				Duration:  time.Second,
			},
			APIKey: apiKey, User: user, Account: account,
		})
	}
}

func TestUsageCommitObserverWaitsForConfirmedPersistence(t *testing.T) {
	writeErr := errors.New("synthetic usage log failure")
	billingErr := errors.New("synthetic billing failure")
	for _, gateway := range []string{"gateway", "openai"} {
		for _, tc := range []struct {
			name          string
			bestEffort    bool
			bestEffortErr error
			createErr     error
			duplicate     bool
			billingErr    error
		}{
			{name: "sync_success"},
			{name: "best_effort_success", bestEffort: true},
			{name: "best_effort_failure_sync_fallback_success", bestEffort: true, bestEffortErr: writeErr},
			{name: "best_effort_drop_sync_fallback_success", bestEffort: true, bestEffortErr: MarkUsageLogCreateDropped(context.DeadlineExceeded)},
			{name: "sync_failure", createErr: writeErr},
			{name: "best_effort_drop_and_fallback_failure", bestEffort: true, bestEffortErr: MarkUsageLogCreateDropped(context.DeadlineExceeded), createErr: writeErr},
			{name: "duplicate_billing", duplicate: true},
			{name: "billing_failure", billingErr: billingErr},
		} {
			t.Run(gateway+"/"+tc.name, func(t *testing.T) {
				entered, release := make(chan struct{}), make(chan struct{})
				var releaseOnce sync.Once
				defer releaseOnce.Do(func() { close(release) })
				var persisted atomic.Bool
				waitForWrite := func() {
					close(entered)
					<-release
				}
				createCalls, bestEffortCalls := 0, 0
				syncRepo := &usageCommitObserverLogRepo{
					create: func(context.Context, *UsageLog) (bool, error) {
						createCalls++
						waitForWrite()
						persisted.Store(tc.createErr == nil)
						return !tc.duplicate && tc.createErr == nil, tc.createErr
					},
				}
				var repo UsageLogRepository = syncRepo
				if tc.bestEffort {
					repo = &usageCommitObserverBestEffortRepo{
						usageCommitObserverLogRepo: syncRepo,
						createBestEffort: func(context.Context, *UsageLog) error {
							bestEffortCalls++
							if tc.bestEffortErr != nil {
								return tc.bestEffortErr
							}
							waitForWrite()
							persisted.Store(true)
							return nil
						},
					}
				}
				billingRepo := &openAIRecordUsageBillingRepoStub{
					result: &UsageBillingApplyResult{Applied: !tc.duplicate},
					err:    tc.billingErr,
				}
				entry := &quotaActivityEntry{}
				trace := &quotaActivityTrace{accounts: map[int64]*quotaActivityEntry{701: entry}}
				receipt := &quotaUsageTaskReceipt{accountID: 701}
				ctx := context.WithValue(context.Background(), quotaActivityContextKey{}, trace)
				ctx = context.WithValue(ctx, quotaUsageTaskContextKey{}, receipt)
				var observerCalls atomic.Int32
				var earlyObserver atomic.Bool
				var observedAccountID atomic.Int64
				record := usageCommitObserverRecordUsageForTest(gateway, repo, billingRepo, func(accountID int64) {
					observerCalls.Add(1)
					observedAccountID.Store(accountID)
					if !persisted.Load() || !receipt.logged.Load() {
						earlyObserver.Store(true)
					}
				})
				done := make(chan error, 1)
				go func() { done <- record(ctx) }()
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("usage log writer did not start")
				}
				assert.Zero(t, observerCalls.Load(), "billing CAS alone must not invalidate log-backed statistics")
				assert.False(t, receipt.logged.Load(), "blocked writer must not mark quota log persisted")
				releaseOnce.Do(func() { close(release) })
				select {
				case err := <-done:
					if tc.billingErr != nil {
						require.ErrorIs(t, err, tc.billingErr)
					} else {
						require.NoError(t, err)
					}
				case <-time.After(time.Second):
					t.Fatal("usage recorder did not finish after writer release")
				}
				assert.Equal(t, 1, billingRepo.calls, "log fallback must not repeat billing")
				if tc.bestEffort {
					assert.Equal(t, 1, bestEffortCalls)
				}
				if !tc.bestEffort || tc.bestEffortErr != nil {
					assert.Equal(t, 1, createCalls)
				} else {
					assert.Zero(t, createCalls)
				}
				shouldNotify := persisted.Load() && !tc.duplicate && tc.billingErr == nil
				if shouldNotify {
					assert.EqualValues(t, 1, observerCalls.Load())
					assert.EqualValues(t, 701, observedAccountID.Load())
				} else {
					assert.Zero(t, observerCalls.Load(), "failed persistence or non-owner billing must not notify")
				}
				assert.False(t, earlyObserver.Load(), "observer must run after both log persistence and quota receipt marking")
				assert.Equal(t, persisted.Load(), receipt.logged.Load())
				assert.Equal(t, persisted.Load(), entry.logged)
				assert.Equal(t, !persisted.Load(), entry.gap, "failed logs must retain quota gap evidence")
			})
		}
	}
}
