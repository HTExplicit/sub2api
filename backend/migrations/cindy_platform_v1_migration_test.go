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
		"set local lock_timeout = '5s'",
		"set local statement_timeout = '60s'",
		"lock table groups, accounts, account_groups",
		"cindy_catalog_managed",
		"cindy_laxa_v1",
		"fallback_group_id_on_invalid_request is null",
		"join accounts strict_member",
		"insert into channel_groups",
		"insert into scheduler_outbox",
		"enqueue_group_api_key_auth_cache_invalidations",
		"trg_groups_cindy_channel_topology",
		"trg_accounts_cindy_identity_auth_cache_invalidation",
		"after update of platform, wire_platform, provider_profile, type, credentials, status, deleted_at on accounts",
		"old.fallback_group_id_on_invalid_request is not distinct from new.fallback_group_id_on_invalid_request",
		"channel_account_stats_pricing_rules",
		"guard_managed_cindy_channel",
		"raise exception",
	} {
		require.Contains(t, sql, fragment)
	}
	require.NotRegexp(t, `where\s+g\.id\s*=\s*\d+`, sql)
	require.NotContains(t, sql, "platform = 'openai' and cmp.platform = 'openai'")
	require.NotContains(t, sql, "gpt-5.6")
	require.NotContains(t, sql, "insert into channel_model_pricing")
}

func TestMigration236PreservesCompleteGroupAuthInvalidationCoverage(t *testing.T) {
	raw, err := FS.ReadFile("236_bind_strict_cindy_groups_to_catalog_channel.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))

	for _, field := range []string{
		"status", "is_exclusive", "allow_image_generation", "platform",
		"subscription_type", "rate_multiplier", "peak_rate_enabled", "peak_start",
		"peak_end", "peak_rate_multiplier", "profit_control_enabled",
		"profit_min_margin", "profit_safety_buffer", "deleted_at",
		"wire_platform", "provider_profile", "fallback_group_id",
		"fallback_group_id_on_invalid_request",
	} {
		require.Contains(t, sql,
			"old."+field+" is not distinct from new."+field,
			"missing durable group auth invalidation field %s", field)
	}
	require.Contains(t, sql, "after update or delete on groups")
	require.NotContains(t, sql, "create trigger trg_groups_auth_cache_invalidation\nafter update of")
}

func TestMigration236TrimsReservedManagedChannelNames(t *testing.T) {
	raw, err := FS.ReadFile("236_bind_strict_cindy_groups_to_catalog_channel.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))

	require.Contains(t, sql, "lower(btrim(c.name)) = lower('cindy catalog')")
	require.Contains(t, sql, "old_reserved := lower(btrim(old.name)) = lower('cindy catalog')")
	require.Contains(t, sql, "new_reserved := lower(btrim(new.name)) = lower('cindy catalog')")
	require.NotContains(t, sql, "lower(c.name) = lower('cindy catalog')")
}
