package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestLoginProtectionSettingsDefaultsValidationAndCache(t *testing.T) {
	repo := &panelRateLimitSettingRepo{}
	svc := NewSettingService(repo, &config.Config{})
	ctx := context.Background()
	settings, err := svc.GetLoginProtectionSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, DefaultLoginProtectionSettings(), settings)
	settings.Enabled = false
	require.NoError(t, svc.SetLoginProtectionSettings(ctx, settings))
	require.False(t, svc.GetLoginProtectionSettingsCached(ctx).Enabled)
	for _, bad := range []LoginProtectionSettings{
		{WindowMinutes: 0, MaxFailures: 5, BanHours: 24}, {WindowMinutes: 1441, MaxFailures: 5, BanHours: 24},
		{WindowMinutes: 5, MaxFailures: 0, BanHours: 24}, {WindowMinutes: 5, MaxFailures: 1001, BanHours: 24},
		{WindowMinutes: 5, MaxFailures: 5, BanHours: 0}, {WindowMinutes: 5, MaxFailures: 5, BanHours: 721},
	} {
		require.Error(t, svc.SetLoginProtectionSettings(ctx, bad))
	}
	repo.getValueErr = errors.New("offline")
	svc.loginProtectionCache.Store(&cachedLoginProtectionSettings{settings, time.Now().Add(-time.Second)})
	require.Equal(t, settings, svc.GetLoginProtectionSettingsCached(ctx))
	for _, value := range []string{"2001:db8::1", "203.0.113.8", "::ffff:203.0.113.8"} {
		ip, err := NormalizeLoginProtectionIP(value)
		require.NoError(t, err)
		require.NotEmpty(t, ip)
	}
	ip, err := NormalizeLoginProtectionIP("::ffff:203.0.113.8")
	require.NoError(t, err)
	require.Equal(t, "203.0.113.8", ip)
	for _, value := range []string{"", "unknown", "203.0.113.8,203.0.113.9", "::", "fe80::1%eth0"} {
		_, err := NormalizeLoginProtectionIP(value)
		require.Error(t, err)
	}
}
