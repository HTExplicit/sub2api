//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func schedulerTicketAccount(id int64, groupID int64, withTicket bool) *Account {
	account := &Account{
		ID:          id,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Priority:    0,
		GroupIDs:    []int64{groupID},
		Credentials: map[string]any{
			"access_token":       "scheduler-ticket-token",
			"chatgpt_account_id": "scheduler-principal",
			"model_mapping":      map[string]any{"gpt-6-astra": "gpt-6-astra"},
		},
	}
	if withTicket {
		setTicketTestProjection(account, "gpt-6-astra", time.Now().Add(time.Hour))
	}
	return account
}

func schedulerTicketMetadataProjection(account *Account) *Account {
	projected := *account
	projected.Credentials = map[string]any{
		"model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"},
	}
	projected.Extra = nil
	return &projected
}

func schedulerTicketConfig() *config.Config {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			OpenAICodexTicket: config.OpenAICodexTicketConfig{
				Enabled:      true,
				TargetLength: 292,
				TTLSeconds:   3600,
				FailClosed:   true,
				Models:       []string{"gpt-6-astra"},
			},
		},
	}
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	return cfg
}

func TestCodexTicketCandidateGateDefersCredentialChecks(t *testing.T) {
	svc := ticketTestService(t, schedulerTicketConfig().Gateway.OpenAICodexTicket, nil)
	ctx := context.Background()
	full := schedulerTicketAccount(41, 101, true)

	require.False(t, svc.isOpenAIAccountRequestRuntimeBlockedContext(ctx, full, "gpt-6-astra"))

	// The projection has neither the ticket nor the identity credentials. The
	// candidate gate must not turn that partial view into a fail-closed block.
	projected := schedulerTicketMetadataProjection(full)
	require.False(t, svc.isOpenAIAccountCandidateRuntimeBlockedContext(ctx, projected, "gpt-6-astra"))

	// A projection that carries a ticket without the owner identity is still
	// rejected by the authoritative gate; this preserves principal binding.
	projectedWithTicket := *projected
	projectedWithTicket.Extra = full.Extra
	require.False(t, svc.isOpenAIAccountCandidateRuntimeBlockedContext(ctx, &projectedWithTicket, "gpt-6-astra"))
	require.True(t, svc.isOpenAIAccountRequestRuntimeBlockedContext(ctx, &projectedWithTicket, "gpt-6-astra"))

	// Missing, expired and wrong-principal grants remain blocked
	// once the authoritative account is available.
	ticketless := *full
	ticketless.Extra = nil
	require.False(t, svc.isOpenAIAccountCandidateRuntimeBlockedContext(ctx, &ticketless, "gpt-6-astra"))
	require.True(t, svc.isOpenAIAccountRequestRuntimeBlockedContext(ctx, &ticketless, "gpt-6-astra"))

	changedPrincipal := *full
	changedPrincipal.Credentials = map[string]any{
		"access_token":       "scheduler-ticket-token",
		"chatgpt_account_id": "another-principal",
	}
	require.True(t, svc.isOpenAIAccountRequestRuntimeBlockedContext(ctx, &changedPrincipal, "gpt-6-astra"))

	expired := *full
	setTicketTestProjection(&expired, "gpt-6-astra", time.Now().Add(-time.Minute))
	require.True(t, svc.isOpenAIAccountRequestRuntimeBlockedContext(ctx, &expired, "gpt-6-astra"))

	// Models outside the configured ticket scope keep their existing behavior.
	require.False(t, svc.isOpenAIAccountRequestRuntimeBlockedContext(ctx, full, "gpt-5.5"))
}

func TestSelectAccountWithSchedulerDefersTicketGateToAuthoritativeAccount(t *testing.T) {
	for _, tc := range []struct {
		name     string
		advanced bool
	}{
		{name: "legacy_load_aware"},
		{name: "advanced_scheduler", advanced: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			ctx := context.Background()
			groupID := int64(92001)
			full := schedulerTicketAccount(42, groupID, true)
			projected := schedulerTicketMetadataProjection(full)
			snapshotCache := &openAISnapshotCacheStub{
				snapshotAccounts: []*Account{projected},
				accountsByID:     map[int64]*Account{full.ID: full},
			}
			svc := &OpenAIGatewayService{
				accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{*full}},
				cfg:                schedulerTicketConfig(),
				schedulerSnapshot:  &SchedulerSnapshotService{cache: snapshotCache},
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
			}
			if tc.advanced {
				svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true")
			}
			svc.pluginManager = ticketTestManager(t, svc.cfg.Gateway.OpenAICodexTicket, nil)

			selection, decision, err := svc.SelectAccountWithScheduler(
				ctx, &groupID, "", "", "gpt-6-astra", nil, OpenAIUpstreamTransportAny, false,
			)

			require.NoError(t, err)
			require.NotNil(t, selection)
			require.NotNil(t, selection.Account)
			require.Equal(t, full.ID, selection.Account.ID)
			require.Equal(t, openAIAccountScheduleLayerLoadBalance, decision.Layer)
			require.NotNil(t, selection.Account.Extra[PluginAccountProjectionKey])
		})
	}
}

func TestSelectAccountWithSchedulerKeepsFailClosedForTicketlessAccounts(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	ctx := context.Background()
	groupID := int64(92002)
	ticketless := schedulerTicketAccount(43, groupID, false)
	projected := schedulerTicketMetadataProjection(ticketless)
	snapshotCache := &openAISnapshotCacheStub{
		snapshotAccounts: []*Account{projected},
		accountsByID:     map[int64]*Account{ticketless.ID: ticketless},
	}
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{*ticketless}},
		cfg:                schedulerTicketConfig(),
		schedulerSnapshot:  &SchedulerSnapshotService{cache: snapshotCache},
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
	svc.pluginManager = ticketTestManager(t, svc.cfg.Gateway.OpenAICodexTicket, nil)

	selection, _, err := svc.SelectAccountWithScheduler(
		ctx, &groupID, "", "", "gpt-6-astra", nil, OpenAIUpstreamTransportAny, false,
	)

	require.Nil(t, selection)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNoAvailableAccounts), "fail-closed semantics must remain on the authoritative recheck")
}
