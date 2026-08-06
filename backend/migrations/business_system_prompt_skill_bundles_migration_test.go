package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBusinessSystemPromptSkillBundlesMigrationAddsImmutableCompositionReference(t *testing.T) {
	raw, err := FS.ReadFile("195_business_system_prompt_skill_bundles.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))

	for _, fragment := range []string{
		"add column if not exists composition_mode",
		"add column if not exists bundle_id",
		"add column if not exists bundle_manifest_sha256",
		"composition_mode in ('inline', 'offline_bundle')",
		"old.composition_mode is distinct from new.composition_mode",
		"old.bundle_id is distinct from new.bundle_id",
		"old.bundle_manifest_sha256 is distinct from new.bundle_manifest_sha256",
		"moxinggang_reverse_skill",
		"c2f0269baffa6a0eb1c9a9e15df815a6582ae6a615bc51d64b7cc5342b5efcb8",
		"byte_length = 7098",
		"moxinggang-reverse-skill",
		"22c227128165afbbcbda0175eb5e991ddb51d105b7d1e704572c625c64b626d7",
	} {
		require.Contains(t, sql, fragment)
	}
}

func TestBusinessSystemPromptSkillBundlesMigrationBackfillIsNarrow(t *testing.T) {
	raw, err := FS.ReadFile("195_business_system_prompt_skill_bundles.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))

	require.Contains(t, sql, "t.is_seed = true")
	require.Contains(t, sql, "v.version = 1")
	require.Contains(t, sql, "v.composition_mode = 'inline'")
	require.Contains(t, sql, "v.bundle_id is null")
	require.Contains(t, sql, "v.bundle_manifest_sha256 is null")
}
