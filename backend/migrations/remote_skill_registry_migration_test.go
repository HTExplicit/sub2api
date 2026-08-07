package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoteSkillRegistryMigrationCreatesIndependentImmutableCatalog(t *testing.T) {
	raw, err := FS.ReadFile("197_remote_skill_registry.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))

	for _, fragment := range []string{
		"create table if not exists system_prompt_skill_bundle_versions",
		"create table if not exists system_prompt_skill_runtime",
		"create table if not exists system_prompt_skill_sync_jobs",
		"manifest_sha256 char(64) not null unique",
		"archive_sha256 char(64) not null",
		"source_commit char(40) not null",
		"active_bundle_version_id",
		"revision bigint not null default 1",
		"status in ('queued', 'running', 'succeeded', 'failed')",
		"before update on system_prompt_skill_bundle_versions",
		"before delete on system_prompt_skill_bundle_versions",
	} {
		require.Contains(t, sql, fragment)
	}
}
