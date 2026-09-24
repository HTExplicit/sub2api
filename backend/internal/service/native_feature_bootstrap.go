package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

const NativeFeatureRetirementSetting = "deplugin_retired_plugins"

var FirstPartyNativePluginKeys = []string{
	"codexrip.account-tools", "codexrip.admin-observability", "codexrip.cindy-provider",
	"codexrip.codex-runtime", "codexrip.image-tools", "codexrip.model-policy", "codexrip.prompt-skills",
}

// These records describe the retired installation, not an enabled native plugin.
// Ciphertext and package bytes remain in their original columns.
type NativeRetirementBinding struct {
	ID             int64  `json:"id"`
	Capability     string `json:"capability"`
	Platform       string `json:"platform"`
	AccountType    string `json:"account_type"`
	Enabled        bool   `json:"enabled"`
	RolloutPercent int    `json:"rollout_percent"`
}

type NativeRetirementPlugin struct {
	ID                int64                     `json:"id"`
	Key               string                    `json:"key"`
	State             string                    `json:"state"`
	RuntimeGeneration int64                     `json:"runtime_generation"`
	Bindings          []NativeRetirementBinding `json:"bindings"`
	Bootstrap         json.RawMessage           `json:"bootstrap,omitempty"`
	ConfigEncrypted   string                    `json:"-"`
	Manifest          json.RawMessage           `json:"-"`
	NativeCreated     bool                      `json:"native_created,omitempty"`
}

type NativeRetirementSnapshot struct {
	Version   int                               `json:"version"`
	Completed bool                              `json:"completed"`
	RetiredAt time.Time                         `json:"retired_at"`
	Plugins   map[string]NativeRetirementPlugin `json:"plugins"`
}

type NativeFeatureBootstrapRepository interface {
	RetireNativeFeatures(context.Context, func(NativeRetirementPlugin) (map[string]json.RawMessage, error)) (*NativeRetirementSnapshot, error)
}

// NativeFeatureBootstrap is a Wire dependency of settings loading: imports and
// retirement must commit before a native feature or the plugin manager starts.
type NativeFeatureBootstrap struct {
	Snapshot *NativeRetirementSnapshot
}

func ProvideNativeFeatureBootstrap(repo NativeFeatureBootstrapRepository, encryptor SecretEncryptor) (*NativeFeatureBootstrap, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	snapshot, err := repo.RetireNativeFeatures(ctx, func(plugin NativeRetirementPlugin) (map[string]json.RawMessage, error) {
		return nativeFeatureSettings(plugin, encryptor)
	})
	if err != nil {
		return nil, fmt.Errorf("initialize native features: %w", err)
	}
	return &NativeFeatureBootstrap{Snapshot: snapshot}, nil
}

func nativeFeatureSettings(plugin NativeRetirementPlugin, encryptor SecretEncryptor) (map[string]json.RawMessage, error) {
	if plugin.State == "starting" || plugin.State == "updating" {
		return nil, fmt.Errorf("%s has an unfinished installation transition", plugin.Key)
	}
	raw := []byte(`{}`)
	if plugin.ConfigEncrypted != "" {
		if encryptor == nil {
			return nil, errors.New("native feature configuration decryptor is unavailable")
		}
		plain, err := encryptor.Decrypt(plugin.ConfigEncrypted)
		if err != nil {
			return nil, fmt.Errorf("cannot decrypt configuration for %s", plugin.Key)
		}
		raw = []byte(plain)
	}
	settings := make(map[string]json.RawMessage)
	put := func(key string, value any) error {
		encoded, err := json.Marshal(value)
		if err == nil {
			settings[key] = encoded
		}
		return err
	}
	switch plugin.Key {
	case "codexrip.image-tools":
		if err := validateNativeUnmappedBindings(plugin, "extensions.request.v1"); err != nil {
			return nil, err
		}
		var value extensionv1.ImageToolsConfig
		if err := DecodeSwitchSettings(raw, &value, "studio_enabled", "responses_image_enabled"); err != nil {
			return nil, fmt.Errorf("invalid saved image tool configuration: %w", err)
		}
		enabled, err := nativeRetirementBindingEnabled(plugin, "extensions.request.v1", "*", "*")
		if err != nil {
			return nil, err
		}
		value.StudioEnabled = value.StudioEnabled && enabled
		value.ResponsesImageEnabled = value.ResponsesImageEnabled && enabled
		return settings, put(SettingKeyImageToolsConfig, value)
	case "codexrip.admin-observability":
		if err := validateNativeUnmappedBindings(plugin, "extensions.observability.v1", "extensions.ui.v1"); err != nil {
			return nil, err
		}
		value := extensionv1.AdminObservabilityConfig{TelemetryEnabled: true, ThemeEnabled: true}
		if err := DecodeSwitchSettings(raw, &value, "telemetry_enabled", "theme_enabled"); err != nil {
			return nil, fmt.Errorf("invalid saved observability configuration: %w", err)
		}
		telemetry, err := nativeRetirementBindingEnabled(plugin, "extensions.observability.v1", "*", "*")
		if err != nil {
			return nil, err
		}
		theme, err := nativeRetirementBindingEnabled(plugin, "extensions.ui.v1", "*", "*")
		if err != nil {
			return nil, err
		}
		value.TelemetryEnabled = value.TelemetryEnabled && telemetry
		value.ThemeEnabled = value.ThemeEnabled && theme
		return settings, put(SettingKeyAdminObservabilityConfig, value)
	case "codexrip.cindy-provider":
		if err := validateNativeUnmappedBindings(plugin, "extensions.provider.v1"); err != nil {
			return nil, err
		}
		value := extensionv1.CindyProviderConfig{BalanceDetection: true}
		if err := DecodeSwitchSettings(raw, &value, "balance_detection", "catalog_enabled", "search_enabled"); err != nil {
			return nil, fmt.Errorf("invalid saved Cindy configuration: %w", err)
		}
		enabled, err := nativeRetirementBindingEnabled(plugin, "extensions.provider.v1", PlatformCindy, AccountTypeAPIKey)
		if err != nil {
			return nil, err
		}
		value.BalanceDetection = value.BalanceDetection && enabled
		value.CatalogEnabled = value.CatalogEnabled && enabled
		value.SearchEnabled = value.SearchEnabled && enabled
		return settings, put(SettingKeyCindyProviderConfig, value)
	default:
		// Domains without a single equivalent switch cannot silently broaden a
		// saved partial rollout or turn a disabled domain back on.
		if plugin.State != "enabled" {
			return nil, fmt.Errorf("%s is disabled; its native availability needs an explicit decision", plugin.Key)
		}
		if err := validateNativeUnmappedBindings(plugin); err != nil {
			return nil, err
		}
		return settings, nil
	}
}

func validateNativeUnmappedBindings(plugin NativeRetirementPlugin, mapped ...string) error {
	if plugin.State != "enabled" {
		return nil
	}
	var manifest struct {
		Capabilities []struct {
			ID          string `json:"id"`
			Platform    string `json:"platform"`
			AccountType string `json:"account_type"`
		} `json:"capabilities"`
	}
	if json.Unmarshal(plugin.Manifest, &manifest) != nil || len(manifest.Capabilities) == 0 {
		return fmt.Errorf("invalid saved manifest for %s", plugin.Key)
	}
	for _, capability := range manifest.Capabilities {
		if !nativeCapabilityScopeKnown(plugin.Key, capability.ID, capability.Platform, capability.AccountType) {
			return fmt.Errorf("unsupported saved capability scope for %s", plugin.Key)
		}
		isMapped := false
		for _, id := range mapped {
			isMapped = isMapped || capability.ID == id
		}
		if isMapped {
			continue
		}
		enabled := false
		for _, binding := range plugin.Bindings {
			if binding.Capability == capability.ID && binding.Platform == capability.Platform && binding.AccountType == capability.AccountType {
				enabled = enabled || (binding.Enabled && binding.RolloutPercent == 100)
			}
		}
		if !enabled {
			return fmt.Errorf("%s has a disabled %s binding without an equivalent native switch", plugin.Key, capability.ID)
		}
	}
	return nil
}

func nativeCapabilityScopeKnown(key, capability, platform, accountType string) bool {
	global := platform == "*" && accountType == "*"
	switch key {
	case "codexrip.account-tools":
		return global && (capability == "extensions.admin.v1" || capability == "extensions.ui.v1")
	case "codexrip.admin-observability":
		return global && (capability == "extensions.admin.v1" || capability == "extensions.ui.v1" || capability == "extensions.observability.v1")
	case "codexrip.model-policy":
		return global && (capability == "extensions.admin.v1" || capability == "extensions.catalog.v1")
	case "codexrip.image-tools":
		return global && (capability == "extensions.admin.v1" || capability == "extensions.request.v1")
	case "codexrip.cindy-provider":
		return (global && capability == "extensions.admin.v1") || (capability == "extensions.provider.v1" && platform == PlatformCindy && accountType == AccountTypeAPIKey)
	case "codexrip.prompt-skills":
		return (global && capability == "extensions.admin.v1") || (capability == "extensions.request.v1" && platform == PlatformOpenAI && accountType == "*")
	case "codexrip.codex-runtime":
		if platform != PlatformOpenAI {
			return false
		}
		if capability == "extensions.recovery.v1" {
			return accountType == "*"
		}
		if accountType != AccountTypeOAuth && accountType != AccountTypeSetupToken {
			return false
		}
		return capability == "extensions.admin.v1" || capability == "extensions.credentials.v1" || capability == "extensions.jobs.v1" || capability == "extensions.request.v1" || capability == "extensions.scheduling.v1"
	}
	return false
}

func nativeRetirementBindingEnabled(plugin NativeRetirementPlugin, capability, platform, accountType string) (bool, error) {
	if plugin.State != "enabled" {
		return false, nil
	}
	enabled := false
	for _, binding := range plugin.Bindings {
		if binding.Capability != capability || !binding.Enabled || binding.RolloutPercent == 0 {
			continue
		}
		if binding.RolloutPercent != 100 || binding.Platform != platform || binding.AccountType != accountType {
			return false, fmt.Errorf("%s has a restricted %s binding that cannot be widened during retirement", plugin.Key, capability)
		}
		enabled = true
	}
	return enabled, nil
}

// ValidateNativeFeatureSetting distinguishes an existing invalid value from a
// missing key. Retirement must never overwrite either an existing setting or a
// database read failure with defaults.
func ValidateNativeFeatureSetting(key string, raw []byte) error {
	switch key {
	case SettingKeyImageToolsConfig:
		return DecodeSwitchSettings(raw, &extensionv1.ImageToolsConfig{}, "studio_enabled", "responses_image_enabled")
	case SettingKeyAdminObservabilityConfig:
		return DecodeSwitchSettings(raw, &extensionv1.AdminObservabilityConfig{}, "telemetry_enabled", "theme_enabled")
	case SettingKeyCindyProviderConfig:
		return DecodeSwitchSettings(raw, &extensionv1.CindyProviderConfig{}, "balance_detection", "catalog_enabled", "search_enabled")
	default:
		return errors.New("unsupported native feature setting")
	}
}
