//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAgentLoadAwareSelectionPreservesExclusionReasons(t *testing.T) {
	accounts := []Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"model_mapping": map[string]any{"other": "other"}}},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"model_mapping": map[string]any{"fixture": "fixture"}}},
	}
	repo := &mockAccountRepoForPlatform{accounts: accounts, accountsByID: map[int64]*Account{1: &accounts[0], 2: &accounts[1]}}
	cfg := testConfig()
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	s := &OpenAIGatewayService{cfg: cfg, accountRepo: repo, concurrencyService: NewConcurrencyService(&mockConcurrencyCache{})}
	s.BlockAccountScheduling(&accounts[1], time.Now().Add(time.Minute), "fixture")
	selection, err := s.selectAccountWithLoadAwareness(context.Background(), nil, PlatformOpenAI, "", "fixture", nil, false, OpenAIEndpointCapabilityChatCompletions, false)
	require.Nil(t, selection)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.ErrorContains(t, err, "model_not_supported=1")
	require.ErrorContains(t, err, "runtime_blocked=1")
	require.ErrorContains(t, err, "pool=2")
	details := GetOpenAISelectionDiagnostics(err)
	require.NotNil(t, details)
	require.Equal(t, 1, details.Rejected["model_not_supported"])
	require.Equal(t, 1, details.Rejected["runtime_blocked"])
	require.InDelta(t, 60, details.RetryAfterSeconds, 1)
}
