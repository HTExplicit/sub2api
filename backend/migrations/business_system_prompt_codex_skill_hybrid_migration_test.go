package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBusinessSystemPromptCodexSkillHybridMigrationRequiresExactRemoteSeed(t *testing.T) {
	raw, err := FS.ReadFile("198_business_system_prompt_codex_skill_hybrid.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))

	for _, fragment := range []string{
		"t.is_seed = true",
		"t.slug = 'codexrip_reverse_skill'",
		"v.version = 1",
		"cbf75cc85cd77860e53d06820e7120802d83c069e9d24b48715711acc15893c6",
		"v.byte_length = 7045",
		"v.composition_mode = 'remote_skill'",
		"v.bundle_id = 'codexrip-reverse-skill'",
		"bundle_manifest_sha256 is null",
		"composition_mode = 'codex_skill_hybrid'",
		"revision = revision + 1",
		"raise exception",
		"composition_mode in ('inline', 'offline_bundle', 'remote_skill', 'codex_skill_hybrid')",
		"create trigger trg_protect_system_prompt_version_content",
		"active skill bundle does not match the exact .6 release seed",
		"07bf0d71dfb687ff3ced0befa39081453c51ce85ae54a02bdb1e1f6fc34d3313",
		"c6920445c55f46c2a30e8a2fe398e7c1cf0b22dcbe4c53ed0cfc105d9c8a5f3e",
	} {
		require.Contains(t, sql, fragment)
	}
}

func TestBusinessSystemPromptCodexSkillHybridMigrationEmbedsValidatedPrompt(t *testing.T) {
	raw, err := FS.ReadFile("198_business_system_prompt_codex_skill_hybrid.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))
	require.Contains(t, sql, "decode(")
	require.Contains(t, sql, "convert_from")
	require.Contains(t, sql, "sha256(")
	require.Contains(t, sql, "octet_length(")
	require.Contains(t, sql, "expected new prompt fingerprint")
}
