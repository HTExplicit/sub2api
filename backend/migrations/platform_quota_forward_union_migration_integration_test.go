//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

// Only the quota CHECK's three relevant embedded migrations execute here.
// The other empty tables are the minimum dependencies of the historical 238.
func TestMigration251PreservesPlatformUnionAndQuotaRows(t *testing.T) {
	for _, history := range []string{"fresh_238_then_241", "already_all_eleven_platforms"} {
		t.Run(history, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			db, schema := remoteSkillMigrationTestDatabase(t)
			t.Logf("isolated fixture schema=%s history=%s", schema, history)
			require.NoError(t, execRemoteSkillSQL(ctx, db, platformQuotaForwardUnionFixtureSQL))
			apply := func(name string) {
				t.Helper()
				migration, err := dbmigrations.FS.ReadFile(name)
				require.NoError(t, err)
				require.NoError(t, execRemoteSkillSQL(ctx, db, string(migration)))
			}
			apply("238_opencode_go_platform.sql")
			if history == "fresh_238_then_241" {
				apply("241_minimax_cindy_platform_quota_compat.sql")
				_, err := db.ExecContext(ctx, `INSERT INTO user_platform_quotas (id, user_id, platform) VALUES (90, 1, 'opencode_go')`)
				requireQuotaForwardUnionCheckFailure(t, err)
				t.Log("historical 238 -> 241 final CHECK rejects opencode_go with SQLSTATE 23514")
			} else {
				_, err := db.ExecContext(ctx, `INSERT INTO user_platform_quotas (id, user_id, platform, daily_limit_usd, daily_usage_usd)
					VALUES (30, 1, 'opencode_go', 9.125, 1.75)`)
				require.NoError(t, err)
			}
			_, err := db.ExecContext(ctx, `
				INSERT INTO user_platform_quotas (
					id, user_id, platform, daily_limit_usd, weekly_limit_usd, monthly_limit_usd,
					daily_usage_usd, weekly_usage_usd, monthly_usage_usd,
					daily_window_start, weekly_window_start, monthly_window_start,
					created_at, updated_at, deleted_at
				) VALUES
					(10, 1, 'cindy', 13.125, 0, NULL, 1.125, 2.25, 3.5,
					 '2026-09-20T00:00:00Z', '2026-09-14T00:00:00Z', '2026-09-01T00:00:00Z',
					 '2026-09-01T01:00:00Z', '2026-09-20T02:00:00Z', NULL),
					(20, 1, 'openai', NULL, 21.75, 50, 0.25, 1.5, 2.75,
					 NULL, '2026-09-14T00:00:00Z', '2026-09-01T00:00:00Z',
					 '2026-09-01T01:00:00Z', '2026-09-20T02:00:00Z', '2026-09-20T03:00:00Z')`)
			require.NoError(t, err)
			before := platformQuotaForwardUnionRows(t, ctx, db)
			apply("251_user_platform_quota_platform_union.sql")
			require.Equal(t, before, platformQuotaForwardUnionRows(t, ctx, db), "all existing values, timestamps, IDs and xmin must remain unchanged")
			platforms := []string{"anthropic", "openai", "gemini", "antigravity", "grok", "kimi", "zhipu", "deepseek", "minimax", "cindy", "opencode_go"}
			for index, platform := range platforms {
				_, err := db.ExecContext(ctx, `INSERT INTO user_platform_quotas (id, user_id, platform, daily_limit_usd)
					VALUES ($1, 2, $2, 12.5)`, 100+index, platform)
				require.NoError(t, err, "supported platform %s must remain insertable", platform)
			}
			_, err = db.ExecContext(ctx, `INSERT INTO user_platform_quotas (id, user_id, platform) VALUES (999, 2, 'unknown')`)
			requireQuotaForwardUnionCheckFailure(t, err)
			after := platformQuotaForwardUnionRows(t, ctx, db)
			apply("251_user_platform_quota_platform_union.sql")
			require.Equal(t, after, platformQuotaForwardUnionRows(t, ctx, db), "reapplying only the new CHECK must not touch any row")
			_, err = db.ExecContext(ctx, `INSERT INTO user_platform_quotas (id, user_id, platform) VALUES (999, 2, 'unknown')`)
			requireQuotaForwardUnionCheckFailure(t, err)
			t.Log("251 accepts all eleven platforms, rejects unknown, preserves row/xmin snapshots and is idempotent")
		})
	}
}

func requireQuotaForwardUnionCheckFailure(t *testing.T, err error) {
	t.Helper()
	var constraintErr *pgconn.PgError
	require.ErrorAs(t, err, &constraintErr)
	require.Equal(t, "23514", constraintErr.Code)
	require.Equal(t, "user_platform_quotas_platform_check", constraintErr.ConstraintName)
}

func platformQuotaForwardUnionRows(t *testing.T, ctx context.Context, db *sql.DB) string {
	t.Helper()
	var snapshot string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(
		jsonb_build_object('row', to_jsonb(q), 'xmin', q.xmin::text) ORDER BY q.id
	), '[]'::jsonb)::text FROM user_platform_quotas q`).Scan(&snapshot))
	return snapshot
}

const platformQuotaForwardUnionFixtureSQL = `
CREATE TABLE user_platform_quotas (
    id BIGINT PRIMARY KEY,
    user_id BIGINT NOT NULL,
    platform TEXT NOT NULL,
    daily_limit_usd NUMERIC(20, 8), weekly_limit_usd NUMERIC(20, 8), monthly_limit_usd NUMERIC(20, 8),
    daily_usage_usd NUMERIC(20, 8) NOT NULL DEFAULT 0,
    weekly_usage_usd NUMERIC(20, 8) NOT NULL DEFAULT 0,
    monthly_usage_usd NUMERIC(20, 8) NOT NULL DEFAULT 0,
    daily_window_start TIMESTAMPTZ, weekly_window_start TIMESTAMPTZ, monthly_window_start TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT '2026-09-01T00:00:00Z',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT '2026-09-20T00:00:00Z',
    deleted_at TIMESTAMPTZ
);
CREATE TABLE composite_model_routes (target_platform TEXT NOT NULL);
CREATE TABLE channel_monitors (provider TEXT NOT NULL);
CREATE TABLE channel_monitor_request_templates (provider TEXT NOT NULL);
`
