package migrations

import (
	"strings"
	"testing"
)

func TestAdminAccountCapabilitiesMigrationPreservesUnmanagedGroupsAndAudit(t *testing.T) {
	raw, err := FS.ReadFile("240_admin_account_capabilities.sql")
	if err != nil {
		t.Fatal(err)
	}
	statement := string(raw)
	for _, expected := range []string{
		"managed_model_routes JSONB NOT NULL DEFAULT '{}'",
		"CREATE TABLE admin_capability_runs",
		"CREATE TABLE admin_capability_items",
		"CREATE TABLE admin_capability_changesets",
		"UNIQUE (created_by, idempotency_key)",
		"UNIQUE (run_id, account_id, upstream_model, protocol, profile)",
		"ON admin_capability_items(account_id) WHERE status = 'running'",
		"ON DELETE RESTRICT",
		"enqueue_group_api_key_auth_cache_invalidations(NEW.id)",
		"enqueue_channel_group_scheduler_invalidation(NEW.id)",
		"OLD.managed_model_routes IS DISTINCT FROM NEW.managed_model_routes",
	} {
		if !strings.Contains(statement, expected) {
			t.Fatalf("missing migration boundary %q", expected)
		}
	}
	for _, forbidden := range []string{"ON DELETE CASCADE", "DELETE FROM accounts", "UPDATE accounts", "UPDATE groups", "expires_at"} {
		if strings.Contains(statement, forbidden) {
			t.Fatalf("unexpected business mutation or audit TTL %q", forbidden)
		}
	}
}
