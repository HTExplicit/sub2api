package service

import (
	"context"
)

// SettingKeyCodexRuntimeConfig stores the Codex request compression switch.
// Without a stored value gateway.openai_codex_request_zstd applies.
const SettingKeyCodexRuntimeConfig = "codex_runtime_config"

// CodexRuntimeConfig is the stored value of SettingKeyCodexRuntimeConfig and
// the body of the Codex runtime settings endpoints.
type CodexRuntimeConfig struct {
	RequestZstd bool `json:"request_zstd"`
}

// EffectiveCodexRuntimeConfig returns the switch this process applies.
func EffectiveCodexRuntimeConfig() CodexRuntimeConfig {
	return CodexRuntimeConfig{RequestZstd: codexRequestZstd.Load()}
}

// LoadCodexRuntimeConfig installs the effective switch for this process at
// startup: the stored value, or gateway.openai_codex_request_zstd when none is
// stored.
func (s *SettingService) LoadCodexRuntimeConfig(ctx context.Context) error {
	config := CodexRuntimeConfig{RequestZstd: true}
	found, err := s.readSwitchSetting(ctx, SettingKeyCodexRuntimeConfig, &config, "request_zstd")
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
func (s *SettingService) UpdateCodexRuntimeConfig(ctx context.Context, config CodexRuntimeConfig) error {
	if err := s.writeJSONSetting(ctx, SettingKeyCodexRuntimeConfig, config); err != nil {
		return err
	}
	SetCodexRequestZstdEnabled(config.RequestZstd)
	return nil
}
