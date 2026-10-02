package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type retirementTestEncryptor struct{ fail bool }

func (e retirementTestEncryptor) Encrypt(value string) (string, error) { return value, nil }
func (e retirementTestEncryptor) Decrypt(value string) (string, error) {
	if e.fail {
		return "", errors.New("sensitive ciphertext must not appear in errors")
	}
	return value, nil
}

func TestNativeFeatureBootstrapPreservesEffectiveConfiguration(t *testing.T) {
	for _, test := range []struct {
		name     string
		plugin   NativeRetirementPlugin
		key      string
		expected string
		wantErr  bool
	}{
		{
			name:   "disabled image installation stays off",
			plugin: NativeRetirementPlugin{Key: "codexrip.image-tools", State: "disabled", ConfigEncrypted: `{"studio_enabled":true,"responses_image_enabled":true}`},
			key:    SettingKeyImageToolsConfig, expected: `{"studio_enabled":false}`,
		},
		{
			name: "separate theme and telemetry bindings",
			plugin: NativeRetirementPlugin{Key: "codexrip.admin-observability", State: "enabled", Manifest: json.RawMessage(`{"capabilities":[{"id":"extensions.observability.v1","platform":"*","account_type":"*"},{"id":"extensions.ui.v1","platform":"*","account_type":"*"}]}`), ConfigEncrypted: `{}`, Bindings: []NativeRetirementBinding{
				{Capability: "extensions.observability.v1", Platform: "*", AccountType: "*", Enabled: true, RolloutPercent: 100},
				{Capability: "extensions.ui.v1", Platform: "*", AccountType: "*", Enabled: false, RolloutPercent: 100},
			}}, key: SettingKeyAdminObservabilityConfig, expected: `{"telemetry_enabled":true,"theme_enabled":false}`,
		},
		{
			name: "retired Cindy provider drops its saved switches",
			plugin: NativeRetirementPlugin{Key: "codexrip.cindy-provider", State: "enabled", Manifest: json.RawMessage(`{"capabilities":[{"id":"extensions.provider.v1","platform":"cindy","account_type":"apikey"}]}`), ConfigEncrypted: `{"catalog_enabled":true,"search_enabled":false}`, Bindings: []NativeRetirementBinding{
				{Capability: "extensions.provider.v1", Platform: "cindy", AccountType: AccountTypeAPIKey, Enabled: true, RolloutPercent: 100},
			}},
		},
		{
			name: "partial telemetry cannot become global",
			plugin: NativeRetirementPlugin{Key: "codexrip.admin-observability", State: "enabled", Manifest: json.RawMessage(`{"capabilities":[{"id":"extensions.observability.v1","platform":"*","account_type":"*"},{"id":"extensions.ui.v1","platform":"*","account_type":"*"}]}`), ConfigEncrypted: `{}`, Bindings: []NativeRetirementBinding{
				{Capability: "extensions.observability.v1", Platform: "*", AccountType: "*", Enabled: true, RolloutPercent: 25},
			}}, wantErr: true,
		},
		{
			name:   "disabled admin action binding is not silently enabled",
			plugin: NativeRetirementPlugin{Key: "codexrip.account-tools", State: "enabled", Manifest: json.RawMessage(`{"capabilities":[{"id":"extensions.admin.v1","platform":"*","account_type":"*"}]}`)}, wantErr: true,
		},
		{
			name: "Codex separately bound OAuth and setup tokens stay equivalent",
			plugin: NativeRetirementPlugin{Key: "codexrip.codex-runtime", State: "enabled", Manifest: json.RawMessage(`{"capabilities":[{"id":"extensions.request.v1","platform":"openai","account_type":"oauth"},{"id":"extensions.request.v1","platform":"openai","account_type":"setup-token"}]}`), Bindings: []NativeRetirementBinding{
				{Capability: "extensions.request.v1", Platform: "openai", AccountType: "oauth", Enabled: true, RolloutPercent: 100},
				{Capability: "extensions.request.v1", Platform: "openai", AccountType: "setup-token", Enabled: true, RolloutPercent: 100},
			}},
		},
		{
			name:   "unfinished update cannot be retired",
			plugin: NativeRetirementPlugin{Key: "codexrip.codex-runtime", State: "updating"}, wantErr: true,
		},
		{
			name:   "disabled whole Codex domain needs an explicit decision",
			plugin: NativeRetirementPlugin{Key: "codexrip.codex-runtime", State: "disabled"}, wantErr: true,
		},
		{
			name:   "missing capability scope is rejected",
			plugin: NativeRetirementPlugin{Key: "codexrip.account-tools", State: "enabled", Manifest: json.RawMessage(`{}`)}, wantErr: true,
		},
		{
			name:   "narrow manifest cannot become a global native scope",
			plugin: NativeRetirementPlugin{Key: "codexrip.account-tools", State: "enabled", Manifest: json.RawMessage(`{"capabilities":[{"id":"extensions.admin.v1","platform":"openai","account_type":"apikey"}]}`)}, wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			settings, err := nativeFeatureSettings(test.plugin, retirementTestEncryptor{})
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if test.key == "" {
				require.Empty(t, settings)
			} else {
				require.JSONEq(t, test.expected, string(settings[test.key]))
			}
		})
	}
	_, err := nativeFeatureSettings(NativeRetirementPlugin{Key: "codexrip.image-tools", State: "disabled", ConfigEncrypted: "private"}, retirementTestEncryptor{fail: true})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "sensitive ciphertext")
	require.Error(t, ValidateNativeFeatureSetting(SettingKeyImageToolsConfig, []byte(`{"studio_enabled":"false"}`)))
	require.Error(t, ValidateNativeFeatureSetting(SettingKeyImageToolsConfig, nil))
}

type retiredPluginSourceStub []*PluginInstallation

func (s retiredPluginSourceStub) RetiredPluginInstallations(context.Context) ([]*PluginInstallation, error) {
	return s, nil
}

func TestNativeFeatureBootstrapListsRetiredPluginsWithDecryptedConfig(t *testing.T) {
	snapshot := &NativeRetirementSnapshot{Version: 1, Completed: true, Plugins: map[string]NativeRetirementPlugin{
		"codexrip.image-tools": {ID: 3, Key: "codexrip.image-tools", State: "enabled"},
	}}
	bootstrap := &NativeFeatureBootstrap{Snapshot: snapshot, encryptor: retirementTestEncryptor{}}
	bootstrap.SetRetiredPluginSource(retiredPluginSourceStub{{
		ID: 3, PluginKey: "codexrip.image-tools", State: "disabled", LastError: "stopped by retirement",
		ConfigEncrypted: `{"studio_enabled":true,"api_key":"saved-secret"}`,
	}})
	view, err := bootstrap.RetiredPlugins(context.Background())
	require.NoError(t, err)
	require.Same(t, snapshot, view.Receipt)
	require.Len(t, view.Installations, 1)
	require.Equal(t, "stopped by retirement", view.Installations[0].LastError)
	require.JSONEq(t, `{"studio_enabled":true,"api_key":"saved-secret"}`, string(view.Installations[0].Config))
}
