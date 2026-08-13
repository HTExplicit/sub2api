package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration207RestoresOnlyCindyNativeOpenAIModes(t *testing.T) {
	raw, err := FS.ReadFile("207_codexrip_restore_cindy_openai_defaults.sql")
	require.NoError(t, err)

	sql := strings.ToLower(string(raw))
	require.Contains(t, sql, "update accounts")
	require.Contains(t, sql, "set extra = jsonb_set")
	require.Contains(t, sql, "'{openai_alpha_search_mode}'")
	require.Contains(t, sql, "to_jsonb('direct'::text)")
	require.Contains(t, sql, "'{openai_prompt_cache_key_mode}'")
	require.Contains(t, sql, "to_jsonb('passthrough'::text)")
	require.Contains(t, sql, "platform = 'openai'")
	require.Contains(t, sql, "type = 'apikey'")
	require.Contains(t, sql, "lower(btrim(credentials->>'base_url'))")
	require.Contains(t, sql, "'https://api.laxarouter.ai/'")
	require.Contains(t, sql, "is distinct from 'direct'")
	require.Contains(t, sql, "is distinct from 'passthrough'")
	require.NotContains(t, sql, "deleted_at is null")
	require.NotContains(t, sql, "set credentials")
	require.NotContains(t, sql, "updated_at =")
}
