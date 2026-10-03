package service

import (
	"context"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

// SettingKeyCodexRuntimeConfig stores the Codex request compression switch.
// Without a stored value gateway.openai_codex_request_zstd applies.
const SettingKeyCodexRuntimeConfig = "codex_runtime_config"

// EffectiveCodexRuntimeConfig returns the switch this process applies.
func EffectiveCodexRuntimeConfig() extensionv1.CodexRuntimeConfig {
	return extensionv1.CodexRuntimeConfig{RequestZstd: codexRequestZstd.Load()}
}

// LoadCodexRuntimeConfig installs the effective switch for this process at
// startup: the stored value, or gateway.openai_codex_request_zstd when none is
// stored.
func (s *SettingService) LoadCodexRuntimeConfig(ctx context.Context) error {
	config := extensionv1.CodexRuntimeConfig{RequestZstd: true}
	found, err := s.readNativeSwitchSetting(ctx, SettingKeyCodexRuntimeConfig, &config, "request_zstd")
	if err != nil {
		return err
	}
	if !found {
		config.RequestZstd = s.cfg != nil && s.cfg.Gateway.OpenAICodexRequestZstd
	}
	SetCodexRequestZstdEnabled(config.RequestZstd)
	return nil
}

// UpdateCodexRuntimeConfig persists the switch and applies it to this process.
func (s *SettingService) UpdateCodexRuntimeConfig(ctx context.Context, config extensionv1.CodexRuntimeConfig) error {
	if err := s.writeJSONSetting(ctx, SettingKeyCodexRuntimeConfig, config); err != nil {
		return err
	}
	SetCodexRequestZstdEnabled(config.RequestZstd)
	return nil
}
