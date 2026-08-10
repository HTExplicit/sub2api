package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoteSkillSourcesAndLegacyCleanupMigrationIsFailClosed(t *testing.T) {
	raw, err := FS.ReadFile("200_remote_skill_sources_and_legacy_cleanup.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))

	for _, fragment := range []string{
		"add column if not exists source_id",
		"add column if not exists remote_root",
		"github_official",
		"moxinggang",
		"source_commit || '/skills'",
		"unique (source_id, manifest_sha256)",
		"old.source_id is distinct from new.source_id",
		"old.remote_root is distinct from new.remote_root",
		"create temporary table legacy_prompt_templates",
		"composition_mode in ('remote_skill', 'offline_bundle')",
		"composition_mode = 'codex_skill_hybrid'",
		"replacement_count <> 1",
		"2107e252ef417561baa4c5349f0c34d4e767ad422dfc463b2eac07bf7bbcc931",
		"octet_length(v.body) = 6724",
		"t.deleted_at is null",
		"unable to migrate active legacy system prompt",
		"delete from system_prompt_template_versions",
		"delete from system_prompt_templates",
		"composition_mode in ('inline', 'codex_skill_hybrid')",
		"create trigger trg_prevent_system_prompt_version_delete",
	} {
		require.Contains(t, sql, fragment)
	}
	require.NotContains(t, sql, "delete from system_prompt_template_versions where composition_mode = 'inline'")
	require.NotContains(t, sql, "delete from system_prompt_template_versions where composition_mode = 'codex_skill_hybrid'")
	require.NotContains(t, sql, "delete from system_prompt_skill_bundle_versions")
}
