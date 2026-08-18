//go:build unit

package migrations

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const cindyBalanceProbeJobsMigration = "224_cindy_balance_probe_jobs.sql"

func readCindyBalanceProbeJobsMigration(t *testing.T) string {
	t.Helper()

	raw, err := FS.ReadFile(cindyBalanceProbeJobsMigration)
	require.NoError(t, err)
	return strings.ToLower(string(raw))
}

func TestMigration224UsesExactFilenameAndCreatesProbeTables(t *testing.T) {
	matches, err := fs.Glob(FS, "224_*.sql")
	require.NoError(t, err)
	require.Contains(t, matches, cindyBalanceProbeJobsMigration)

	sql := readCindyBalanceProbeJobsMigration(t)
	require.Contains(t, sql, "create table if not exists cindy_balance_probe_jobs")
	require.Contains(t, sql, "create table if not exists cindy_balance_probe_items")
	require.Contains(t, sql, "job_id bigint not null references cindy_balance_probe_jobs(id) on delete cascade")
	require.Contains(t, sql, "unique (job_id, account_id)")
	require.Contains(t, sql, "unique (job_id, ordinal)")
}

func TestMigration224AllowsOnlyOneActiveProbeJob(t *testing.T) {
	sql := readCindyBalanceProbeJobsMigration(t)
	require.Contains(t, sql, "create unique index if not exists idx_cindy_balance_probe_jobs_one_active")
	require.Contains(t, sql, "on cindy_balance_probe_jobs ((1))")
	require.Contains(t, sql, "where status in ('queued', 'running', 'paused', 'paused_upstream', 'cancel_requested')")
}

func TestMigration224ConstrainsRateAndRequestCounts(t *testing.T) {
	sql := readCindyBalanceProbeJobsMigration(t)
	require.Contains(t, sql, "rate_rps numeric(2,1) not null default 0.5 check (rate_rps >= 0.1 and rate_rps <= 1.0)")
	require.Contains(t, sql, "request_count integer not null default 0 check (request_count >= 0)")
	require.Contains(t, sql, "request_count smallint not null default 0 check (request_count >= 0 and request_count <= 2)")
}

func TestMigration224DoesNotPersistProbeSecretsOrRawPayloads(t *testing.T) {
	sql := readCindyBalanceProbeJobsMigration(t)

	for _, forbidden := range []string{
		"api_key",
		"access_token",
		"refresh_token",
		"credentials",
		"request_body",
		"response_body",
		"raw_request",
		"raw_response",
		"raw_error",
		"error_body",
	} {
		require.NotContains(t, sql, forbidden)
	}

	require.Contains(t, sql, "identity_fingerprint text not null")
	require.Contains(t, sql, "luna_outcome text null")
	require.Contains(t, sql, "terra_outcome text null")
	require.Contains(t, sql, "final_outcome text null")
}
