package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// An older client may still send the retired Codex route-qualification
// settings. The save ignores them: it succeeds, the dormant rows stay as they
// are, and neither the reply nor GET exposes them.
func TestSettingsUpdateIgnoresRetiredCodexTicketFields(t *testing.T) {
	const proxyKey, enabledKey = "openai_codex_ticket_harvest_proxy_url", "openai_codex_ticket_enabled"
	oldProxy := "http://user:fixture-old-secret@old.example.com:8080"
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{proxyKey: oldProxy, enabledKey: "true"})
	rec := doUpdateSettings(t, h, map[string]any{
		proxyKey:                          "ftp://user:fixture-new-secret@new.example.com:21",
		enabledKey:                        false,
		"openai_codex_ticket_clear_proxy": true,
		"site_name":                       "updated",
	}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, oldProxy, repo.values[proxyKey], "the dormant row is not written")
	require.Equal(t, "true", repo.values[enabledKey])
	require.NotContains(t, rec.Body.String(), "openai_codex_ticket")
	require.NotContains(t, rec.Body.String(), "fixture-new-secret")
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
