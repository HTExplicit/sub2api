//go:build integration

package repository

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

const codexTicketPurgeMigration = "263_purge_retired_codex_ticket_data.sql"

// TestMain applied every migration, 263 included, to an empty database. The
// test restores the state the previous release left behind (the objects of
// 244/245 and representative dormant rows next to rows that must survive),
// applies 263, checks what was purged and what stayed, and applies it again.
func TestMigration263PurgesRetiredCodexTicketData(t *testing.T) {
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
	migration := func(name string) string {
		t.Helper()
		content, err := dbmigrations.FS.ReadFile(name)
		require.NoError(t, err)
		return string(content)
	}

	requireCodexTicketObjects(t, ctx, tx, false)
	exec(migration("244_codex_ticket_lifecycle.sql"))
	exec(migration("245_codex_ticket_account_status_independent.sql"))
	requireCodexTicketObjects(t, ctx, tx, true)

	var userID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, role, status, balance, concurrency)
VALUES ('migration-263@example.com', 'hash', 'admin', 'active', 0, 1)
RETURNING id`).Scan(&userID))
	insertAccount := func(name, platform, extra string) int64 {
		t.Helper()
		var id int64
		require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO accounts (name, platform, type, extra) VALUES ($1, $2, 'oauth', $3::jsonb) RETURNING id`,
			name, platform, extra).Scan(&id))
		return id
	}
	ticketID := insertAccount("migration-263-ticket", "openai", `{
		"openai_long_context_billing_enabled": true,
		"codex_fingerprint_seed": "11111111-1111-4111-8111-111111111111",
		"codex_turn_ticket:gpt-6-astra": {"state": "gAAAAA-dormant-ticket", "length": 292},
		"codex_ticket_runtime:gpt-6-astra": {"phase": "ready"},
		"codex_harvest_proxy_url": "http://user:fixture-proxy-secret@proxy.example.test:8080",
		"codex_turn_ticket_summary": "kept: not the retired prefix",
		"plugin_account_projections": {"codexrip.codex-runtime": {"identity": "owner", "scheduling": {"gpt-6-astra": {"effect": "deny"}}}}
	}`)
	sharedID := insertAccount("migration-263-shared-projection", "openai", `{
		"openai_long_context_billing_enabled": false,
		"plugin_account_projections": {"codexrip.codex-runtime": {"identity": "owner"}, "acme.other-plugin": {"identity": "kept"}}
	}`)
	cleanID := insertAccount("migration-263-clean", "openai", `{
		"openai_long_context_billing_enabled": true,
		"codex_fingerprint_seed": "22222222-2222-4222-8222-222222222222",
		"plugin_account_projections": {"acme.other-plugin": {"identity": "kept"}}
	}`)
	arrayID := insertAccount("migration-263-array-extra", "anthropic", `[1, 2, 3]`)
	accountIDs := []int64{ticketID, sharedID, cleanID, arrayID}

	exec(`INSERT INTO settings (key, value) VALUES
		('openai_codex_ticket_enabled', 'true'),
		('openai_codex_ticket_harvest_proxy_url', 'http://user:fixture-proxy-secret@proxy.example.test:8080'),
		('codex_native_runtime_source', 'encrypted-source-kept'),
		('codex_native_runtime_config', '{"version":1,"config_version":3,"config_sha256":"kept"}'),
		('openai_codex_client_version', '0.156.0')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	exec(`INSERT INTO openai_codex_ticket_runtime (account_id, model, identity, phase) VALUES ($1, 'gpt-6-astra', 'owner', 'ready')`, ticketID)
	exec(`INSERT INTO openai_codex_ticket_proxy_trust (proxy_key, certificates) VALUES ('migration-263', '["fixture-public-certificate"]')`)

	const codex, private = "codexrip.codex-runtime", "codex-routing-private"
	insertState := func(plugin, namespace, key, value string) {
		t.Helper()
		exec(`INSERT INTO sub2api_plugin_state (plugin_key, namespace, state_key, value) VALUES ($1, $2, $3, $4::jsonb)`,
			plugin, namespace, key, value)
	}
	purgedState := [][2]string{
		{"tickets", "m263.ticket"},
		{"routing-demand", "m263.ticket"},
		{"proxy-trust", "m263.proxy"},
		{private, "bundle.m263.live"},
		{private, "bundle.quality.m263.candidate"},
		{private, "clock.m263"},
		{private, "clock.quality.m263"},
		{private, "seen.m263"},
		{private, "validation-result.m263"},
	}
	for _, row := range purgedState {
		insertState(codex, row[0], row[1], `{"cookies":[{"name":"__cflb","value":"fixture-cookie-value"}]}`)
	}
	keptState := map[string]string{
		"quality-run.11111111-1111-4111-8111-111111111111": `{"run_id":"11111111-1111-4111-8111-111111111111","qualification":{"bundle":{"key":"bundle.quality.m263.live"}}}`,
		"validation.22222222-2222-4222-8222-222222222222":  `{"account_id":41,"used":2,"stages":{"gpt-6-astra:http:acquire":true}}`,
		"spent.m263":              `{"spent":true}`,
		"wire.m263-observed":      `{"user_agent":"codex_cli_rs/0.156.0","state_present":false}`,
		"other.m263-unclassified": `{"kept":true}`,
	}
	for key, value := range keptState {
		insertState(codex, private, key, value)
	}
	insertState(codex, private, "wire.m263-cookies", `{"user_agent":"codex_cli_rs/0.156.0","state":"opaque-state","cookie_versions":{"__cflb":"0123456789abcdef"},"cookies":[{"name":"__cflb","value":"fixture-cookie-value","routing":true}]}`)
	insertState("acme.other-plugin", "tickets", "m263.ticket", `{"kept":true}`)
	insertState("acme.other-plugin", private, "bundle.m263.live", `{"kept":true}`)

	insertLease := func(plugin, namespace, key string) {
		t.Helper()
		exec(`INSERT INTO sub2api_plugin_leases (plugin_key, namespace, lease_key, owner, expires_at) VALUES ($1, $2, $3, 'owner', NOW())`,
			plugin, namespace, key)
	}
	insertLease(codex, "routing-account", "m263")
	insertLease(codex, "tickets", "m263.ticket")
	insertLease("acme.other-plugin", "routing-account", "m263")

	insertJob := func(kind string) int64 {
		t.Helper()
		var id int64
		require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO admin_account_jobs (created_by, kind, idempotency_key, request_hash, status, target_count, processed_count, failed_count)
VALUES ($1, $2, $3, $4, 'failed', 1, 1, 1) RETURNING id`,
			userID, kind, "migration-263-"+kind, strings.Repeat("a", 64)).Scan(&id))
		exec(`INSERT INTO admin_account_job_items (job_id, ordinal, target_account_id, status, metadata) VALUES ($1, 1, $2, 'failed', '{"model_id":"gpt-6-astra"}')`,
			id, ticketID)
		return id
	}
	retiredJobs := []int64{insertJob("codex_ticket_harvest"), insertJob("codex_ticket_stop"), insertJob("extension_operation")}
	keptJob := insertJob("account_bulk_update")

	ctid := func(table, column string, key any) string {
		t.Helper()
		return text(`SELECT ctid::text FROM `+table+` WHERE `+column+` = $1`, key)
	}
	stateCTID := func(key string) string {
		t.Helper()
		return text(`SELECT ctid::text || '/' || revision FROM sub2api_plugin_state WHERE plugin_key = $1 AND namespace = $2 AND state_key = $3`, codex, private, key)
	}
	untouchedAccounts := map[int64]string{cleanID: ctid("accounts", "id", cleanID), arrayID: ctid("accounts", "id", arrayID)}
	untouchedState := map[string]string{}
	for key := range keptState {
		untouchedState[key] = stateCTID(key)
	}
	untouchedSettings := map[string]string{}
	for _, key := range []string{"codex_native_runtime_source", "codex_native_runtime_config", "openai_codex_client_version"} {
		untouchedSettings[key] = ctid("settings", "key", key)
	}
	outboxBefore := count(`SELECT count(*) FROM scheduler_outbox WHERE account_id = ANY($1)`, accountIDs)
	wireRevision := count(`SELECT revision FROM sub2api_plugin_state WHERE plugin_key = $1 AND namespace = $2 AND state_key = 'wire.m263-cookies'`, codex, private)

	exec(migration(codexTicketPurgeMigration))

	requireCodexTicketObjects(t, ctx, tx, false)
	require.Zero(t, count(`SELECT count(*) FROM settings WHERE key IN ('openai_codex_ticket_enabled', 'openai_codex_ticket_harvest_proxy_url')`))
	require.Equal(t, "encrypted-source-kept", text(`SELECT value FROM settings WHERE key = 'codex_native_runtime_source'`))
	for key, before := range untouchedSettings {
		require.Equal(t, before, ctid("settings", "key", key), key)
	}

	require.JSONEq(t, `{
		"openai_long_context_billing_enabled": true,
		"codex_fingerprint_seed": "11111111-1111-4111-8111-111111111111",
		"codex_turn_ticket_summary": "kept: not the retired prefix"
	}`, text(`SELECT extra::text FROM accounts WHERE id = $1`, ticketID))
	require.JSONEq(t, `{
		"openai_long_context_billing_enabled": false,
		"plugin_account_projections": {"acme.other-plugin": {"identity": "kept"}}
	}`, text(`SELECT extra::text FROM accounts WHERE id = $1`, sharedID))
	for id, before := range untouchedAccounts {
		require.Equal(t, before, ctid("accounts", "id", id), "account %d holds no retired key and is not rewritten", id)
	}
	require.Equal(t, outboxBefore, count(`SELECT count(*) FROM scheduler_outbox WHERE account_id = ANY($1)`, accountIDs))

	for _, row := range purgedState {
		require.Zero(t, count(`SELECT count(*) FROM sub2api_plugin_state WHERE plugin_key = $1 AND namespace = $2 AND state_key = $3`, codex, row[0], row[1]), row)
	}
	require.Zero(t, count(`SELECT count(*) FROM sub2api_plugin_state WHERE plugin_key = $1 AND namespace IN ('tickets', 'routing-demand', 'proxy-trust')`, codex))
	for key, before := range untouchedState {
		require.Equal(t, before, stateCTID(key), key)
	}
	require.JSONEq(t, `{"user_agent":"codex_cli_rs/0.156.0","state":"opaque-state","cookie_versions":{"__cflb":"0123456789abcdef"}}`,
		text(`SELECT value::text FROM sub2api_plugin_state WHERE plugin_key = $1 AND namespace = $2 AND state_key = 'wire.m263-cookies'`, codex, private))
	require.Equal(t, wireRevision+1, count(`SELECT revision FROM sub2api_plugin_state WHERE plugin_key = $1 AND namespace = $2 AND state_key = 'wire.m263-cookies'`, codex, private))
	require.Equal(t, 2, count(`SELECT count(*) FROM sub2api_plugin_state WHERE plugin_key = 'acme.other-plugin' AND state_key IN ('m263.ticket', 'bundle.m263.live')`))

	require.Zero(t, count(`SELECT count(*) FROM sub2api_plugin_leases WHERE plugin_key = $1 AND namespace IN ('routing-account', 'tickets')`, codex))
	require.Equal(t, 1, count(`SELECT count(*) FROM sub2api_plugin_leases WHERE plugin_key = 'acme.other-plugin' AND lease_key = 'm263'`))

	require.Zero(t, count(`SELECT count(*) FROM admin_account_jobs WHERE id = ANY($1)`, retiredJobs))
	require.Zero(t, count(`SELECT count(*) FROM admin_account_job_items WHERE job_id = ANY($1)`, retiredJobs))
	require.Equal(t, 1, count(`SELECT count(*) FROM admin_account_jobs WHERE id = $1`, keptJob))
	require.Equal(t, 1, count(`SELECT count(*) FROM admin_account_job_items WHERE job_id = $1`, keptJob))

	// The triggers went before their tables, so account status/credential
	// writes and setting writes no longer reach the dropped tables.
	exec(`UPDATE accounts SET status = 'disabled', credentials = credentials || '{"chatgpt_account_id":"changed"}'::jsonb WHERE id = $1`, ticketID)
	exec(`UPDATE settings SET value = '0.157.0' WHERE key = 'openai_codex_client_version'`)

	ticketCTID := ctid("accounts", "id", ticketID)
	exec(migration(codexTicketPurgeMigration))
	requireCodexTicketObjects(t, ctx, tx, false)
	require.Equal(t, ticketCTID, ctid("accounts", "id", ticketID), "a second run rewrites no account")
	require.Equal(t, wireRevision+1, count(`SELECT revision FROM sub2api_plugin_state WHERE plugin_key = $1 AND namespace = $2 AND state_key = 'wire.m263-cookies'`, codex, private))
	require.Equal(t, 1, count(`SELECT count(*) FROM admin_account_jobs WHERE id = $1`, keptJob))
}

func requireCodexTicketObjects(t *testing.T, ctx context.Context, tx *sql.Tx, present bool) {
	t.Helper()
	var relations, triggers, functions int
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT
    (SELECT count(*) FROM unnest(ARRAY['openai_codex_ticket_runtime', 'openai_codex_ticket_due_idx',
        'openai_codex_ticket_proxy_trust', 'openai_codex_ticket_proxy_generation']) AS name
     WHERE to_regclass(name) IS NOT NULL),
    (SELECT count(*) FROM pg_trigger WHERE tgname IN ('codex_ticket_account_claim_guard', 'codex_ticket_proxy_trust_guard')),
    (SELECT count(*) FROM pg_proc WHERE proname IN ('invalidate_codex_ticket_account_claims', 'invalidate_codex_ticket_proxy_trust'))
`).Scan(&relations, &triggers, &functions))
	if present {
		require.Equal(t, []int{4, 2, 2}, []int{relations, triggers, functions})
		return
	}
	require.Equal(t, []int{0, 0, 0}, []int{relations, triggers, functions})
}
