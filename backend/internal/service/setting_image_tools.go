package service

import (
	"context"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

// SettingKeyImageToolsConfig stores the Image Studio switch formerly kept in
// the image-tools plugin configuration. A missing switch is off.
const SettingKeyImageToolsConfig = "image_tools_config"

// EffectiveImageToolsConfig returns the switch this process applies.
func EffectiveImageToolsConfig() extensionv1.ImageToolsConfig {
	config, _ := currentImageToolsConfig()
	return config
}

// LoadImageToolsConfig installs the stored switch for this process at startup.
// Without a stored value the deploy-time flag stays in force.
func (s *SettingService) LoadImageToolsConfig(ctx context.Context) error {
	var stored extensionv1.ImageToolsConfig
	found, err := s.readNativeSwitchSetting(ctx, SettingKeyImageToolsConfig, &stored, "studio_enabled")
	if err != nil {
		return err
	}
	if found {
		ConfigureImageTools(&stored)
	}
	return nil
}

// UpdateImageToolsConfig persists the switch and applies it to this process,
// starting the Image Studio runtime when it is switched on.
func (s *SettingService) UpdateImageToolsConfig(ctx context.Context, config extensionv1.ImageToolsConfig) error {
	if err := s.writeJSONSetting(ctx, SettingKeyImageToolsConfig, config); err != nil {
		return err
	}
	ConfigureImageTools(&config)
	// image_studio_enabled is a public setting embedded in the served HTML.
	if s.onUpdate != nil {
		s.onUpdate()
	}
	return nil
}
