package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration212DurablyInvalidatesMaterializedCindyGroupIdentity(t *testing.T) {
	raw, err := FS.ReadFile("212_codexrip_materialize_cindy_group_identity.sql")
	require.NoError(t, err)

	sql := strings.ToLower(string(raw))
	require.Contains(t, sql, "insert into auth_cache_invalidation_outbox")
	require.Contains(t, sql, "after update of platform, type, credentials, status, deleted_at on accounts")
	require.Contains(t, sql, "before delete on accounts")
	require.Contains(t, sql, "after insert or update or delete on account_groups")
	require.Contains(t, sql, "old.account_id is not distinct from new.account_id")
	require.Contains(t, sql, "old.group_id is not distinct from new.group_id")
	require.Contains(t, sql, "select distinct ag.group_id")
	require.Contains(t, sql, "encode(sha256(convert_to(k.key, 'utf8')), 'hex')")
	require.NotContains(t, sql, "schedulable")
	require.NotContains(t, sql, "temp_unschedulable")
	require.NotContains(t, sql, "cindy_balance_insufficient_at")
}
