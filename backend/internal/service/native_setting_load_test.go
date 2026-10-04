package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/stretchr/testify/require"
)

type nativeSwitchReadRepository struct {
	SettingRepository
	value string
	err   error
}

func (r *nativeSwitchReadRepository) GetValue(context.Context, string) (string, error) {
	return r.value, r.err
}

func TestNativeSettingLoadFailureNeverReenablesSavedSwitches(t *testing.T) {
	oldObservability := EffectiveAdminObservabilityConfig()
	oldCodexRuntime := EffectiveCodexRuntimeConfig()
	t.Cleanup(func() {
		ConfigureAdminObservability(&oldObservability)
		SetCodexRequestZstdEnabled(oldCodexRuntime.RequestZstd)
	})
	ConfigureAdminObservability(&extensionv1.AdminObservabilityConfig{})
	SetCodexRequestZstdEnabled(false)
	for _, test := range []struct {
		name  string
		value string
		err   error
	}{
		{name: "database error", err: errors.New("fixture database unavailable")},
		{name: "invalid JSON", value: `{"broken"`},
		{name: "invalid value type", value: `{"telemetry_enabled":"false"}`},
		{name: "empty existing key", value: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc := NewSettingService(&nativeSwitchReadRepository{value: test.value, err: test.err}, nil)
			for _, load := range []func(context.Context) error{svc.LoadAdminObservabilityConfig, svc.LoadCodexRuntimeConfig} {
				require.Error(t, load(context.Background()))
			}
			require.Equal(t, extensionv1.AdminObservabilityConfig{}, EffectiveAdminObservabilityConfig())
			require.Equal(t, extensionv1.CodexRuntimeConfig{}, EffectiveCodexRuntimeConfig())
		})
	}
	svc := NewSettingService(&nativeSwitchReadRepository{err: ErrSettingNotFound}, nil)
	var value map[string]json.RawMessage
	found, err := svc.readNativeSwitchSetting(context.Background(), SettingKeyAdminObservabilityConfig, &value, "telemetry_enabled", "theme_enabled")
	require.NoError(t, err)
	require.False(t, found)
}

type codexRuntimeSettingRepository struct {
	SettingRepository
	values map[string]string
}

func (r *codexRuntimeSettingRepository) GetValue(_ context.Context, key string) (string, error) {
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *codexRuntimeSettingRepository) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

// The Codex request compression switch is one plain settings row. Without a
// row the deploy-time gateway.openai_codex_request_zstd applies; a row that is
// not exactly a request_zstd boolean object is an error.
func TestCodexRuntimeSettingIsOnePlainRowOverTheDeployDefault(t *testing.T) {
	old := EffectiveCodexRuntimeConfig()
	t.Cleanup(func() { SetCodexRequestZstdEnabled(old.RequestZstd) })
	ctx := context.Background()
	load := func(deploy bool, stored ...string) (extensionv1.CodexRuntimeConfig, error) {
		repo := &codexRuntimeSettingRepository{values: map[string]string{}}
		if len(stored) > 0 {
			repo.values["codex_runtime_config"] = stored[0]
		}
		cfg := &config.Config{}
		cfg.Gateway.OpenAICodexRequestZstd = deploy
		err := NewSettingService(repo, cfg).LoadCodexRuntimeConfig(ctx)
		return EffectiveCodexRuntimeConfig(), err
	}
	for _, test := range []struct {
		name     string
		deploy   bool
		stored   []string
		expected bool
	}{
		{name: "no row follows the deploy default on", deploy: true, expected: true},
		{name: "no row follows the deploy default off", deploy: false, expected: false},
		{name: "stored off overrides the deploy default", deploy: true, stored: []string{`{"request_zstd":false}`}, expected: false},
		{name: "stored on overrides the deploy default", deploy: false, stored: []string{`{"request_zstd":true}`}, expected: true},
		{name: "an omitted switch is on", deploy: false, stored: []string{`{}`}, expected: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			SetCodexRequestZstdEnabled(!test.expected)
			got, err := load(test.deploy, test.stored...)
			require.NoError(t, err)
			require.Equal(t, extensionv1.CodexRuntimeConfig{RequestZstd: test.expected}, got)
		})
	}
	for _, stored := range []string{`{"request_zstd":"true"}`, `{"request_zstd":null}`, `{"request_zstd":true,"other":true}`, `{"other":true}`, `null`, `[]`, `true`, ``} {
		SetCodexRequestZstdEnabled(false)
		got, err := load(true, stored)
		require.Error(t, err, stored)
		require.False(t, got.RequestZstd, "a rejected row must not switch compression on: %s", stored)
	}

	repo := &codexRuntimeSettingRepository{values: map[string]string{}}
	svc := NewSettingService(repo, &config.Config{})
	require.NoError(t, svc.UpdateCodexRuntimeConfig(ctx, extensionv1.CodexRuntimeConfig{RequestZstd: true}))
	require.Equal(t, map[string]string{"codex_runtime_config": `{"request_zstd":true}`}, repo.values)
	require.True(t, EffectiveCodexRuntimeConfig().RequestZstd)
	require.NoError(t, svc.UpdateCodexRuntimeConfig(ctx, extensionv1.CodexRuntimeConfig{}))
	require.Equal(t, map[string]string{"codex_runtime_config": `{"request_zstd":false}`}, repo.values)
	require.False(t, EffectiveCodexRuntimeConfig().RequestZstd)
}
