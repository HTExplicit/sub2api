package migrations

import (
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTypeSafePlatformMigration(t *testing.T) {
	content, err := FS.ReadFile("241_add_typesafe_platform.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check")
	// Downstream: the quota list keeps `cindy` like the downstream 237 and 238,
	// so the file also applies to a database that has not reached 260 and still
	// holds `cindy` quota rows; 260 removes them and 265 states the final list.
	require.Contains(t, sql,
		"CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'minimax', 'cindy', 'opencode_go', 'typesafe'))")
	require.Contains(t, sql,
		"CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go', 'typesafe'))")
}

// The runner applies unrecorded files in filename order, so a database ends
// with the definition in the last file that adds a constraint: every file on a
// new database, the still unrecorded ones on a database that is already past
// the downstream 260. Upstream's 241 sorts before the downstream files that
// re-create the quota CHECK (241 compat, 251, 260), so the downstream tail
// states the union again; nothing downstream re-creates the route CHECK.
func TestTypeSafeStaysInTheFinalPlatformChecks(t *testing.T) {
	const tail = "265_user_platform_quota_typesafe_union.sql"
	const platforms = "('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go', 'typesafe')"

	names, err := fs.Glob(FS, "*.sql")
	require.NoError(t, err)
	sort.Strings(names)
	final := map[string]string{}
	statements := map[string]string{}
	for _, name := range names {
		content, err := FS.ReadFile(name)
		require.NoError(t, err)
		statements[name] = strings.Join(strings.Fields(string(content)), " ")
		for _, constraint := range []string{"user_platform_quotas_platform_check", "composite_model_routes_target_platform_check"} {
			if strings.Contains(statements[name], "ADD CONSTRAINT "+constraint+" ") {
				final[constraint] = name
			}
		}
	}

	require.Equal(t, tail, final["user_platform_quotas_platform_check"])
	require.Equal(t, "241_add_typesafe_platform.sql", final["composite_model_routes_target_platform_check"])
	require.Contains(t, statements["241_add_typesafe_platform.sql"],
		"ADD CONSTRAINT composite_model_routes_target_platform_check CHECK (target_platform IN "+platforms+")")

	sql := statements[tail]
	require.Contains(t, sql, "SET LOCAL lock_timeout = '10s'")
	require.Contains(t, sql, "SET LOCAL statement_timeout = '120s'")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check")
	require.Contains(t, sql, "ADD CONSTRAINT user_platform_quotas_platform_check CHECK (platform IN "+platforms+")")
	// Only the quota CHECK changes: no other table and no quota row.
	require.Equal(t, 2, strings.Count(sql, "ALTER TABLE "))
	require.Equal(t, 2, strings.Count(sql, "ALTER TABLE user_platform_quotas "))
	for _, statement := range []string{"UPDATE ", "DELETE ", "INSERT "} {
		require.NotContains(t, sql, statement)
	}
}
