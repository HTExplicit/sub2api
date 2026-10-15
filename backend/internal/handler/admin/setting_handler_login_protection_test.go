package admin

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type loginProtectionAdminSettingsRepo struct {
	service.SettingRepository
	values map[string]string
}

func (r *loginProtectionAdminSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	value, ok := r.values[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return value, nil
}
func (r *loginProtectionAdminSettingsRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func TestLoginProtectionAdminSettingsBindingAndImmediateUpdate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := service.NewSettingService(&loginProtectionAdminSettingsRepo{values: map[string]string{}}, &config.Config{})
	h := &SettingHandler{settingService: settings}
	router := gin.New()
	router.GET("/settings", h.GetLoginProtection)
	router.PUT("/settings", h.UpdateLoginProtection)
	put := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PUT", "/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		router.ServeHTTP(result, req)
		return result
	}
	for _, body := range []string{
		`{"window_minutes":5,"max_failures":5,"ban_hours":24}`,
		`{"enabled":true,"window_minutes":0,"max_failures":5,"ban_hours":24}`,
		`{"enabled":true,"window_minutes":1441,"max_failures":5,"ban_hours":24}`,
		`{"enabled":true,"window_minutes":5,"max_failures":5.5,"ban_hours":24}`,
		`{"enabled":true,"window_minutes":5,"max_failures":1001,"ban_hours":24}`,
		`{"enabled":true,"window_minutes":5,"max_failures":5,"ban_hours":721}`,
	} {
		require.Equal(t, 400, put(body).Code)
	}
	require.Equal(t, 200, put(`{"enabled":false,"window_minutes":10,"max_failures":8,"ban_hours":12}`).Code)
	require.Equal(t, service.LoginProtectionSettings{Enabled: false, WindowMinutes: 10, MaxFailures: 8, BanHours: 12}, settings.GetLoginProtectionSettingsCached(context.Background()))
	result := httptest.NewRecorder()
	router.ServeHTTP(result, httptest.NewRequest("GET", "/settings", nil))
	require.Equal(t, 200, result.Code)
	require.Contains(t, result.Body.String(), `"enabled":false`)
}
