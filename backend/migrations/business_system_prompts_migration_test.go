package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBusinessSystemPromptsMigrationCreatesVersionedRuntimeSchema(t *testing.T) {
	raw, err := FS.ReadFile("194_business_system_prompts.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))
	for _, fragment := range []string{
		"create table if not exists system_prompt_templates",
		"create table if not exists system_prompt_template_versions",
		"create table if not exists system_prompt_runtime",
		"unique (template_id, version)",
		"enabled boolean not null default false",
		"expose_server_prompt boolean not null default false",
		"compact_enabled boolean not null default false",
		"check (id = 1)",
		"before delete on system_prompt_template_versions",
		"system prompt versions cannot be deleted",
	} {
		require.Contains(t, sql, fragment)
	}
}
