//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io/fs"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

const (
	miniMaxPlatformMigration    = "237_add_minimax_platform.sql"
	cindyQuotaMigration         = "237_user_platform_quotas_add_cindy.sql"
	miniMaxCindyQuotaMigration  = "241_minimax_cindy_platform_quota_compat.sql"
	cindyQuotaPublishedChecksum = "0e61f662164c98f0710794bf27e4f116b2ab68edb8ca19425d2e080b865d8cf3"
)

func TestMiniMaxCindyQuotaMigrationsUpgradePreservesRowsAndLedger(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := miniMaxCindyQuotaMigrationDatabase(t, ctx)

	// Reconstruct only the affected historical migration chain. All quota
	// DDL and ledger writes go through the real embedded SQL and runner.
	_, err := db.ExecContext(ctx, miniMaxCindyQuotaMigrationDependencyDDL)
	require.NoError(t, err)
	fsys := fstest.MapFS{}
	for _, name := range []string{
		"142_user_platform_quotas.sql",
		"157_user_platform_quotas_add_grok.sql",
		"224_user_platform_quotas_add_cn_providers.sql",
		cindyQuotaMigration,
	} {
		addMiniMaxCindyQuotaMigration(t, fsys, name)
	}
	require.NoError(t, applyMigrationsFS(ctx, db, fsys))
	_, err = db.ExecContext(ctx, `
INSERT INTO user_platform_quotas (
    user_id, platform, daily_limit_usd, weekly_limit_usd, monthly_limit_usd,
    daily_usage_usd, weekly_usage_usd, monthly_usage_usd,
    daily_window_start, weekly_window_start, monthly_window_start,
    created_at, updated_at, deleted_at
) VALUES
    (1, 'cindy', 13.125, 0, NULL, 1.125, 2.25, 3.5,
     '2026-09-09T00:00:00Z', '2026-09-07T00:00:00Z', '2026-09-01T00:00:00Z',
     '2026-09-01T01:00:00Z', '2026-09-09T02:00:00Z', NULL),
    (1, 'cindy', 20, 30, 40, 4, 5, 6,
     '2026-08-31T00:00:00Z', '2026-08-24T00:00:00Z', '2026-08-01T00:00:00Z',
     '2026-08-01T01:00:00Z', '2026-09-01T02:00:00Z', '2026-09-01T02:00:00Z'),
    (1, 'openai', NULL, 21.75, 50, 0.25, 1.5, 2.75,
     NULL, '2026-09-07T00:00:00Z', '2026-09-01T00:00:00Z',
     '2026-09-01T01:00:00Z', '2026-09-09T02:00:00Z', NULL)
`)
	require.NoError(t, err)
	legacyRows := miniMaxCindyQuotaRows(t, ctx, db, 1)
	legacyLedger := miniMaxCindyQuotaLedger(t, ctx, db)
	require.Equal(t, cindyQuotaPublishedChecksum, legacyLedger[cindyQuotaMigration].Checksum)

	// 241 cannot rescue a failing 237. The first newly introduced migration
	// must itself accept existing Cindy rows, while the published 237 skips.
	addMiniMaxCindyQuotaMigration(t, fsys, miniMaxPlatformMigration)
	require.NoError(t, applyMigrationsFS(ctx, db, fsys))
	require.Equal(t, legacyRows, miniMaxCindyQuotaRows(t, ctx, db, 1))

	addMiniMaxCindyQuotaMigration(t, fsys, miniMaxCindyQuotaMigration)
	require.NoError(t, applyMigrationsFS(ctx, db, fsys))
	require.Equal(t, legacyRows, miniMaxCindyQuotaRows(t, ctx, db, 1))
	upgradedLedger := miniMaxCindyQuotaLedger(t, ctx, db)
	require.Len(t, upgradedLedger, len(legacyLedger)+2)
	for filename, before := range legacyLedger {
		require.Equal(t, before, upgradedLedger[filename], "historical ledger row %s changed", filename)
	}
	for _, name := range []string{miniMaxPlatformMigration, miniMaxCindyQuotaMigration} {
		require.Equal(t, miniMaxCindyQuotaChecksum(fsys[name].Data), upgradedLedger[name].Checksum)
	}

	assertMiniMaxCindyQuotaWrites(t, ctx, db, 2)
	newRows := miniMaxCindyQuotaRows(t, ctx, db, 2)

	// With a MiniMax row now present, replaying the historical Cindy 237
	// would fail. A normal restart must validate and skip every ledger row.
	require.NoError(t, applyMigrationsFS(ctx, db, fsys))
	require.Equal(t, upgradedLedger, miniMaxCindyQuotaLedger(t, ctx, db))
	require.Equal(t, legacyRows, miniMaxCindyQuotaRows(t, ctx, db, 1))
	require.Equal(t, newRows, miniMaxCindyQuotaRows(t, ctx, db, 2))

	// The new compatibility migration is also independently idempotent.
	_, err = db.ExecContext(ctx, string(fsys[miniMaxCindyQuotaMigration].Data))
	require.NoError(t, err)
	require.Equal(t, legacyRows, miniMaxCindyQuotaRows(t, ctx, db, 1))
	require.Equal(t, newRows, miniMaxCindyQuotaRows(t, ctx, db, 2))
	require.Equal(t, upgradedLedger, miniMaxCindyQuotaLedger(t, ctx, db))

	// Do not extend Cindy quota compatibility to the upstream-only route or
	// monitor platform lists. These remain the official nine-platform set.
	for _, fixture := range []struct {
		table  string
		column string
	}{
		{table: "composite_model_routes", column: "target_platform"},
		{table: "channel_monitors", column: "provider"},
		{table: "channel_monitor_request_templates", column: "provider"},
	} {
		_, err = db.ExecContext(ctx, "INSERT INTO "+fixture.table+" ("+fixture.column+") VALUES ('minimax')")
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, "INSERT INTO "+fixture.table+" ("+fixture.column+") VALUES ('cindy')")
		var constraintErr *pgconn.PgError
		require.ErrorAs(t, err, &constraintErr)
		require.Equal(t, "23514", constraintErr.Code)
	}
}

func TestMiniMaxCindyQuotaMigrationsFreshFilenameOrderAndRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// TestMain installs the entire embedded migration FS into an empty
	// PostgreSQL database once. Reuse that actual full-install result instead
	// of repeating all unrelated migrations in another fixture.
	ledger := miniMaxCindyQuotaLedger(t, ctx, integrationDB)
	files, err := fs.Glob(dbmigrations.FS, "*.sql")
	require.NoError(t, err)
	sort.Strings(files)
	applied := 0
	var previous time.Time
	for _, name := range files {
		content, err := dbmigrations.FS.ReadFile(name)
		require.NoError(t, err)
		if strings.TrimSpace(string(content)) == "" {
			continue
		}
		entry, found := ledger[name]
		require.True(t, found, "missing migration %s", name)
		require.Equal(t, miniMaxCindyQuotaChecksum(content), entry.Checksum, name)
		require.False(t, entry.AppliedAt.Before(previous), "migrations must follow full filename order: %s", name)
		previous = entry.AppliedAt
		applied++
	}
	require.Len(t, ledger, applied)
	require.Equal(t, cindyQuotaPublishedChecksum, ledger[cindyQuotaMigration].Checksum)
	for _, name := range []string{miniMaxPlatformMigration, cindyQuotaMigration, miniMaxCindyQuotaMigration} {
		require.Contains(t, ledger, name)
	}

	var userID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
INSERT INTO users (email, password_hash) VALUES ($1, 'migration-fixture') RETURNING id
`, "minimax-cindy-"+uuid.NewString()+"@example.com").Scan(&userID))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), "DELETE FROM users WHERE id = $1", userID)
		require.NoError(t, err)
	})
	assertMiniMaxCindyQuotaWrites(t, ctx, integrationDB, userID)
	rows := miniMaxCindyQuotaRows(t, ctx, integrationDB, userID)

	require.NoError(t, ApplyMigrations(ctx, integrationDB))
	require.Equal(t, ledger, miniMaxCindyQuotaLedger(t, ctx, integrationDB))
	require.Equal(t, rows, miniMaxCindyQuotaRows(t, ctx, integrationDB, userID))
}

func miniMaxCindyQuotaMigrationDatabase(t *testing.T, ctx context.Context) *sql.DB {
	t.Helper()
	// Reuse the harness container, but use a separate database: 237 inspects
	// pg_constraint by relation name, and the runner checks the public schema.
	conn, err := integrationDB.Conn(ctx)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	var config *pgx.ConnConfig
	require.NoError(t, conn.Raw(func(driverConn any) error {
		config = driverConn.(*stdlib.Conn).Conn().Config()
		return nil
	}))
	databaseName := "minimax_cindy_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedName := pgx.Identifier{databaseName}.Sanitize()
	_, err = conn.ExecContext(ctx, "CREATE DATABASE "+quotedName)
	require.NoError(t, err)
	config.Database = databaseName
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() {
		_ = db.Close()
		_, err := integrationDB.ExecContext(context.Background(), "DROP DATABASE "+quotedName)
		require.NoError(t, err)
	})
	require.NoError(t, db.PingContext(ctx))
	return db
}

func addMiniMaxCindyQuotaMigration(t *testing.T, fsys fstest.MapFS, name string) {
	t.Helper()
	content, err := dbmigrations.FS.ReadFile(name)
	require.NoError(t, err)
	fsys[name] = &fstest.MapFile{Data: content}
}

type miniMaxCindyQuotaLedgerEntry struct {
	Checksum  string
	AppliedAt time.Time
}

func miniMaxCindyQuotaLedger(t *testing.T, ctx context.Context, db *sql.DB) map[string]miniMaxCindyQuotaLedgerEntry {
	t.Helper()
	rows, err := db.QueryContext(ctx, "SELECT filename, checksum, applied_at FROM schema_migrations")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	ledger := map[string]miniMaxCindyQuotaLedgerEntry{}
	for rows.Next() {
		var filename string
		var entry miniMaxCindyQuotaLedgerEntry
		require.NoError(t, rows.Scan(&filename, &entry.Checksum, &entry.AppliedAt))
		ledger[filename] = entry
	}
	require.NoError(t, rows.Err())
	return ledger
}

func miniMaxCindyQuotaChecksum(content []byte) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(string(content))))
	return hex.EncodeToString(sum[:])
}

func miniMaxCindyQuotaRows(t *testing.T, ctx context.Context, db *sql.DB, userID int64) string {
	t.Helper()
	var snapshot string
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COALESCE(jsonb_agg(to_jsonb(q) ORDER BY id), '[]'::jsonb)::text
FROM user_platform_quotas q WHERE user_id = $1
`, userID).Scan(&snapshot))
	return snapshot
}

func assertMiniMaxCindyQuotaWrites(t *testing.T, ctx context.Context, db *sql.DB, userID int64) {
	t.Helper()
	platforms := []string{
		"anthropic", "openai", "gemini", "antigravity", "grok",
		"kimi", "zhipu", "deepseek", "minimax", "cindy",
	}
	result, err := db.ExecContext(ctx, `
INSERT INTO user_platform_quotas (user_id, platform, daily_limit_usd, daily_usage_usd)
SELECT $1, platform, 12.5, 0.25 FROM unnest($2::text[]) AS platform
`, userID, platforms)
	require.NoError(t, err)
	count, err := result.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(10), count)
	result, err = db.ExecContext(ctx, `
UPDATE user_platform_quotas SET weekly_limit_usd = 25, weekly_usage_usd = 1.5
WHERE user_id = $1 AND platform IN ('minimax', 'cindy')
`, userID)
	require.NoError(t, err)
	count, err = result.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(2), count)
	_, err = db.ExecContext(ctx, "INSERT INTO user_platform_quotas (user_id, platform) VALUES ($1, 'unknown')", userID)
	var constraintErr *pgconn.PgError
	require.ErrorAs(t, err, &constraintErr)
	require.Equal(t, "23514", constraintErr.Code)
	require.Equal(t, "user_platform_quotas_platform_check", constraintErr.ConstraintName)
}

const miniMaxCindyQuotaMigrationDependencyDDL = `
CREATE TABLE users (id BIGINT PRIMARY KEY);
INSERT INTO users (id) VALUES (1), (2);
CREATE TABLE composite_model_routes (
    target_platform TEXT NOT NULL CONSTRAINT composite_model_routes_target_platform_check
        CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek'))
);
CREATE TABLE channel_monitors (
    provider TEXT NOT NULL CONSTRAINT channel_monitors_provider_check
        CHECK (provider IN ('openai', 'anthropic', 'gemini', 'grok', 'antigravity', 'kimi', 'zhipu', 'deepseek'))
);
CREATE TABLE channel_monitor_request_templates (
    provider TEXT NOT NULL CONSTRAINT channel_monitor_request_templates_provider_check
        CHECK (provider IN ('openai', 'anthropic', 'gemini', 'grok', 'antigravity', 'kimi', 'zhipu', 'deepseek'))
);
`
