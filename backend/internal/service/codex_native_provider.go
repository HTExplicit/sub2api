package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func (s *OpenAIGatewayService) NativeCodexConfiguration(ctx context.Context) (json.RawMessage, NativeCodexMetadata, error) {
	if s == nil || s.nativeCodexRuntime == nil || ctx == nil || ctx.Err() != nil {
		return nil, NativeCodexMetadata{}, ErrNativeCodexRuntimeUnavailable
	}
	runtime := s.nativeCodexRuntime
	// Keep the receipt in the same lifecycle epoch throughout the persistent read.
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	snapshot := runtime.snapshot.Load()
	if !runtime.started || runtime.repo == nil || snapshot == nil || snapshot.metadata == nil || len(snapshot.raw) == 0 ||
		snapshot.ctx == nil || snapshot.ctx.Err() != nil || ctx.Err() != nil {
		return nil, NativeCodexMetadata{}, ErrNativeCodexRuntimeUnavailable
	}
	metadata := *snapshot.metadata
	if metadata.ID <= 0 || metadata.ConfigVersion <= 0 || metadata.RuntimeGeneration <= 0 {
		return nil, NativeCodexMetadata{}, ErrNativeCodexRuntimeUnavailable
	}
	raw := append(json.RawMessage(nil), snapshot.raw...)
	sum := sha256.Sum256(raw)
	if metadata.ConfigSHA256 != hex.EncodeToString(sum[:]) {
		return nil, NativeCodexMetadata{}, ErrNativeCodexRuntimeUnavailable
	}
	current, err := runtime.repo.LoadNativeCodexMetadata(ctx)
	if err != nil || current == nil || *current != metadata || ctx.Err() != nil || snapshot.ctx.Err() != nil {
		return nil, NativeCodexMetadata{}, ErrNativeCodexRuntimeUnavailable
	}
	return raw, metadata, nil
}

func (s *OpenAIGatewayService) UpdateNativeCodexConfiguration(ctx context.Context, raw json.RawMessage, encryptor SecretEncryptor) error {
	if s == nil || s.nativeCodexRuntime == nil {
		return ErrNativeCodexRuntimeUnavailable
	}
	return s.nativeCodexRuntime.UpdateConfig(ctx, raw, encryptor)
}
