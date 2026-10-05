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

const firstPartyPluginPurgeMigration = "272_purge_first_party_plugins.sql"

// TestMain applied every migration, 272 included, to an empty database: the
// fresh-database run. The other cases put back the schema 272 removes by
// running 248, 249, 250 and the index statements of 246 again, store what a
// database of the previous release can hold next to a third-party
// installation, apply 272 and check what went and what stayed.
func TestMigration272PurgesFirstPartyPlugins(t *testing.T) {
	ctx := context.Background()
	read := func(name string) string {
		t.Helper()
		content, err := dbmigrations.FS.ReadFile(name)
		require.NoError(t, err)
		return string(content)
	}
	migration := read(firstPartyPluginPurgeMigration)
	upstreamSchema := read("229_plugins.sql") + read("230_plugin_artifacts.sql")
	// 246 narrows the index before it creates the tables 264 dropped.
	narrowedIndex, _, found := strings.Cut(read("246_plugin_extension_state.sql"), "CREATE TABLE")
	require.True(t, found)
	bundleSchema := read("248_plugin_bundle_bootstrap.sql") + read("249_plugin_bundle_binding_intent.sql") + read("250_plugin_independent_updates.sql") + narrowedIndex

	type fixture struct {
		tx *sql.Tx
		t  *testing.T
	}
	exec := func(f fixture, query string, args ...any) {
		f.t.Helper()
		_, err := f.tx.ExecContext(ctx, query, args...)
		require.NoError(f.t, err)
	}
	text := func(f fixture, query string, args ...any) string {
		f.t.Helper()
		var value string
		require.NoError(f.t, f.tx.QueryRowContext(ctx, query, args...).Scan(&value))
		return value
	}
	count := func(f fixture, query string, args ...any) int {
		f.t.Helper()
		var value int
		require.NoError(f.t, f.tx.QueryRowContext(ctx, query, args...).Scan(&value))
		return value
	}
	put := func(f fixture, key, value string) {
		f.t.Helper()
		exec(f, `INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, value)
	}
	apply := func(f fixture) { f.t.Helper(); exec(f, migration) }

	// Every column, constraint, index and trigger of the two plugin tables in
	// one schema, without the schema's name.
	tables := func(f fixture, schema string) string {
		f.t.Helper()
		listing := text(f, `
SELECT string_agg(line, E'\n' ORDER BY line) FROM (
	SELECT format('column %s #%s %s %s not null=%s default=%s', c.relname, row_number() OVER (PARTITION BY c.oid ORDER BY a.attnum),
			a.attname, format_type(a.atttypid, a.atttypmod), a.attnotnull, COALESCE(pg_get_expr(d.adbin, d.adrelid), '')) AS line
	FROM pg_class AS c
	JOIN pg_attribute AS a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
	LEFT JOIN pg_attrdef AS d ON d.adrelid = c.oid AND d.adnum = a.attnum
	WHERE c.relnamespace = $1::regnamespace AND c.relname = ANY($2)
	UNION ALL
	SELECT format('constraint %s %s %s', c.relname, k.conname, pg_get_constraintdef(k.oid))
	FROM pg_class AS c JOIN pg_constraint AS k ON k.conrelid = c.oid
	WHERE c.relnamespace = $1::regnamespace AND c.relname = ANY($2)
	UNION ALL
	SELECT format('index %s', pg_get_indexdef(i.indexrelid))
	FROM pg_class AS c JOIN pg_index AS i ON i.indrelid = c.oid
	WHERE c.relnamespace = $1::regnamespace AND c.relname = ANY($2)
	UNION ALL
	SELECT format('trigger %s', pg_get_triggerdef(g.oid))
	FROM pg_class AS c JOIN pg_trigger AS g ON g.tgrelid = c.oid AND NOT g.tgisinternal
	WHERE c.relnamespace = $1::regnamespace AND c.relname = ANY($2)
) AS lines`, schema, []string{"sub2api_plugin_installations", "sub2api_plugin_bindings"})
		return strings.ReplaceAll(listing, schema+".", "")
	}
	// What only the first-party bundle used: its two tables and the function.
	bundle := func(f fixture) string {
		f.t.Helper()
		return text(f, `SELECT concat_ws(',', to_regclass('sub2api_plugin_bootstrap'), to_regclass('sub2api_plugin_updates'), to_regprocedure('sub2api_plugin_revision()'))`)
	}
	// open starts a case and builds what 229 and 230 alone define, in a schema
	// of its own, to compare the plugin tables with.
	open := func(t *testing.T) (fixture, string) {
		f := fixture{tx: testTx(t), t: t}
		exec(f, `CREATE SCHEMA migration_272_upstream`)
		exec(f, `SET LOCAL search_path = migration_272_upstream, public`)
		exec(f, upstreamSchema)
		exec(f, `SET LOCAL search_path TO DEFAULT`)
		return f, tables(f, "migration_272_upstream")
	}

	const other, second = "acme.transport", "acme.second"
	firstParty := []string{
		"codexrip.account-tools", "codexrip.admin-observability", "codexrip.cindy-provider", "codexrip.codex-runtime",
		"codexrip.image-tools", "codexrip.model-policy", "codexrip.prompt-skills",
	}
	// What 272 deletes: the first-party installations and the receipt.
	purged := func(f fixture) int {
		f.t.Helper()
		return count(f, `SELECT (SELECT count(*) FROM sub2api_plugin_installations WHERE plugin_key = ANY($1))
			+ (SELECT count(*) FROM settings WHERE key = 'deplugin_retired_plugins')`, firstParty)
	}
	clean := func(f fixture, upstream string) {
		f.t.Helper()
		require.Zero(f.t, purged(f), "this fixture requires an isolated test database")
		require.Zero(f.t, count(f, `SELECT count(*) FROM sub2api_plugin_installations WHERE plugin_key IN ($1, $2)`, other, second))
		require.Equal(f.t, upstream, tables(f, "public"), "the migrations leave both plugin tables as 229 and 230 define them")
		require.Empty(f.t, bundle(f))
	}
	restoreBundleSchema := func(f fixture, upstream string) {
		f.t.Helper()
		exec(f, bundleSchema)
		restored := tables(f, "public")
		require.NotEqual(f.t, upstream, restored)
		for _, added := range []string{" revision bigint ", " runtime_generation bigint ", " package_sha256 text ", " update_policy text ", "'updating'", "trigger CREATE TRIGGER sub2api_plugin_revision ", "'extensions.provider.v1'"} {
			require.Contains(f.t, restored, added)
		}
		require.Equal(f.t, "sub2api_plugin_bootstrap,sub2api_plugin_updates,sub2api_plugin_revision()", bundle(f))
	}

	// One installation with its saved configuration and package; blank is the
	// row 264 left of codexrip.codex-runtime.
	install := func(f fixture, key, state string, blank bool) int64 {
		f.t.Helper()
		policy := "pinned"
		if strings.HasPrefix(key, "codexrip.") {
			policy = "bundled"
		}
		values := []any{key, "fixture description", `{"id":"` + key + `"}`, "data/plugins/packages/" + key + ".s2plugin", "data/plugins/installed/" + key,
			"data/plugins/installed/" + key + "/plugin", strings.Repeat("b", 64), "trusted", state, "fixture-config-cipher", []byte{1, 2, 3}, strings.Repeat("c", 64), policy}
		if blank {
			values = []any{key, "", `{}`, "", "", "", "", "unsigned", state, "", []byte(nil), "", "pinned"}
		}
		var id int64
		require.NoError(f.t, f.tx.QueryRowContext(ctx, `
INSERT INTO sub2api_plugin_installations
	(plugin_key, name, version, description, author, manifest, artifact_path, install_path, binary_path, binary_sha256,
	 signature_status, state, config_encrypted, last_error, enabled_at, artifact_data, runtime_generation, package_sha256, update_policy)
VALUES ($1, 'fixture', '0.2.13', $2, 'fixture author', $3::jsonb, $4, $5, $6, $7, $8, $9, $10, '', NOW(), $11, 7, $12, $13)
RETURNING id`, values...).Scan(&id))
		return id
	}
	bind := func(f fixture, id int64, capability, platform, accountType string, enabled bool, rollout int) {
		f.t.Helper()
		exec(f, `INSERT INTO sub2api_plugin_bindings (plugin_id, capability, platform, account_type, enabled, rollout_percent) VALUES ($1, $2, $3, $4, $5, $6)`,
			id, capability, platform, accountType, enabled, rollout)
	}
	// The receipt of the startup retirement: one entry per retired installation.
	receipt := func(f fixture, completed string, keys ...string) {
		f.t.Helper()
		entries := make([]string, 0, len(keys))
		for _, key := range keys {
			entries = append(entries, `"`+key+`":{"id":1,"key":"`+key+`","state":"enabled","runtime_generation":7,"bindings":[]}`)
		}
		put(f, "deplugin_retired_plugins", `{"version":1,"completed":`+completed+`,"retired_at":"2026-09-24T14:38:04.441369821Z","plugins":{`+strings.Join(entries, ",")+`}}`)
	}
	// A third-party installation that is enabled, and the settings 272 must
	// leave alone.
	thirdParty := func(f fixture) int64 {
		f.t.Helper()
		id := install(f, other, "enabled", false)
		bind(f, id, "openai.oauth.outbound_transport.v1", "openai", "oauth", true, 37)
		put(f, "admin_observability_config", `{"telemetry_enabled":true,"theme_enabled":false}`)
		put(f, "deplugin_retired_plugins_note", "kept: not the receipt")
		return id
	}
	// Every upstream column and the row versions of what 272 must leave alone.
	kept := func(f fixture, id int64) map[string]string {
		f.t.Helper()
		return map[string]string{
			"installation": text(f, `SELECT row_to_json(r)::text FROM (
				SELECT ctid, id, plugin_key, name, version, description, author, manifest, artifact_path, install_path, binary_path, binary_sha256,
				       signature_status, state, config_encrypted, last_error, installed_by, installed_at, enabled_at, updated_at, encode(artifact_data, 'hex') AS artifact_data
				FROM sub2api_plugin_installations WHERE id = $1) AS r`, id),
			"bindings":       text(f, `SELECT json_agg(json_build_array(ctid::text, to_jsonb(b)) ORDER BY id)::text FROM sub2api_plugin_bindings AS b WHERE plugin_id = $1`, id),
			"observability":  text(f, `SELECT ctid::text || '/' || value FROM settings WHERE key = 'admin_observability_config'`),
			"similar key":    text(f, `SELECT ctid::text || '/' || value FROM settings WHERE key = 'deplugin_retired_plugins_note'`),
			"other settings": text(f, `SELECT count(*)::text FROM settings WHERE key <> 'deplugin_retired_plugins'`),
			"other plugins":  text(f, `SELECT count(*)::text FROM sub2api_plugin_installations WHERE plugin_key <> ALL($1)`, firstParty),
			"other bindings": text(f, `SELECT count(*)::text FROM sub2api_plugin_bindings WHERE plugin_id IN (SELECT id FROM sub2api_plugin_installations WHERE plugin_key <> ALL($1))`, firstParty),
		}
	}

	t.Run("fresh database", func(t *testing.T) {
		f, upstream := open(t)
		clean(f, upstream)
		apply(f)
		clean(f, upstream)
	})

	t.Run("retired installations of the previous release", func(t *testing.T) {
		f, upstream := open(t)
		clean(f, upstream)
		restoreBundleSchema(f, upstream)

		ids := make([]int64, 0, len(firstParty))
		for _, key := range firstParty {
			blank := key == "codexrip.codex-runtime"
			id := install(f, key, "disabled", blank)
			ids = append(ids, id)
			if blank {
				continue
			}
			bind(f, id, "extensions.admin.v1", "*", "*", false, 100)
			bind(f, id, "extensions.ui.v1", "*", "*", false, 100)
			exec(f, `INSERT INTO sub2api_plugin_bootstrap (plugin_key, bundle_sha256, migration_profile, desired_enabled, completed, state_imported, user_removed)
				VALUES ($1, 'fixture', 'fixture-v1', true, true, true, true)`, key)
		}
		exec(f, `INSERT INTO sub2api_plugin_updates (plugin_id, artifact_data, package_sha256, source_generation, update_policy) VALUES ($1, '\x0a0b'::bytea, $2, 7, 'bundled')`,
			ids[0], strings.Repeat("d", 64))
		receipt(f, "true", firstParty...)
		otherID := thirdParty(f)
		require.Equal(t, 8, purged(f))
		require.Equal(t, 12, count(f, `SELECT count(*) FROM sub2api_plugin_bindings WHERE plugin_id = ANY($1)`, ids))

		before := kept(f, otherID)
		for run := 1; run <= 2; run++ {
			apply(f)
			require.Zero(t, purged(f), "run %d", run)
			require.Zero(t, count(f, `SELECT count(*) FROM sub2api_plugin_bindings WHERE plugin_id = ANY($1)`, ids), "run %d", run)
			require.Equal(t, upstream, tables(f, "public"), "run %d", run)
			require.Empty(t, bundle(f), "run %d", run)
			require.Equal(t, before, kept(f, otherID), "run %d changes nothing else", run)
		}
	})

	// Only an installation without any receipt is retired by an earlier release;
	// a receipt that is stored but does not cover an installation is not.
	const (
		earlier   = " in v0.2.13-codexrip.8 as .downstream/native-domains.md describes, then upgrade"
		unretired = "; start v0.2.13-codexrip.8 once as .downstream/native-domains.md describes, then upgrade"
		uncovered = "; no release repairs it, resolve it as .downstream/native-domains.md describes, then upgrade"
	)
	for _, test := range []struct {
		name      string
		installed []string // first-party installations the database holds
		completed string   // the receipt's completed value; "" for no receipt
		entries   []string // the receipt's entries
		store     func(f fixture, otherID int64)
		refusal   string // what 272 raises; "" when it applies
	}{
		{name: "installation without a receipt", installed: []string{"codexrip.admin-observability"},
			refusal: "first-party plugin installations were never retired (codexrip.admin-observability)" + unretired},
		{name: "receipt that is not completed", installed: []string{"codexrip.admin-observability"}, completed: "false", entries: []string{"codexrip.admin-observability"},
			refusal: "the retirement receipt is not completed or has no entry for first-party plugin installations (codexrip.admin-observability)" + uncovered},
		{name: "receipt without an entry for an installation", installed: []string{"codexrip.admin-observability", "codexrip.model-policy", "codexrip.account-tools"},
			completed: "true", entries: []string{"codexrip.admin-observability"},
			refusal: "the retirement receipt is not completed or has no entry for first-party plugin installations (codexrip.account-tools, codexrip.model-policy)" + uncovered},
		{name: "receipt without an installation", completed: "false", entries: []string{"codexrip.admin-observability"}},
		{name: "third-party installation in the state updating", completed: "true",
			store: func(f fixture, otherID int64) {
				exec(f, `UPDATE sub2api_plugin_installations SET state = 'updating' WHERE id = $1`, otherID)
			},
			refusal: "plugin installations are in a state the plugin manager does not have (acme.transport is updating); disable or uninstall them" + earlier},
		{name: "two third-party bindings enabled for one scope", completed: "true",
			store: func(f fixture, otherID int64) {
				bind(f, otherID, "extensions.request.v1", "openai", "oauth", true, 100)
				bind(f, install(f, second, "enabled", false), "extensions.request.v1", "openai", "oauth", true, 100)
			},
			refusal: "more than one enabled plugin binding shares a scope (extensions.request.v1 openai/oauth); disable all but one of the plugins" + earlier},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, upstream := open(t)
			clean(f, upstream)
			restoreBundleSchema(f, upstream)
			for _, key := range test.installed {
				bind(f, install(f, key, "enabled", false), "extensions.admin.v1", "*", "*", true, 100)
			}
			if test.completed != "" {
				receipt(f, test.completed, test.entries...)
			}
			otherID := thirdParty(f)
			if test.store != nil {
				test.store(f, otherID)
			}
			if test.refusal != "" {
				// The runner applies a file in one transaction, so a file that
				// raises changes nothing: only what it raises is checked here.
				_, err := f.tx.ExecContext(ctx, migration)
				require.ErrorContains(t, err, "migration 272: "+test.refusal)
				return
			}
			before := kept(f, otherID)
			apply(f)
			require.Zero(t, purged(f))
			require.Equal(t, upstream, tables(f, "public"))
			require.Empty(t, bundle(f))
			require.Equal(t, before, kept(f, otherID))
		})
	}
}
