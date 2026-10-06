package service

import (
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

var adminObservabilityConfig atomic.Pointer[AdminObservabilityConfig]

// defaultAdminObservabilityConfig is the deploy-time default: account traffic
// telemetry follows gateway.account_traffic_telemetry_disabled and the flat
// theme is on.
func defaultAdminObservabilityConfig(cfg *config.Config) AdminObservabilityConfig {
	return AdminObservabilityConfig{TelemetryEnabled: cfg == nil || !cfg.Gateway.AccountTrafficTelemetryDisabled, ThemeEnabled: true}
}

// ConfigureAdminObservability installs the effective telemetry and theme
// switches (startup load, admin update, tests). A nil value restores the
// built-in default.
func ConfigureAdminObservability(config *AdminObservabilityConfig) {
	adminObservabilityConfig.Store(config)
}

// EffectiveAdminObservabilityConfig returns the switches this process applies.
func EffectiveAdminObservabilityConfig() AdminObservabilityConfig {
	if config := adminObservabilityConfig.Load(); config != nil {
		return *config
	}
	return defaultAdminObservabilityConfig(nil)
}

// FlatThemeEnabled reports whether the flat site theme is switched on.
func FlatThemeEnabled() bool {
	return EffectiveAdminObservabilityConfig().ThemeEnabled
}
