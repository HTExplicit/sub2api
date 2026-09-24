package service

import (
	"context"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// SettingKeyImageToolsConfig stores the Image Studio and Responses image bridge
// switches formerly kept in the image-tools plugin configuration.
const SettingKeyImageToolsConfig = "image_tools_config"

// GetImageToolsConfig returns the stored switches, or the deploy-time rollout
// flags when the administrator has not saved them yet.
func (s *SettingService) GetImageToolsConfig(ctx context.Context) extensionv1.ImageToolsConfig {
	var stored extensionv1.ImageToolsConfig
	if s.readJSONSetting(ctx, SettingKeyImageToolsConfig, &stored) {
		return stored
	}
	return LegacyImageToolsConfig()
}

// LoadImageToolsConfig installs the stored switches for this process at startup.
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
	return nil
}
