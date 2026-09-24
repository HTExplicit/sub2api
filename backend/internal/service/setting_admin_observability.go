package service

import (
	"context"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

// SettingKeyAdminObservabilityConfig stores the account traffic telemetry and
// flat theme switches formerly kept in the admin-observability plugin
// configuration.
const SettingKeyAdminObservabilityConfig = "admin_observability_config"

// EffectiveAdminObservabilityConfig returns the switches this process applies.
func EffectiveAdminObservabilityConfig() extensionv1.AdminObservabilityConfig {
	return currentAdminObservabilityConfig()
}

// LoadAdminObservabilityConfig installs the effective switches for this process
// at startup: the stored value, or the deploy-time default when none is stored.
func (s *SettingService) LoadAdminObservabilityConfig(ctx context.Context) error {
	config := extensionv1.AdminObservabilityConfig{TelemetryEnabled: true, ThemeEnabled: true}
	found, err := s.readNativeSwitchSetting(ctx, SettingKeyAdminObservabilityConfig, &config, "telemetry_enabled", "theme_enabled")
	if err != nil {
		return err
	}
	if !found {
		if s == nil {
			config = LegacyAdminObservabilityConfig(nil)
		} else {
			config = LegacyAdminObservabilityConfig(s.cfg)
		}
	}
	ConfigureAdminObservability(&config)
	return nil
}

// UpdateAdminObservabilityConfig persists the switches and applies them to this
// process.
func (s *SettingService) UpdateAdminObservabilityConfig(ctx context.Context, config extensionv1.AdminObservabilityConfig) error {
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
