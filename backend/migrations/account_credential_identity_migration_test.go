package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration230CreatesCredentialIdentityGenerationWithoutStartupMachinery(t *testing.T) {
	raw, err := FS.ReadFile("230_account_credential_identities.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))

	for _, fragment := range []string{
		"create table if not exists account_credential_identities",
		"fingerprint char(64) not null",
		"generation bigint not null default 1",
		"account_credential_identities_fingerprint_idx",
		"account_credential_identities_active_account_uq",
		"account_credential_identities_account_generation_idx",
		"sub2api/account-credential-identity/v1",
		"int4send(octet_length",
		"from accounts a",
		"a.platform = 'cindy'",
		"on conflict (account_id) where active do nothing",
	} {
		require.Contains(t, sql, fragment)
	}
	require.NotContains(t, sql, "unique index if not exists account_credential_identities_fingerprint")

	for _, forbidden := range []string{
		"api_key text",
		"credentials jsonb",
		"backfill_state",
		"remediation",
		"startup",
		"trigger",
		"effect_journal",
	} {
		require.NotContains(t, sql, forbidden)
	}
}
