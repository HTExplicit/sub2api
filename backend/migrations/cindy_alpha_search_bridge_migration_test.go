package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration209RestoresOnlyCindyAlphaSearchBridge(t *testing.T) {
	raw, err := FS.ReadFile("209_codexrip_restore_cindy_alpha_search_bridge.sql")
	require.NoError(t, err)

	sql := strings.ToLower(string(raw))
	require.Contains(t, sql, "update accounts")
	require.Contains(t, sql, "set extra = jsonb_set")
	require.Contains(t, sql, "'{openai_alpha_search_mode}'")
	require.Contains(t, sql, "to_jsonb('responses_web_search'::text)")
	require.Contains(t, sql, "platform = 'openai'")
	require.Contains(t, sql, "type = 'apikey'")
	require.Contains(t, sql, "lower(btrim(credentials->>'base_url'))")
	require.Contains(t, sql, "is distinct from 'responses_web_search'")
	require.NotContains(t, sql, "'{openai_prompt_cache_key_mode}'")
	require.NotContains(t, sql, "'{openai_responses_mode}'")
	require.NotContains(t, sql, "deleted_at is null")
	require.NotContains(t, sql, "set credentials")
	require.NotContains(t, sql, "updated_at =")
}
