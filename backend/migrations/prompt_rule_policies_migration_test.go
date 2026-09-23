package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestPromptRuleMigrationPreservesRuntimeIntent(t *testing.T) {
	raw, err := os.ReadFile("253_prompt_rule_policies.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, expected := range []string{"'legacy-default'", "'follow_active', true", "'native_control'", "'control_append'", "'models', '[]'::jsonb", "ON CONFLICT (id) DO NOTHING"} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("missing legacy migration contract %q", expected)
		}
	}
	for _, forbidden := range []string{"UPDATE accounts", "UPDATE system_prompt_runtime", "expose_server_prompt", "compact_enabled"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("migration changed existing intent: %s", forbidden)
		}
	}
}
