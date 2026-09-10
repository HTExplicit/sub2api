//go:build unit

package repository

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestCapabilityPublicationApplyGroupMergePreservesExplicitSettingsAndManualRoutes(t *testing.T) {
	repo, mock := newPublicationRepoTest(t)
	const selector = "s2pub-g23-fixture-branch"
	gp := service.CapabilityPublicationGroupPatch{
		GroupID: 23, Name: "fixture-public-group", Platform: service.PlatformComposite,
		RateMultiplier: 0.2, Status: service.StatusActive,
		ManagedModelRoutes: domain.ManagedModelRoutesConfig{
			Version: 2, Enabled: true,
			Routes: []domain.ManagedModelRoute{{
				PublicModel: "claude-fable-5-1", Endpoints: []string{"messages", "responses"},
				Branches: []domain.ManagedModelRouteBranch{{
					Selector: selector, TargetPlatform: service.PlatformAnthropic,
					UpstreamProtocol: "messages", Endpoints: []string{"messages", "responses"},
					Accounts: []domain.ManagedModelRouteAccount{{
						AccountID: 7, UpstreamModel: "claude-fable-5-1", AccountFingerprint: "fixture-fingerprint",
						Endpoints: []string{"messages", "responses"},
					}},
				}},
			}},
		},
		ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"claude-fable-5-1"}},
		// Nonempty dispatch rules must not silently turn an explicit false back
		// on, and publication must not clear the preexisting default mapping.
		MessagesDispatch: service.OpenAIMessagesDispatchModelConfig{
			ExactModelMappings: map[string]string{"private-model": "private-mapped-model"},
		},
		AllowMessagesDispatch: false,
		DefaultMappedModel:    "private-default-model",
		ChannelMapping: map[string]map[string]string{
			service.PlatformAnthropic: {"claude-fable-5-1": selector},
		},
		ChannelFeatures: "[]", ChannelFeaturesConfig: map[string]any{},
		CompositeRoutes: []service.CompositeModelRoute{
			{
				ID: 301, GroupID: 23, PublicModel: "existing-private-prefix-", MatchType: service.CompositeRouteMatchPrefix,
				TargetPlatform: service.PlatformAnthropic, UpstreamModel: "private/upstream",
				Endpoint: service.CompositeRouteEndpointMessages, Priority: 7, Enabled: false, Notes: "retain manual identity",
			},
			{
				GroupID: 23, PublicModel: "new-prefix-", MatchType: service.CompositeRouteMatchPrefix,
				TargetPlatform: service.PlatformOpenAI, UpstreamModel: "exact/configured-target",
				Endpoint: service.CompositeRouteEndpointChatCompletions, Priority: 17, Enabled: false, Notes: "new configured route",
			},
		},
	}
	marshal := func(value any) string {
		body, err := json.Marshal(value)
		require.NoError(t, err)
		return string(body)
	}
	before := marshal(gp)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE groups SET platform=$2,wire_platform=$2,managed_model_routes=$3::jsonb,model_allowlist=$4::jsonb,messages_dispatch_model_config=$5::jsonb,allow_messages_dispatch=$6,default_mapped_model=$8,status=$7,updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL`)).
		WithArgs(int64(23), service.PlatformComposite, marshal(gp.ManagedModelRoutes), marshal(gp.ModelAllowlist), marshal(gp.MessagesDispatch), false, service.StatusActive, "private-default-model").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM channels WHERE name=$1 FOR UPDATE`)).
		WithArgs("public-capabilities-g23").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(41))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM channel_groups WHERE channel_id=$1 AND group_id=$2) AND NOT EXISTS(SELECT 1 FROM channel_groups WHERE channel_id=$1 AND group_id<>$2)`)).
		WithArgs(int64(41), int64(23)).WillReturnRows(sqlmock.NewRows([]string{"only_this_group"}).AddRow(true))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE channels SET model_mapping=$2::jsonb,billing_model_source='requested',restrict_models=false,features=$3,features_config=$4::jsonb,apply_pricing_to_account_stats=$5,updated_at=NOW() WHERE id=$1`)).
		WithArgs(int64(41), marshal(gp.ChannelMapping), "[]", "{}", false).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM channel_model_pricing WHERE channel_id = $1`)).
		WithArgs(int64(41)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM channel_account_stats_pricing_rules WHERE channel_id = $1`)).
		WithArgs(int64(41)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO channel_groups(channel_id,group_id) VALUES($1,$2) ON CONFLICT(group_id) DO UPDATE SET channel_id=EXCLUDED.channel_id`)).
		WithArgs(int64(41), int64(23)).WillReturnResult(sqlmock.NewResult(0, 1))
	// The existing manual row is excluded from retirement and is not reinserted
	// or rewritten. Exact expectations reject blanket deletion or normalization.
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE composite_model_routes SET deleted_at=NOW(),updated_at=NOW() WHERE group_id=$1 AND deleted_at IS NULL AND NOT (id=ANY($2))`)).
		WithArgs(int64(23), pq.Array([]int64{301})).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO composite_model_routes(group_id,public_model,match_type,target_platform,upstream_model,endpoint,priority,enabled,notes) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`)).
		WithArgs(int64(23), "new-prefix-", service.CompositeRouteMatchPrefix, service.PlatformOpenAI, "exact/configured-target", service.CompositeRouteEndpointChatCompletions, 17, false, "new configured route").
		WillReturnResult(sqlmock.NewResult(302, 1))
	mock.ExpectCommit()

	tx, err := repo.db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	require.NoError(t, publicationApplyGroup(context.Background(), tx, gp))
	require.NoError(t, tx.Commit())
	require.Equal(t, before, marshal(gp), "SQL application must not mutate the approved merge plan")
}
