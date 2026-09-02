//go:build unit

package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration233CreatesOnlySlimImageStudioTables(t *testing.T) {
	// Migration identity is the complete filename. Official and downstream
	// migrations intentionally share numeric prefixes.
	raw, err := FS.ReadFile("233_image_studio_jobs.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))

	for _, table := range []string{
		"image_studio_jobs",
		"image_studio_items",
		"image_studio_artifacts",
	} {
		require.Contains(t, sql, "create table if not exists "+table)
	}
	require.Equal(t, 3, strings.Count(sql, "create table if not exists image_studio_"))
	require.Contains(t, sql, "api_key_id bigint not null references api_keys(id)")
	require.Contains(t, sql, "user_id bigint not null references users(id)")
	require.Contains(t, sql, "where status in ('pending', 'preparing', 'running')")
	require.Contains(t, sql, "count between 1 and 4")
	require.Contains(t, sql, "expires_at timestamptz not null")

	for _, forbidden := range []string{
		"worker_lease",
		"execution_reservation",
		"reconciliation",
		"receipt",
		"effect_ledger",
		"outbox",
		"advisory",
		"ambiguity",
	} {
		require.NotContains(t, sql, forbidden)
	}
}
