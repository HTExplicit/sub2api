package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration211RestoresOnlyExactUnresolvedCindyBudgetMarkers(t *testing.T) {
	raw, err := FS.ReadFile("211_codexrip_restore_exact_cindy_budget_markers.sql")
	require.NoError(t, err)

	sql := strings.ToLower(string(raw))
	require.Contains(t, sql, "update accounts")
	require.Contains(t, sql, "set cindy_balance_insufficient_at = u.latest_exact_at")
	require.Contains(t, sql, "cindy_balance_insufficient_at is null")
	require.Contains(t, sql, "platform = 'openai'")
	require.Contains(t, sql, "type = 'apikey'")
	require.Contains(t, sql, "jsonb_typeof(a.credentials->'base_url') = 'string'")
	require.Contains(t, sql, "e.upstream_error_detail is json")
	require.Contains(t, sql, "coalesce(e.upstream_status_code, e.status_code) = 429")
	require.Contains(t, sql, "jsonb_typeof(payload #> '{error,type}') = 'string'")
	require.Contains(t, sql, "payload #>> '{error,type}' = 'budget_exceeded'")
	require.Contains(t, sql, "jsonb_typeof(payload #> '{error,code}') = 'string'")
	require.Contains(t, sql, "payload #>> '{error,code}' = '429'")
	require.Contains(t, sql, "u.created_at > x.latest_exact_at")
	require.NotContains(t, sql, "provider_error_type")
	require.NotContains(t, sql, "provider_error_code")
	require.NotContains(t, sql, "exceededbudget")
	require.NotContains(t, sql, "set credentials")
	require.NotContains(t, sql, "set extra")
	require.NotContains(t, sql, "set status")
	require.NotContains(t, sql, "set schedulable")
}
