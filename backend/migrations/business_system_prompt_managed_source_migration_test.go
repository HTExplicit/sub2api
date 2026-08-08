package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBusinessSystemPromptManagedSourceMigrationIsAdditiveAndImmutable(t *testing.T) {
	raw, err := FS.ReadFile("199_business_system_prompt_managed_source.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))

	for _, fragment := range []string{
		"add column if not exists managed_source",
		"idx_system_prompt_templates_managed_source",
		"source_repository",
		"source_commit",
		"source_version",
		"source_artifact",
		"source_artifact_sha256",
		"source_license_sha256",
		"system_prompt_source_fields_all_or_none",
		"system_prompt_source_commit_hex",
		"system_prompt_source_artifact_sha256_hex",
		"system_prompt_source_license_sha256_hex",
		"protect_system_prompt_template_managed_source",
		"trg_protect_system_prompt_template_managed_source",
		"old.source_repository is distinct from new.source_repository",
		"old.source_commit is distinct from new.source_commit",
		"old.source_artifact_sha256 is distinct from new.source_artifact_sha256",
	} {
		require.Contains(t, sql, fragment)
	}
	require.NotContains(t, sql, "insert into system_prompt_templates")
	require.NotContains(t, sql, "update system_prompt_runtime")
}
