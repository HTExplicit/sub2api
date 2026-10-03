//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

const (
	codexRuntimePurgeMigration = "264_purge_codex_runtime_plugin_data.sql"
	codexRuntimeZstdTrueHash   = "7818a2a138b902a949b9fd77ba6bb62d8d77dcfbfa93ca1a16e778c609172304"
	codexRuntimeZstdFalseHash  = "1ee89db3b3c06e8da7f0524370502e503df48204fa16813ded97b7981b653aaa"
)

// TestMain applied every migration, 264 included, to an empty database: the
// fresh-database run. Each case restores the tables 264 drops and the rows a
// database of the previous release can hold, applies 264 and checks what went
// and what stayed.
func TestMigration264PurgesCodexRuntimePluginData(t *testing.T) {
	ctx := context.Background()
	content, err := dbmigrations.FS.ReadFile(codexRuntimePurgeMigration)
	require.NoError(t, err)
	migration := string(content)

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
	droppedTables := func(f fixture) int {
		f.t.Helper()
		return count(f, `SELECT count(*) FROM unnest(ARRAY['sub2api_plugin_state', 'sub2api_plugin_leases']) AS name WHERE to_regclass(name) IS NOT NULL`)
	}
	// The two tables of 246, which 264 drops.
	restoreSchema := func(f fixture) {
		f.t.Helper()
		require.Zero(f.t, droppedTables(f))
		exec(f, `CREATE TABLE sub2api_plugin_state (
			plugin_key VARCHAR(160) NOT NULL, namespace VARCHAR(128) NOT NULL, state_key VARCHAR(256) NOT NULL,
			revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0), value JSONB NOT NULL, next_at TIMESTAMPTZ,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), PRIMARY KEY (plugin_key, namespace, state_key))`)
		exec(f, `CREATE INDEX sub2api_plugin_state_due ON sub2api_plugin_state(plugin_key,namespace,next_at) WHERE next_at IS NOT NULL`)
		exec(f, `CREATE TABLE sub2api_plugin_leases (
			plugin_key VARCHAR(160) NOT NULL, namespace VARCHAR(128) NOT NULL, lease_key VARCHAR(256) NOT NULL,
			owner VARCHAR(256) NOT NULL, generation BIGINT NOT NULL DEFAULT 1 CHECK (generation > 0),
			expires_at TIMESTAMPTZ NOT NULL, PRIMARY KEY (plugin_key, namespace, lease_key))`)
		require.Equal(f.t, 2, droppedTables(f))
	}
	const codex = "codexrip.codex-runtime"
	installation := func(f fixture, key, name, description, cipher string, artifact []byte) int64 {
		f.t.Helper()
		var id int64
		require.NoError(f.t, f.tx.QueryRowContext(ctx, `
INSERT INTO sub2api_plugin_installations
	(plugin_key, name, version, description, author, manifest, artifact_path, install_path, binary_path, binary_sha256,
	 signature_status, state, config_encrypted, last_error, enabled_at, artifact_data, runtime_generation, package_sha256, update_policy)
VALUES ($1, $2, '0.2.13', $3, 'fixture author', $4::jsonb, $5, $6, $7, $8, $9, 'disabled', $10, $11, NOW(), $12, 9, $13, $14)
RETURNING id`,
			key, name, description, `{"id":"`+key+`","capabilities":[{"id":"extensions.request.v1","platform":"openai","account_type":"oauth"}]}`,
			"/data/plugins/"+key+".zip", "/data/plugins/"+key, "/data/plugins/"+key+"/plugin", strings.Repeat("b", 64),
			"trusted", cipher, "fixture last error", artifact, strings.Repeat("c", 64), "bundled").Scan(&id))
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
	record := func(hash string) string {
		return `{"version":1,"config_version":2,"config_sha256":"` + hash + `"}`
	}
	apply := func(f fixture) { f.t.Helper(); exec(f, migration) }
	clean := func(f fixture) {
		f.t.Helper()
		require.Zero(f.t, count(f, `SELECT count(*) FROM sub2api_plugin_installations WHERE plugin_key IN ('codexrip.codex-runtime', 'codexrip.image-tools', 'acme.other-plugin')`), "this fixture requires an isolated test database")
		require.Zero(f.t, count(f, `SELECT count(*) FROM settings WHERE key IN ('deplugin_retired_plugins', 'codex_native_runtime_source', 'codex_native_runtime_config', 'codex_runtime_config')`))
	}

	t.Run("fresh database", func(t *testing.T) {
		f := open(t)
		clean(f)
		require.Zero(t, droppedTables(f), "264 already dropped the state and lease tables")
		apply(f)
		clean(f)
		require.Zero(t, droppedTables(f))
	})

	t.Run("retired installation with a saved configuration", func(t *testing.T) {
		f := open(t)
		clean(f)
		restoreSchema(f)

		codexID := installation(f, codex, "Codex 运行扩展", "fixture description", "fixture-codex-config-cipher", []byte{1, 2, 3})
		imageID := installation(f, "codexrip.image-tools", "图片工具", "sibling retired plugin", "fixture-image-config-cipher", []byte{4, 5, 6})
		otherID := installation(f, "acme.other-plugin", "Other", "third-party plugin", "fixture-other-config-cipher", []byte{7, 8, 9})
		for _, id := range []int64{codexID, imageID, otherID} {
			exec(f, `INSERT INTO sub2api_plugin_bindings (plugin_id, capability, platform, account_type, enabled, rollout_percent) VALUES
				($1, 'extensions.request.v1', 'openai', 'oauth', false, 100), ($1, 'extensions.admin.v1', 'openai', 'oauth', false, 100)`, id)
			exec(f, `INSERT INTO sub2api_plugin_updates (plugin_id, artifact_data, package_sha256, source_generation, update_policy) VALUES ($1, '\x0a0b'::bytea, $2, 9, 'bundled')`, id, strings.Repeat("d", 64))
		}
		exec(f, `INSERT INTO sub2api_plugin_bootstrap (plugin_key, bundle_sha256, migration_profile, desired_enabled, completed, state_imported, user_removed) VALUES
			($1, 'fixture', 'codex-runtime-v1', true, true, true, true), ('codexrip.image-tools', 'fixture', 'image-tools-v1', true, true, true, true)`, codex)

		receipt(f, map[string]string{
			codex:                  `{"id":41,"key":"codexrip.codex-runtime","state":"enabled","runtime_generation":8,"bindings":[{"id":1,"capability":"extensions.request.v1","platform":"openai","account_type":"oauth","enabled":true,"rollout_percent":100}],"bootstrap":{"plugin_key":"codexrip.codex-runtime","bundle_sha256":"fixture"}}`,
			"codexrip.image-tools": `{"id":42,"key":"codexrip.image-tools","state":"enabled","runtime_generation":3,"bindings":[]}`,
		})
		put(f, "codex_native_runtime_source", "0W4ImxvW4Uv4FtA8OnXubVaSZLizP3M5zpFTKdIfruNAlfRUIUMxFF7hld+gJuPMHC4=")
		put(f, "codex_native_runtime_config", record(codexRuntimeZstdFalseHash))
		put(f, "image_tools_config", `{"studio_enabled":false}`)
		put(f, "openai_codex_client_version", "0.156.0")

		const private = "codex-routing-private"
		for _, key := range []string{"quality-run.11111111-1111-4111-8111-111111111111", "validation.22222222-2222-4222-8222-222222222222", "spent.m264", "wire.41", "other.m264-unclassified"} {
			exec(f, `INSERT INTO sub2api_plugin_state (plugin_key, namespace, state_key, value, next_at) VALUES ($1, $2, $3, '{"fixture":true}'::jsonb, NOW())`, codex, private, key)
		}
		exec(f, `INSERT INTO sub2api_plugin_state (plugin_key, namespace, state_key, value) VALUES ($1, 'another-namespace', 'm264', '{"fixture":true}'::jsonb)`, codex)
		exec(f, `INSERT INTO sub2api_plugin_leases (plugin_key, namespace, lease_key, owner, expires_at) VALUES ($1, 'routing-account', 'm264', 'owner', NOW()), ('acme.other-plugin', 'routing-account', 'm264', 'owner', NOW())`, codex)

		account := func(name, platform, accountType, extra string, deleted bool) int64 {
			t.Helper()
			var id int64
			require.NoError(t, f.tx.QueryRowContext(ctx, `
INSERT INTO accounts (name, platform, type, extra, deleted_at) VALUES ($1, $2, $3, $4::jsonb, CASE WHEN $5 THEN NOW() END) RETURNING id`,
				name, platform, accountType, extra, deleted).Scan(&id))
			return id
		}
		const selected = `{"v":2,"source":"reference_derived_windows_cli","originator":"codex_exec","client_version":"0.156.0","os_type":"Windows","os_version":"10.0.26220","arch":"x86_64","terminal":"dumb","sandbox":"windows_sandbox","generated_at":"2026-09-26T08:00:00Z"}`
		const derived = `{"v":1,"os_type":"Mac OS","os_version":"15.6.1","arch":"arm64","terminal":"Apple_Terminal/455","sandbox":"seatbelt","generated_at":"2026-09-20T08:00:00Z"}`
		selectedID := account("migration-264-selected-profile", "openai", "oauth", `{"openai_long_context_billing_enabled":true,"codex_fingerprint_mode":"device","codex_fingerprint_seed":"11111111-1111-4111-8111-111111111111","codex_client_identity":`+selected+`}`, false)
		deletedID := account("migration-264-deleted-selected-profile", "openai", "oauth", `{"openai_long_context_billing_enabled":false,"codex_fingerprint_seed":"22222222-2222-4222-8222-222222222222","codex_client_identity":`+selected+`}`, true)
		membersID := account("migration-264-extra-members", "openai", "oauth", `{"openai_long_context_billing_enabled":true,"codex_fingerprint_seed":"33333333-3333-4333-8333-333333333333","codex_client_identity":{"source":"","originator":"","client_version":"","v":1,"os_type":"Mac OS","os_version":"15.6.1","arch":"arm64","terminal":"Apple_Terminal/455","sandbox":"seatbelt","generated_at":"2026-09-20T08:00:00Z"}}`, false)
		derivedID := account("migration-264-derived-explicit-off", "openai", "oauth", `{"openai_long_context_billing_enabled":false,"codex_fingerprint_mode":"off","codex_fingerprint_seed":"44444444-4444-4444-8444-444444444444","codex_client_identity":`+derived+`}`, false)
		noModeID := account("migration-264-no-mode", "openai", "oauth", `{"openai_long_context_billing_enabled":true}`, false)
		blankModeID := account("migration-264-setup-token-blank-mode", "openai", "setup-token", `{"openai_long_context_billing_enabled":false,"codex_fingerprint_mode":" "}`, false)
		apiKeyID := account("migration-264-api-key", "openai", "apikey", `{"openai_long_context_billing_enabled":true}`, false)
		claudeID := account("migration-264-other-platform", "anthropic", "oauth", `{}`, false)
		arrayID := account("migration-264-array-extra", "anthropic", "oauth", `[1, 2, 3]`, false)
		accountIDs := []int64{selectedID, deletedID, membersID, derivedID, noModeID, blankModeID, apiKeyID, claudeID, arrayID}

		ctid := func(table, column string, key any) string {
			t.Helper()
			return text(f, `SELECT ctid::text FROM `+table+` WHERE `+column+` = $1`, key)
		}
		untouchedAccounts := map[int64]string{}
		for _, id := range []int64{derivedID, apiKeyID, claudeID, arrayID} {
			untouchedAccounts[id] = ctid("accounts", "id", id)
		}
		untouchedSettings := map[string]string{}
		for _, key := range []string{service.NativeFeatureRetirementSetting, "image_tools_config", "openai_codex_client_version"} {
			untouchedSettings[key] = ctid("settings", "key", key)
		}
		imageBefore, otherBefore := installationRow(f, imageID), installationRow(f, otherID)
		imageCTID, otherCTID := ctid("sub2api_plugin_installations", "id", imageID), ctid("sub2api_plugin_installations", "id", otherID)
		outboxBefore := count(f, `SELECT count(*) FROM scheduler_outbox WHERE account_id = ANY($1)`, accountIDs)

		apply(f)

		value, found := setting(f, "codex_runtime_config")
		require.True(t, found)
		require.Equal(t, `{"request_zstd":false}`, value)
		var config struct {
			RequestZstd bool `json:"request_zstd"`
		}
		require.NoError(t, service.DecodeSwitchSettings([]byte(value), &config, "request_zstd"), "the strict reader accepts the migrated row")
		require.Zero(t, count(f, `SELECT count(*) FROM settings WHERE key IN ('codex_native_runtime_source', 'codex_native_runtime_config')`))
		for key, before := range untouchedSettings {
			require.Equal(t, before, ctid("settings", "key", key), key)
		}

		require.Zero(t, droppedTables(f))

		require.JSONEq(t, `{"plugin_key":"codexrip.codex-runtime","name":"Codex 运行扩展","version":"0.2.13","description":"","author":"fixture author",
			"manifest":{},"artifact_path":"","install_path":"","binary_path":"","binary_sha256":"","signature_status":"unsigned","state":"disabled",
			"config_encrypted":"","last_error":"","enabled":true,"artifact":null,"runtime_generation":9,"package_sha256":"","update_policy":"pinned"}`,
			installationRow(f, codexID))
		require.Equal(t, imageBefore, installationRow(f, imageID))
		require.Equal(t, otherBefore, installationRow(f, otherID))
		require.Equal(t, imageCTID, ctid("sub2api_plugin_installations", "id", imageID))
		require.Equal(t, otherCTID, ctid("sub2api_plugin_installations", "id", otherID))
		for table, column := range map[string]string{"sub2api_plugin_bindings": "plugin_id", "sub2api_plugin_updates": "plugin_id"} {
			require.Zero(t, count(f, `SELECT count(*) FROM `+table+` WHERE `+column+` = $1`, codexID), table)
			require.NotZero(t, count(f, `SELECT count(*) FROM `+table+` WHERE `+column+` = $1`, imageID), table)
			require.NotZero(t, count(f, `SELECT count(*) FROM `+table+` WHERE `+column+` = $1`, otherID), table)
		}
		require.Zero(t, count(f, `SELECT count(*) FROM sub2api_plugin_bootstrap WHERE plugin_key = $1`, codex))
		require.Equal(t, 1, count(f, `SELECT count(*) FROM sub2api_plugin_bootstrap WHERE plugin_key = 'codexrip.image-tools'`))

		require.JSONEq(t, `{"openai_long_context_billing_enabled":true,"codex_fingerprint_mode":"device","codex_fingerprint_seed":"11111111-1111-4111-8111-111111111111"}`, text(f, `SELECT extra::text FROM accounts WHERE id = $1`, selectedID))
		require.JSONEq(t, `{"openai_long_context_billing_enabled":false,"codex_fingerprint_seed":"22222222-2222-4222-8222-222222222222"}`, text(f, `SELECT extra::text FROM accounts WHERE id = $1`, deletedID))
		require.JSONEq(t, `{"openai_long_context_billing_enabled":true,"codex_fingerprint_mode":"device","codex_fingerprint_seed":"33333333-3333-4333-8333-333333333333","codex_client_identity":`+derived+`}`, text(f, `SELECT extra::text FROM accounts WHERE id = $1`, membersID))
		require.JSONEq(t, `{"openai_long_context_billing_enabled":true,"codex_fingerprint_mode":"device"}`, text(f, `SELECT extra::text FROM accounts WHERE id = $1`, noModeID))
		require.JSONEq(t, `{"openai_long_context_billing_enabled":false,"codex_fingerprint_mode":"device"}`, text(f, `SELECT extra::text FROM accounts WHERE id = $1`, blankModeID))
		for id, before := range untouchedAccounts {
			require.Equal(t, before, ctid("accounts", "id", id), "account %d has nothing to change and is not rewritten", id)
		}
		require.Equal(t, outboxBefore, count(f, `SELECT count(*) FROM scheduler_outbox WHERE account_id = ANY($1)`, accountIDs))

		// The retired-plugins view still loads the receipt and both retired rows.
		snapshot := &service.NativeRetirementSnapshot{}
		stored, _ := setting(f, service.NativeFeatureRetirementSetting)
		require.NoError(t, json.Unmarshal([]byte(stored), snapshot))
		require.True(t, snapshot.Completed)
		require.Len(t, snapshot.Plugins, 2)
		require.Len(t, snapshot.Plugins[codex].Bindings, 1)
		rows, err := f.tx.QueryContext(ctx, pluginSelectSQL+` WHERE plugin_key LIKE 'codexrip.%' ORDER BY id`)
		require.NoError(t, err)
		var retired migration264RetiredRows
		for rows.Next() {
			plugin, scanErr := scanPlugin(rows)
			require.NoError(t, scanErr)
			retired = append(retired, plugin)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		bootstrap := &service.NativeFeatureBootstrap{Snapshot: snapshot}
		bootstrap.SetRetiredPluginSource(retired)
		view, err := bootstrap.RetiredPlugins(ctx)
		require.NoError(t, err)
		require.Len(t, view.Installations, 2)
		require.Equal(t, codex, view.Installations[0].PluginKey)
		require.Equal(t, "Codex 运行扩展", view.Installations[0].Name)
		require.Empty(t, view.Installations[0].ConfigError)
		require.Nil(t, view.Installations[0].Config)
		require.Equal(t, "configuration decryptor is unavailable", view.Installations[1].ConfigError, "the sibling still holds its saved configuration")

		// A second run changes nothing.
		before := map[string]string{
			"installation": ctid("sub2api_plugin_installations", "id", codexID),
			"selected":     ctid("accounts", "id", selectedID),
			"members":      ctid("accounts", "id", membersID),
			"no mode":      ctid("accounts", "id", noModeID),
			"config":       ctid("settings", "key", "codex_runtime_config"),
			"receipt":      ctid("settings", "key", service.NativeFeatureRetirementSetting),
		}
		apply(f)
		require.Equal(t, before, map[string]string{
			"installation": ctid("sub2api_plugin_installations", "id", codexID),
			"selected":     ctid("accounts", "id", selectedID),
			"members":      ctid("accounts", "id", membersID),
			"no mode":      ctid("accounts", "id", noModeID),
			"config":       ctid("settings", "key", "codex_runtime_config"),
			"receipt":      ctid("settings", "key", service.NativeFeatureRetirementSetting),
		})
		require.Zero(t, droppedTables(f))
	})

	// The value the previous release applied is kept in every stored shape.
	for _, test := range []struct {
		name          string
		source        bool   // settings.codex_native_runtime_source exists
		installed     string // config_encrypted of a retired installation; "-" for no installation
		hash          string // recorded hash; "" for no record
		existing      string // codex_runtime_config before the migration
		want          string // codex_runtime_config after it; "" for no row
		wantErr       string
		nativeCreated bool
		otherState    bool // sub2api_plugin_state holds a row of another plugin key
	}{
		{name: "saved through the settings page as true", source: true, installed: "", hash: codexRuntimeZstdTrueHash, want: `{"request_zstd":true}`},
		{name: "saved only in the plugin era", installed: "fixture-cipher", hash: codexRuntimeZstdFalseHash, want: `{"request_zstd":false}`},
		{name: "nothing saved keeps following the deployment value", installed: "", hash: codexRuntimeZstdFalseHash},
		{name: "nothing saved and never a plugin", installed: "-", hash: codexRuntimeZstdTrueHash},
		{name: "a plain row already exists", source: true, installed: "", hash: codexRuntimeZstdFalseHash, existing: `{"request_zstd":true}`, want: `{"request_zstd":true}`},
		{name: "runtime-created installation", source: true, installed: "", hash: codexRuntimeZstdFalseHash, want: `{"request_zstd":false}`, nativeCreated: true},
		{name: "saved value with an unknown hash", source: true, installed: "", hash: strings.Repeat("a", 64), wantErr: "is neither known value"},
		{name: "saved value without a recorded hash", installed: "fixture-cipher", wantErr: "is neither known value"},
		{name: "installation that was never retired", installed: "unretired", hash: codexRuntimeZstdTrueHash, wantErr: "was never retired"},
		{name: "state rows of another plugin", installed: "", hash: codexRuntimeZstdTrueHash, otherState: true, wantErr: "rows of other plugins (acme.other-plugin)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := open(t)
			clean(f)
			restoreSchema(f)
			imageID := installation(f, "codexrip.image-tools", "图片工具", "sibling retired plugin", "fixture-image-config-cipher", []byte{4, 5, 6})
			plugins := map[string]string{"codexrip.image-tools": `{"id":42,"key":"codexrip.image-tools","state":"enabled","runtime_generation":3,"bindings":[]}`}
			var codexID int64
			switch {
			case test.installed == "-":
			case test.nativeCreated:
				require.NoError(t, f.tx.QueryRowContext(ctx, `
INSERT INTO sub2api_plugin_installations (plugin_key,name,version,description,manifest,artifact_path,install_path,binary_path,binary_sha256,state,runtime_generation)
VALUES ($1,'Native Codex state anchor','0.0.0','Persistent native runtime generation; no plugin executable','{}'::jsonb,'','','','','disabled',2) RETURNING id`, codex).Scan(&codexID))
				plugins[codex] = `{"id":1,"key":"codexrip.codex-runtime","state":"disabled","runtime_generation":1,"bindings":null,"native_created":true}`
			case test.installed == "unretired":
				codexID = installation(f, codex, "Codex 运行扩展", "fixture", "", nil)
			default:
				codexID = installation(f, codex, "Codex 运行扩展", "fixture", test.installed, []byte{1, 2, 3})
				plugins[codex] = `{"id":41,"key":"codexrip.codex-runtime","state":"enabled","runtime_generation":8,"bindings":[]}`
			}
			receipt(f, plugins)
			if test.source {
				put(f, "codex_native_runtime_source", "VQmyjPJPUS2RwKWoHHjlVWTboI1GXzaC1W5IryauikWXMaktEin/jnbFOMIX5RQucg==")
			}
			if test.hash != "" {
				put(f, "codex_native_runtime_config", record(test.hash))
			}
			if test.existing != "" {
				put(f, "codex_runtime_config", test.existing)
			}
			if test.otherState {
				exec(f, `INSERT INTO sub2api_plugin_state (plugin_key, namespace, state_key, value) VALUES ('acme.other-plugin', 'm264', 'kept', '{"fixture":true}'::jsonb), ($1, 'm264', 'purged', '{"fixture":true}'::jsonb)`, codex)
			}

			if test.wantErr != "" {
				exec(f, `SAVEPOINT before_264`)
				_, err := f.tx.ExecContext(ctx, migration)
				require.ErrorContains(t, err, test.wantErr)
				exec(f, `ROLLBACK TO SAVEPOINT before_264`)
				require.Equal(t, 2, droppedTables(f), "a refused migration changes nothing")
				if test.otherState {
					require.Equal(t, 2, count(f, `SELECT count(*) FROM sub2api_plugin_state`))
				}
				require.Equal(t, 1, count(f, `SELECT count(*) FROM sub2api_plugin_installations WHERE id = $1`, codexID))
				_, found := setting(f, "codex_runtime_config")
				require.False(t, found)
				return
			}
			apply(f)

			value, found := setting(f, "codex_runtime_config")
			require.Equal(t, test.want != "", found)
			require.Equal(t, test.want, value)
			require.Zero(t, count(f, `SELECT count(*) FROM settings WHERE key IN ('codex_native_runtime_source', 'codex_native_runtime_config')`))
			require.Zero(t, droppedTables(f))
			require.Equal(t, 1, count(f, `SELECT count(*) FROM sub2api_plugin_installations WHERE id = $1 AND config_encrypted = 'fixture-image-config-cipher' AND artifact_data IS NOT NULL`, imageID))

			stored, _ := setting(f, service.NativeFeatureRetirementSetting)
			snapshot := &service.NativeRetirementSnapshot{}
			require.NoError(t, json.Unmarshal([]byte(stored), snapshot))
			require.True(t, snapshot.Completed)
			require.Equal(t, 1, snapshot.Version)
			require.False(t, snapshot.RetiredAt.IsZero())
			require.Contains(t, snapshot.Plugins, "codexrip.image-tools")
			switch {
			case test.installed == "-":
				require.NotContains(t, snapshot.Plugins, codex)
			case test.nativeCreated:
				require.NotContains(t, snapshot.Plugins, codex, "the runtime's own row leaves the receipt with it")
				require.NotContains(t, stored, "native_created")
				require.Zero(t, count(f, `SELECT count(*) FROM sub2api_plugin_installations WHERE plugin_key = $1`, codex))
			default:
				require.Contains(t, snapshot.Plugins, codex)
				require.Equal(t, 1, count(f, `SELECT count(*) FROM sub2api_plugin_installations WHERE id = $1 AND config_encrypted = '' AND artifact_data IS NULL AND manifest = '{}'::jsonb`, codexID))
			}

			apply(f)
			again, found := setting(f, "codex_runtime_config")
			require.Equal(t, test.want != "", found)
			require.Equal(t, test.want, again)
		})
	}
}

type migration264RetiredRows []*service.PluginInstallation

func (rows migration264RetiredRows) RetiredPluginInstallations(context.Context) ([]*service.PluginInstallation, error) {
	return rows, nil
}
