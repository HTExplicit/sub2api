package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration208UsesCindyPromptCacheHashOnly(t *testing.T) {
	raw, err := FS.ReadFile("208_codexrip_use_cindy_prompt_cache_hash.sql")
	require.NoError(t, err)

	sql := strings.ToLower(string(raw))
	require.Contains(t, sql, "update accounts")
	require.Contains(t, sql, "set extra = jsonb_set")
	require.Contains(t, sql, "'{openai_prompt_cache_key_mode}'")
	require.Contains(t, sql, "to_jsonb('sha256_64'::text)")
	require.Contains(t, sql, "platform = 'openai'")
	require.Contains(t, sql, "type = 'apikey'")
	require.Contains(t, sql, "lower(btrim(credentials->>'base_url'))")
	require.Contains(t, sql, "is distinct from 'sha256_64'")
	require.NotContains(t, sql, "'{openai_alpha_search_mode}'")
	require.NotContains(t, sql, "'{openai_responses_mode}'")
	require.NotContains(t, sql, "deleted_at is null")
	require.NotContains(t, sql, "set credentials")
	require.NotContains(t, sql, "updated_at =")
}
