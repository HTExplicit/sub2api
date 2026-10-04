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

const (
	imageStudioDropMigration   = "270_drop_image_studio.sql"
	imageStudioTablesMigration = "233_image_studio_jobs.sql"
)

// TestMain applied every migration, 270 included, to an empty database: the
// fresh-database run. The stored case restores the tables 270 drops by running
// 233 again, stores the rows a database of the previous release can hold next
// to rows that must stay, applies 270 twice and checks what went and what
// stayed.
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
	put := func(f fixture, key, value string) {
		f.t.Helper()
		exec(f, `INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, value)
	}
	ctid := func(f fixture, table, column string, key any) string {
		f.t.Helper()
		return text(f, `SELECT ctid::text FROM `+table+` WHERE `+column+` = $1`, key)
	}
	// The Image Studio switch.
	switchRows := func(f fixture) int {
		f.t.Helper()
		return count(f, `SELECT count(*) FROM settings WHERE key = 'image_tools_config'`)
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
	apply := func(f fixture) { f.t.Helper(); exec(f, migration) }

	t.Run("fresh database", func(t *testing.T) {
		f := open(t)
		require.Zero(t, storedObjects(f), "270 already dropped the tables 233 created")
		require.Zero(t, switchRows(f))
		apply(f)
		require.Zero(t, storedObjects(f))
		require.Zero(t, switchRows(f))
	})

	t.Run("stored jobs and the switch", func(t *testing.T) {
		f := open(t)
		restoreTables(f)

		userID, keyID := storeJob(f, "migration-270-stored")
		put(f, "image_tools_config", `{"studio_enabled":true}`)
		put(f, "admin_observability_config", `{"telemetry_enabled":true,"theme_enabled":false}`)
		put(f, "image_tools_config_note", "kept: not the Image Studio switch")
		exec(f, `INSERT INTO sub2api_plugin_installations
			(plugin_key, name, version, manifest, artifact_path, install_path, binary_path, binary_sha256, config_encrypted, artifact_data)
			VALUES ('codexrip.image-tools', 'fixture', '0.2.13', '{"id":"codexrip.image-tools"}', '/data/plugins/fixture.zip',
				'/data/plugins/fixture', '/data/plugins/fixture/plugin', repeat('b', 64), 'fixture-config-cipher', '\x040506')`)

		// The row versions of everything 270 must leave alone, and every column
		// of the plugin installation.
		kept := func() map[string]string {
			return map[string]string{
				"user":           ctid(f, "users", "id", userID),
				"api key":        ctid(f, "api_keys", "id", keyID),
				"observability":  ctid(f, "settings", "key", "admin_observability_config"),
				"similar key":    ctid(f, "settings", "key", "image_tools_config_note"),
				"other settings": text(f, `SELECT count(*)::text FROM settings WHERE key <> 'image_tools_config'`),
				"installation":   text(f, `SELECT ctid::text || '/' || i::text FROM sub2api_plugin_installations AS i WHERE plugin_key = 'codexrip.image-tools'`),
			}
		}
		before := kept()

		for run := 1; run <= 2; run++ {
			apply(f)
			require.Zero(t, storedObjects(f), "run %d", run)
			require.Zero(t, switchRows(f), "run %d", run)
			require.Equal(t, before, kept(), "run %d changes nothing else", run)
		}
	})
}
