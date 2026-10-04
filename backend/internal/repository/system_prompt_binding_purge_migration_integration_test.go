//go:build integration

package repository

import (
	"context"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

const systemPromptBindingPurgeMigration = "268_purge_system_prompt_bindings_without_insertion_point.sql"

// TestMain applied every migration, 268 included, to an empty database. The
// test stores bindings on accounts with and without an insertion point,
// applies 268, checks which bindings went and which rows were left alone, and
// applies it again.
func TestMigration268PurgesSystemPromptBindingsWithoutInsertionPoint(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	text := func(query string, args ...any) string {
		t.Helper()
		var value string
		require.NoError(t, tx.QueryRowContext(ctx, query, args...).Scan(&value))
		return value
	}
	content, err := dbmigrations.FS.ReadFile(systemPromptBindingPurgeMigration)
	require.NoError(t, err)
	migration := string(content)

	const off = `"system_prompt": {"mode": "off"}`
	const custom = `"system_prompt": {"mode": "custom", "prompt_id": "p-0123456789ab"}`
	accounts := []struct {
		name, platform, extra string
		deleted               bool
		purged                string // the extra 268 leaves; empty when it must not write the row
		id                    int64
		ctid                  string
	}{
		{name: "typesafe", platform: "typesafe", extra: `{` + off + `, "upstream_request_id_header": "x-request-id"}`, purged: `{"upstream_request_id_header": "x-request-id"}`},
		{name: "typesafe-deleted", platform: "typesafe", extra: `{` + custom + `}`, deleted: true, purged: `{}`},
		{name: "legacy-platform", platform: "kiro", extra: `{` + custom + `, "quota_limit": 5}`, purged: `{"quota_limit": 5}`},
		{name: "typesafe-unbound", platform: "typesafe", extra: `{"upstream_request_id_header": "x-request-id"}`},
		{name: "typesafe-array-extra", platform: "typesafe", extra: `["system_prompt"]`},
		{name: "openai", platform: "openai", extra: `{` + custom + `, "openai_long_context_billing_enabled": false}`},
		{name: "anthropic", platform: "anthropic", extra: `{` + off + `}`},
		{name: "opencode-deleted", platform: "opencode_go", extra: `{` + off + `}`, deleted: true},
	}
	for i := range accounts {
		account := &accounts[i]
		require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO accounts (name, platform, type, extra, deleted_at)
VALUES ($1, $2, 'apikey', $3::jsonb, CASE WHEN $4 THEN NOW() END)
RETURNING id, ctid::text`,
			"migration-268-"+account.name, account.platform, account.extra, account.deleted).Scan(&account.id, &account.ctid))
	}

	for run := 1; run <= 2; run++ {
		_, err := tx.ExecContext(ctx, migration)
		require.NoError(t, err, "run %d", run)

		for _, account := range accounts {
			extra := text(`SELECT extra::text FROM accounts WHERE id = $1`, account.id)
			if account.purged != "" {
				require.JSONEq(t, account.purged, extra, "run %d %s", run, account.name)
				continue
			}
			require.JSONEq(t, account.extra, extra, "run %d %s", run, account.name)
			require.Equal(t, account.ctid, text(`SELECT ctid::text FROM accounts WHERE id = $1`, account.id),
				"run %d writes no %s row", run, account.name)
		}
	}
}
