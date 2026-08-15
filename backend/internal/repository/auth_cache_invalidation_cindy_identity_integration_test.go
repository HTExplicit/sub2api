//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAuthCacheInvalidationTriggersCoverCindyIdentityAndMembership(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	group := mustCreateGroup(t, integrationEntClient, &service.Group{
		Name: fmt.Sprintf("cindy-identity-trigger-%d", suffix), Platform: service.PlatformOpenAI, RateMultiplier: 1,
	})
	otherGroup := mustCreateGroup(t, integrationEntClient, &service.Group{
		Name: fmt.Sprintf("cindy-identity-trigger-other-%d", suffix), Platform: service.PlatformOpenAI, RateMultiplier: 1,
	})
	user := mustCreateUser(t, integrationEntClient, &service.User{
		Email: fmt.Sprintf("cindy-identity-trigger-%d@example.com", suffix), Concurrency: 5,
	})
	keyValue := fmt.Sprintf("sk-cindy-identity-trigger-%d", suffix)
	key := &service.APIKey{
		UserID: user.ID, GroupID: &group.ID, Key: keyValue,
		Name: "cindy identity trigger", Status: service.StatusActive,
	}
	require.NoError(t, NewAPIKeyRepository(integrationEntClient, integrationDB).Create(ctx, key))
	account, err := integrationEntClient.Account.Create().
		SetName(fmt.Sprintf("cindy-identity-trigger-account-%d", suffix)).
		SetPlatform(service.PlatformOpenAI).
		SetType(service.AccountTypeAPIKey).
		SetStatus(service.StatusActive).
		SetCredentials(map[string]any{
			"api_key":  "upstream-secret",
			"base_url": "https://api.laxarouter.ai",
		}).
		Save(ctx)
	require.NoError(t, err)

	sum := sha256.Sum256([]byte(keyValue))
	cacheKey := hex.EncodeToString(sum[:])
	clear := func() {
		_, clearErr := integrationDB.ExecContext(ctx, "DELETE FROM auth_cache_invalidation_outbox WHERE cache_key = $1", cacheKey)
		require.NoError(t, clearErr)
	}
	count := func() int {
		var value int
		require.NoError(t, integrationDB.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM auth_cache_invalidation_outbox WHERE cache_key = $1", cacheKey).Scan(&value))
		return value
	}
	clear()
	t.Cleanup(clear)
	t.Cleanup(func() {
		_, cleanupErr := integrationDB.ExecContext(ctx, "DELETE FROM account_groups WHERE account_id = $1", account.ID)
		require.NoError(t, cleanupErr)
		_, cleanupErr = integrationDB.ExecContext(ctx, "DELETE FROM accounts WHERE id = $1", account.ID)
		require.NoError(t, cleanupErr)
		_, cleanupErr = integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE id = $1", key.ID)
		require.NoError(t, cleanupErr)
		_, cleanupErr = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", user.ID)
		require.NoError(t, cleanupErr)
		_, cleanupErr = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id IN ($1, $2)", group.ID, otherGroup.ID)
		require.NoError(t, cleanupErr)
	})

	_, err = integrationDB.ExecContext(ctx,
		"INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, 50, NOW())",
		account.ID, group.ID)
	require.NoError(t, err)
	require.Equal(t, 1, count(), "membership insert must invalidate group API keys")
	clear()

	_, err = integrationDB.ExecContext(ctx,
		"UPDATE account_groups SET priority = priority + 1 WHERE account_id = $1 AND group_id = $2",
		account.ID, group.ID)
	require.NoError(t, err)
	require.Zero(t, count(), "priority-only membership updates must not invalidate identity")

	_, err = integrationDB.ExecContext(ctx, "UPDATE accounts SET schedulable = NOT schedulable WHERE id = $1", account.ID)
	require.NoError(t, err)
	require.Zero(t, count(), "runtime schedulability updates must not invalidate identity")

	_, err = integrationDB.ExecContext(ctx, `
		UPDATE accounts
		SET credentials = jsonb_build_object('api_key', 'upstream-secret', 'base_url', 'https://api.openai.com')
		WHERE id = $1`, account.ID)
	require.NoError(t, err)
	require.Equal(t, 1, count(), "credentials changes must invalidate group API keys")
	clear()

	_, err = integrationDB.ExecContext(ctx, "UPDATE accounts SET status = 'disabled' WHERE id = $1", account.ID)
	require.NoError(t, err)
	require.Equal(t, 1, count(), "status changes must invalidate group API keys")
	clear()

	_, err = integrationDB.ExecContext(ctx,
		"UPDATE account_groups SET group_id = $1 WHERE account_id = $2 AND group_id = $3",
		otherGroup.ID, account.ID, group.ID)
	require.NoError(t, err)
	require.Equal(t, 1, count(), "membership moves must invalidate the old bound group")
	clear()

	_, err = integrationDB.ExecContext(ctx,
		"UPDATE account_groups SET group_id = $1 WHERE account_id = $2 AND group_id = $3",
		group.ID, account.ID, otherGroup.ID)
	require.NoError(t, err)
	clear()

	_, err = integrationDB.ExecContext(ctx,
		"DELETE FROM account_groups WHERE account_id = $1 AND group_id = $2",
		account.ID, group.ID)
	require.NoError(t, err)
	require.Equal(t, 1, count(), "membership deletion must invalidate group API keys")
}
