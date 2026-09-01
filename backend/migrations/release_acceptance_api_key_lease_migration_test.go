package migrations

import (
	"strings"
	"testing"
)

func TestReleaseAcceptanceAPIKeyLeaseMigrationIsPurposeScoped(t *testing.T) {
	raw, err := FS.ReadFile("239_codexrip_release_acceptance_api_key_leases.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(raw)
	for _, required := range []string{
		"purpose VARCHAR(32) NOT NULL DEFAULT 'user'",
		"lease_id VARCHAR(64)",
		"CHECK (purpose IN ('user', 'release_acceptance'))",
		"WHERE lease_id IS NOT NULL",
		"WHERE purpose = 'release_acceptance' AND deleted_at IS NULL",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration missing %q", required)
		}
	}
}
