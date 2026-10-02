package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// The setting repository, bootstrap snapshot and quota activity only served
// the retired route-qualification feature; the parameters keep the wire graph
// unchanged.
func ProvideNativeCodexRuntime(gateway *OpenAIGatewayService, repo NativeCodexRepository, _ SettingRepository, encryptor SecretEncryptor, cfg *config.Config, _ *NativeFeatureBootstrap, _ *QuotaActivityService) *NativeCodexRuntime {
	factory := func(ctx context.Context) (json.RawMessage, error) {
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
			raw, err = json.Marshal(map[string]any{"request_zstd": cfg.Gateway.OpenAICodexRequestZstd})
			if err != nil {
				return nil, err
			}
		}
		// A saved configuration may still hold retired settings; normalization drops them.
		return NormalizeNativeCodexConfig(ctx, raw)
	}
	return NewNativeCodexRuntime(gateway, repo, factory)
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
