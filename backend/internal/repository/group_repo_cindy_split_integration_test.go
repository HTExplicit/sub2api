//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestCindyGroupSplitPreviewDriftAndAtomicCommit(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newGroupRepositoryWithSQL(client, integrationDB)
	suffix := time.Now().UnixNano()

	source := mustCreateGroup(t, client, &service.Group{
		Name:                 fmt.Sprintf("cindy-split-source-%d", suffix),
		Platform:             service.PlatformOpenAI,
		Status:               service.StatusActive,
		SubscriptionType:     service.SubscriptionTypeStandard,
		RateMultiplier:       1.75,
		ProfitControlEnabled: true,
		ProfitMinMargin:      0.20,
		ProfitSafetyBuffer:   0.03,
	})
	cindy := mustCreateAccount(t, client, &service.Account{
		Name:     fmt.Sprintf("cindy-split-cindy-%d", suffix),
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		Status:   service.StatusActive,
		Credentials: map[string]any{
			"api_key":  "sk-cindy-test",
			"base_url": "https://api.laxarouter.ai",
		},
	})
	ordinary := mustCreateAccount(t, client, &service.Account{
		Name:     fmt.Sprintf("cindy-split-ordinary-%d", suffix),
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		Status:   service.StatusDisabled,
		Credentials: map[string]any{
			"api_key":  "sk-ordinary-test",
			"base_url": "https://api.openai.com/v1",
		},
	})
	_, err := integrationDB.ExecContext(ctx, `
		INSERT INTO account_groups (account_id, group_id, priority, created_at)
		VALUES ($1, $3, 17, NOW()), ($2, $3, 29, NOW())
	`, cindy.ID, ordinary.ID, source.ID)
	require.NoError(t, err)

	user := mustCreateUser(t, client, &service.User{Email: fmt.Sprintf("cindy-split-%d@example.com", suffix)})
	selectedKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID:  user.ID,
		Key:     fmt.Sprintf("sk-cindy-split-%d-a", suffix),
		Name:    "selected",
		Status:  service.StatusActive,
		GroupID: &source.ID,
	})
	unselectedKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID:  user.ID,
		Key:     fmt.Sprintf("sk-cindy-split-%d-b", suffix),
		Name:    "unselected",
		Status:  service.StatusDisabled,
		GroupID: &source.ID,
	})

	targetName := fmt.Sprintf("cindy-split-target-%d", suffix)
	var targetID int64
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM api_keys WHERE id = ANY($1)", pq.Array([]int64{selectedKey.ID, unselectedKey.ID}))
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_groups WHERE account_id = ANY($1)", pq.Array([]int64{cindy.ID, ordinary.ID}))
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = ANY($1)", pq.Array([]int64{cindy.ID, ordinary.ID}))
		if targetID > 0 {
			_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE group_id = $1", targetID)
		}
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE group_id = $1", source.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM groups WHERE name IN ($1, $2)", source.Name, targetName)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM users WHERE id = $1", user.ID)
	})

	audit, err := repo.AuditCindyGroups(ctx)
	require.NoError(t, err)
	entry := findCindyAuditEntry(t, audit, source.ID)
	require.Equal(t, service.CindyGroupClassificationMixed, entry.Classification)
	require.Equal(t, int64(1), entry.CindyAccountCount)
	require.Equal(t, int64(1), entry.OrdinaryAccountCount)
	require.Equal(t, int64(2), entry.APIKeyCount)

	input := service.CindyGroupSplitInput{
		SourceKeeps: service.CindyGroupSourceKeepsOrdinary,
		TargetName:  targetName,
		APIKeyIDs:   []int64{selectedKey.ID},
	}
	preview, err := repo.PreviewCindyGroupSplit(ctx, source.ID, input)
	require.NoError(t, err)
	require.Equal(t, int64(1), preview.Preview.AccountsToMove)
	require.Equal(t, int64(1), preview.Preview.APIKeysToRebind)
	require.Equal(t, int64(1), preview.Preview.APIKeysRemaining)
	require.Equal(t, service.CindyGroupClassificationPureCindy, preview.Preview.TargetClassification)
	require.Len(t, preview.Preview.MemberFingerprint, 64)

	_, err = integrationDB.ExecContext(ctx,
		"UPDATE account_groups SET priority = 18 WHERE account_id = $1 AND group_id = $2",
		cindy.ID,
		source.ID,
	)
	require.NoError(t, err)
	staleInput := input
	staleInput.MemberFingerprint = preview.Preview.MemberFingerprint
	_, err = repo.CommitCindyGroupSplit(ctx, source.ID, staleInput)
	require.ErrorIs(t, err, service.ErrCindyGroupSplitDrift)

	fresh, err := repo.PreviewCindyGroupSplit(ctx, source.ID, input)
	require.NoError(t, err)
	input.MemberFingerprint = fresh.Preview.MemberFingerprint
	result, err := repo.CommitCindyGroupSplit(ctx, source.ID, input)
	require.NoError(t, err)
	targetID = result.TargetGroupID
	require.Positive(t, targetID)

	target, err := repo.GetByIDLite(ctx, targetID)
	require.NoError(t, err)
	require.Equal(t, source.RateMultiplier, target.RateMultiplier)
	require.Equal(t, service.PlatformOpenAI, target.Platform)
	require.Equal(t, source.Status, target.Status)
	require.True(t, target.ProfitControlEnabled)
	require.Equal(t, source.ProfitMinMargin, target.ProfitMinMargin)
	require.Equal(t, source.ProfitSafetyBuffer, target.ProfitSafetyBuffer)

	assertCindySplitMembership(t, ctx, cindy.ID, targetID, 18)
	assertCindySplitMembership(t, ctx, ordinary.ID, source.ID, 29)
	assertCindySplitKeyGroup(t, ctx, selectedKey.ID, targetID)
	assertCindySplitKeyGroup(t, ctx, unselectedKey.ID, source.ID)

	var cindyBaseURL, cindyStatus string
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT credentials ->> 'base_url', status FROM accounts WHERE id = $1",
		cindy.ID,
	).Scan(&cindyBaseURL, &cindyStatus))
	require.Equal(t, "https://api.laxarouter.ai", cindyBaseURL)
	require.Equal(t, service.StatusActive, cindyStatus)
}

func findCindyAuditEntry(t *testing.T, entries []service.CindyGroupAuditEntry, groupID int64) service.CindyGroupAuditEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.GroupID == groupID {
			return entry
		}
	}
	t.Fatalf("Cindy audit entry for group %d not found", groupID)
	return service.CindyGroupAuditEntry{}
}

func assertCindySplitMembership(t *testing.T, ctx context.Context, accountID, groupID int64, priority int) {
	t.Helper()
	var gotGroupID int64
	var gotPriority int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT group_id, priority FROM account_groups WHERE account_id = $1",
		accountID,
	).Scan(&gotGroupID, &gotPriority))
	require.Equal(t, groupID, gotGroupID)
	require.Equal(t, priority, gotPriority)
}

func assertCindySplitKeyGroup(t *testing.T, ctx context.Context, keyID, groupID int64) {
	t.Helper()
	var gotGroupID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT group_id FROM api_keys WHERE id = $1",
		keyID,
	).Scan(&gotGroupID))
	require.Equal(t, groupID, gotGroupID)
}
