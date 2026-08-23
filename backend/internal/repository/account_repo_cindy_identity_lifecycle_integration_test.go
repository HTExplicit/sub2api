//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newCindyIdentityLifecycleSchedulerCache(t *testing.T) service.SchedulerCache {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return newSchedulerCacheWithChunkSizes(rdb, defaultSchedulerSnapshotMGetChunkSize, defaultSchedulerSnapshotWriteChunkSize)
}

func TestDeleteCanonicalCindyAccountRetiresIdentityAndDeletesHealth(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	cache := newCindyIdentityLifecycleSchedulerCache(t)
	repo := newAccountRepositoryWithSQL(client, integrationDB, cache)
	suffix := time.Now().UnixNano()
	account := &service.Account{
		Name: fmt.Sprintf("cindy-delete-%d", suffix), Platform: service.PlatformCindy,
		WirePlatform: service.WirePlatformOpenAI, ProviderProfile: service.ProviderProfileCindyLaxaV1,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_key": fmt.Sprintf("key-%d", suffix), "base_url": "https://api.laxarouter.ai"},
		Extra:       map[string]any{},
	}
	require.NoError(t, repo.Create(ctx, account))
	fingerprint, err := service.AccountCredentialFingerprint(
		service.ProviderProfileCindyLaxaV1,
		service.AccountTypeAPIKey,
		"https://api.laxarouter.ai",
		account.GetCredential("api_key"),
	)
	require.NoError(t, err)
	var identityID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		INSERT INTO account_credential_identities
			(account_id, provider_profile, auth_type, normalized_base_url, fingerprint, generation, active)
		VALUES ($1, 'cindy_laxa_v1', 'apikey', 'https://api.laxarouter.ai', $2, 1, TRUE)
		RETURNING id`, account.ID, fingerprint).Scan(&identityID))
	_, err = integrationDB.ExecContext(ctx, `
		INSERT INTO cindy_health_states
			(account_id, credential_identity_id, credential_generation, episode_id, status, evidence,
			 observed_at, quarantine_until)
		VALUES ($1, $2, 1, 'delete-test', 'quarantined', 'test', NOW(), NOW() + INTERVAL '1 hour')`,
		account.ID, identityID)
	require.NoError(t, err)
	require.NoError(t, cache.SetAccount(ctx, account))
	_, err = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM cindy_health_states WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_credential_identities WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", account.ID)
	})

	require.NoError(t, repo.Delete(ctx, account.ID))

	var active bool
	var retiredAt *time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT active, retired_at FROM account_credential_identities WHERE id = $1", identityID).
		Scan(&active, &retiredAt))
	require.False(t, active)
	require.NotNil(t, retiredAt)
	var healthCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM cindy_health_states WHERE account_id = $1", account.ID).
		Scan(&healthCount))
	require.Zero(t, healthCount)
	cached, err := cache.GetAccount(ctx, account.ID)
	require.NoError(t, err)
	require.Nil(t, cached, "an owned committed delete must evict the account cache immediately")
	var outboxCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM scheduler_outbox WHERE account_id = $1", account.ID).
		Scan(&outboxCount))
	require.Equal(t, 1, outboxCount)
}

func TestDeleteInsideOuterTransactionDefersSchedulerSideEffectsToCommittedOutbox(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	cache := newCindyIdentityLifecycleSchedulerCache(t)
	repo := newAccountRepositoryWithSQL(client, integrationDB, cache)
	suffix := time.Now().UnixNano()
	account := &service.Account{
		Name: fmt.Sprintf("cindy-delete-tx-%d", suffix), Platform: service.PlatformCindy,
		WirePlatform: service.WirePlatformOpenAI, ProviderProfile: service.ProviderProfileCindyLaxaV1,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_key": fmt.Sprintf("tx-key-%d", suffix), "base_url": "https://api.laxarouter.ai"},
		Extra:       map[string]any{},
	}
	require.NoError(t, repo.Create(ctx, account))
	require.NoError(t, cache.SetAccount(ctx, account))
	_, err := integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM cindy_health_states WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_credential_identities WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", account.ID)
	})

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	txCtx := dbent.NewTxContext(ctx, tx)
	require.NoError(t, repo.Delete(txCtx, account.ID))

	cached, err := cache.GetAccount(ctx, account.ID)
	require.NoError(t, err)
	require.NotNil(t, cached, "an uncommitted delete must not publish a cache side effect")
	var committedOutboxCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM scheduler_outbox WHERE account_id = $1", account.ID).
		Scan(&committedOutboxCount))
	require.Zero(t, committedOutboxCount, "outbox must not escape the outer transaction")
	rows, err := tx.Client().QueryContext(ctx, "SELECT COUNT(*) FROM scheduler_outbox WHERE account_id = $1", account.ID)
	require.NoError(t, err)
	require.True(t, rows.Next())
	var transactionalOutboxCount int
	require.NoError(t, rows.Scan(&transactionalOutboxCount))
	require.NoError(t, rows.Close())
	require.Equal(t, 1, transactionalOutboxCount)
	require.NoError(t, tx.Rollback())

	_, err = repo.GetByID(ctx, account.ID)
	require.NoError(t, err, "rolling back the outer transaction must preserve the account")
}

func TestDeleteThroughTransactionalRepositoryDefersSchedulerSideEffectsToCommittedOutbox(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	cache := newCindyIdentityLifecycleSchedulerCache(t)
	repo := newAccountRepositoryWithSQL(client, integrationDB, cache)
	suffix := time.Now().UnixNano()
	account := &service.Account{
		Name: fmt.Sprintf("cindy-delete-transactional-repository-%d", suffix), Platform: service.PlatformCindy,
		WirePlatform: service.WirePlatformOpenAI, ProviderProfile: service.ProviderProfileCindyLaxaV1,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_key": fmt.Sprintf("transactional-repository-key-%d", suffix), "base_url": "https://api.laxarouter.ai"},
		Extra:       map[string]any{},
	}
	require.NoError(t, repo.Create(ctx, account))
	require.NoError(t, cache.SetAccount(ctx, account))
	_, err := integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM cindy_health_states WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_credential_identities WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", account.ID)
	})

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	txRepo := newAccountRepositoryWithSQL(tx.Client(), tx.Client(), cache)
	require.NoError(t, txRepo.Delete(ctx, account.ID))

	cached, err := cache.GetAccount(ctx, account.ID)
	require.NoError(t, err)
	require.NotNil(t, cached, "a delete through an already-transactional repository must not evict cache before commit")
	var committedOutboxCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM scheduler_outbox WHERE account_id = $1", account.ID).
		Scan(&committedOutboxCount))
	require.Zero(t, committedOutboxCount, "outbox must not escape the transactional repository")
	rows, err := tx.Client().QueryContext(ctx, "SELECT COUNT(*) FROM scheduler_outbox WHERE account_id = $1", account.ID)
	require.NoError(t, err)
	require.True(t, rows.Next())
	var transactionalOutboxCount int
	require.NoError(t, rows.Scan(&transactionalOutboxCount))
	require.NoError(t, rows.Close())
	require.Equal(t, 1, transactionalOutboxCount)
	require.NoError(t, tx.Rollback())

	_, err = repo.GetByID(ctx, account.ID)
	require.NoError(t, err, "rolling back the transactional repository must preserve the account")
}

func TestCredentialIdentityReimportKeepsRetiredOwnerHistory(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newAccountRepositoryWithSQL(client, integrationDB, nil)
	identityRepo := NewAccountCredentialIdentityRepository(integrationDB)
	suffix := time.Now().UnixNano()
	key := fmt.Sprintf("reimport-key-%d", suffix)
	newAccount := func(name string) *service.Account {
		return &service.Account{
			Name: name, Platform: service.PlatformCindy,
			WirePlatform: service.WirePlatformOpenAI, ProviderProfile: service.ProviderProfileCindyLaxaV1,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
			Credentials: map[string]any{"api_key": key, "base_url": "https://api.laxarouter.ai"},
			Extra:       map[string]any{},
		}
	}
	owner := newAccount(fmt.Sprintf("cindy-reimport-owner-%d", suffix))
	require.NoError(t, repo.Create(ctx, owner))
	fingerprint, err := service.AccountCredentialFingerprint(
		service.ProviderProfileCindyLaxaV1, service.AccountTypeAPIKey, "https://api.laxarouter.ai", key,
	)
	require.NoError(t, err)
	ownerBinding, err := identityRepo.Bind(ctx, service.BindAccountCredentialIdentityParams{
		AccountID: owner.ID, ProviderProfile: service.ProviderProfileCindyLaxaV1,
		AuthType: service.AccountTypeAPIKey, NormalizedBaseURL: "https://api.laxarouter.ai",
		Fingerprint: fingerprint,
	})
	require.NoError(t, err)
	require.NoError(t, repo.Delete(ctx, owner.ID))

	reimported := newAccount(fmt.Sprintf("cindy-reimport-target-%d", suffix))
	require.NoError(t, repo.Create(ctx, reimported))
	t.Cleanup(func() {
		for _, accountID := range []int64{owner.ID, reimported.ID} {
			_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id = $1", accountID)
			_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM cindy_health_states WHERE account_id = $1", accountID)
			_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_credential_identities WHERE account_id = $1", accountID)
			_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", accountID)
		}
	})
	targetBinding, err := identityRepo.Bind(ctx, service.BindAccountCredentialIdentityParams{
		AccountID: reimported.ID, ProviderProfile: service.ProviderProfileCindyLaxaV1,
		AuthType: service.AccountTypeAPIKey, NormalizedBaseURL: "https://api.laxarouter.ai",
		Fingerprint: fingerprint,
	})
	require.NoError(t, err)
	require.True(t, targetBinding.Created)
	require.NotEqual(t, ownerBinding.Identity.ID, targetBinding.Identity.ID)

	var historicalOwnerID int64
	var historicalActive bool
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT account_id, active FROM account_credential_identities WHERE id = $1", ownerBinding.Identity.ID).
		Scan(&historicalOwnerID, &historicalActive))
	require.Equal(t, owner.ID, historicalOwnerID)
	require.False(t, historicalActive)
	require.Equal(t, reimported.ID, targetBinding.Identity.AccountID)
}
