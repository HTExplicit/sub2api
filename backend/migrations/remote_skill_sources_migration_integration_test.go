//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
)

var remoteSkillMigrationDSN string
var remoteSkillMigrationDB *sql.DB
var remoteSkillMigrationSchemaSequence uint64

func TestMain(m *testing.M) {
	ctx := context.Background()
	remoteSkillMigrationDSN = strings.TrimSpace(os.Getenv("SUB2API_MIGRATION_TEST_DSN"))
	var container *tcpostgres.PostgresContainer
	var err error
	if remoteSkillMigrationDSN == "" {
		if runtime.GOOS == "windows" {
			fmt.Fprintln(os.Stderr, "remote-skill migration integration tests require SUB2API_MIGRATION_TEST_DSN on Windows")
			os.Exit(0)
		}
		container, err = tcpostgres.Run(
			ctx,
			"postgres:18.1-alpine3.23",
			tcpostgres.WithDatabase("sub2api_migration_test"),
			tcpostgres.WithUsername("postgres"),
			tcpostgres.WithPassword("postgres"),
			tcpostgres.BasicWaitStrategies(),
		)
		if err == nil {
			remoteSkillMigrationDSN, err = container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
		}
	}
	if err == nil {
		remoteSkillMigrationDB, err = sql.Open("postgres", remoteSkillMigrationDSN)
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

func TestRemoteSkillPairedMigrationAndStartupRemoveEightGitHubVersions(t *testing.T) {
	ctx := context.Background()
	db, schema := remoteSkillMigrationTestDatabase(t)
	require.NoError(t, execRemoteSkillSQL(ctx, db, remoteSkillPost200FixtureSQL))
	require.NoError(t, insertEightLegacyRemoteSkillVersions(ctx, db))

	migrationSQL, err := dbmigrations.FS.ReadFile("201_remote_skill_paired_candidates.sql")
	require.NoError(t, err)
	require.NoError(t, execRemoteSkillSQL(ctx, db, string(migrationSQL)))

	var managedSource string
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT managed_source FROM system_prompt_templates
		WHERE slug = 'codexrip_reverse_skill'`).Scan(&managedSource))
	require.Equal(t, service.BusinessSystemPromptManagedSourceRemoteSkill, managedSource)

	for _, column := range []string{
		"bundle_id", "source_id", "remote_root", "source_commit", "overlay_sha256",
		"manifest_sha256", "archive_sha256", "total_bytes",
	} {
		assertRemoteSkillColumnMissing(t, ctx, db, schema, "system_prompt_skill_bundle_versions", column)
	}
	for _, column := range []string{"source_id", "source_commit"} {
		assertRemoteSkillColumnMissing(t, ctx, db, schema, "system_prompt_skill_sync_jobs", column)
	}

	registryRoot := t.TempDir()
	for _, name := range []string{
		"private/seed/legacy.txt",
		"private/versions/legacy.txt",
		"public/reverse-skill/current.json",
		"public/bootstrap/legacy.ps1",
		"public/versions/legacy.zip",
		"staging/incomplete/partial.txt",
	} {
		path := filepath.Join(registryRoot, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte("legacy"), 0o640))
	}

	registryFiles := service.NewRemoteSkillRegistryFilesystem(registryRoot)
	registryStore := repository.NewRemoteSkillRegistryRepository(db)
	registryService := service.NewRemoteSkillRegistryService(registryStore, nil, registryFiles, nil)
	require.NoError(t, registryService.Initialize(ctx))

	snapshot := registryService.CurrentSnapshot()
	require.NotNil(t, snapshot.Active)
	require.NotNil(t, snapshot.ActivePrompt)
	require.Equal(t, int64(5), snapshot.Revision)
	require.Equal(t, service.RemoteSkillUpstreamSourceID, snapshot.Active.UpstreamSourceID)
	require.Equal(t, service.RemoteSkillUpstreamRoot, snapshot.Active.UpstreamRoot)
	require.Equal(t, service.RemoteSkillPublicRoot, snapshot.Active.PublicRoot)
	require.Equal(t, 73, snapshot.Active.FileCount)
	require.Equal(t, snapshot.Active.PromptVersionID, snapshot.ActivePrompt.ID)

	var versionCount, promptCount, jobCount, legacyCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM system_prompt_skill_bundle_versions`).Scan(&versionCount))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM system_prompt_skill_prompt_versions`).Scan(&promptCount))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM system_prompt_skill_sync_jobs`).Scan(&jobCount))
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM system_prompt_skill_bundle_versions
		WHERE upstream_source_id IS DISTINCT FROM $1 OR upstream_root IS DISTINCT FROM $2
		   OR public_root IS DISTINCT FROM $3 OR prompt_version_id IS NULL`,
		service.RemoteSkillUpstreamSourceID, service.RemoteSkillUpstreamRoot, service.RemoteSkillPublicRoot).Scan(&legacyCount))
	require.Equal(t, 1, versionCount)
	require.Equal(t, 1, promptCount)
	require.Zero(t, jobCount)
	require.Zero(t, legacyCount)

	// Startup is idempotent, while a later no-change sync remains a distinct
	// audit candidate that shares the same immutable content directory.
	require.NoError(t, registryService.Initialize(ctx))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM system_prompt_skill_bundle_versions`).Scan(&versionCount))
	require.Equal(t, 1, versionCount)
	second, err := registryFiles.LoadSeed(ctx)
	require.NoError(t, err)
	second.Version.FetchedAt = snapshot.Active.FetchedAt.Add(time.Minute).UTC()
	second.Version.AddedFiles = 0
	second.Version.ModifiedFiles = 0
	second.Version.DeletedFiles = 0
	second.Version.ScriptChanges = 0
	second.Version.BinaryChanges = 0
	second.FileChanges = []service.RemoteSkillFileChange{}
	require.NoError(t, registryFiles.InstallCandidate(ctx, second))
	job, err := registryStore.CreateRemoteSkillSyncJob(ctx, 0, snapshot.Revision, false)
	require.NoError(t, err)
	job, err = registryStore.CompleteRemoteSkillSyncJob(ctx, job.ID, second)
	require.NoError(t, err)
	require.NotEqual(t, snapshot.Active.ID, job.CandidateBundleVersionID)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM system_prompt_skill_bundle_versions`).Scan(&versionCount))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM system_prompt_skill_prompt_versions`).Scan(&promptCount))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM system_prompt_skill_sync_jobs`).Scan(&jobCount))
	require.Equal(t, 2, versionCount)
	require.Equal(t, 1, promptCount)
	require.Equal(t, 1, jobCount)

	for _, name := range []string{"private", "public", "staging"} {
		_, err := os.Stat(filepath.Join(registryRoot, filepath.FromSlash(name)))
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	pairedEntries, err := os.ReadDir(filepath.Join(registryRoot, "paired"))
	require.NoError(t, err)
	require.Len(t, pairedEntries, 1)
}

func remoteSkillMigrationTestDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	require.NotNil(t, remoteSkillMigrationDB)
	schema := fmt.Sprintf("remote_skill_paired_%d", atomic.AddUint64(&remoteSkillMigrationSchemaSequence, 1))
	_, err := remoteSkillMigrationDB.ExecContext(context.Background(), "CREATE SCHEMA "+schema)
	require.NoError(t, err)

	db, err := sql.Open("postgres", remoteSkillMigrationDSN)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	require.NoError(t, db.PingContext(context.Background()))
	_, err = db.ExecContext(context.Background(), "SET search_path TO "+schema+", public")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = remoteSkillMigrationDB.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
	})
	return db, schema
}

func insertEightLegacyRemoteSkillVersions(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO system_prompt_templates
			(id, slug, name, description, is_seed, managed_source)
		VALUES
			(1, 'codexrip_reverse_skill', 'Legacy remote skill', 'legacy', TRUE, NULL);

		INSERT INTO system_prompt_skill_bundle_versions
			(id, bundle_id, source_id, remote_root, source_commit, overlay_sha256,
			 manifest_sha256, archive_sha256, file_count, total_bytes, created_at)
		SELECT
			value,
			'codexrip-reverse-skill',
			'github_official',
			'https://raw.githubusercontent.com/zhaoxuya520/reverse-skill/' || LPAD(value::text, 40, '0') || '/skills',
			LPAD(value::text, 40, '0'),
			LPAD((value + 10)::text, 64, '0'),
			LPAD((value + 20)::text, 64, '0'),
			LPAD((value + 30)::text, 64, '0'),
			545,
			1048576,
			NOW() - make_interval(days => 9 - value)
		FROM generate_series(1, 8) AS value;

		INSERT INTO system_prompt_skill_runtime
			(id, active_bundle_version_id, revision, updated_at)
		VALUES (1, 8, 4, NOW());

		INSERT INTO system_prompt_skill_sync_jobs
			(id, status, progress_stage, source_id, source_commit,
			 candidate_bundle_version_id, created_at, completed_at)
		SELECT value, 'succeeded', 'candidate_ready', 'github_official',
		       LPAD(value::text, 40, '0'), value, NOW() - INTERVAL '1 day', NOW()
		FROM generate_series(1, 8) AS value;
	`)
	return err
}

func assertRemoteSkillColumnMissing(t *testing.T, ctx context.Context, db *sql.DB, schema, table, column string) {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2 AND column_name = $3`,
		schema, table, column).Scan(&count))
	require.Zero(t, count, "%s.%s must be removed", table, column)
}

func execRemoteSkillSQL(ctx context.Context, db *sql.DB, query string) error {
	_, err := db.ExecContext(ctx, query)
	return err
}

const remoteSkillPost200FixtureSQL = `
CREATE TABLE system_prompt_templates (
    id BIGSERIAL PRIMARY KEY,
    slug VARCHAR(100) NOT NULL UNIQUE,
    name VARCHAR(200) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    is_seed BOOLEAN NOT NULL DEFAULT FALSE,
    managed_source VARCHAR(100),
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE OR REPLACE FUNCTION protect_system_prompt_template_managed_source()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.managed_source IS DISTINCT FROM NEW.managed_source THEN
        RAISE EXCEPTION 'system prompt managed source is immutable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER trg_protect_system_prompt_template_managed_source
BEFORE UPDATE ON system_prompt_templates
FOR EACH ROW EXECUTE FUNCTION protect_system_prompt_template_managed_source();

CREATE TABLE system_prompt_skill_bundle_versions (
    id BIGSERIAL PRIMARY KEY,
    bundle_id VARCHAR(128) NOT NULL,
    source_id VARCHAR(32) NOT NULL DEFAULT 'github_official',
    remote_root TEXT NOT NULL,
    source_commit CHAR(40) NOT NULL,
    overlay_sha256 CHAR(64) NOT NULL,
    manifest_sha256 CHAR(64) NOT NULL,
    archive_sha256 CHAR(64) NOT NULL,
    file_count INTEGER NOT NULL CHECK (file_count > 0 AND file_count <= 2000),
    total_bytes BIGINT NOT NULL CHECK (total_bytes > 0 AND total_bytes <= 268435456),
    added_files INTEGER NOT NULL DEFAULT 0 CHECK (added_files >= 0),
    modified_files INTEGER NOT NULL DEFAULT 0 CHECK (modified_files >= 0),
    deleted_files INTEGER NOT NULL DEFAULT 0 CHECK (deleted_files >= 0),
    script_changes INTEGER NOT NULL DEFAULT 0 CHECK (script_changes >= 0),
    binary_changes INTEGER NOT NULL DEFAULT 0 CHECK (binary_changes >= 0),
    created_by BIGINT,
    published_at TIMESTAMPTZ,
    published_by BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT system_prompt_skill_bundle_id CHECK (bundle_id = 'codexrip-reverse-skill'),
    CONSTRAINT system_prompt_skill_source_commit_hex CHECK (source_commit ~ '^[0-9a-f]{40}$'),
    CONSTRAINT system_prompt_skill_overlay_sha256_hex CHECK (overlay_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT system_prompt_skill_manifest_sha256_hex CHECK (manifest_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT system_prompt_skill_archive_sha256_hex CHECK (archive_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT system_prompt_skill_source_manifest_unique UNIQUE (source_id, manifest_sha256),
    CONSTRAINT system_prompt_skill_source_identity CHECK (
        source_id = 'github_official' AND
        remote_root = 'https://raw.githubusercontent.com/zhaoxuya520/reverse-skill/' || source_commit || '/skills'
    )
);

CREATE TABLE system_prompt_skill_runtime (
    id SMALLINT PRIMARY KEY DEFAULT 1,
    active_bundle_version_id BIGINT REFERENCES system_prompt_skill_bundle_versions(id) ON DELETE RESTRICT,
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_by BIGINT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (id = 1)
);

CREATE TABLE system_prompt_skill_sync_jobs (
    id BIGSERIAL PRIMARY KEY,
    status VARCHAR(16) NOT NULL DEFAULT 'queued',
    progress_stage VARCHAR(64) NOT NULL DEFAULT 'queued',
    source_id VARCHAR(32) NOT NULL DEFAULT 'github_official',
    source_commit CHAR(40),
    candidate_bundle_version_id BIGINT REFERENCES system_prompt_skill_bundle_versions(id) ON DELETE RESTRICT,
    error_code VARCHAR(100),
    created_by BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    CONSTRAINT system_prompt_skill_sync_status CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
    CONSTRAINT system_prompt_skill_sync_source_commit_hex CHECK (source_commit IS NULL OR source_commit ~ '^[0-9a-f]{40}$'),
    CONSTRAINT system_prompt_skill_sync_source_id CHECK (source_id IN ('github_official', 'moxinggang'))
);

CREATE OR REPLACE FUNCTION protect_system_prompt_skill_bundle_version()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.bundle_id IS DISTINCT FROM NEW.bundle_id
       OR OLD.source_id IS DISTINCT FROM NEW.source_id
       OR OLD.remote_root IS DISTINCT FROM NEW.remote_root
       OR OLD.source_commit IS DISTINCT FROM NEW.source_commit
       OR OLD.overlay_sha256 IS DISTINCT FROM NEW.overlay_sha256
       OR OLD.manifest_sha256 IS DISTINCT FROM NEW.manifest_sha256
       OR OLD.archive_sha256 IS DISTINCT FROM NEW.archive_sha256
       OR OLD.file_count IS DISTINCT FROM NEW.file_count
       OR OLD.total_bytes IS DISTINCT FROM NEW.total_bytes THEN
        RAISE EXCEPTION 'system prompt skill bundle version is immutable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER trg_protect_system_prompt_skill_bundle_version
BEFORE UPDATE ON system_prompt_skill_bundle_versions
FOR EACH ROW EXECUTE FUNCTION protect_system_prompt_skill_bundle_version();

CREATE OR REPLACE FUNCTION prevent_system_prompt_skill_bundle_version_delete()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'system prompt skill bundle versions cannot be deleted';
END;
$$;
CREATE TRIGGER trg_prevent_system_prompt_skill_bundle_version_delete
BEFORE DELETE ON system_prompt_skill_bundle_versions
FOR EACH ROW EXECUTE FUNCTION prevent_system_prompt_skill_bundle_version_delete();
`
