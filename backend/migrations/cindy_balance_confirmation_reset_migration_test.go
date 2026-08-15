package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration213ResetsOnlyStrictCindyBalanceMarkers(t *testing.T) {
	raw, err := FS.ReadFile("213_codexrip_reset_unconfirmed_cindy_balance_markers.sql")
	require.NoError(t, err)

	sql := strings.ToLower(string(raw))
	require.Contains(t, sql, "update accounts as a")
	require.Contains(t, sql, "set cindy_balance_insufficient_at = null")
	require.Contains(t, sql, "cindy_balance_insufficient_at is not null")
	require.Contains(t, sql, "platform = 'openai'")
	require.Contains(t, sql, "type = 'apikey'")
	require.Contains(t, sql, "jsonb_typeof(a.credentials->'base_url') = 'string'")
	require.Contains(t, sql, "lower(btrim(a.credentials->>'base_url'))")
	require.Contains(t, sql, "'https://api.laxarouter.ai'")
	require.Contains(t, sql, "'https://api.laxarouter.ai/'")
	require.NotContains(t, sql, "set credentials")
	require.NotContains(t, sql, "set extra")
	require.NotContains(t, sql, "set status")
	require.NotContains(t, sql, "set schedulable")
	require.NotContains(t, sql, "deleted_at")
	require.NotContains(t, sql, "ops_error_logs")
}
