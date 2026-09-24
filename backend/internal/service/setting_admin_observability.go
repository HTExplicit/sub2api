package service

import (
	"context"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// SettingKeyAdminObservabilityConfig stores the account traffic telemetry and
// flat theme switches formerly kept in the admin-observability plugin
// configuration.
const SettingKeyAdminObservabilityConfig = "admin_observability_config"

// GetAdminObservabilityConfig returns the stored switches, or the deploy-time
// default when the administrator has not saved them yet.
func (s *SettingService) GetAdminObservabilityConfig(ctx context.Context) extensionv1.AdminObservabilityConfig {
	stored := extensionv1.AdminObservabilityConfig{TelemetryEnabled: true, ThemeEnabled: true}
	if s.readJSONSetting(ctx, SettingKeyAdminObservabilityConfig, &stored) {
		return stored
	}
	if s == nil {
		return LegacyAdminObservabilityConfig(nil)
	}
	return LegacyAdminObservabilityConfig(s.cfg)
}

// LoadAdminObservabilityConfig installs the effective switches for this process
// at startup, including the deploy-time default.
func (s *SettingService) LoadAdminObservabilityConfig(ctx context.Context) {
	config := s.GetAdminObservabilityConfig(ctx)
	ConfigureAdminObservability(&config)
}

// UpdateAdminObservabilityConfig persists the switches and applies them to this
// process.
func (s *SettingService) UpdateAdminObservabilityConfig(ctx context.Context, config extensionv1.AdminObservabilityConfig) error {
	if err := s.writeJSONSetting(ctx, SettingKeyAdminObservabilityConfig, config); err != nil {
		return err
	}
	ConfigureAdminObservability(&config)
	return nil
}
