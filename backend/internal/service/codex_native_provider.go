package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func ProvideNativeCodexRuntime(gateway *OpenAIGatewayService, repo NativeCodexRepository, settings SettingRepository, encryptor SecretEncryptor, cfg *config.Config, bootstrap *NativeFeatureBootstrap, activity *QuotaActivityService) *NativeCodexRuntime {
	factory := func(ctx context.Context) (json.RawMessage, error) {
		_, sourceErr := settings.GetValue(ctx, "codex_native_runtime_source")
		if sourceErr != nil && !errors.Is(sourceErr, ErrSettingNotFound) {
			return nil, sourceErr
		}
		cipher, present, err := repo.ReadNativeCodexStoredConfig(ctx)
		if err != nil {
			return nil, err
		}
		var raw json.RawMessage
		if present {
			plain, decryptErr := encryptor.Decrypt(cipher)
			if decryptErr != nil {
				return nil, errors.New("cannot decrypt saved Codex configuration")
			}
			raw = json.RawMessage(plain)
		} else {
			configuration := cfg.Gateway.OpenAICodexTicket
			models := configuration.Models
			if len(models) == 0 {
				models = []string{"gpt-6-astra", "gpt-5.6-sol"}
			}
			raw, err = json.Marshal(map[string]any{
				"enabled": configuration.Enabled, "fail_closed": configuration.FailClosed,
				"proxy_url": configuration.HarvestProxyURL, "models": models,
				"request_zstd": cfg.Gateway.OpenAICodexRequestZstd,
			})
			if err != nil {
				return nil, err
			}
		}
		if errors.Is(sourceErr, ErrSettingNotFound) && bootstrap != nil && bootstrap.Snapshot != nil {
			if previous, ok := bootstrap.Snapshot.Plugins[NativeCodexPluginKey]; ok && !previous.NativeCreated && previous.State != "enabled" {
				var fields map[string]json.RawMessage
				if json.Unmarshal(raw, &fields) != nil || fields == nil {
					return nil, errors.New("invalid saved Codex configuration")
				}
				fields["enabled"] = json.RawMessage(`false`)
				raw, err = json.Marshal(fields)
				if err != nil {
					return nil, err
				}
			}
		}
		return NormalizeNativeCodexConfig(ctx, raw)
	}
	runtime := NewNativeCodexRuntime(gateway, repo, factory)
	runtime.SetQuotaActivity(activity)
	return runtime
}

func (s *OpenAIGatewayService) NativeCodexConfiguration() (json.RawMessage, error) {
	if s == nil || s.nativeCodexRuntime == nil {
		return nil, ErrNativeCodexRuntimeUnavailable
	}
	snapshot := s.nativeCodexRuntime.snapshot.Load()
	if snapshot == nil {
		return nil, ErrNativeCodexRuntimeUnavailable
	}
	return append(json.RawMessage(nil), snapshot.raw...), nil
}

func (s *OpenAIGatewayService) UpdateNativeCodexConfiguration(ctx context.Context, raw json.RawMessage, encryptor SecretEncryptor) error {
	if s == nil || s.nativeCodexRuntime == nil {
		return ErrNativeCodexRuntimeUnavailable
	}
	return s.nativeCodexRuntime.UpdateConfig(ctx, raw, encryptor)
}
