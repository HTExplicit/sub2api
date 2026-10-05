//go:build integration

package repository

import (
	"context"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

// TestMain applied every migration, 271 included, to an empty database: the
// fresh-database run. The test puts the column back as 238 added it, stores
// billing claims with and without an account next to an archived claim,
// applies 271 twice and checks that only the column went.
func TestMigration271DropsUsageBillingDedupAccountID(t *testing.T) {
	ctx := context.Background()
	migration, err := dbmigrations.FS.ReadFile("271_drop_usage_billing_dedup_account_id.sql")
	require.NoError(t, err)

	tx := testTx(t)
	exec := func(query string) {
		t.Helper()
		_, err := tx.ExecContext(ctx, query)
		require.NoError(t, err)
	}
	text := func(query string) string {
		t.Helper()
		var value string
		require.NoError(t, tx.QueryRowContext(ctx, query).Scan(&value))
		return value
	}
	columns := func(table string) string {
		t.Helper()
		return text(`SELECT string_agg(column_name, ',' ORDER BY ordinal_position) FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = '` + table + `'`)
	}
	const claimColumns = "id,request_id,api_key_id,request_fingerprint,created_at"
	require.Equal(t, claimColumns, columns("usage_billing_dedup"), "271 already dropped the column 238 added")

	exec(`ALTER TABLE usage_billing_dedup ADD COLUMN account_id BIGINT`)
	exec(`INSERT INTO usage_billing_dedup (request_id, api_key_id, request_fingerprint, created_at, account_id) VALUES
		('migration-271-with-account', 9100271, repeat('a', 64), '2026-09-01T00:00:00Z', 42),
		('migration-271-without-account', 9100271, repeat('b', 64), '2026-09-02T00:00:00Z', NULL)`)
	exec(`INSERT INTO usage_billing_dedup_archive (request_id, api_key_id, request_fingerprint, created_at, archived_at) VALUES
		('migration-271-archived', 9100271, repeat('c', 64), '2025-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)

	// The rows, row versions and indexes 271 must leave alone.
	kept := func() map[string]string {
		return map[string]string{
			"claims": text(`SELECT json_agg(json_build_array(ctid::text, id, request_id, api_key_id, request_fingerprint, created_at) ORDER BY id)::text
				FROM usage_billing_dedup WHERE api_key_id = 9100271`),
			"claim count": text(`SELECT count(*)::text FROM usage_billing_dedup`),
			"archive": text(`SELECT json_agg(json_build_array(ctid::text, request_id, api_key_id, request_fingerprint, created_at, archived_at))::text
				FROM usage_billing_dedup_archive WHERE api_key_id = 9100271`),
			"archive columns": columns("usage_billing_dedup_archive"),
			"indexes": text(`SELECT string_agg(indexdef, ';' ORDER BY indexname) FROM pg_indexes
				WHERE schemaname = 'public' AND tablename = 'usage_billing_dedup'`),
		}
	}
	before := kept()
	require.Contains(t, before["claims"], "migration-271-with-account")
	require.Contains(t, before["claims"], "migration-271-without-account")
	require.Contains(t, before["indexes"], "idx_usage_billing_dedup_request_api_key")
	require.Contains(t, before["indexes"], "idx_usage_billing_dedup_created_at_brin")

	for run := 1; run <= 2; run++ {
		exec(string(migration))
		require.Equal(t, claimColumns, columns("usage_billing_dedup"), "run %d", run)
		require.Equal(t, before, kept(), "run %d changes nothing else", run)
	}
}
