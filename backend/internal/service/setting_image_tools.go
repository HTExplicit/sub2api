package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// SettingKeyImageToolsConfig stores the Image Studio and Responses image bridge
// switches formerly kept in the image-tools plugin configuration.
const SettingKeyImageToolsConfig = "image_tools_config"

func (s *SettingService) storedImageToolsConfig(ctx context.Context) (extensionv1.ImageToolsConfig, bool) {
	if s == nil || s.settingRepo == nil {
		return extensionv1.ImageToolsConfig{}, false
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), gatewayForwardingDBTimeout)
	defer cancel()
	raw, err := s.settingRepo.GetValue(dbCtx, SettingKeyImageToolsConfig)
	if err != nil || strings.TrimSpace(raw) == "" {
		return extensionv1.ImageToolsConfig{}, false
	}
	var stored extensionv1.ImageToolsConfig
	if json.Unmarshal([]byte(raw), &stored) != nil {
		return extensionv1.ImageToolsConfig{}, false
	}
	return stored, true
}

// GetImageToolsConfig returns the stored switches, or the deploy-time rollout
// flags when the administrator has not saved them yet.
func (s *SettingService) GetImageToolsConfig(ctx context.Context) extensionv1.ImageToolsConfig {
	if stored, ok := s.storedImageToolsConfig(ctx); ok {
		return stored
	}
	return LegacyImageToolsConfig()
}

// LoadImageToolsConfig installs the stored switches for this process at startup.
func (s *SettingService) LoadImageToolsConfig(ctx context.Context) {
	if stored, ok := s.storedImageToolsConfig(ctx); ok {
		ConfigureImageTools(&stored)
	}
}

// UpdateImageToolsConfig persists the switches and applies them to this process.
// Image Studio's background runtime still starts only at boot, as before.
func (s *SettingService) UpdateImageToolsConfig(ctx context.Context, config extensionv1.ImageToolsConfig) error {
	if s == nil || s.settingRepo == nil {
		return errors.New("settings are unavailable")
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), gatewayForwardingDBTimeout)
	defer cancel()
	if err := s.settingRepo.Set(dbCtx, SettingKeyImageToolsConfig, string(raw)); err != nil {
		return err
	}
	ConfigureImageTools(&config)
	return nil
}
