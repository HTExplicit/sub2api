//go:build integration

package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

const (
	imageStudioDropMigration   = "270_drop_image_studio.sql"
	imageStudioTablesMigration = "233_image_studio_jobs.sql"
)

// TestMain applied every migration, 270 included, to an empty database: the
// fresh-database run. Each case restores the tables 270 drops by running 233
// again, stores the rows a database of the previous release can hold next to
// rows that must stay, applies 270 and checks what went and what stayed.
func TestMigration270DropsImageStudio(t *testing.T) {
	ctx := context.Background()
	read := func(name string) string {
		t.Helper()
		content, err := dbmigrations.FS.ReadFile(name)
		require.NoError(t, err)
		return string(content)
	}
	migration, tables := read(imageStudioDropMigration), read(imageStudioTablesMigration)

	type fixture struct {
		tx *sql.Tx
		t  *testing.T
	}
	open := func(t *testing.T) fixture { return fixture{tx: testTx(t), t: t} }
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
	setting := func(f fixture, key string) (string, bool) {
		f.t.Helper()
		var value string
		err := f.tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&value)
		if errors.Is(err, sql.ErrNoRows) {
			return "", false
		}
		require.NoError(f.t, err)
		return value, true
	}
	put := func(f fixture, key, value string) {
		f.t.Helper()
		exec(f, `INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, value)
	}
	ctid := func(f fixture, table, column string, key any) string {
		f.t.Helper()
		return text(f, `SELECT ctid::text FROM `+table+` WHERE `+column+` = $1`, key)
	}
	// Tables, indexes and sequences of 233, and the constraints that name them.
	storedObjects := func(f fixture) int {
		f.t.Helper()
		return count(f, `SELECT (SELECT count(*) FROM pg_class WHERE relname LIKE 'image\_studio\_%')
			+ (SELECT count(*) FROM pg_constraint WHERE conname LIKE 'image\_studio\_%')`)
	}
	restoreTables := func(f fixture) {
		f.t.Helper()
		require.Zero(f.t, storedObjects(f))
		exec(f, tables)
		require.Equal(f.t, 3, count(f, `SELECT count(*) FROM unnest(ARRAY['image_studio_jobs', 'image_studio_items', 'image_studio_artifacts']) AS name WHERE to_regclass(name) IS NOT NULL`))
	}
	// One finished job with its item and stored image record; returns the
	// owner and the API key the job references.
	storeJob := func(f fixture, name string) (userID, keyID int64) {
		f.t.Helper()
		var jobID, itemID int64
		require.NoError(f.t, f.tx.QueryRowContext(ctx, `INSERT INTO users (email, password_hash, balance) VALUES ($1, 'fixture', 42) RETURNING id`, name+"@example.invalid").Scan(&userID))
		require.NoError(f.t, f.tx.QueryRowContext(ctx, `INSERT INTO api_keys (user_id, key, name) VALUES ($1, $2, 'migration-270') RETURNING id`, userID, name+"-fixture-key").Scan(&keyID))
		require.NoError(f.t, f.tx.QueryRowContext(ctx, `
INSERT INTO image_studio_jobs (user_id, api_key_id, mode, model, prompt, count, status, processed_count, succeeded_count)
VALUES ($1, $2, 'generate', 'gpt-image-2', 'fixture prompt', 1, 'succeeded', 1, 1) RETURNING id`, userID, keyID).Scan(&jobID))
		require.NoError(f.t, f.tx.QueryRowContext(ctx, `INSERT INTO image_studio_items (job_id, ordinal, status) VALUES ($1, 1, 'succeeded') RETURNING id`, jobID).Scan(&itemID))
		exec(f, `INSERT INTO image_studio_artifacts (job_id, item_id, kind, storage_key, content_type, byte_size, expires_at)
			VALUES ($1, $2, 'output', $3, 'image/png', 68, NOW() + INTERVAL '1 day')`, jobID, itemID, strings.Repeat("a", 32)+".png")
		return userID, keyID
	}
	const imageTools = "codexrip.image-tools"
	installation := func(f fixture, key, name, state, cipher string, artifact []byte) int64 {
		f.t.Helper()
		var id int64
		require.NoError(f.t, f.tx.QueryRowContext(ctx, `
INSERT INTO sub2api_plugin_installations
	(plugin_key, name, version, description, author, manifest, artifact_path, install_path, binary_path, binary_sha256,
	 signature_status, state, config_encrypted, last_error, enabled_at, artifact_data, runtime_generation, package_sha256, update_policy)
VALUES ($1, $2, '0.2.13', 'fixture description', 'fixture author', $3::jsonb, $4, $5, $6, $7, 'trusted', $8, $9, 'fixture last error', NOW(), $10, 9, $11, 'bundled')
RETURNING id`,
			key, name, `{"id":"`+key+`","capabilities":[{"id":"extensions.request.v1","platform":"*","account_type":"*"}]}`,
			"/data/plugins/"+key+".zip", "/data/plugins/"+key, "/data/plugins/"+key+"/plugin", strings.Repeat("b", 64),
			state, cipher, artifact, strings.Repeat("c", 64)).Scan(&id))
		exec(f, `INSERT INTO sub2api_plugin_bindings (plugin_id, capability, platform, account_type, enabled, rollout_percent) VALUES
			($1, 'extensions.request.v1', '*', '*', false, 100), ($1, 'extensions.admin.v1', '*', '*', false, 100)`, id)
		exec(f, `INSERT INTO sub2api_plugin_updates (plugin_id, artifact_data, package_sha256, source_generation, update_policy) VALUES ($1, '\x0a0b'::bytea, $2, 9, 'bundled')`, id, strings.Repeat("d", 64))
		return id
	}
	installationRow := func(f fixture, id int64) string {
		f.t.Helper()
		return text(f, `SELECT row_to_json(row)::text FROM (
			SELECT plugin_key, name, version, description, author, manifest, artifact_path, install_path, binary_path, binary_sha256,
			       signature_status, state, config_encrypted, last_error, enabled_at IS NOT NULL AS enabled, encode(artifact_data, 'hex') AS artifact,
			       runtime_generation, package_sha256, update_policy
			FROM sub2api_plugin_installations WHERE id = $1) AS row`, id)
	}
	receipt := func(f fixture, plugins map[string]string) {
		f.t.Helper()
		entries := make([]string, 0, len(plugins))
		for key, entry := range plugins {
			entries = append(entries, `"`+key+`":`+entry)
		}
		put(f, service.NativeFeatureRetirementSetting, `{"version":1,"completed":true,"retired_at":"2026-09-24T11:20:00.123456789Z","plugins":{`+strings.Join(entries, ",")+`}}`)
	}
	apply := func(f fixture) { f.t.Helper(); exec(f, migration) }
	clean := func(f fixture) {
		f.t.Helper()
		require.Zero(f.t, count(f, `SELECT count(*) FROM sub2api_plugin_installations WHERE plugin_key IN ('codexrip.image-tools', 'codexrip.admin-observability', 'acme.other-plugin')`), "this fixture requires an isolated test database")
		require.Zero(f.t, count(f, `SELECT count(*) FROM settings WHERE key IN ('deplugin_retired_plugins', 'image_tools_config')`))
	}

	t.Run("fresh database", func(t *testing.T) {
		f := open(t)
		clean(f)
		require.Zero(t, storedObjects(f), "270 already dropped the tables 233 created")
		apply(f)
		clean(f)
		require.Zero(t, storedObjects(f))
	})

	t.Run("stored jobs, the switch and a retired installation", func(t *testing.T) {
		f := open(t)
		clean(f)
		restoreTables(f)

		userID, keyID := storeJob(f, "migration-270-stored")
		put(f, "image_tools_config", `{"studio_enabled":true}`)
		put(f, "admin_observability_config", `{"telemetry_enabled":true,"theme_enabled":false}`)
		put(f, "image_tools_config_note", "kept: not the Image Studio switch")

		imageID := installation(f, imageTools, "图片工具", "disabled", "fixture-image-config-cipher", []byte{4, 5, 6})
		siblingID := installation(f, "codexrip.admin-observability", "界面与观测", "disabled", "fixture-sibling-config-cipher", []byte{1, 2, 3})
		otherID := installation(f, "acme.other-plugin", "Other", "disabled", "fixture-other-config-cipher", []byte{7, 8, 9})
		exec(f, `INSERT INTO sub2api_plugin_bootstrap (plugin_key, bundle_sha256, migration_profile, desired_enabled, completed, state_imported, user_removed) VALUES
			($1, 'fixture', 'image-tools-v1', true, true, true, true), ('codexrip.admin-observability', 'fixture', 'admin-observability-v1', true, true, true, true)`, imageTools)
		receipt(f, map[string]string{
			imageTools:                     `{"id":42,"key":"codexrip.image-tools","state":"enabled","runtime_generation":3,"bindings":[{"id":1,"capability":"extensions.request.v1","platform":"*","account_type":"*","enabled":true,"rollout_percent":100}],"bootstrap":{"plugin_key":"codexrip.image-tools","bundle_sha256":"fixture"}}`,
			"codexrip.admin-observability": `{"id":41,"key":"codexrip.admin-observability","state":"enabled","runtime_generation":3,"bindings":[]}`,
		})

		// The row versions and contents of everything 270 must leave alone.
		kept := func() map[string]string {
			stored, _ := setting(f, service.NativeFeatureRetirementSetting)
			return map[string]string{
				"user":             ctid(f, "users", "id", userID),
				"api key":          ctid(f, "api_keys", "id", keyID),
				"receipt":          ctid(f, "settings", "key", service.NativeFeatureRetirementSetting) + "/" + stored,
				"observability":    ctid(f, "settings", "key", "admin_observability_config"),
				"similar key":      ctid(f, "settings", "key", "image_tools_config_note"),
				"sibling":          ctid(f, "sub2api_plugin_installations", "id", siblingID) + "/" + installationRow(f, siblingID),
				"third party":      ctid(f, "sub2api_plugin_installations", "id", otherID) + "/" + installationRow(f, otherID),
				"other settings":   text(f, `SELECT count(*)::text FROM settings WHERE key <> 'image_tools_config'`),
				"sibling bindings": text(f, `SELECT count(*)::text FROM sub2api_plugin_bindings WHERE plugin_id IN ($1, $2)`, siblingID, otherID),
				"sibling updates":  text(f, `SELECT count(*)::text FROM sub2api_plugin_updates WHERE plugin_id IN ($1, $2)`, siblingID, otherID),
				"sibling record":   text(f, `SELECT count(*)::text FROM sub2api_plugin_bootstrap WHERE plugin_key = 'codexrip.admin-observability'`),
			}
		}
		before := kept()
		require.Equal(t, "4", before["sibling bindings"])
		require.Equal(t, "2", before["sibling updates"])
		require.Equal(t, "1", before["sibling record"])

		for run := 1; run <= 2; run++ {
			var purged string
			if run == 2 {
				purged = ctid(f, "sub2api_plugin_installations", "id", imageID)
			}
			apply(f)

			require.Zero(t, storedObjects(f), "run %d", run)
			_, found := setting(f, "image_tools_config")
			require.False(t, found, "run %d", run)
			require.Equal(t, before, kept(), "run %d changes nothing else", run)

			require.JSONEq(t, `{"plugin_key":"codexrip.image-tools","name":"图片工具","version":"0.2.13","description":"","author":"fixture author",
				"manifest":{},"artifact_path":"","install_path":"","binary_path":"","binary_sha256":"","signature_status":"unsigned","state":"disabled",
				"config_encrypted":"","last_error":"","enabled":true,"artifact":null,"runtime_generation":9,"package_sha256":"","update_policy":"pinned"}`,
				installationRow(f, imageID))
			for _, table := range []string{"sub2api_plugin_bindings", "sub2api_plugin_updates"} {
				require.Zero(t, count(f, `SELECT count(*) FROM `+table+` WHERE plugin_id = $1`, imageID), table)
			}
			require.Zero(t, count(f, `SELECT count(*) FROM sub2api_plugin_bootstrap WHERE plugin_key = $1`, imageTools))
			if run == 2 {
				require.Equal(t, purged, ctid(f, "sub2api_plugin_installations", "id", imageID), "a second run rewrites nothing")
			}
		}
	})

	// An installation the retirement never processed is not purged unseen.
	for _, test := range []struct {
		name    string
		plugins map[string]string
	}{
		{name: "never retired: no receipt"},
		{name: "never retired: a receipt without its entry", plugins: map[string]string{
			"codexrip.admin-observability": `{"id":41,"key":"codexrip.admin-observability","state":"enabled","runtime_generation":3,"bindings":[]}`,
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := open(t)
			clean(f)
			restoreTables(f)
			storeJob(f, "migration-270-unretired")
			put(f, "image_tools_config", `{"studio_enabled":true}`)
			imageID := installation(f, imageTools, "图片工具", "enabled", "fixture-image-config-cipher", []byte{4, 5, 6})
			if test.plugins != nil {
				receipt(f, test.plugins)
			}
			objects, row := storedObjects(f), installationRow(f, imageID)

			exec(f, `SAVEPOINT before_270`)
			_, err := f.tx.ExecContext(ctx, migration)
			require.ErrorContains(t, err, "was never retired")
			exec(f, `ROLLBACK TO SAVEPOINT before_270`)

			require.Equal(t, objects, storedObjects(f), "a refused migration changes nothing")
			require.Equal(t, 1, count(f, `SELECT count(*) FROM image_studio_artifacts`))
			_, found := setting(f, "image_tools_config")
			require.True(t, found)
			require.Equal(t, row, installationRow(f, imageID))
			require.Equal(t, 2, count(f, `SELECT count(*) FROM sub2api_plugin_bindings WHERE plugin_id = $1`, imageID))
		})
	}
}
