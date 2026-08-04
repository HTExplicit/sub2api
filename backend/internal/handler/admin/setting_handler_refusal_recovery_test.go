//go:build unit

package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUpdateSettingsPersistsNormalizedOpenAIRefusalRecoveryFields(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	rec := doUpdateSettings(t, h, map[string]any{
		"openai_refusal_recovery_enabled": true,
		"openai_cyber_failover_enabled":   true,
		"openai_refusal_rewrite_enabled":  true,
		"openai_refusal_keywords":         []string{" sorry ", "SORRY", "我不能"},
		"openai_refusal_replacement":      "继续当前任务",
	}, nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "true", repo.values[service.SettingKeyOpenAIRefusalRecoveryEnabled])
	require.Equal(t, "true", repo.values[service.SettingKeyOpenAICyberFailoverEnabled])
	require.Equal(t, "true", repo.values[service.SettingKeyOpenAIRefusalRewriteEnabled])
	require.Equal(t, "继续当前任务", repo.values[service.SettingKeyOpenAIRefusalReplacement])
	var keywords []string
	require.NoError(t, json.Unmarshal([]byte(repo.values[service.SettingKeyOpenAIRefusalKeywords]), &keywords))
	require.Equal(t, []string{"sorry", "我不能"}, keywords)
}

func TestUpdateSettingsRejectsOpenAIRefusalRewriteWithoutReplacement(t *testing.T) {
	h, _ := newStepUpSwitchTestHandler(t, map[string]string{})
	rec := doUpdateSettings(t, h, map[string]any{
		"openai_refusal_recovery_enabled": true,
		"openai_refusal_rewrite_enabled":  true,
		"openai_refusal_keywords":         []string{"cannot"},
		"openai_refusal_replacement":      "",
	}, nil)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUpdateSettingsRejectsOpenAICyberFailoverWhenSessionBlockEnabled(t *testing.T) {
	h, _ := newStepUpSwitchTestHandler(t, map[string]string{
		service.SettingKeyCyberSessionBlockEnabled: "true",
	})
	rec := doUpdateSettings(t, h, map[string]any{
		"openai_refusal_recovery_enabled": true,
		"openai_cyber_failover_enabled":   true,
	}, nil)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUpdateSettingsRejectsIndependentCyberFailoverSessionConflict(t *testing.T) {
	h, _ := newStepUpSwitchTestHandler(t, map[string]string{
		service.SettingKeyCyberSessionBlockEnabled: "true",
	})
	rec := doUpdateSettings(t, h, map[string]any{
		"openai_refusal_recovery_enabled": false,
		"openai_cyber_failover_enabled":   true,
		"openai_refusal_rewrite_enabled":  true,
		"openai_refusal_keywords":         []string{"cannot"},
		"openai_refusal_replacement":      "",
	}, nil)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDiffSettingsReportsOpenAIRefusalRecoveryFieldsWithoutContent(t *testing.T) {
	before := &service.SystemSettings{}
	after := &service.SystemSettings{
		OpenAIRefusalRecoveryEnabled: true,
		OpenAICyberFailoverEnabled:   true,
		OpenAIRefusalRewriteEnabled:  true,
		OpenAIRefusalKeywords:        []string{"cannot"},
		OpenAIRefusalReplacement:     "private replacement",
	}

	changed := diffSettings(before, after, nil, nil, UpdateSettingsRequest{})

	require.ElementsMatch(t, []string{
		"openai_refusal_recovery_enabled",
		"openai_cyber_failover_enabled",
		"openai_refusal_rewrite_enabled",
		"openai_refusal_keywords",
		"openai_refusal_replacement",
	}, changed)
	require.NotContains(t, changed, "private replacement")
}
