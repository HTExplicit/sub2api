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
	deletedGroup := mustCreateGroup(t, client, &service.Group{
		Name:             fmt.Sprintf("cindy-split-deleted-%d", suffix),
		Platform:         service.PlatformOpenAI,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	})
	_, err := integrationDB.ExecContext(ctx, "UPDATE groups SET deleted_at = NOW() WHERE id = $1", deletedGroup.ID)
	require.NoError(t, err)
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
	_, err = integrationDB.ExecContext(ctx, `
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
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM groups WHERE id = $1", deletedGroup.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM groups WHERE name IN ($1, $2)", source.Name, targetName)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM users WHERE id = $1", user.ID)
	})

	audit, err := repo.AuditCindyGroups(ctx)
	require.NoError(t, err)
	for _, auditEntry := range audit {
		require.NotEqual(t, deletedGroup.ID, auditEntry.GroupID, "soft-deleted groups must not be audited")
	}
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

	// Normal request traffic updates last-used timestamps and generic updated_at
	// columns, while health/quota processing can also change row status. None of
	// those writes changes group membership, Cindy classification, membership
	// priority, or API-key binding, so they must not invalidate the preview.
	_, err = integrationDB.ExecContext(ctx,
		"UPDATE accounts SET last_used_at = NOW(), status = $2, updated_at = NOW() WHERE id = $1",
		cindy.ID,
		service.StatusError,
	)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx,
		"UPDATE api_keys SET last_used_at = NOW(), status = $2, updated_at = NOW() WHERE id = $1",
		selectedKey.ID,
		service.StatusDisabled,
	)
	require.NoError(t, err)
	afterRuntimeWrites, err := repo.PreviewCindyGroupSplit(ctx, source.ID, input)
	require.NoError(t, err)
	require.Equal(t, preview.Preview.MemberFingerprint, afterRuntimeWrites.Preview.MemberFingerprint)
	_, err = integrationDB.ExecContext(ctx, "UPDATE accounts SET status = $2, updated_at = NOW() WHERE id = $1", cindy.ID, service.StatusActive)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, "UPDATE api_keys SET status = $2, updated_at = NOW() WHERE id = $1", selectedKey.ID, service.StatusActive)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, "UPDATE groups SET updated_at = NOW() WHERE id = $1", source.ID)
	require.NoError(t, err)
	afterTimestampOnlyWrites, err := repo.PreviewCindyGroupSplit(ctx, source.ID, input)
	require.NoError(t, err)
	require.Equal(t, preview.Preview.MemberFingerprint, afterTimestampOnlyWrites.Preview.MemberFingerprint)

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

func TestCindyGroupSplitRollsBackAllWritesWhenOutboxInsertFails(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newGroupRepositoryWithSQL(client, integrationDB)
	suffix := time.Now().UnixNano()

	source := mustCreateGroup(t, client, &service.Group{
		Name:             fmt.Sprintf("cindy-split-rollback-source-%d", suffix),
		Platform:         service.PlatformOpenAI,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	})
	cindy := mustCreateAccount(t, client, &service.Account{
		Name:     fmt.Sprintf("cindy-split-rollback-cindy-%d", suffix),
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		Status:   service.StatusActive,
		Credentials: map[string]any{
			"api_key":  "sk-cindy-rollback-test",
			"base_url": "https://api.laxarouter.ai",
		},
	})
	ordinary := mustCreateAccount(t, client, &service.Account{
		Name:     fmt.Sprintf("cindy-split-rollback-ordinary-%d", suffix),
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		Status:   service.StatusActive,
		Credentials: map[string]any{
			"api_key":  "sk-ordinary-rollback-test",
			"base_url": "https://api.openai.com/v1",
		},
	})
	_, err := integrationDB.ExecContext(ctx, `
		INSERT INTO account_groups (account_id, group_id, priority, created_at)
		VALUES ($1, $3, 17, NOW()), ($2, $3, 29, NOW())
	`, cindy.ID, ordinary.ID, source.ID)
	require.NoError(t, err)

	user := mustCreateUser(t, client, &service.User{
		Email: fmt.Sprintf("cindy-split-rollback-%d@example.com", suffix),
	})
	selectedKey := mustCreateApiKey(t, client, &service.APIKey{
		UserID:  user.ID,
		Key:     fmt.Sprintf("sk-cindy-split-rollback-%d", suffix),
		Name:    "selected",
		Status:  service.StatusActive,
		GroupID: &source.ID,
	})

	targetName := fmt.Sprintf("cindy-split-rollback-target-%d", suffix)
	functionName := fmt.Sprintf("fail_cindy_split_outbox_%d", suffix)
	triggerName := fmt.Sprintf("fail_cindy_split_outbox_trigger_%d", suffix)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON scheduler_outbox",
			triggerName,
		))
		_, _ = integrationDB.ExecContext(context.Background(), fmt.Sprintf(
			"DROP FUNCTION IF EXISTS %s()",
			functionName,
		))
		_, _ = integrationDB.ExecContext(context.Background(), `
			DELETE FROM scheduler_outbox
			WHERE group_id = $1
			   OR group_id IN (SELECT id FROM groups WHERE name = $2)
			   OR COALESCE(payload, '{}'::jsonb) @>
			      jsonb_build_object('account_ids', jsonb_build_array($3::bigint))
			   OR COALESCE(payload, '{}'::jsonb) @>
			      jsonb_build_object('account_ids', jsonb_build_array($4::bigint))
		`, source.ID, targetName, cindy.ID, ordinary.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM api_keys WHERE id = $1", selectedKey.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_groups WHERE account_id = ANY($1)", pq.Array([]int64{cindy.ID, ordinary.ID}))
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = ANY($1)", pq.Array([]int64{cindy.ID, ordinary.ID}))
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM groups WHERE name IN ($1, $2)", source.Name, targetName)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM users WHERE id = $1", user.ID)
	})

	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.group_id IS NOT NULL AND EXISTS (
				SELECT 1 FROM groups WHERE id = NEW.group_id AND name = '%s'
			) THEN
				RAISE EXCEPTION 'forced Cindy split outbox failure';
			END IF;
			RETURN NEW;
		END;
		$$`, functionName, targetName))
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(
		"CREATE TRIGGER %s BEFORE INSERT ON scheduler_outbox FOR EACH ROW EXECUTE FUNCTION %s()",
		triggerName,
		functionName,
	))
	require.NoError(t, err)

	input := service.CindyGroupSplitInput{
		SourceKeeps: service.CindyGroupSourceKeepsOrdinary,
		TargetName:  targetName,
		APIKeyIDs:   []int64{selectedKey.ID},
	}
	preview, err := repo.PreviewCindyGroupSplit(ctx, source.ID, input)
	require.NoError(t, err)
	input.MemberFingerprint = preview.Preview.MemberFingerprint

	_, err = repo.CommitCindyGroupSplit(ctx, source.ID, input)
	require.ErrorContains(t, err, "forced Cindy split outbox failure")

	var targetCount, sourceMemberships, movedMemberships, sourceOutboxEvents int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM groups WHERE name = $1",
		targetName,
	).Scan(&targetCount))
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM account_groups WHERE group_id = $1 AND account_id = ANY($2)",
		source.ID,
		pq.Array([]int64{cindy.ID, ordinary.ID}),
	).Scan(&sourceMemberships))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM account_groups ag
		JOIN groups g ON g.id = ag.group_id
		WHERE g.name = $1 AND ag.account_id = $2
	`, targetName, cindy.ID).Scan(&movedMemberships))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM scheduler_outbox
		WHERE group_id = $1
		   OR COALESCE(payload, '{}'::jsonb) @>
		      jsonb_build_object('account_ids', jsonb_build_array($2::bigint))
	`, source.ID, cindy.ID).Scan(&sourceOutboxEvents))

	require.Zero(t, targetCount, "the target group must roll back")
	require.Equal(t, 2, sourceMemberships, "all source memberships must remain")
	require.Zero(t, movedMemberships, "no target membership may survive")
	assertCindySplitKeyGroup(t, ctx, selectedKey.ID, source.ID)
	require.Zero(t, sourceOutboxEvents, "all split outbox events must roll back")
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
