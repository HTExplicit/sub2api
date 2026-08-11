package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoteSkillPairedCandidatesMigrationUsesOnlyFixedMoxinggangSource(t *testing.T) {
	raw, err := FS.ReadFile("201_remote_skill_paired_candidates.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))
	for _, fragment := range []string{
		"create table if not exists system_prompt_skill_prompt_versions",
		"raw_sha256 char(64) not null",
		"effective_sha256 char(64) not null",
		"raw_body text not null",
		"effective_body text not null",
		"diff text not null",
		"upstream_source_id",
		"https://moxinggang.com/skills/security-research/current",
		"https://codexrip.vip/skills/security-research/current",
		"raw_tree_sha256",
		"effective_tree_sha256",
		"prompt_version_id",
		"active_prompt_version_id",
		"prompt_capture_provided",
		"sub2api.remote_skill_cleanup",
		"system prompt skill prompt version is immutable",
		"managed_source = 'remote_skill_registry'",
		"unexpected managed source on remote skill prompt",
	} {
		require.Contains(t, sql, fragment)
	}
	for _, forbidden := range []string{
		"github_official",
		"bootstrap",
		"descriptor",
	} {
		require.NotContains(t, sql, forbidden)
	}
	for _, legacyColumn := range []string{"source_commit", "manifest_sha256", "archive_sha256"} {
		require.NotContains(t, sql, "add column "+legacyColumn)
		require.NotContains(t, sql, "select "+legacyColumn)
	}
}
