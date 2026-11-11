package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type switchReadRepository struct {
	SettingRepository
	value string
	err   error
}

func (r *switchReadRepository) GetValue(context.Context, string) (string, error) {
	return r.value, r.err
}

func TestSettingLoadFailureNeverReenablesSavedSwitches(t *testing.T) {
	oldObservability := EffectiveAdminObservabilityConfig()
	t.Cleanup(func() {
		ConfigureAdminObservability(&oldObservability)
	})
	ConfigureAdminObservability(&AdminObservabilityConfig{})
	for _, test := range []struct {
		name  string
		value string
		err   error
	}{
		{name: "database error", err: errors.New("fixture database unavailable")},
		{name: "invalid JSON", value: `{"broken"`},
		{name: "invalid value type", value: `{"theme_enabled":"false"}`},
		{name: "empty existing key", value: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc := NewSettingService(&switchReadRepository{value: test.value, err: test.err}, nil)
			require.Error(t, svc.LoadAdminObservabilityConfig(context.Background()))
			require.Equal(t, AdminObservabilityConfig{}, EffectiveAdminObservabilityConfig())
		})
	}
	svc := NewSettingService(&switchReadRepository{err: ErrSettingNotFound}, nil)
	var value map[string]json.RawMessage
	found, err := svc.readSwitchSetting(context.Background(), SettingKeyAdminObservabilityConfig, &value, "theme_enabled")
	require.NoError(t, err)
	require.False(t, found)
}

func TestDecodeSwitchSettingsRejectsUnknownAndNonBooleanValues(t *testing.T) {
	type switches struct {
		Kept    bool `json:"kept"`
		Changed bool `json:"changed"`
	}
	config := switches{Kept: true, Changed: true}
	require.NoError(t, DecodeSwitchSettings([]byte(`{"changed":false}`), &config, "kept", "changed"))
	require.Equal(t, switches{Kept: true}, config, "omitted switches keep their defaults")
	for _, raw := range []string{`{"changed":null}`, `{"change":true}`, `{"changed":"false"}`, `[]`, `null`} {
		require.Error(t, DecodeSwitchSettings([]byte(raw), &config, "kept", "changed"), raw)
	}
}
