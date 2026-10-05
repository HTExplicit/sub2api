package service

import (
	"context"
)

// SettingKeyAdminObservabilityConfig stores the account traffic telemetry and
// flat theme switches.
const SettingKeyAdminObservabilityConfig = "admin_observability_config"

// AdminObservabilityConfig is the stored value of
// SettingKeyAdminObservabilityConfig and the body of the observability settings
// endpoints.
type AdminObservabilityConfig struct {
	TelemetryEnabled bool `json:"telemetry_enabled"`
	ThemeEnabled     bool `json:"theme_enabled"`
}

// LoadAdminObservabilityConfig installs the effective switches for this process
// at startup: the stored value, or the deploy-time default when none is stored.
func (s *SettingService) LoadAdminObservabilityConfig(ctx context.Context) error {
	config := AdminObservabilityConfig{TelemetryEnabled: true, ThemeEnabled: true}
	found, err := s.readSwitchSetting(ctx, SettingKeyAdminObservabilityConfig, &config, "telemetry_enabled", "theme_enabled")
	if err != nil {
		return err
	}
	if !found {
		config = defaultAdminObservabilityConfig(s.cfg)
	}
	ConfigureAdminObservability(&config)
	return nil
}

// UpdateAdminObservabilityConfig persists the switches and applies them to this
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
