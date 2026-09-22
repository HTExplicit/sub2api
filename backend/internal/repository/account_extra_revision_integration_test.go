//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAccountExtraRevisionRejectsStalePolicyResult(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	account := mustCreateAccount(t, client, &service.Account{Name: "profile-cas-" + uuid.NewString(),
		Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Concurrency: 1,
		Credentials: map[string]any{"chatgpt_account_id": "fixture-account"}, Extra: map[string]any{"operator_note": "keep"}})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM scheduler_outbox WHERE account_id=$1`, account.ID)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, account.ID)
	})
	repo := &accountRepository{client: client, sql: integrationDB}
	var original time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT updated_at FROM accounts WHERE id=$1`, account.ID).Scan(&original))
	applied, err := repo.UpdateExtraIfRevision(ctx, account.ID, original, map[string]any{"fixture_metadata": "first"})
	require.NoError(t, err)
	require.True(t, applied)
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET updated_at=clock_timestamp(), extra=extra || '{"fixture_metadata":"newer"}'::jsonb WHERE id=$1`, account.ID)
	require.NoError(t, err)
	applied, err = repo.UpdateExtraIfRevision(ctx, account.ID, original, map[string]any{"fixture_metadata": "stale"})
	require.NoError(t, err)
	require.False(t, applied)
	var value, note string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT extra->>'fixture_metadata',extra->>'operator_note' FROM accounts WHERE id=$1`, account.ID).Scan(&value, &note))
	require.Equal(t, "newer", value)
	require.Equal(t, "keep", note)
}
