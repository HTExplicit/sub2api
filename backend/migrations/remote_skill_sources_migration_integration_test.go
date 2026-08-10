//go:build integration

package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const canonicalRemoteSkillPromptSHA256 = "2107e252ef417561baa4c5349f0c34d4e767ad422dfc463b2eac07bf7bbcc931"

var remoteSkillMigrationSchemaSequence uint64
var remoteSkillMigrationDB *sql.DB

func TestMain(m *testing.M) {
	ctx := context.Background()
	dsn := strings.TrimSpace(os.Getenv("SUB2API_MIGRATION_TEST_DSN"))
	var container *tcpostgres.PostgresContainer
	var err error
	if dsn == "" {
		container, err = tcpostgres.Run(
			ctx,
			"postgres:18.1-alpine3.23",
			tcpostgres.WithDatabase("sub2api_migration_test"),
			tcpostgres.WithUsername("postgres"),
			tcpostgres.WithPassword("postgres"),
			tcpostgres.BasicWaitStrategies(),
		)
		if err == nil {
			dsn, err = container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
		}
	}
	if err == nil {
		remoteSkillMigrationDB, err = sql.Open("postgres", dsn)
	}
	if err == nil {
		err = remoteSkillMigrationDB.PingContext(ctx)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "remote-skill migration PostgreSQL setup:", err)
		if container != nil {
			_ = container.Terminate(ctx)
		}
		os.Exit(1)
	}

	code := m.Run()
	_ = remoteSkillMigrationDB.Close()
	if container != nil {
		_ = container.Terminate(ctx)
	}
	os.Exit(code)
}

func TestRemoteSkillSourcesMigrationMigratesExactLegacyRuntime(t *testing.T) {
	ctx := context.Background()
	tx := remoteSkillMigrationTestTx(t)
	createRemoteSkillMigrationFixture(t, tx)

	canonicalBody := canonicalRemoteSkillPromptBody(t)
	legacyBody := "legacy remote prompt"
	inlineBody := "inline prompt"
	insertPromptTemplate(t, tx, 1, "codexrip_reverse_skill", true)
	insertPromptVersion(t, tx, 1, 1, 1, legacyBody, "remote_skill", "codexrip-reverse-skill", nil)
	insertPromptVersion(t, tx, 2, 1, 2, canonicalBody, "codex_skill_hybrid", "codexrip-reverse-skill", nil)
	insertPromptVersion(t, tx, 3, 1, 3, inlineBody, "inline", "", nil)
	insertPromptTemplate(t, tx, 2, "legacy_orphan", false)
	insertPromptVersion(t, tx, 4, 2, 1, legacyBody, "offline_bundle", "moxinggang-reverse-skill", stringPointer(strings.Repeat("9", 64)))

	_, err := tx.ExecContext(ctx, `INSERT INTO system_prompt_runtime
		(id, active_template_id, active_version_id, revision, updated_at)
		VALUES (1, 1, 1, 7, NOW())`)
	require.NoError(t, err)
	insertRemoteSkillVersionFixture(t, tx, 10, strings.Repeat("1", 40), strings.Repeat("3", 64))
	_, err = tx.ExecContext(ctx, `INSERT INTO system_prompt_skill_runtime
		(id, active_bundle_version_id, revision) VALUES (1, 10, 4)`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO system_prompt_skill_sync_jobs
		(id, status, progress_stage, candidate_bundle_version_id) VALUES (20, 'succeeded', 'candidate_ready', 10)`)
	require.NoError(t, err)

	_, err = tx.ExecContext(ctx, remoteSkillSourcesMigrationSQL(t))
	require.NoError(t, err)

	var activeVersionID, revision int64
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT active_version_id, revision FROM system_prompt_runtime WHERE id = 1`).Scan(&activeVersionID, &revision))
	require.Equal(t, int64(2), activeVersionID)
	require.Equal(t, int64(8), revision)

	var legacyCount, retainedCount, orphanCount int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM system_prompt_template_versions WHERE composition_mode IN ('remote_skill', 'offline_bundle')`).Scan(&legacyCount))
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM system_prompt_template_versions WHERE id IN (2, 3)`).Scan(&retainedCount))
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM system_prompt_templates WHERE id = 2`).Scan(&orphanCount))
	require.Zero(t, legacyCount)
	require.Equal(t, 2, retainedCount)
	require.Zero(t, orphanCount)

	var versionCount, jobCount int
	var versionSource, versionRoot, jobSource string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM system_prompt_skill_bundle_versions`).Scan(&versionCount))
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT source_id, remote_root FROM system_prompt_skill_bundle_versions WHERE id = 10`).Scan(&versionSource, &versionRoot))
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*), MIN(source_id) FROM system_prompt_skill_sync_jobs WHERE id = 20`).Scan(&jobCount, &jobSource))
	require.Equal(t, 1, versionCount)
	require.Equal(t, "github_official", versionSource)
	require.Equal(t, "https://raw.githubusercontent.com/zhaoxuya520/reverse-skill/"+strings.Repeat("1", 40)+"/skills", versionRoot)
	require.Equal(t, 1, jobCount)
	require.Equal(t, "github_official", jobSource)

	insertRemoteSkillVersionWithSourceFixture(t, tx, 11, "moxinggang", "https://moxinggang.com/skills/security-research/current", strings.Repeat("2", 40), strings.Repeat("3", 64))
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM system_prompt_skill_bundle_versions WHERE manifest_sha256 = $1`, strings.Repeat("3", 64)).Scan(&versionCount))
	require.Equal(t, 2, versionCount)

	assertPromptVersionDeleteProtected(t, tx, 3)
}

func TestRemoteSkillSourcesMigrationRejectsMissingOrAmbiguousReplacement(t *testing.T) {
	for _, candidateCount := range []int{0, 2} {
		name := "missing"
		if candidateCount == 2 {
			name = "ambiguous"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			tx := remoteSkillMigrationTestTx(t)
			createRemoteSkillMigrationFixture(t, tx)
			insertPromptTemplate(t, tx, 1, "codexrip_reverse_skill", true)
			insertPromptVersion(t, tx, 1, 1, 1, "legacy remote prompt", "remote_skill", "codexrip-reverse-skill", nil)
			for index := 0; index < candidateCount; index++ {
				insertPromptVersion(t, tx, int64(index+2), 1, int64(index+2), canonicalRemoteSkillPromptBody(t), "codex_skill_hybrid", "codexrip-reverse-skill", nil)
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO system_prompt_runtime
				(id, active_template_id, active_version_id, revision, updated_at)
				VALUES (1, 1, 1, 3, NOW())`)
			require.NoError(t, err)
			insertRemoteSkillVersionFixture(t, tx, 10, strings.Repeat("1", 40), strings.Repeat("3", 64))
			_, err = tx.ExecContext(ctx, `INSERT INTO system_prompt_skill_runtime
				(id, active_bundle_version_id, revision) VALUES (1, 10, 4)`)
			require.NoError(t, err)

			require.NoError(t, execMigrationFixtureSQL(t, tx, "SAVEPOINT before_migration"))
			_, err = tx.ExecContext(ctx, remoteSkillSourcesMigrationSQL(t))
			require.ErrorContains(t, err, "unable to migrate active legacy system prompt")
			require.NoError(t, execMigrationFixtureSQL(t, tx, "ROLLBACK TO SAVEPOINT before_migration"))

			var versionCount, activeVersionID, revision int
			require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM system_prompt_template_versions`).Scan(&versionCount))
			require.NoError(t, tx.QueryRowContext(ctx, `SELECT active_version_id, revision FROM system_prompt_runtime WHERE id = 1`).Scan(&activeVersionID, &revision))
			require.Equal(t, candidateCount+1, versionCount)
			require.Equal(t, 1, activeVersionID)
			require.Equal(t, 3, revision)
			assertPromptVersionDeleteProtected(t, tx, 1)
		})
	}
}

func createRemoteSkillMigrationFixture(t *testing.T, tx *sql.Tx) {
	t.Helper()
	schema := `
		CREATE TABLE system_prompt_templates (
			id BIGINT PRIMARY KEY, slug VARCHAR(100) NOT NULL UNIQUE, name VARCHAR(200) NOT NULL,
			description TEXT NOT NULL DEFAULT '', is_seed BOOLEAN NOT NULL DEFAULT FALSE,
			deleted_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE TABLE system_prompt_template_versions (
			id BIGINT PRIMARY KEY, template_id BIGINT NOT NULL, version BIGINT NOT NULL,
			body TEXT NOT NULL, sha256 CHAR(64) NOT NULL, byte_length INTEGER NOT NULL,
			composition_mode VARCHAR(32) NOT NULL, bundle_id VARCHAR(128), bundle_manifest_sha256 CHAR(64),
			CONSTRAINT system_prompt_template_versions_composition CHECK (composition_mode IN ('inline', 'offline_bundle', 'remote_skill', 'codex_skill_hybrid'))
		);
		CREATE TABLE system_prompt_runtime (
			id SMALLINT PRIMARY KEY, active_template_id BIGINT, active_version_id BIGINT,
			revision BIGINT NOT NULL, updated_at TIMESTAMPTZ NOT NULL
		);
		CREATE TABLE system_prompt_skill_bundle_versions (
			id BIGINT PRIMARY KEY, bundle_id VARCHAR(128) NOT NULL, source_commit CHAR(40) NOT NULL,
			overlay_sha256 CHAR(64) NOT NULL, manifest_sha256 CHAR(64) NOT NULL UNIQUE,
			archive_sha256 CHAR(64) NOT NULL, file_count INTEGER NOT NULL, total_bytes BIGINT NOT NULL,
			added_files INTEGER NOT NULL DEFAULT 0, modified_files INTEGER NOT NULL DEFAULT 0,
			deleted_files INTEGER NOT NULL DEFAULT 0, script_changes INTEGER NOT NULL DEFAULT 0,
			binary_changes INTEGER NOT NULL DEFAULT 0, created_by BIGINT, published_at TIMESTAMPTZ,
			published_by BIGINT, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE TABLE system_prompt_skill_runtime (
			id SMALLINT PRIMARY KEY, active_bundle_version_id BIGINT, revision BIGINT NOT NULL
		);
		CREATE TABLE system_prompt_skill_sync_jobs (
			id BIGINT PRIMARY KEY, status VARCHAR(16) NOT NULL, progress_stage VARCHAR(64) NOT NULL,
			source_commit CHAR(40), candidate_bundle_version_id BIGINT, error_code VARCHAR(100)
		);
		CREATE FUNCTION prevent_system_prompt_version_delete()
		RETURNS TRIGGER LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'system prompt versions cannot be deleted';
		END;
		$$;
		CREATE TRIGGER trg_prevent_system_prompt_version_delete
		BEFORE DELETE ON system_prompt_template_versions
		FOR EACH ROW EXECUTE FUNCTION prevent_system_prompt_version_delete();`
	require.NoError(t, execMigrationFixtureSQL(t, tx, schema))
}

func insertPromptTemplate(t *testing.T, tx *sql.Tx, id int64, slug string, seed bool) {
	t.Helper()
	_, err := tx.Exec(`INSERT INTO system_prompt_templates (id, slug, name, is_seed) VALUES ($1, $2, $2, $3)`, id, slug, seed)
	require.NoError(t, err)
}

func insertPromptVersion(t *testing.T, tx *sql.Tx, id, templateID, version int64, body, mode, bundleID string, manifest *string) {
	t.Helper()
	digest := sha256.Sum256([]byte(body))
	var bundle any
	if bundleID != "" {
		bundle = bundleID
	}
	_, err := tx.Exec(`INSERT INTO system_prompt_template_versions
		(id, template_id, version, body, sha256, byte_length, composition_mode, bundle_id, bundle_manifest_sha256)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, templateID, version, body, hex.EncodeToString(digest[:]), len([]byte(body)), mode, bundle, manifest)
	require.NoError(t, err)
}

func insertRemoteSkillVersionFixture(t *testing.T, tx *sql.Tx, id int64, commit, manifest string) {
	t.Helper()
	_, err := tx.Exec(`INSERT INTO system_prompt_skill_bundle_versions
		(id, bundle_id, source_commit, overlay_sha256, manifest_sha256, archive_sha256, file_count, total_bytes)
		VALUES ($1, 'codexrip-reverse-skill', $2, $3, $4, $5, 6, 1200)`,
		id, commit, strings.Repeat("2", 64), manifest, strings.Repeat("4", 64))
	require.NoError(t, err)
}

func insertRemoteSkillVersionWithSourceFixture(t *testing.T, tx *sql.Tx, id int64, sourceID, remoteRoot, commit, manifest string) {
	t.Helper()
	_, err := tx.Exec(`INSERT INTO system_prompt_skill_bundle_versions
		(id, bundle_id, source_id, remote_root, source_commit, overlay_sha256, manifest_sha256, archive_sha256, file_count, total_bytes)
		VALUES ($1, 'codexrip-reverse-skill', $2, $3, $4, $5, $6, $7, 6, 1200)`,
		id, sourceID, remoteRoot, commit, strings.Repeat("5", 64), manifest, strings.Repeat("6", 64))
	require.NoError(t, err)
}

func canonicalRemoteSkillPromptBody(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../internal/service/prompts/codexrip_reverse_skill_system_prompt.txt")
	require.NoError(t, err)
	body := strings.TrimSuffix(string(raw), "\n")
	digest := sha256.Sum256([]byte(body))
	require.Equal(t, canonicalRemoteSkillPromptSHA256, hex.EncodeToString(digest[:]))
	require.Len(t, []byte(body), 6724)
	return body
}

func remoteSkillSourcesMigrationSQL(t *testing.T) string {
	t.Helper()
	raw, err := FS.ReadFile("200_remote_skill_sources_and_legacy_cleanup.sql")
	require.NoError(t, err)
	return string(raw)
}

func assertPromptVersionDeleteProtected(t *testing.T, tx *sql.Tx, versionID int64) {
	t.Helper()
	require.NoError(t, execMigrationFixtureSQL(t, tx, "SAVEPOINT delete_protection"))
	_, err := tx.Exec(`DELETE FROM system_prompt_template_versions WHERE id = $1`, versionID)
	require.ErrorContains(t, err, "system prompt versions cannot be deleted")
	require.NoError(t, execMigrationFixtureSQL(t, tx, "ROLLBACK TO SAVEPOINT delete_protection"))
}

func execMigrationFixtureSQL(t *testing.T, tx *sql.Tx, query string) error {
	t.Helper()
	_, err := tx.ExecContext(context.Background(), query)
	return err
}

func stringPointer(value string) *string {
	return &value
}

func remoteSkillMigrationTestTx(t *testing.T) *sql.Tx {
	t.Helper()
	require.NotNil(t, remoteSkillMigrationDB)
	_, err := remoteSkillMigrationDB.ExecContext(context.Background(), "CREATE EXTENSION IF NOT EXISTS pgcrypto")
	require.NoError(t, err)

	tx, err := remoteSkillMigrationDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	schema := fmt.Sprintf("remote_skill_migration_%d", atomic.AddUint64(&remoteSkillMigrationSchemaSequence, 1))
	_, err = tx.ExecContext(context.Background(), "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(context.Background(), "SET LOCAL search_path TO "+schema+", public")
	require.NoError(t, err)
	return tx
}
