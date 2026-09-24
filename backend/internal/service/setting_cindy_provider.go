package service

import (
	"context"
	"sync"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

// SettingKeyCindyProviderConfig stores the Cindy balance detection, catalog and
// search switches formerly kept in the cindy-provider plugin configuration.
const SettingKeyCindyProviderConfig = "cindy_provider_config"

var cindyProviderSettingsMu sync.Mutex

// EffectiveCindyProviderConfig returns the switches this process applies.
func EffectiveCindyProviderConfig() extensionv1.CindyProviderConfig {
	config, _ := currentCindyProviderConfig()
	return config
}

// LoadCindyProviderConfig installs the stored switches for this process at
// startup. Without a stored value the deploy-time rollout flags stay in force.
func (s *SettingService) LoadCindyProviderConfig(ctx context.Context) error {
	config := extensionv1.CindyProviderConfig{BalanceDetection: true}
	found, err := s.readNativeSwitchSetting(ctx, SettingKeyCindyProviderConfig, &config, "balance_detection", "catalog_enabled", "search_enabled")
	if err != nil {
		return err
	}
	if found {
		ConfigureCindyProvider(&config)
	}
	return nil
}

// UpdateCindyProviderConfig persists the switches and applies them to this
// process. Existing account health records are kept.
func (s *SettingService) UpdateCindyProviderConfig(ctx context.Context, config extensionv1.CindyProviderConfig) error {
	cindyProviderSettingsMu.Lock()
	defer cindyProviderSettingsMu.Unlock()
	if err := s.writeJSONSetting(ctx, SettingKeyCindyProviderConfig, config); err != nil {
		return err
	}
	ConfigureCindyProvider(&config)
	return nil
}
