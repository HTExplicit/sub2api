//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestMigration274RemovesReleaseAcceptanceKeysSafely(t *testing.T) {
	raw, err := dbmigrations.FS.ReadFile("274_purge_release_acceptance_api_keys.sql")
	require.NoError(t, err)
	for _, state := range []string{"retired", "active_acceptance", "missing_lease", "ordinary_lease"} {
		t.Run(state, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			db, _ := remoteSkillMigrationTestDatabase(t)
			require.NoError(t, execRemoteSkillSQL(ctx, db, releaseAcceptanceRetirementFixtureSQL))
			apply := func() error {
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				defer tx.Rollback()
				if _, err := tx.ExecContext(ctx, string(raw)); err != nil {
					return err
				}
				return tx.Commit()
			}
			if state != "retired" {
				mutation := map[string]string{
					"active_acceptance": "UPDATE api_keys SET deleted_at = NULL WHERE id = 2",
					"missing_lease":     "UPDATE api_keys SET lease_id = NULL WHERE id = 2",
					"ordinary_lease":    "UPDATE api_keys SET lease_id = 'ordinary-lease' WHERE id = 1",
				}[state]
				_, err := db.ExecContext(ctx, mutation)
				require.NoError(t, err)
				before := releaseAcceptanceRetirementSnapshot(t, ctx, db)
				require.ErrorContains(t, apply(), "release-acceptance keys are not retired leased keys")
				require.Equal(t, before, releaseAcceptanceRetirementSnapshot(t, ctx, db), "refusal must roll back every data and schema change")
				return
			}

			var ordinaryBefore, ordinaryAfter string
			ordinaryQuery := `SELECT jsonb_build_object('id', id, 'key', key, 'deleted_at', deleted_at, 'xmin', xmin::text)::text FROM api_keys WHERE id = 1`
			require.NoError(t, db.QueryRowContext(ctx, ordinaryQuery).Scan(&ordinaryBefore))
			ordinaryRows := map[string]string{}
			for _, table := range []string{"usage_logs", "billing_usage_entries", "usage_billing_dedup", "usage_billing_dedup_archive"} {
				var row string
				require.NoError(t, db.QueryRowContext(ctx, "SELECT to_jsonb(t)::text FROM "+table+" t WHERE api_key_id = 1").Scan(&row))
				ordinaryRows[table] = row
			}
			diagnostics := map[string]string{}
			for _, table := range []string{"ops_error_logs", "ops_system_logs", "ops_ingress_reject_aggregates"} {
				diagnostics[table] = releaseAcceptanceRetirementTable(t, ctx, db, table)
			}
			require.NoError(t, apply())
			require.NoError(t, db.QueryRowContext(ctx, ordinaryQuery).Scan(&ordinaryAfter))
			require.Equal(t, ordinaryBefore, ordinaryAfter, "ordinary key values and xmin must stay")
			for _, table := range []string{"api_keys", "usage_logs", "billing_usage_entries", "usage_billing_dedup", "usage_billing_dedup_archive"} {
				var count int
				require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count))
				require.Equal(t, 1, count, "only the ordinary key's row remains in %s", table)
			}
			for table, before := range ordinaryRows {
				var row string
				require.NoError(t, db.QueryRowContext(ctx, "SELECT to_jsonb(t)::text FROM "+table+" t WHERE api_key_id = 1").Scan(&row))
				require.Equal(t, before, row, "ordinary key usage and claims in %s stay", table)
			}
			for table, before := range diagnostics {
				require.Equal(t, before, releaseAcceptanceRetirementTable(t, ctx, db, table), "diagnostic key references and content in %s stay", table)
			}
			var nullAuditReferences int
			require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM prompt_audit_events WHERE api_key_id IS NULL").Scan(&nullAuditReferences))
			require.Equal(t, 2, nullAuditReferences, "SET NULL audit references must retain their rows")
			var leaseObjects int
			require.NoError(t, db.QueryRowContext(ctx, `SELECT
				(SELECT count(*) FROM pg_attribute WHERE attrelid = 'api_keys'::regclass AND attname IN ('purpose', 'lease_id') AND NOT attisdropped) +
				(SELECT count(*) FROM pg_constraint WHERE conrelid = 'api_keys'::regclass AND conname = 'api_keys_purpose_valid') +
				(SELECT count(*) FROM pg_class WHERE relnamespace = (SELECT relnamespace FROM pg_class WHERE oid = 'api_keys'::regclass) AND relname IN ('api_keys_acceptance_expiry', 'api_keys_lease_id_unique'))`).Scan(&leaseObjects))
			require.Zero(t, leaseObjects, "both lease columns, dedicated indexes and constraint are gone")
			after := releaseAcceptanceRetirementSnapshot(t, ctx, db)
			require.NoError(t, apply())
			require.Equal(t, after, releaseAcceptanceRetirementSnapshot(t, ctx, db), "reapplying after the columns are gone is safe")
		})
	}
}

func releaseAcceptanceRetirementTable(t *testing.T, ctx context.Context, db *sql.DB, table string) string {
	t.Helper()
	var rows string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text), '[]'::jsonb)::text FROM "+table+" t").Scan(&rows))
	return rows
}

func releaseAcceptanceRetirementSnapshot(t *testing.T, ctx context.Context, db *sql.DB) map[string]string {
	t.Helper()
	rows := map[string]string{}
	for _, table := range []string{"api_keys", "usage_logs", "billing_usage_entries", "usage_billing_dedup", "usage_billing_dedup_archive", "ops_error_logs", "ops_system_logs", "ops_ingress_reject_aggregates", "prompt_audit_events"} {
		rows[table] = releaseAcceptanceRetirementTable(t, ctx, db, table)
	}
	return rows
}

const releaseAcceptanceRetirementFixtureSQL = `
CREATE TABLE api_keys (
    id BIGINT PRIMARY KEY, key TEXT NOT NULL, purpose VARCHAR(32) NOT NULL DEFAULT 'user',
    lease_id VARCHAR(64), expires_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ,
    CONSTRAINT api_keys_purpose_valid CHECK (purpose IN ('user', 'release_acceptance'))
);
CREATE UNIQUE INDEX api_keys_lease_id_unique ON api_keys (lease_id) WHERE lease_id IS NOT NULL;
CREATE INDEX api_keys_acceptance_expiry ON api_keys (expires_at) WHERE purpose = 'release_acceptance' AND deleted_at IS NULL;
CREATE TABLE usage_logs (id BIGINT PRIMARY KEY, api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE, amount NUMERIC NOT NULL);
CREATE TABLE billing_usage_entries (id BIGINT PRIMARY KEY, api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE, amount NUMERIC NOT NULL);
CREATE TABLE usage_billing_dedup (request_id TEXT NOT NULL, api_key_id BIGINT NOT NULL, PRIMARY KEY (request_id, api_key_id));
CREATE TABLE usage_billing_dedup_archive (request_id TEXT NOT NULL, api_key_id BIGINT NOT NULL, PRIMARY KEY (request_id, api_key_id));
CREATE TABLE ops_error_logs (id BIGINT PRIMARY KEY, api_key_id BIGINT NOT NULL, body TEXT NOT NULL);
CREATE TABLE ops_system_logs (id BIGINT PRIMARY KEY, api_key_id BIGINT NOT NULL, body TEXT NOT NULL);
CREATE TABLE ops_ingress_reject_aggregates (id BIGINT PRIMARY KEY, api_key_id BIGINT NOT NULL, body TEXT NOT NULL);
CREATE TABLE prompt_audit_events (id BIGINT PRIMARY KEY, api_key_id BIGINT REFERENCES api_keys(id) ON DELETE SET NULL, body TEXT NOT NULL);
INSERT INTO api_keys VALUES
    (1, 'ordinary-key', 'user', NULL, NULL, NULL),
    (2, 'retired-key-2', 'release_acceptance', 'acceptance-2', '2026-09-01', '2026-09-01'),
    (3, 'retired-key-3', 'release_acceptance', 'acceptance-3', '2026-09-07', '2026-09-07');
INSERT INTO usage_logs VALUES (1, 1, 0.5), (2, 2, 0.25), (3, 3, 0.75);
INSERT INTO billing_usage_entries SELECT * FROM usage_logs;
INSERT INTO usage_billing_dedup VALUES ('ordinary-request', 1), ('acceptance-request-2', 2), ('acceptance-request-3', 3);
INSERT INTO usage_billing_dedup_archive SELECT * FROM usage_billing_dedup;
INSERT INTO ops_error_logs VALUES (1, 1, 'ordinary-error'), (2, 2, 'acceptance-error'), (3, 3, 'acceptance-error-3');
INSERT INTO ops_system_logs SELECT * FROM ops_error_logs;
INSERT INTO ops_ingress_reject_aggregates SELECT * FROM ops_error_logs;
INSERT INTO prompt_audit_events SELECT * FROM ops_error_logs;
`
