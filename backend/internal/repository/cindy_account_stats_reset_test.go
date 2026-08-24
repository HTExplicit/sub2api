//go:build unit

package repository

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCindyAccountStatsResetWatermarkAppearsInSingleAndBatchSQL(t *testing.T) {
	raw, err := os.ReadFile("usage_log_repo_stats.go")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))
	require.GreaterOrEqual(t, strings.Count(sql, "greatest($2, coalesce(a.cindy_account_stats_reset_at"), 3)
	require.Contains(t, sql, "group by ul.account_id")
}
