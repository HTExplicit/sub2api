//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCindyGroupSplitKeepsCanonicalSourceAndTreatsLegacyLaxaAsOrdinary(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newGroupRepositoryWithSQL(client, integrationDB)
	suffix := time.Now().UnixNano()
	source := mustCreateGroup(t, client, &service.Group{Name: fmt.Sprintf("cindy-source-identity-%d", suffix), Platform: service.PlatformOpenAI, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard})
	var cindyID, legacyID, targetID int64
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_groups WHERE account_id = ANY($1)", []int64{cindyID, legacyID})
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = ANY($1)", []int64{cindyID, legacyID})
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE group_id = ANY($1)", []int64{source.ID, targetID})
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM groups WHERE id = ANY($1)", []int64{source.ID, targetID})
	})
	cindy := mustCreateAccount(t, client, &service.Account{Name: fmt.Sprintf("canonical-cindy-%d", suffix), Platform: service.PlatformCindy, WirePlatform: service.WirePlatformOpenAI, ProviderProfile: service.ProviderProfileCindyLaxaV1, Type: service.AccountTypeAPIKey, Status: service.StatusDisabled, Credentials: map[string]any{"api_key": "synthetic-canonical-key", "base_url": "https://api.laxarouter.ai"}})
	cindyID = cindy.ID
	legacy := mustCreateAccount(t, client, &service.Account{Name: fmt.Sprintf("legacy-laxa-%d", suffix), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Credentials: map[string]any{"api_key": "synthetic-legacy-key", "base_url": "https://api.laxarouter.ai"}})
	legacyID = legacy.ID
	_, err := integrationDB.ExecContext(ctx, "INSERT INTO account_groups (account_id,group_id,priority) VALUES ($1,$3,3),($2,$3,5)", cindyID, legacyID, source.ID)
	require.NoError(t, err)
	input := service.CindyGroupSplitInput{SourceKeeps: service.CindyGroupSourceKeepsCindy, TargetName: fmt.Sprintf("ordinary-target-%d", suffix)}
	preview, err := repo.PreviewCindyGroupSplit(ctx, source.ID, input)
	require.NoError(t, err)
	require.EqualValues(t, 1, preview.Preview.CindyAccountCount)
	require.EqualValues(t, 1, preview.Preview.OrdinaryAccountCount)
	input.MemberFingerprint = preview.Preview.MemberFingerprint
	result, err := repo.CommitCindyGroupSplit(ctx, source.ID, input)
	require.NoError(t, err)
	targetID = result.TargetGroupID
	after, err := repo.GetByIDLite(ctx, source.ID)
	require.NoError(t, err)
	require.Equal(t, service.PlatformCindy, after.Platform)
	require.Equal(t, service.ProviderProfileCindyLaxaV1, after.ProviderProfile)
	target, err := repo.GetByIDLite(ctx, targetID)
	require.NoError(t, err)
	require.Equal(t, service.PlatformOpenAI, target.Platform)
	assertCindySplitMembership(t, ctx, cindyID, source.ID, 3)
	assertCindySplitMembership(t, ctx, legacyID, targetID, 5)
}
