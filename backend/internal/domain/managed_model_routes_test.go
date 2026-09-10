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

func TestManagedModelRoutesV2JSONPreservesDistinctBranches(t *testing.T) {
	config := ManagedModelRoutesConfig{Version: 2, Enabled: true, Routes: []ManagedModelRoute{{
		PublicModel: "claude-fable-5.1", QuotaPlatform: "anthropic", Endpoints: []string{"responses", "messages"},
		Branches: []ManagedModelRouteBranch{
			{Selector: "s2pub-g23-bnative", TargetPlatform: "anthropic", UpstreamProtocol: "messages", Endpoints: []string{"responses", "messages"}, Accounts: []ManagedModelRouteAccount{{AccountID: 41, UpstreamModel: "claude-fable-5.1", AccountFingerprint: "native-proof", Endpoints: []string{"responses", "messages"}}}},
			{Selector: "s2pub-g23-bcompatible", TargetPlatform: "openai", UpstreamProtocol: "chat_completions", Endpoints: []string{"responses", "messages"}, Accounts: []ManagedModelRouteAccount{{AccountID: 42, UpstreamModel: "provider/claude-fable-5.1-CC", AccountFingerprint: "compat-proof", Endpoints: []string{"responses", "messages"}}}},
		},
	}}}
	body, err := json.Marshal(config)
	require.NoError(t, err)
	var decoded ManagedModelRoutesConfig
	require.NoError(t, json.Unmarshal(body, &decoded))
	require.Equal(t, config, decoded)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(body, &envelope))
	routes, ok := envelope["routes"].([]any)
	require.True(t, ok)
	require.Len(t, routes, 1)
	route, ok := routes[0].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, route, "selector", "a v2 model has no single winning selector")
	require.NotContains(t, route, "target_platform")
	require.NotContains(t, route, "accounts")
}
