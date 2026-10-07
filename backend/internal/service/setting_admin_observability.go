package service

import (
	"context"
	"sync/atomic"
)

// SettingKeyAdminObservabilityConfig stores the flat theme switch.
const SettingKeyAdminObservabilityConfig = "admin_observability_config"

// AdminObservabilityConfig is the stored value of
// SettingKeyAdminObservabilityConfig and the body of the observability settings
// endpoints.
type AdminObservabilityConfig struct {
	ThemeEnabled bool `json:"theme_enabled"`
}

var adminObservabilityConfig atomic.Pointer[AdminObservabilityConfig]

// ConfigureAdminObservability installs the effective theme switch (startup
// load, admin update, tests). A nil value restores the built-in default.
func ConfigureAdminObservability(config *AdminObservabilityConfig) {
	adminObservabilityConfig.Store(config)
}

// EffectiveAdminObservabilityConfig returns the switch this process applies.
// The built-in default is the flat theme on.
func EffectiveAdminObservabilityConfig() AdminObservabilityConfig {
	if config := adminObservabilityConfig.Load(); config != nil {
		return *config
	}
	return AdminObservabilityConfig{ThemeEnabled: true}
}

// FlatThemeEnabled reports whether the flat site theme is switched on.
func FlatThemeEnabled() bool {
	return EffectiveAdminObservabilityConfig().ThemeEnabled
}

// LoadAdminObservabilityConfig installs the effective switch for this process
// at startup: the stored value, or the flat theme on when none is stored.
func (s *SettingService) LoadAdminObservabilityConfig(ctx context.Context) error {
	config := AdminObservabilityConfig{ThemeEnabled: true}
	if _, err := s.readSwitchSetting(ctx, SettingKeyAdminObservabilityConfig, &config, "theme_enabled"); err != nil {
		return err
	}
	ConfigureAdminObservability(&config)
	return nil
}

// UpdateAdminObservabilityConfig persists the switch and applies it to this
// process.
func (s *SettingService) UpdateAdminObservabilityConfig(ctx context.Context, config AdminObservabilityConfig) error {
	if err := s.writeJSONSetting(ctx, SettingKeyAdminObservabilityConfig, config); err != nil {
		return err
	}
	ConfigureAdminObservability(&config)
	// flat_theme_enabled is a public setting embedded in the served HTML.
	if s.onUpdate != nil {
		s.onUpdate()
	}
	return nil
}
