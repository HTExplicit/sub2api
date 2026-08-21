//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	dbgroup "github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCindyInsufficientDeleteIsProtectedAndRejectsStalePreview(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newAccountRepositoryWithSQL(client, integrationDB, nil)
	baseline, err := repo.PreviewCindyInsufficientDeletion(ctx)
	require.NoError(t, err)
	require.Zero(t, baseline.Count, "integration database must not contain pre-existing eligible Cindy cleanup candidates")

	stamp := time.Now().UTC()
	namePrefix := fmt.Sprintf("cindy-delete-%d", time.Now().UnixNano())
	cindyCredentials := map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": "test"}
	createAccount := func(suffix string, credentials map[string]any, status string, marked bool) *service.Account {
		account := &service.Account{
			Name: namePrefix + "-" + suffix, Platform: service.PlatformCindy, WirePlatform: service.WirePlatformOpenAI,
			ProviderProfile: service.ProviderProfileCindyLaxaV1, Type: service.AccountTypeAPIKey,
			Status: status, Schedulable: true, Credentials: credentials,
		}
		if marked {
			account.CindyBalanceInsufficientAt = &stamp
		}
		return mustCreateAccount(t, client, account)
	}

	eligibleOne := createAccount("eligible-1", cindyCredentials, service.StatusActive, true)
	manualDisabled := createAccount("disabled", cindyCredentials, service.StatusDisabled, true)
	manualPaused := createAccount("paused", cindyCredentials, service.StatusActive, true)
	_, err = client.Account.UpdateOneID(manualPaused.ID).SetSchedulable(false).Save(ctx)
	require.NoError(t, err)
	nonCindy := createAccount("non-cindy", map[string]any{"base_url": "https://api.laxarouter.ai/v1"}, service.StatusActive, true)
	unmarkedCindy := createAccount("unmarked", cindyCredentials, service.StatusActive, false)
	createdAccountIDs := []int64{eligibleOne.ID, manualDisabled.ID, manualPaused.ID, nonCindy.ID, unmarkedCindy.ID}
	var outboxEventID int64

	group := mustCreateGroup(t, client, &service.Group{
		Name: namePrefix + "-group", Platform: service.PlatformCindy, WirePlatform: service.WirePlatformOpenAI,
		ProviderProfile: service.ProviderProfileCindyLaxaV1, RateMultiplier: 1,
	})
	t.Cleanup(func() {
		if outboxEventID != 0 {
			_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM scheduler_outbox WHERE id = $1`, outboxEventID)
		}
		_, _ = client.Account.Delete().Where(dbaccount.IDIn(createdAccountIDs...)).Exec(mixins.SkipSoftDelete(context.Background()))
		_, _ = client.Group.Delete().Where(dbgroup.IDEQ(group.ID)).Exec(mixins.SkipSoftDelete(context.Background()))
	})
	_, err = client.AccountGroup.Create().SetAccountID(eligibleOne.ID).SetGroupID(group.ID).SetPriority(50).Save(ctx)
	require.NoError(t, err)
	var planID int64
	err = integrationDB.QueryRowContext(ctx, `INSERT INTO scheduled_test_plans (account_id) VALUES ($1) RETURNING id`, eligibleOne.ID).Scan(&planID)
	require.NoError(t, err)

	preview, err := repo.PreviewCindyInsufficientDeletion(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, preview.Count)

	eligibleTwo := createAccount("eligible-2", cindyCredentials, service.StatusActive, true)
	createdAccountIDs = append(createdAccountIDs, eligibleTwo.ID)
	_, err = repo.DeleteCindyInsufficient(ctx, preview.Count, preview.Fingerprint)
	require.ErrorIs(t, err, service.ErrCindyInsufficientDeleteChanged)
	for _, accountID := range []int64{eligibleOne.ID, eligibleTwo.ID} {
		_, getErr := client.Account.Get(ctx, accountID)
		require.NoError(t, getErr, "stale preview must roll back without deleting account %d", accountID)
	}

	var outboxBefore int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM scheduler_outbox`).Scan(&outboxBefore))
	preview, err = repo.PreviewCindyInsufficientDeletion(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, preview.Count)
	result, err := repo.DeleteCindyInsufficient(ctx, preview.Count, preview.Fingerprint)
	require.NoError(t, err)
	require.Equal(t, 2, result.DeletedCount)
	require.ElementsMatch(t, []int64{eligibleOne.ID, eligibleTwo.ID}, result.DeletedAccountIDs)

	for _, accountID := range []int64{eligibleOne.ID, eligibleTwo.ID} {
		_, getErr := client.Account.Query().Where(dbaccount.IDEQ(accountID)).Only(mixins.SkipSoftDelete(ctx))
		require.True(t, dbent.IsNotFound(getErr), "eligible account %d must be physically deleted", accountID)
	}
	for _, accountID := range []int64{manualDisabled.ID, manualPaused.ID, nonCindy.ID, unmarkedCindy.ID} {
		_, getErr := client.Account.Get(ctx, accountID)
		require.NoError(t, getErr, "protected account %d must remain", accountID)
	}

	var eligibleBindingCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_groups WHERE account_id = $1`, eligibleOne.ID).Scan(&eligibleBindingCount))
	require.Zero(t, eligibleBindingCount)
	var planCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_test_plans WHERE id = $1`, planID).Scan(&planCount))
	require.Zero(t, planCount)

	var payloadRaw []byte
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT id, payload FROM scheduler_outbox
		WHERE id > $1 AND event_type = $2
		ORDER BY id DESC LIMIT 1`, outboxBefore, service.SchedulerOutboxEventAccountBulkChanged).Scan(&outboxEventID, &payloadRaw))
	var payload struct {
		AccountIDs []int64 `json:"account_ids"`
		GroupIDs   []int64 `json:"group_ids"`
	}
	require.NoError(t, json.Unmarshal(payloadRaw, &payload))
	require.ElementsMatch(t, []int64{eligibleOne.ID, eligibleTwo.ID}, payload.AccountIDs)
	require.Equal(t, []int64{group.ID}, payload.GroupIDs)
}
