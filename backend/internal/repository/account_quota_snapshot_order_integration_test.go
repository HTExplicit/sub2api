//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestQuotaSnapshotOrderingAndLegacyWindowClock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := testEntClient(t)
	account := mustCreateAccount(t, client, &service.Account{Name: "quota-order-" + uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM accounts WHERE id=$1`, account.ID)
	})
	repo := newAccountRepositoryWithSQL(client, integrationDB, nil)
	initial := time.Date(2026, 9, 19, 6, 0, 0, 0, time.UTC)
	legacy, _ := json.Marshal(map[string]any{"codex_usage_updated_at": initial.Format(time.RFC3339), "codex_secondary_used_percent": 100, "codex_secondary_window_minutes": 43200, "codex_secondary_reset_after_seconds": 7200})
	_, err := integrationDB.ExecContext(ctx, `UPDATE accounts SET extra=$2::jsonb WHERE id=$1`, account.ID, legacy)
	require.NoError(t, err)
	require.NoError(t, repo.UpdateExtra(ctx, account.ID, map[string]any{"codex_usage_updated_at": initial.Add(time.Minute).Format(time.RFC3339), "codex_primary_used_percent": 10}))
	var raw []byte
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT extra FROM accounts WHERE id=$1`, account.ID).Scan(&raw))
	var extra map[string]any
	require.NoError(t, json.Unmarshal(raw, &extra))
	require.Equal(t, initial.Format(time.RFC3339), extra["codex_secondary_observed_at"])
	windows := service.OpenAIQuotaWindows(extra, initial.Add(time.Hour))
	require.Len(t, windows, 2)
	require.Equal(t, initial.Add(2*time.Hour), *windows[1].ResetsAt)

	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `SELECT id FROM accounts WHERE id=$1 FOR NO KEY UPDATE`, account.ID)
	require.NoError(t, err)
	finished := make(chan error, 1)
	go func() {
		finished <- repo.UpdateExtra(ctx, account.ID, map[string]any{"codex_usage_updated_at": initial.Add(2 * time.Minute).Format(time.RFC3339), "codex_primary_used_percent": 20})
	}()
	require.Eventually(t, func() bool {
		var waiting bool
		_ = integrationDB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%WITH target AS MATERIALIZED%')`).Scan(&waiting)
		return waiting
	}, 5*time.Second, 10*time.Millisecond, "the older observation must reach the locked account")
	newer, _ := json.Marshal(map[string]any{"codex_usage_updated_at": initial.Add(3 * time.Minute).Format(time.RFC3339), "codex_primary_used_percent": 30})
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET extra=extra||$2::jsonb WHERE id=$1`, account.ID, newer)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	select {
	case err = <-finished:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT extra FROM accounts WHERE id=$1`, account.ID).Scan(&raw))
	require.NoError(t, json.Unmarshal(raw, &extra))
	require.Equal(t, 30.0, extra["codex_primary_used_percent"], "an older writer cannot overwrite a newer committed observation")
}
