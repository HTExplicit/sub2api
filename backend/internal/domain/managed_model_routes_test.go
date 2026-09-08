package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManagedModelRoutesConfigJSON(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		enabled bool
	}{
		{name: "existing empty column is unmanaged", payload: `{}`},
		{name: "enabled empty routes stay enabled", payload: `{"version":1,"enabled":true}`, enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var config ManagedModelRoutesConfig
			require.NoError(t, json.Unmarshal([]byte(tc.payload), &config))
			require.Equal(t, tc.enabled, config.Enabled)
			require.Empty(t, config.Routes)
		})
	}

	want := ManagedModelRoutesConfig{
		Version: 1,
		Enabled: true,
		Routes: []ManagedModelRoute{{
			PublicModel:    "gpt-public",
			Aliases:        []string{"gpt-public-alias"},
			Selector:       "s2pub-g23-m0123456789abcdef",
			TargetPlatform: "openai",
			Endpoints:      []string{"responses", "messages"},
			Accounts: []ManagedModelRouteAccount{{
				AccountID:          42,
				UpstreamModel:      "namespace/gpt-upstream-vip",
				AccountFingerprint: "fingerprint-for-test",
				Endpoints:          []string{"responses", "messages"},
			}},
		}},
	}
	encoded, err := json.Marshal(want)
	require.NoError(t, err)
	var got ManagedModelRoutesConfig
	require.NoError(t, json.Unmarshal(encoded, &got))
	require.Equal(t, want, got)
}
