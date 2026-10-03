//go:build integration

package repository

import (
	"context"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

const accountReasoningOptionsPurgeMigration = "267_purge_account_reasoning_options.sql"

// TestMain applied every migration, 267 included, to an empty database. The
// test stores accounts as the previous release could hold them, applies 267,
// checks which keys went and which rows were not written, and applies it again.
func TestMigration267PurgesAccountReasoningOptions(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	text := func(query string, args ...any) string {
		t.Helper()
		var value string
		require.NoError(t, tx.QueryRowContext(ctx, query, args...).Scan(&value))
		return value
	}
	count := func(query string, args ...any) int {
		t.Helper()
		var value int
		require.NoError(t, tx.QueryRowContext(ctx, query, args...).Scan(&value))
		return value
	}
	content, err := dbmigrations.FS.ReadFile(accountReasoningOptionsPurgeMigration)
	require.NoError(t, err)
	migration := string(content)

	insert := func(name, platform, accountType, extra string, deleted bool) int64 {
		t.Helper()
		var id int64
		require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO accounts (name, platform, type, extra, deleted_at)
VALUES ($1, $2, $3, $4::jsonb, CASE WHEN $5 THEN NOW() END)
RETURNING id`, name, platform, accountType, extra, deleted).Scan(&id))
		return id
	}
	type account struct {
		id      int64
		want    string
		written bool
	}
	accounts := map[string]account{
		"both keys": {
			id:      insert("migration-267-both", "openai", "apikey", `{"openai_long_context_billing_enabled": false, "openai_chat_reasoning_replay_enabled": false, "openai_reasoning_signature_recovery_enabled": true, "openai_passthrough": true}`, false),
			want:    `{"openai_long_context_billing_enabled": false, "openai_passthrough": true}`,
			written: true,
		},
		"one key with a value that is not a boolean": {
			id:      insert("migration-267-one", "openai", "oauth", `{"openai_long_context_billing_enabled": true, "openai_reasoning_signature_recovery_enabled": "off", "codex_fingerprint_mode": "device"}`, false),
			want:    `{"openai_long_context_billing_enabled": true, "codex_fingerprint_mode": "device"}`,
			written: true,
		},
		"deleted account": {
			id:      insert("migration-267-deleted", "openai", "apikey", `{"openai_long_context_billing_enabled": false, "openai_chat_reasoning_replay_enabled": null}`, true),
			want:    `{"openai_long_context_billing_enabled": false}`,
			written: true,
		},
		"another platform": {
			id:      insert("migration-267-other-platform", "anthropic", "oauth", `{"openai_chat_reasoning_replay_enabled": true, "window_cost_limit": 5}`, false),
			want:    `{"window_cost_limit": 5}`,
			written: true,
		},
		"neither key": {
			id:   insert("migration-267-untouched", "openai", "apikey", `{"openai_long_context_billing_enabled": false, "openai_prompt_cache_key_mode": "sha256_64", "note": "openai_reasoning_signature_recovery_enabled"}`, false),
			want: `{"openai_long_context_billing_enabled": false, "openai_prompt_cache_key_mode": "sha256_64", "note": "openai_reasoning_signature_recovery_enabled"}`,
		},
		"the key only inside another object": {
			id:   insert("migration-267-nested", "openai", "apikey", `{"openai_long_context_billing_enabled": false, "model_rate_limits": {"openai_chat_reasoning_replay_enabled": true}}`, false),
			want: `{"openai_long_context_billing_enabled": false, "model_rate_limits": {"openai_chat_reasoning_replay_enabled": true}}`,
		},
	}
	ctid := func(id int64) string {
		t.Helper()
		return text(`SELECT ctid::text FROM accounts WHERE id = $1`, id)
	}
	before := map[string]string{}
	for name, row := range accounts {
		before[name] = ctid(row.id)
	}
	outbox := count(`SELECT count(*) FROM scheduler_outbox`)

	_, err = tx.ExecContext(ctx, migration)
	require.NoError(t, err)
	after := map[string]string{}
	for name, row := range accounts {
		require.JSONEq(t, row.want, text(`SELECT extra::text FROM accounts WHERE id = $1`, row.id), name)
		after[name] = ctid(row.id)
		require.Equal(t, row.written, after[name] != before[name], "%s: only a row that held a key is written", name)
	}
	require.Equal(t, outbox, count(`SELECT count(*) FROM scheduler_outbox`), "the purge queues no scheduler event")

	_, err = tx.ExecContext(ctx, migration)
	require.NoError(t, err)
	for name, row := range accounts {
		require.JSONEq(t, row.want, text(`SELECT extra::text FROM accounts WHERE id = $1`, row.id), name)
		require.Equal(t, after[name], ctid(row.id), "%s: the second run writes no row", name)
	}
}
