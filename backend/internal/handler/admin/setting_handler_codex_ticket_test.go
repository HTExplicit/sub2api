package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSettingsCodexTicketConfigurationHasOnlyPluginWritePath(t *testing.T) {
	key := service.SettingKeyOpenAICodexTicketHarvestProxyURL
	oldProxy := "http://user:fixture-old-secret@old.example.com:8080"
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{key: oldProxy, service.SettingKeyOpenAICodexTicketEnabled: "true"})
	for _, body := range []map[string]any{
		{key: "ftp://user:fixture-new-secret@new.example.com:21"},
		{"openai_codex_ticket_enabled": false},
		{"openai_codex_ticket_clear_proxy": true},
	} {
		rec := doUpdateSettings(t, h, body, nil)
		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		require.NotContains(t, rec.Body.String(), "fixture-new-secret")
		require.Equal(t, oldProxy, repo.values[key])
		require.Equal(t, "true", repo.values[service.SettingKeyOpenAICodexTicketEnabled])
	}
	rec := doUpdateSettings(t, h, map[string]any{"site_name": "updated"}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, oldProxy, repo.values[key], "legacy seed must remain intact and read-only")
	require.NotContains(t, rec.Body.String(), "openai_codex_ticket")
	require.NotContains(t, rec.Body.String(), "fixture-old-secret")
	get := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(get)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	h.GetSettings(c)
	require.Equal(t, http.StatusOK, get.Code)
	require.Equal(t, "no-store", get.Header().Get("Cache-Control"))
	require.NotContains(t, get.Body.String(), "fixture-old-secret")
	require.NotContains(t, get.Body.String(), "openai_codex_ticket")
}
