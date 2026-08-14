package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration210ResetsOnlyLegacyCindyBalanceMarkers(t *testing.T) {
	raw, err := FS.ReadFile("210_codexrip_reset_legacy_cindy_balance_markers.sql")
	require.NoError(t, err)

	sql := strings.ToLower(string(raw))
	require.Contains(t, sql, "update accounts")
	require.Contains(t, sql, "set cindy_balance_insufficient_at = null")
	require.Contains(t, sql, "cindy_balance_insufficient_at is not null")
	require.Contains(t, sql, "platform = 'openai'")
	require.Contains(t, sql, "type = 'apikey'")
	require.Contains(t, sql, "jsonb_typeof(a.credentials->'base_url') = 'string'")
	require.Contains(t, sql, "lower(btrim(a.credentials->>'base_url'))")
	require.Contains(t, sql, "'https://api.laxarouter.ai'")
	require.Contains(t, sql, "'https://api.laxarouter.ai/'")
	require.Contains(t, sql, "from ops_error_logs as e")
	require.Contains(t, sql, "coalesce(e.upstream_status_code, e.status_code) = 429")
	require.Contains(t, sql, "e.upstream_error_detail is json")
	require.Contains(t, sql, "jsonb_typeof(parsed.payload #> '{error,type}') = 'string'")
	require.Contains(t, sql, "parsed.payload #>> '{error,type}' = 'budget_exceeded'")
	require.Contains(t, sql, "jsonb_typeof(parsed.payload #> '{error,code}') = 'string'")
	require.Contains(t, sql, "parsed.payload #>> '{error,code}' = '429'")
	require.Contains(t, sql, "interval '2 minutes'")
	require.NotContains(t, sql, "provider_error_type")
	require.NotContains(t, sql, "provider_error_code")
	require.NotContains(t, sql, "exceededbudget")
	require.NotContains(t, sql, "set credentials")
	require.NotContains(t, sql, "set extra")
	require.NotContains(t, sql, "set status")
	require.NotContains(t, sql, "set schedulable")
	require.NotContains(t, sql, "deleted_at is null")
}
