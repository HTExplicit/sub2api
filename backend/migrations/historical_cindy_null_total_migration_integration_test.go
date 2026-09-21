//go:build integration

package migrations_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

const nullTotalMigration252 = "252_cindy_identity_null_total.sql"
const nullTotalMigration229 = "229_cindy_platform_wire_identity.sql"
const nullTotalMigration234 = "234_fix_cindy_platform_projection_round_trip.sql"
const nullTotalMigration235 = "235_preserve_mixed_openai_cindy_groups.sql"
const nullTotalMigration236 = "236_bind_strict_cindy_groups_to_catalog_channel.sql"
const nullTotalMigration241 = "241_minimax_cindy_platform_quota_compat.sql"
const nullTotalPurge238 = "238_purge_unlimited_user_platform_quotas.sql"

func TestMigration252NullTotalIsSchemaOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, _ := remoteSkillMigrationTestDatabase(t)
	require.NoError(t, execRemoteSkillSQL(ctx, db, historicalNullTotalFixtureSQL))
	require.NoError(t, execRemoteSkillSQL(ctx, db, `
INSERT INTO accounts (id, platform, wire_platform, provider_profile, credentials, status) VALUES
 (1, 'cindy', 'openai', 'cindy_laxa_v1', '{"base_url":" HTTPS://API.LAXAROUTER.AI/ "}', 'disabled'),
 (2, 'openai', 'openai', '', '{}', 'active');
INSERT INTO groups (id, platform, wire_platform, provider_profile, fallback_group_id) VALUES
 (10, 'cindy', 'openai', 'cindy_laxa_v1', NULL), (20, 'openai', 'openai', '', 10);
INSERT INTO account_groups VALUES (1, 10), (2, 20);
INSERT INTO cindy_platform_v1_projection VALUES ('account', 1, 'openai', 'openai', '');`))
	before := historicalNullTotalRows(t, ctx, db)
	migration, err := dbmigrations.FS.ReadFile(nullTotalMigration252)
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migration))
	if err != nil {
		_ = tx.Rollback()
	}
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	require.Equal(t, before, historicalNullTotalRows(t, ctx, db), "DDL must preserve every business value and xmin")
	var validated, strict, ordinary bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT convalidated FROM pg_constraint
WHERE conrelid = 'accounts'::regclass AND conname = 'accounts_cindy_platform_identity_check'`).Scan(&validated))
	require.True(t, validated)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT project_is_strict_cindy_group(10), project_is_strict_cindy_group(20)`).Scan(&strict, &ordinary))
	require.True(t, strict)
	require.False(t, ordinary)

	// The old projection entrypoints are deliberate raising sentinels in this
	// fixture. Successful migration proves that neither was invoked automatically.
	var accounts, groups int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT * FROM project_cindy_platform_v1_discover_legacy()`).Scan(&accounts, &groups))
	require.Zero(t, accounts, "ordinary missing URL is not a Cindy fallback candidate")
	require.Zero(t, groups)
	require.Equal(t, before, historicalNullTotalRows(t, ctx, db))
	for _, credentials := range []any{`{}`, `{"base_url":null}`, nil} {
		_, err := db.ExecContext(ctx, `INSERT INTO accounts (id, platform, wire_platform, provider_profile, credentials)
VALUES (99, 'cindy', 'openai', 'cindy_laxa_v1', $1::jsonb)`, credentials)
		var checkErr *pgconn.PgError
		require.ErrorAs(t, err, &checkErr)
		require.Equal(t, "23514", checkErr.Code)
		require.Equal(t, "accounts_cindy_platform_identity_check", checkErr.ConstraintName)
	}

	// Exercise the new strict predicate independently of the new CHECK; all
	// fixture-only changes, including the constraint drop, are rolled back.
	tx, err = db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `ALTER TABLE accounts DROP CONSTRAINT accounts_cindy_platform_identity_check;
INSERT INTO accounts (id, platform, wire_platform, provider_profile, credentials) VALUES (99, 'cindy', 'openai', 'cindy_laxa_v1', '{}');
INSERT INTO account_groups VALUES (99, 10);`)
	require.NoError(t, err)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT project_is_strict_cindy_group(10)`).Scan(&strict))
	require.False(t, strict, "one valid member must not hide a member with missing URL")
	require.NoError(t, tx.Rollback())

	tx, err = db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET credentials = '{"base_url":"https://api.laxarouter.ai"}' WHERE id = 2`)
	require.NoError(t, err)
	err = tx.QueryRowContext(ctx, `SELECT * FROM project_cindy_platform_v1_discover_legacy()`).Scan(&accounts, &groups)
	var guardErr *pgconn.PgError
	require.ErrorAs(t, err, &guardErr)
	require.Equal(t, "P0001", guardErr.Code)
	require.Equal(t, "Cindy projection candidate has fallback_group_id", guardErr.Message)
	require.NoError(t, tx.Rollback())
	require.Equal(t, before, historicalNullTotalRows(t, ctx, db))
	t.Log("252 validates a NULL-total CHECK and strict membership, preserves real fallback guard and every row/xmin, and never invokes projection entrypoints")
}

func TestMigration252DirtyInputRollsBackItsSchema(t *testing.T) {
	for _, test := range []struct{ name, seed, message string }{
		{"account", `INSERT INTO accounts (id, platform, wire_platform, provider_profile, credentials) VALUES (1, 'cindy', 'openai', 'cindy_laxa_v1', '{}');`, "CINDY_IDENTITY_NULL_TOTAL_VIOLATIONS: accounts=1"},
		{"group_topology", `INSERT INTO accounts (id) VALUES (1);
INSERT INTO groups (id, platform, wire_platform, provider_profile) VALUES (10, 'cindy', 'openai', 'cindy_laxa_v1');
INSERT INTO account_groups VALUES (1, 10);`, "CINDY_GROUP_NULL_TOTAL_VIOLATIONS: groups=1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			db, _ := remoteSkillMigrationTestDatabase(t)
			require.NoError(t, execRemoteSkillSQL(ctx, db, historicalNullTotalFixtureSQL+test.seed))
			rowsBefore, schemaBefore := historicalNullTotalRows(t, ctx, db), historicalNullTotalDefinitions(t, ctx, db)
			migration, err := dbmigrations.FS.ReadFile(nullTotalMigration252)
			require.NoError(t, err)
			tx, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			_, err = tx.ExecContext(ctx, string(migration))
			var rejected *pgconn.PgError
			require.ErrorAs(t, err, &rejected)
			require.Equal(t, "P0001", rejected.Code)
			require.Equal(t, test.message, rejected.Message)
			require.NoError(t, tx.Rollback())
			require.Equal(t, rowsBefore, historicalNullTotalRows(t, ctx, db))
			require.Equal(t, schemaBefore, historicalNullTotalDefinitions(t, ctx, db))
		})
	}
}

func TestHistoricalMigrationPreflightRejectsBeforeMetadataOrPrefixWrites(t *testing.T) {
	oldProjection := []string{nullTotalMigration229, nullTotalMigration234, nullTotalMigration235, nullTotalMigration236}
	for _, test := range []struct {
		name                  string
		applied               []string
		seed, code, migration string
		count                 int64
		ids                   []int64
	}{
		{"229_null_fallback", nil, historicalNullFallbackSeedSQL, "HISTORICAL_CINDY_PROJECTION_PRECONDITION", nullTotalMigration229, 1, []int64{20}},
		{"234_null_fallback", []string{nullTotalMigration229}, historicalNullFallbackSeedSQL, "HISTORICAL_CINDY_PROJECTION_PRECONDITION", nullTotalMigration234, 1, []int64{20}},
		{"235_guard_after_rollback", []string{nullTotalMigration229, nullTotalMigration234}, `
INSERT INTO accounts (id, platform, wire_platform, provider_profile, credentials) VALUES (2, 'cindy', 'openai', 'cindy_laxa_v1', '{}');
INSERT INTO groups (id, platform, wire_platform, provider_profile, fallback_group_id) VALUES (20, 'cindy', 'openai', 'cindy_laxa_v1', 10);
INSERT INTO account_groups VALUES (2, 20);
INSERT INTO cindy_platform_v1_projection VALUES ('account', 2, 'openai', 'openai', ''), ('group', 20, 'openai', 'openai', '');`,
			"HISTORICAL_CINDY_PROJECTION_PRECONDITION", nullTotalMigration235, 1, []int64{20}},
		{"241_retained_go", oldProjection, `INSERT INTO user_platform_quotas (id, platform, daily_limit_usd) VALUES (70, 'opencode_go', 9.125);`, "HISTORICAL_QUOTA_241_UNSUPPORTED_PLATFORM", nullTotalMigration241, 1, []int64{70}},
		{"241_purge_already_applied", append(append([]string{}, oldProjection...), nullTotalPurge238), `INSERT INTO user_platform_quotas (id, platform) VALUES (71, 'opencode_go');`, "HISTORICAL_QUOTA_241_UNSUPPORTED_PLATFORM", nullTotalMigration241, 1, []int64{71}},
		{"252_dirty_canonical_count_and_bounded_ids", append(append([]string{}, oldProjection...), nullTotalMigration241), `
INSERT INTO accounts (id, platform, wire_platform, provider_profile, credentials)
SELECT id, 'cindy', 'openai', 'cindy_laxa_v1', '{}'::jsonb FROM generate_series(30, 35) AS id;`,
			"CINDY_IDENTITY_NULL_TOTAL_VIOLATIONS", nullTotalMigration252, 6, []int64{30, 31, 32, 33, 34}},
		{"252_dirty_group", append(append([]string{}, oldProjection...), nullTotalMigration241), `
INSERT INTO accounts (id) VALUES (2);
INSERT INTO groups (id, platform, wire_platform, provider_profile) VALUES (20, 'cindy', 'openai', 'cindy_laxa_v1');
INSERT INTO account_groups VALUES (2, 20);`, "CINDY_GROUP_NULL_TOTAL_VIOLATIONS", nullTotalMigration252, 1, []int64{20}},
		{"checksum_first", []string{nullTotalMigration229}, `UPDATE schema_migrations SET checksum = 'private-ledger-value-must-not-leak';`, "HISTORICAL_MIGRATION_CHECKSUM_MISMATCH", nullTotalMigration229, 1, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			db, _ := remoteSkillMigrationTestDatabase(t)
			require.NoError(t, execRemoteSkillSQL(ctx, db, historicalNullTotalFixtureSQL))
			historicalNullTotalRecordFixtureHistory(t, ctx, db, test.applied)
			require.NoError(t, execRemoteSkillSQL(ctx, db, test.seed))
			before, definitions := historicalNullTotalRows(t, ctx, db), historicalNullTotalDefinitions(t, ctx, db)
			// Only intentionally rejected runner paths execute here. Never run the
			// repository integration bootstrap or a successful full-chain migration.
			err := repository.ApplyMigrations(ctx, db)
			var blocked *repository.HistoricalMigrationPreflightError
			require.ErrorAs(t, err, &blocked)
			require.Equal(t, test.code, blocked.Code)
			require.Equal(t, test.migration, blocked.Migration)
			require.Equal(t, test.count, blocked.Count)
			require.Equal(t, test.ids, blocked.SampleIDs)
			require.NotContains(t, err.Error(), "private-ledger-value-must-not-leak")
			require.Equal(t, before, historicalNullTotalRows(t, ctx, db))
			require.Equal(t, definitions, historicalNullTotalDefinitions(t, ctx, db), "no ledger/Atlas/prefix DDL or function writes")
			var atlas, prefix bool
			require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('atlas_schema_revisions') IS NOT NULL, to_regclass('users') IS NOT NULL`).Scan(&atlas, &prefix))
			require.False(t, atlas)
			require.False(t, prefix)
			t.Logf("blocked %s count=%d before any runner metadata/prefix write", blocked.Code, blocked.Count)
		})
	}
}

func TestHistoricalMigrationPreflightAllowsConcreteSafePendingStatesWithoutWrites(t *testing.T) {
	for _, name := range []string{"fresh", "populated_legacy_and_pending_purge", "recorded_241_keeps_finite_go"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			db, _ := remoteSkillMigrationTestDatabase(t)
			if name != "fresh" {
				require.NoError(t, execRemoteSkillSQL(ctx, db, historicalNullTotalFixtureSQL))
				require.NoError(t, execRemoteSkillSQL(ctx, db, `
INSERT INTO accounts (id, credentials) VALUES (1, '{"base_url":"https://api.laxarouter.ai"}'), (2, '{}');
INSERT INTO groups (id) VALUES (10), (20);
INSERT INTO account_groups VALUES (1, 10), (2, 20);
INSERT INTO user_platform_quotas (id, platform) VALUES (70, 'opencode_go');`))
				if name == "recorded_241_keeps_finite_go" {
					historicalNullTotalRecordFixtureHistory(t, ctx, db, []string{nullTotalMigration229, nullTotalMigration234, nullTotalMigration235, nullTotalMigration236, nullTotalMigration241})
					require.NoError(t, execRemoteSkillSQL(ctx, db, `UPDATE user_platform_quotas SET daily_limit_usd = 9.125 WHERE id = 70`))
				}
			}
			before, definitions := historicalNullTotalRows(t, ctx, db), historicalNullTotalDefinitions(t, ctx, db)
			require.NoError(t, repository.CheckHistoricalMigrationPreconditions(ctx, db))
			require.Equal(t, before, historicalNullTotalRows(t, ctx, db))
			require.Equal(t, definitions, historicalNullTotalDefinitions(t, ctx, db))
		})
	}
}

func TestHistoricalMigrationPreflightProjectionPreviewBoundary(t *testing.T) {
	for _, name := range []string{"234_recorded_identity_restore", "229_adds_missing_identity_columns", "missing_required_credentials_column"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			db, _ := remoteSkillMigrationTestDatabase(t)
			require.NoError(t, execRemoteSkillSQL(ctx, db, historicalNullTotalFixtureSQL))
			switch name {
			case "234_recorded_identity_restore":
				historicalNullTotalRecordFixtureHistory(t, ctx, db, []string{nullTotalMigration229, nullTotalMigration235, nullTotalMigration236, nullTotalMigration241})
				require.NoError(t, execRemoteSkillSQL(ctx, db, `
INSERT INTO accounts (id, credentials) VALUES (2, '{"base_url":"https://api.laxarouter.ai"}');
INSERT INTO groups (id, fallback_group_id) VALUES (20, 10);
INSERT INTO account_groups VALUES (2, 20);
INSERT INTO cindy_platform_v1_projection VALUES ('account', 2, 'openai', 'openai', '');`))
			case "229_adds_missing_identity_columns":
				require.NoError(t, execRemoteSkillSQL(ctx, db, `
ALTER TABLE accounts DROP CONSTRAINT accounts_cindy_platform_identity_check;
ALTER TABLE accounts DROP COLUMN wire_platform, DROP COLUMN provider_profile;
ALTER TABLE groups DROP COLUMN wire_platform, DROP COLUMN provider_profile;
INSERT INTO accounts (id, credentials) VALUES (2, '{}');
INSERT INTO groups (id) VALUES (20);
INSERT INTO account_groups VALUES (2, 20);`))
			case "missing_required_credentials_column":
				require.NoError(t, execRemoteSkillSQL(ctx, db, `ALTER TABLE accounts DROP COLUMN credentials CASCADE;`))
			}
			before, definitions := historicalNullTotalRows(t, ctx, db), historicalNullTotalDefinitions(t, ctx, db)
			err := repository.CheckHistoricalMigrationPreconditions(ctx, db)
			if name == "missing_required_credentials_column" {
				var unsupported *repository.HistoricalMigrationPreflightError
				require.ErrorAs(t, err, &unsupported)
				require.Equal(t, "HISTORICAL_MIGRATION_SCHEMA_UNSUPPORTED", unsupported.Code)
				require.Equal(t, "accounts", unsupported.Entity)
				require.Equal(t, "missing_column_credentials", unsupported.Reason)
			} else {
				require.NoError(t, err, "a populated but concretely safe historical state must remain accepted")
			}
			require.Equal(t, before, historicalNullTotalRows(t, ctx, db))
			require.Equal(t, definitions, historicalNullTotalDefinitions(t, ctx, db))
		})
	}
}

// All synthetic ledger entries live only in this test's isolated schema. This
// helper is never part of the migration/preflight implementation.
func historicalNullTotalRecordFixtureHistory(t *testing.T, ctx context.Context, db *sql.DB, names []string) {
	t.Helper()
	if len(names) == 0 {
		return
	}
	require.NoError(t, execRemoteSkillSQL(ctx, db, `CREATE TABLE schema_migrations (filename TEXT PRIMARY KEY, checksum TEXT NOT NULL);`))
	for _, name := range names {
		data, err := dbmigrations.FS.ReadFile(name)
		require.NoError(t, err)
		sum := sha256.Sum256([]byte(strings.TrimSpace(string(data))))
		_, err = db.ExecContext(ctx, `INSERT INTO schema_migrations VALUES ($1, $2)`, name, hex.EncodeToString(sum[:]))
		require.NoError(t, err)
	}
}

func historicalNullTotalRows(t *testing.T, ctx context.Context, db *sql.DB) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, table := range []string{"accounts", "groups", "account_groups", "cindy_platform_v1_projection", "user_platform_quotas", "schema_migrations"} {
		var exists bool
		require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists))
		if !exists {
			result[table] = "absent"
			continue
		}
		var rows string
		require.NoError(t, db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COALESCE(jsonb_agg(jsonb_build_object('row', to_jsonb(t), 'xmin', t.xmin::text) ORDER BY to_jsonb(t)::text), '[]'::jsonb)::text FROM %s t`, table)).Scan(&rows))
		result[table] = rows
	}
	return result
}

func historicalNullTotalDefinitions(t *testing.T, ctx context.Context, db *sql.DB) string {
	t.Helper()
	var definition string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT jsonb_build_object(
 'relations', (SELECT COALESCE(jsonb_agg(c.relname || ':' || c.relkind::text ORDER BY c.relname), '[]'::jsonb)
               FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = current_schema()),
 'constraints', (SELECT COALESCE(jsonb_agg(pg_get_constraintdef(c.oid) ORDER BY c.conname), '[]'::jsonb)
                 FROM pg_constraint c JOIN pg_namespace n ON n.oid = c.connamespace WHERE n.nspname = current_schema()),
 'functions', (SELECT COALESCE(jsonb_agg(pg_get_functiondef(p.oid) ORDER BY p.proname), '[]'::jsonb)
               FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = current_schema())
)::text`).Scan(&definition))
	return definition
}

const historicalNullFallbackSeedSQL = `
INSERT INTO accounts (id, credentials) VALUES (2, '{}');
INSERT INTO groups (id, fallback_group_id) VALUES (20, 10);
INSERT INTO account_groups VALUES (2, 20);
`

const historicalNullTotalFixtureSQL = `
CREATE TABLE accounts (
 id BIGINT PRIMARY KEY, platform TEXT NOT NULL DEFAULT 'openai', wire_platform TEXT NOT NULL DEFAULT 'openai',
 provider_profile TEXT NOT NULL DEFAULT '', type TEXT NOT NULL DEFAULT 'apikey', credentials JSONB DEFAULT '{}',
 status TEXT NOT NULL DEFAULT 'active', deleted_at TIMESTAMPTZ,
 CONSTRAINT accounts_cindy_platform_identity_check CHECK (platform <> 'cindy' OR (
   wire_platform = 'openai' AND provider_profile = 'cindy_laxa_v1' AND type = 'apikey'
   AND jsonb_typeof(credentials->'base_url') = 'string'
   AND LOWER(BTRIM(credentials->>'base_url')) IN ('https://api.laxarouter.ai', 'https://api.laxarouter.ai/')))
);
CREATE TABLE groups (
 id BIGINT PRIMARY KEY, platform TEXT NOT NULL DEFAULT 'openai', wire_platform TEXT NOT NULL DEFAULT 'openai',
 provider_profile TEXT NOT NULL DEFAULT '', fallback_group_id BIGINT, fallback_group_id_on_invalid_request BIGINT,
 deleted_at TIMESTAMPTZ
);
CREATE TABLE account_groups (account_id BIGINT NOT NULL REFERENCES accounts(id), group_id BIGINT NOT NULL REFERENCES groups(id), PRIMARY KEY (account_id, group_id));
CREATE TABLE cindy_platform_v1_projection (
 entity_type TEXT NOT NULL, entity_id BIGINT NOT NULL, original_platform TEXT NOT NULL,
 original_wire_platform TEXT NOT NULL, original_provider_profile TEXT NOT NULL, PRIMARY KEY (entity_type, entity_id)
);
CREATE TABLE user_platform_quotas (
 id BIGINT PRIMARY KEY, platform TEXT NOT NULL, daily_limit_usd NUMERIC(20,8), weekly_limit_usd NUMERIC(20,8), monthly_limit_usd NUMERIC(20,8)
);
CREATE FUNCTION project_is_strict_cindy_group(BIGINT) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$ SELECT FALSE $$;
CREATE FUNCTION project_cindy_platform_v1_discover_legacy() RETURNS TABLE(promoted_accounts BIGINT, promoted_groups BIGINT)
LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture projection must not run automatically'; END $$;
CREATE FUNCTION project_cindy_platform_v1_from_legacy() RETURNS TABLE(promoted_accounts BIGINT, promoted_groups BIGINT)
LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture projection must not run automatically'; END $$;
CREATE FUNCTION project_cindy_platform_v1_to_legacy() RETURNS TABLE(restored_accounts BIGINT, restored_groups BIGINT)
LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture projection must not run automatically'; END $$;
`
