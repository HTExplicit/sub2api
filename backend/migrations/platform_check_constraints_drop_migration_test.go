package migrations

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDropPlatformCheckConstraintsMigration(t *testing.T) {
	content, err := FS.ReadFile("242_drop_platform_check_constraints.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql,
		"ALTER TABLE user_platform_quotas DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;")
	require.Contains(t, sql,
		"ALTER TABLE composite_model_routes DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;")
	require.NotContains(t, sql, "ADD CONSTRAINT")
	require.NotContains(t, sql, "DROP CONSTRAINT IF EXISTS channel_monitors_provider_check")
	require.NotContains(t, sql, "DROP CONSTRAINT IF EXISTS channel_monitor_request_templates_provider_check")
}

// Immutable downstream migrations 251, 260 and 265 predate this integration.
// Check the final action across the complete migration sequence, including 277,
// so fresh installs cannot finish with a restored database whitelist.
func TestLaterMigrationsDoNotRestorePlatformCheckConstraints(t *testing.T) {
	entries, err := fs.ReadDir(FS, ".")
	require.NoError(t, err)
	comments := regexp.MustCompile(`(?ms)/\*.*?\*/|--[^\n]*`)
	constraintAction := regexp.MustCompile(`(?i)\b(ADD|DROP)\s+CONSTRAINT(?:\s+IF\s+EXISTS)?\s+"?(user_platform_quotas_platform_check|composite_model_routes_target_platform_check)\b`)
	lastAction := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		content, err := FS.ReadFile(entry.Name())
		require.NoError(t, err)
		for _, action := range constraintAction.FindAllSubmatch(comments.ReplaceAll(content, nil), -1) {
			lastAction[strings.ToLower(string(action[2]))] = strings.ToUpper(string(action[1]))
		}
	}
	require.Equal(t, "DROP", lastAction["user_platform_quotas_platform_check"])
	require.Equal(t, "DROP", lastAction["composite_model_routes_target_platform_check"])
}
