//go:build unit

package migrations

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration231CreatesGenerationBoundSlimHealthState(t *testing.T) {
	matches, err := fs.Glob(FS, "231_*.sql")
	require.NoError(t, err)
	require.Equal(t, []string{"231_cindy_health_state.sql"}, matches)
	raw, err := FS.ReadFile(matches[0])
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))

	require.Contains(t, sql, "create table if not exists cindy_health_states")
	require.Contains(t, sql, "credential_generation bigint not null")
	require.Contains(t, sql, "episode_id varchar(64) not null")
	require.Contains(t, sql, "confirmed_exhausted")
	require.Contains(t, sql, "references account_credential_identities(id)")
	for _, forbidden := range []string{"invalid_credential", "token_not_found", "probe_lease", "receipt", "effect_ledger"} {
		require.NotContains(t, sql, forbidden)
	}
}

func TestMigration237AddsGenerationBoundBannedTerminalState(t *testing.T) {
	raw, err := FS.ReadFile("237_cindy_terminal_health_and_cleanup.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))

	require.Contains(t, sql, "cindy_banned_at")
	require.Contains(t, sql, "status in ('quarantined', 'confirmed_exhausted', 'banned')")
	require.Contains(t, sql, "credential_generation")
}
