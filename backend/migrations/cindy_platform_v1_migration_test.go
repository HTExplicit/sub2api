package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration229ProjectsOnlyExactLegacyCindyAndHasReversibleFunctions(t *testing.T) {
	raw, err := FS.ReadFile("229_cindy_platform_wire_identity.sql")
	require.NoError(t, err)
	sql := string(raw)
	lower := strings.ToLower(sql)

	for _, fragment := range []string{
		"ADD COLUMN IF NOT EXISTS wire_platform",
		"ADD COLUMN IF NOT EXISTS provider_profile",
		"CREATE TABLE IF NOT EXISTS cindy_platform_v1_projection",
		"jsonb_typeof(a.credentials->'base_url') = 'string'",
		"'https://api.laxarouter.ai', 'https://api.laxarouter.ai/'",
		"SET platform = 'cindy'",
		"wire_platform = 'openai'",
		"provider_profile = 'cindy_laxa_v1'",
		"project_cindy_platform_v1_from_legacy",
		"project_cindy_platform_v1_to_legacy",
		"fallback_group_id IS NOT NULL",
	} {
		require.Contains(t, sql, fragment)
	}

	require.NotContains(t, lower, "like '%laxarouter.ai%'")
	require.NotContains(t, lower, "position('laxarouter.ai'")
	require.NotContains(t, lower, "create trigger")
	require.NotContains(t, lower, "constraint trigger")
	require.NotContains(t, lower, "auth_cache")
	require.NotContains(t, lower, "remediation")
	require.NotContains(t, lower, "startup")
}

func TestMigration229UsesReservedPostUpstreamNumber(t *testing.T) {
	entries, err := FS.ReadDir(".")
	require.NoError(t, err)
	found := false
	for _, entry := range entries {
		if entry.Name() == "229_cindy_platform_wire_identity.sql" {
			found = true
		}
	}
	require.True(t, found)
}
