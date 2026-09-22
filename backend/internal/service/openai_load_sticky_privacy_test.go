package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type loadPrivacySnapshotRepo struct {
	*guardianAffinityAccountRepo
	snapshot []Account
}

func (r *loadPrivacySnapshotRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]Account, error) {
	if r.snapshot != nil {
		return r.snapshot, nil
	}
	return r.guardianAffinityAccountRepo.ListSchedulableByGroupIDAndPlatform(ctx, groupID, platform)
}

// Exercise the real load-aware Layer-1 selection with only repository/cache
// adapters faked. No upstream, database, Redis or model request is made.
func TestOpenAILoadAwareStickyHonorsGroupPrivacy(t *testing.T) {
	for _, test := range []struct {
		name          string
		required      bool
		stickyPrivate bool
		groupError    error
		wantFallback  bool
		stalePool     bool
	}{
		{name: "required_privacy_rejects_stale_binding", required: true, wantFallback: true},
		{name: "privacy_policy_lookup_failure_is_closed", groupError: errors.New("fixture unavailable"), wantFallback: true},
		{name: "private_sticky_remains_usable", required: true, stickyPrivate: true},
		{name: "group_without_requirement_retains_sticky"},
		{name: "fresh_database_privacy_overrides_stale_pool", required: true, wantFallback: true, stalePool: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			const stickyID, fallbackID = int64(49831), int64(49832)
			groupID := int64(4983)
			accounts := []Account{
				{ID: stickyID, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
					Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID}},
				{ID: fallbackID, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
					Schedulable: true, Concurrency: 1, Priority: 5, GroupIDs: []int64{groupID},
					Extra: map[string]any{"privacy_mode": PrivacyModeTrainingOff}},
			}
			if test.stickyPrivate {
				accounts[0].Extra = map[string]any{"privacy_mode": PrivacyModeTrainingOff}
			}
			repo := &loadPrivacySnapshotRepo{guardianAffinityAccountRepo: &guardianAffinityAccountRepo{schedulerGroupAwareOpenAIAccountRepo: schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}}}}
			session := "privacy-load-sticky"
			if test.stalePool {
				repo.snapshot = append([]Account(nil), accounts...)
				repo.snapshot[0].Extra = map[string]any{"privacy_mode": PrivacyModeTrainingOff}
				session = ""
			}
			cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:privacy-load-sticky": stickyID}}
			var acquired []int64
			svc := &OpenAIGatewayService{
				accountRepo: repo, cache: cache,
				cfg: &config.Config{Gateway: config.GatewayConfig{Scheduling: config.GatewaySchedulingConfig{
					LoadBatchEnabled: true, StickySessionMaxWaiting: 3, StickySessionWaitTimeout: time.Second,
					FallbackMaxWaiting: 10, FallbackWaitTimeout: time.Second,
				}}},
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquiredIDs: &acquired}),
				schedulerSnapshot: &SchedulerSnapshotService{accountRepo: repo, groupRepo: guardianAffinityGroupRepo{
					group: &Group{ID: groupID, RequirePrivacySet: test.required}, err: test.groupError,
				}},
			}
			selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, session, "gpt-5.4", nil)
			require.NoError(t, err)
			require.NotNil(t, selection)
			if selection.ReleaseFunc != nil {
				defer selection.ReleaseFunc()
			}
			wantID := stickyID
			if test.wantFallback {
				wantID = fallbackID
			}
			require.Equal(t, wantID, selection.Account.ID)
			require.Equal(t, []int64{wantID}, acquired, "privacy must be checked before reserving the sticky account")
			if test.wantFallback && !test.stalePool {
				require.Equal(t, 1, cache.deletedSessions["openai:privacy-load-sticky"])
			} else {
				require.Zero(t, cache.deletedSessions["openai:privacy-load-sticky"])
			}
			require.Zero(t, repo.setErrorCalls, "a group-specific miss must not disable a shared account")
			require.True(t, accounts[0].Schedulable)
			require.Equal(t, StatusActive, accounts[0].Status)
		})
	}
}
