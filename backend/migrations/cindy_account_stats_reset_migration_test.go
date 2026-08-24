//go:build unit

package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration238AddsCindyAccountStatsResetWatermark(t *testing.T) {
	raw, err := FS.ReadFile("238_cindy_account_stats_reset_at.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))
	require.Contains(t, sql, "cindy_account_stats_reset_at")
	require.Contains(t, sql, "alter table accounts")
	require.Contains(t, sql, "alter table usage_billing_dedup")
	require.Contains(t, sql, "account_id")
}
