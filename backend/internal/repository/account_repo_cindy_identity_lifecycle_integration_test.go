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

func TestDeleteCanonicalCindyAccountRetiresIdentityAndDeletesHealth(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newAccountRepositoryWithSQL(client, integrationDB, nil)
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
}
