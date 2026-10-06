package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeSwitchSettingsRejectsUnknownAndNonBooleanValues(t *testing.T) {
	config := AdminObservabilityConfig{TelemetryEnabled: true, ThemeEnabled: true}
	require.NoError(t, DecodeSwitchSettings([]byte(`{"theme_enabled":false}`), &config, "telemetry_enabled", "theme_enabled"))
	require.Equal(t, AdminObservabilityConfig{TelemetryEnabled: true}, config, "omitted switches keep their defaults")
	for _, raw := range []string{`{"theme_enabled":null}`, `{"theme":true}`, `{"theme_enabled":"false"}`, `[]`, `null`} {
		require.Error(t, DecodeSwitchSettings([]byte(raw), &config, "telemetry_enabled", "theme_enabled"), raw)
	}
}
