//go:build unit

package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration232CreatesOnlySlimAccountJobTables(t *testing.T) {
	// Migration identity is the complete filename. Official and downstream
	// migrations intentionally share numeric prefixes.
	raw, err := FS.ReadFile("232_admin_account_jobs.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))

	require.Equal(t, 2, strings.Count(sql, "create table if not exists"))
	require.Contains(t, sql, "create table if not exists admin_account_jobs")
	require.Contains(t, sql, "create table if not exists admin_account_job_items")
	require.Contains(t, sql, "admin_account_jobs_idempotency_uq")
	require.Contains(t, sql, "admin_account_jobs_admin_kind_running_uq")
	require.Contains(t, sql, "raw_payload_expires_at")
	require.Contains(t, sql, "retry_of_job_id")

	for _, forbidden := range []string{
		"lease_token", "lease_until", "heartbeat_at", "operation_receipt",
		"external_effect", "effect_state", "cleanup_outbox", "startup_remediation",
	} {
		require.NotContains(t, sql, forbidden)
	}
}
