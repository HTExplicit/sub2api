//go:build integration

package repository

import (
	"context"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

const openAIAPIKeyCompatSettingsPurgeMigration = "266_purge_retired_openai_apikey_compat_settings.sql"

// TestMain applied every migration, 266 included, to an empty database. The
// test restores the two rows the previous release stored next to settings and
// an account option that must survive, applies 266, checks what was deleted
// and what stayed, and applies it again.
func TestMigration266PurgesRetiredOpenAIAPIKeyCompatSettings(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := tx.ExecContext(ctx, query, args...)
		require.NoError(t, err)
	}
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
	content, err := dbmigrations.FS.ReadFile(openAIAPIKeyCompatSettingsPurgeMigration)
	require.NoError(t, err)
	migration := string(content)

	const retired = `key IN ('openai_apikey_alpha_search_responses_bridge_enabled', 'openai_apikey_prompt_cache_key_normalization_enabled')`
	exec(`INSERT INTO settings (key, value) VALUES
		('openai_apikey_alpha_search_responses_bridge_enabled', 'true'),
		('openai_apikey_prompt_cache_key_normalization_enabled', 'false'),
		('openai_refusal_recovery_enabled', 'true'),
		('openai_cyber_failover_enabled', 'false'),
		('openai_apikey_alpha_search_responses_bridge_enabled_note', 'kept: not a retired key')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	require.Equal(t, 2, count(`SELECT count(*) FROM settings WHERE `+retired))

	var accountID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO accounts (name, platform, type, extra)
VALUES ('migration-266-prompt-cache-key-mode', 'openai', 'apikey',
	'{"openai_long_context_billing_enabled": false, "openai_prompt_cache_key_mode": "sha256_64"}'::jsonb)
RETURNING id`).Scan(&accountID))

	kept := map[string]string{
		"openai_refusal_recovery_enabled":                          "true",
		"openai_cyber_failover_enabled":                            "false",
		"openai_apikey_alpha_search_responses_bridge_enabled_note": "kept: not a retired key",
	}
	settingRow := func(key string) string {
		t.Helper()
		return text(`SELECT ctid::text || '/' || value FROM settings WHERE key = $1`, key)
	}
	untouched := map[string]string{}
	for key := range kept {
		untouched[key] = settingRow(key)
	}
	accountCTID := text(`SELECT ctid::text FROM accounts WHERE id = $1`, accountID)
	others := count(`SELECT count(*) FROM settings WHERE NOT (` + retired + `)`)

	for run := 1; run <= 2; run++ {
		exec(migration)

		require.Zero(t, count(`SELECT count(*) FROM settings WHERE `+retired), "run %d", run)
		require.Equal(t, others, count(`SELECT count(*) FROM settings`), "run %d deletes no other setting", run)
		for key, value := range kept {
			require.Equal(t, value, text(`SELECT value FROM settings WHERE key = $1`, key), key)
			require.Equal(t, untouched[key], settingRow(key), "run %d rewrites no kept setting", run)
		}
		require.Equal(t, accountCTID, text(`SELECT ctid::text FROM accounts WHERE id = $1`, accountID), "run %d writes no account", run)
		require.JSONEq(t, `{"openai_long_context_billing_enabled": false, "openai_prompt_cache_key_mode": "sha256_64"}`,
			text(`SELECT extra::text FROM accounts WHERE id = $1`, accountID))
	}
}
