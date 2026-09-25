//go:build unit

package admin

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUpdateSettingsRejectsLegacyPromptEdits(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		service.SettingKeyClaudeOAuthSystemPrompt:       "old prompt",
		service.SettingKeyClaudeOAuthSystemPromptBlocks: `[{"type":"text","text":"old block"}]`,
	})
	for _, key := range []string{"claude_oauth_system_prompt", "claude_oauth_system_prompt_blocks"} {
		t.Run(key, func(t *testing.T) {
			result := doUpdateSettings(t, h, map[string]any{key: "changed", "site_name": "must not be saved"}, nil)
			require.Equal(t, http.StatusConflict, result.Code)
			require.Contains(t, result.Body.String(), "prompt_configuration_moved")
			require.NotEqual(t, "must not be saved", repo.values[service.SettingKeySiteName])
		})
	}
	result := doUpdateSettings(t, h, map[string]any{
		"claude_oauth_system_prompt":                  "old prompt",
		"claude_oauth_system_prompt_blocks":           `[{"type":"text","text":"old block"}]`,
		"enable_claude_oauth_system_prompt_injection": false,
	}, nil)
	require.Equal(t, http.StatusOK, result.Code)
	require.Equal(t, "old prompt", repo.values[service.SettingKeyClaudeOAuthSystemPrompt])
	require.Equal(t, "false", repo.values[service.SettingKeyEnableClaudeOAuthSystemPromptInjection])
}
