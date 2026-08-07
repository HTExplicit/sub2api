package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBusinessSystemPromptRemoteSkillMigrationRewritesOnlyExactSeedV1(t *testing.T) {
	raw, err := FS.ReadFile("196_business_system_prompt_remote_skill.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))

	for _, fragment := range []string{
		"moxinggang_reverse_skill",
		"codexrip_reverse_skill",
		"t.is_seed = true",
		"v.version = 1",
		"c2f0269baffa6a0eb1c9a9e15df815a6582ae6a615bc51d64b7cc5342b5efcb8",
		"v.byte_length = 7098",
		"v.composition_mode = 'offline_bundle'",
		"v.bundle_id = 'moxinggang-reverse-skill'",
		"22c227128165afbbcbda0175eb5e991ddb51d105b7d1e704572c625c64b626d7",
		"drop trigger if exists trg_protect_system_prompt_version_content",
		"composition_mode = 'remote_skill'",
		"bundle_id = 'codexrip-reverse-skill'",
		"bundle_manifest_sha256 = null",
		"revision = revision + 1",
		"enabled = false",
		"expose_server_prompt = false",
		"compact_enabled = false",
		"composition_mode in ('inline', 'offline_bundle', 'remote_skill')",
		"bundle_id is not null",
		"bundle_manifest_sha256 is not null",
		"raise exception",
		"create trigger trg_protect_system_prompt_version_content",
	} {
		require.Contains(t, sql, fragment)
	}
}

func TestBusinessSystemPromptRemoteSkillMigrationEmbedsCanonicalPromptDigest(t *testing.T) {
	raw, err := FS.ReadFile("196_business_system_prompt_remote_skill.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))
	require.Contains(t, sql, "decode(")
	require.Contains(t, sql, "convert_from")
	require.Contains(t, sql, "sha256(")
	require.Contains(t, sql, "octet_length(")
	require.Contains(t, sql, "cbf75cc85cd77860e53d06820e7120802d83c069e9d24b48715711acc15893c6")
	require.Contains(t, sql, "new_length <> 7045")
}
