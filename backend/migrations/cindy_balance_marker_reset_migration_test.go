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
	require.Contains(t, sql, "e.provider_error_type = 'budget_exceeded'")
	require.Contains(t, sql, "e.provider_error_code = '429'")
	require.Contains(t, sql, "interval '2 minutes'")
	require.NotContains(t, sql, "set credentials")
	require.NotContains(t, sql, "set extra")
	require.NotContains(t, sql, "set status")
	require.NotContains(t, sql, "set schedulable")
	require.NotContains(t, sql, "deleted_at is null")
}
