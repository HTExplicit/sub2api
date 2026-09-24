package service

import (
	"context"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// SettingKeyImageToolsConfig stores the Image Studio and Responses image bridge
// switches formerly kept in the image-tools plugin configuration.
const SettingKeyImageToolsConfig = "image_tools_config"

// EffectiveImageToolsConfig returns the switches this process applies.
func EffectiveImageToolsConfig() extensionv1.ImageToolsConfig {
	config, _ := currentImageToolsConfig()
	return config
}

// LoadImageToolsConfig installs the stored switches for this process at
// startup. Without a stored value the deploy-time rollout flags stay in force.
func (s *SettingService) LoadImageToolsConfig(ctx context.Context) {
	var stored extensionv1.ImageToolsConfig
	if s.readJSONSetting(ctx, SettingKeyImageToolsConfig, &stored) {
		ConfigureImageTools(&stored)
	}
}

// UpdateImageToolsConfig persists the switches and applies them to this process,
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
