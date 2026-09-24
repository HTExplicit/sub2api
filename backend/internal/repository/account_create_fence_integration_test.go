//go:build integration

package repository

import (
	"context"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

// Native creation still writes the account inside its caller's transaction.
func TestAccountCreateEntFenceIntegration(t *testing.T) {
	ctx := context.Background()
	var before int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM accounts`).Scan(&before))
	require.Zero(t, before)
	tx, err := testEntClient(t).Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	account := &service.Account{Name: "synthetic-create-fenced", Platform: service.PlatformCindy, WirePlatform: service.WirePlatformOpenAI,
		ProviderProfile: service.ProviderProfileCindyLaxaV1, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 3,
		Credentials: map[string]any{"base_url": "https://api.laxarouter.ai", "api_key": "synthetic-create-only"},
		Extra:       map[string]any{service.CindyDeviceIDExtraKey: strings.Repeat("b", 64), service.CindyDeviceIDSourceExtraKey: "input-preserved"}}
	accountRepo := NewAccountRepository(testEntClient(t), integrationDB, nil)
	require.NoError(t, accountRepo.Create(dbent.NewTxContext(ctx, tx), account))
	require.Positive(t, account.ID)
	inside, err := tx.Client().Account.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, inside)
	var outside int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM accounts`).Scan(&outside))
	require.Zero(t, outside, "account persistence shares the still-open fenced transaction")
	require.NoError(t, tx.Rollback())
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT count(*) FROM accounts`).Scan(&outside))
	require.Zero(t, outside)
}
