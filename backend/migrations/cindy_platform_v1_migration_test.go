package migrations

import (
	"io/fs"
	"os"
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

func TestMigration235RestrictsLedgerReplayToLifecycleExcludedRows(t *testing.T) {
	matches, err := fs.Glob(FS, "235_*.sql")
	require.NoError(t, err)
	require.Equal(t, []string{"235_preserve_mixed_openai_cindy_groups.sql"}, matches)

	raw, err := FS.ReadFile(matches[0])
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))

	beginAt := strings.Index(sql, "begin;")
	restoreAt := strings.Index(sql, "select * from project_cindy_platform_v1_to_legacy()")
	redefineAt := strings.Index(sql, "create or replace function project_cindy_platform_v1_from_legacy()")
	forwardAt := strings.LastIndex(sql, "select * from project_cindy_platform_v1_from_legacy()")
	commitAt := strings.LastIndex(sql, "commit;")
	require.GreaterOrEqual(t, beginAt, 0)
	require.Greater(t, restoreAt, beginAt)
	require.Greater(t, redefineAt, restoreAt)
	require.Greater(t, forwardAt, redefineAt)
	require.Greater(t, commitAt, forwardAt)
	functionEnd := redefineAt + strings.Index(sql[redefineAt:], "$$;")
	require.Greater(t, functionEnd, redefineAt)
	forwardFunction := sql[redefineAt:functionEnd]
	require.Contains(t, forwardFunction, "return query")
	require.Contains(t, forwardFunction, "from project_cindy_platform_v1_discover_legacy()")
	require.Contains(t, forwardFunction, "cindy_platform_v1_projection")
	require.Contains(t, forwardFunction, "a.deleted_at is not null")
	require.Contains(t, forwardFunction, "g.deleted_at is not null or not exists")
	require.NotContains(t, sql, "create temp table")
	require.NotContains(t, sql, "insert into account_groups")
	require.NotContains(t, sql, "update account_groups")
	require.NotContains(t, sql, "delete from account_groups")
	require.NotContains(t, sql, "delete from cindy_platform_v1_projection")

	for _, workflow := range []string{
		"../../.github/workflows/downstream-verify.yml",
		"../../.github/workflows/downstream-release.yml",
	} {
		raw, err := os.ReadFile(workflow)
		require.NoError(t, err)
		contents := string(raw)
		require.Contains(t, contents, "TestMigration235RestrictsLedgerReplayToLifecycleExcludedRows")
		require.Contains(t, contents, "TestMigration235PreservesMixedOpenAIGroupsAfterCanonicalReplay")
	}
}

func TestMigration236DefinesManagedCindyChannelAndDurableInvalidation(t *testing.T) {
	matches, err := fs.Glob(FS, "236_*.sql")
	require.NoError(t, err)
	require.Equal(t, []string{"236_bind_strict_cindy_groups_to_catalog_channel.sql"}, matches)

	raw, err := FS.ReadFile(matches[0])
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))

	for _, fragment := range []string{
		"cindy_catalog_managed",
		"cindy_laxa_v1",
		"insert into channel_groups",
		"insert into scheduler_outbox",
		"enqueue_group_api_key_auth_cache_invalidations",
		"raise exception",
	} {
		require.Contains(t, sql, fragment)
	}
	require.NotRegexp(t, `where\s+g\.id\s*=\s*\d+`, sql)
	require.NotContains(t, sql, "platform = 'openai' and cmp.platform = 'openai'")
}
