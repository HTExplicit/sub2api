package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

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
	oldImage := EffectiveImageToolsConfig()
	oldCindy := EffectiveCindyProviderConfig()
	oldObservability := EffectiveAdminObservabilityConfig()
	t.Cleanup(func() {
		ConfigureImageTools(&oldImage)
		ConfigureCindyProvider(&oldCindy)
		ConfigureAdminObservability(&oldObservability)
	})
	ConfigureImageTools(&extensionv1.ImageToolsConfig{})
	ConfigureCindyProvider(&extensionv1.CindyProviderConfig{})
	ConfigureAdminObservability(&extensionv1.AdminObservabilityConfig{})
	for _, test := range []struct {
		name  string
		value string
		err   error
	}{
		{name: "database error", err: errors.New("fixture database unavailable")},
		{name: "invalid JSON", value: `{"broken"`},
		{name: "invalid value type", value: `{"studio_enabled":"false"}`},
		{name: "empty existing key", value: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc := NewSettingService(&nativeSwitchReadRepository{value: test.value, err: test.err}, nil)
			for _, load := range []func(context.Context) error{svc.LoadImageToolsConfig, svc.LoadCindyProviderConfig, svc.LoadAdminObservabilityConfig} {
				require.Error(t, load(context.Background()))
			}
			require.Equal(t, extensionv1.ImageToolsConfig{}, EffectiveImageToolsConfig())
			require.Equal(t, extensionv1.CindyProviderConfig{}, EffectiveCindyProviderConfig())
			require.Equal(t, extensionv1.AdminObservabilityConfig{}, EffectiveAdminObservabilityConfig())
		})
	}
	svc := NewSettingService(&nativeSwitchReadRepository{err: ErrSettingNotFound}, nil)
	var value map[string]json.RawMessage
	found, err := svc.readNativeSwitchSetting(context.Background(), SettingKeyImageToolsConfig, &value, "studio_enabled", "responses_image_enabled")
	require.NoError(t, err)
	require.False(t, found)
}
