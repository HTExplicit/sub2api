package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestInternalRateConversionAdminSetting(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	get := func() map[string]any {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
		h.GetSettings(c)
		require.Equal(t, http.StatusOK, rec.Code)
		var response struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
		return response.Data
	}
	require.Equal(t, true, get()["internal_rate_conversion_enabled"])
	require.True(t, h.settingService.IsInternalRateConversionEnabled(context.Background()))
	for _, enabled := range []bool{false, true, false} {
		rec := doUpdateSettings(t, h, map[string]any{"internal_rate_conversion_enabled": enabled}, nil)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, enabled, get()["internal_rate_conversion_enabled"])
		require.Equal(t, enabled, h.settingService.IsInternalRateConversionEnabled(context.Background()))
	}
	rec := doUpdateSettings(t, h, map[string]any{"site_name": "Preserved"}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "false", repo.values[service.SettingKeyInternalRateConversionEnabled], "an old client omission retains the saved value")
	require.False(t, h.settingService.IsInternalRateConversionEnabled(context.Background()))

	changed := diffSettings(&service.SystemSettings{InternalRateConversionEnabled: true}, &service.SystemSettings{}, nil, nil, UpdateSettingsRequest{})
	require.Contains(t, changed, "internal_rate_conversion_enabled")
	publicJSON, err := json.Marshal(dto.PublicSettings{})
	require.NoError(t, err)
	require.NotContains(t, string(publicJSON), "internal_rate_conversion")
}
