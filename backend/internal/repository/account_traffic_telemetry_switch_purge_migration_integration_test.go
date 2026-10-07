//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

const accountTrafficTelemetrySwitchPurgeMigration = "273_purge_account_traffic_telemetry_switch.sql"

// TestMain applied every migration, 273 included, to an empty database, which
// stores no observability row. Each case stores the row as a database of an
// earlier release can hold it next to settings that must stay, applies 273,
// checks which key went and which rows were written, reads what is left as the
// application does at start, and applies 273 again.
func TestMigration273PurgesAccountTrafficTelemetrySwitch(t *testing.T) {
	ctx := context.Background()
	content, err := dbmigrations.FS.ReadFile(accountTrafficTelemetrySwitchPurgeMigration)
	require.NoError(t, err)
	migration := string(content)

	const observability = "admin_observability_config"
	// The rows 273 must not write: the other switch setting, an object under
	// another key that holds the same switch name, and text that is not JSON.
	kept := map[string]string{
		"codex_runtime_config":            `{"request_zstd":true}`,
		"migration_273_other_object":      `{"telemetry_enabled":true,"theme_enabled":true}`,
		"migration_273_text_that_is_kept": `telemetry_enabled`,
	}

	for _, tc := range []struct {
		name string
		// stored is the observability row before 273; absent stores none.
		stored string
		absent bool
		// left is the JSON 273 leaves in a row it writes. A row it does not
		// write has no left and keeps its bytes.
		left string
		// readable rows are JSON objects; themeOn is what their reader yields.
		readable, themeOn bool
	}{
		{name: "both switches on", stored: `{"telemetry_enabled":true,"theme_enabled":true}`, left: `{"theme_enabled":true}`, readable: true, themeOn: true},
		{name: "both switches off", stored: `{"telemetry_enabled":false,"theme_enabled":false}`, left: `{"theme_enabled":false}`, readable: true},
		{name: "telemetry off and theme on", stored: `{"telemetry_enabled":false,"theme_enabled":true}`, left: `{"theme_enabled":true}`, readable: true, themeOn: true},
		{name: "only the telemetry switch", stored: `{"telemetry_enabled":true}`, left: `{}`, readable: true, themeOn: true},
		{name: "only the theme switch", stored: `{"theme_enabled":false}`, readable: true},
		{name: "no switch", stored: `{}`, readable: true, themeOn: true},
		{name: "an array that names the switch", stored: `["telemetry_enabled"]`},
		{name: "text that is not JSON", stored: `telemetry_enabled`},
		{name: "no row", absent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := testTx(t)
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
			put := func(key, value string) {
				t.Helper()
				exec(`INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, value)
			}
			// The row version and its bytes: equal as long as the row is not written.
			row := func(key string) string {
				t.Helper()
				return text(`SELECT ctid::text || '/' || value FROM settings WHERE key = $1`, key)
			}

			require.Zero(t, count(`SELECT count(*) FROM settings WHERE key = $1`, observability))
			if !tc.absent {
				put(observability, tc.stored)
			}
			untouched := map[string]string{}
			for key, value := range kept {
				put(key, value)
				untouched[key] = row(key)
			}
			rows := count(`SELECT count(*) FROM settings`)
			before := ""
			if !tc.absent {
				before = row(observability)
			}

			after := ""
			for run := 1; run <= 2; run++ {
				exec(migration)

				require.Equal(t, rows, count(`SELECT count(*) FROM settings`), "run %d adds and deletes no setting", run)
				for key := range kept {
					require.Equal(t, untouched[key], row(key), "run %d writes no other setting: %s", run, key)
				}
				if tc.absent {
					continue
				}
				value := text(`SELECT value FROM settings WHERE key = $1`, observability)
				if run == 1 {
					after = row(observability)
					require.Equal(t, tc.left != "", after != before, "only a row that held the switch is written")
				}
				require.Equal(t, after, row(observability), "run %d: the second run writes no row", run)
				if tc.left != "" {
					require.JSONEq(t, tc.left, value)
				} else {
					require.Equal(t, tc.stored, value)
				}
				if tc.readable {
					config := service.AdminObservabilityConfig{ThemeEnabled: true}
					require.NoError(t, service.DecodeSwitchSettings([]byte(value), &config, "theme_enabled"), "the strict reader accepts the row")
					require.Equal(t, tc.themeOn, config.ThemeEnabled)
				}
			}
		})
	}
}
