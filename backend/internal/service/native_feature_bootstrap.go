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
// Ciphertext and package bytes are not copied into them.
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

	encryptor SecretEncryptor
	retired   RetiredPluginSource
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
	return &NativeFeatureBootstrap{Snapshot: snapshot, encryptor: encryptor}, nil
}

// RetiredPluginSource reads the retired first-party installation rows. They
// stay outside the plugin manager, whose reconciliation would clean them up.
type RetiredPluginSource interface {
	RetiredPluginInstallations(context.Context) ([]*PluginInstallation, error)
}

// SetRetiredPluginSource is called by the plugin repository boundary.
func (b *NativeFeatureBootstrap) SetRetiredPluginSource(source RetiredPluginSource) {
	if b != nil {
		b.retired = source
	}
}

// RetiredPluginInstallation is the read-only admin view of one retired
// first-party installation row with its decrypted saved configuration.
type RetiredPluginInstallation struct {
	ID              int64           `json:"id"`
	PluginKey       string          `json:"plugin_key"`
	Name            string          `json:"name"`
	Version         string          `json:"version"`
	Description     string          `json:"description"`
	Author          string          `json:"author"`
	Manifest        PluginManifest  `json:"manifest"`
	ArtifactPath    string          `json:"artifact_path"`
	InstallPath     string          `json:"install_path"`
	BinaryPath      string          `json:"binary_path"`
	BinarySHA256    string          `json:"binary_sha256"`
	SignatureStatus string          `json:"signature_status"`
	State           string          `json:"state"`
	LastError       string          `json:"last_error"`
	InstalledBy     *int64          `json:"installed_by"`
	InstalledAt     time.Time       `json:"installed_at"`
	EnabledAt       *time.Time      `json:"enabled_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	Bindings        []PluginBinding `json:"bindings"`
	Config          json.RawMessage `json:"config,omitempty"`
	ConfigText      string          `json:"config_text,omitempty"`
	ConfigError     string          `json:"config_error,omitempty"`
}

// RetiredPluginsView pairs the retirement receipt with the current rows.
type RetiredPluginsView struct {
	Receipt       *NativeRetirementSnapshot   `json:"receipt"`
	Installations []RetiredPluginInstallation `json:"installations"`
}

// RetiredPlugins lists the retired first-party installations read-only, with
// their saved configuration decrypted as the plugin configuration API did.
func (b *NativeFeatureBootstrap) RetiredPlugins(ctx context.Context) (*RetiredPluginsView, error) {
	view := &RetiredPluginsView{Installations: []RetiredPluginInstallation{}}
	if b == nil {
		return view, nil
	}
	view.Receipt = b.Snapshot
	if b.retired == nil {
		return view, nil
	}
	rows, err := b.retired.RetiredPluginInstallations(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row == nil {
			continue
		}
		item := RetiredPluginInstallation{
			ID: row.ID, PluginKey: row.PluginKey, Name: row.Name, Version: row.Version,
			Description: row.Description, Author: row.Author, Manifest: row.Manifest,
			ArtifactPath: row.ArtifactPath, InstallPath: row.InstallPath, BinaryPath: row.BinaryPath,
			BinarySHA256: row.BinarySHA256, SignatureStatus: row.SignatureStatus, State: row.State,
			LastError: row.LastError, InstalledBy: row.InstalledBy, InstalledAt: row.InstalledAt,
			EnabledAt: row.EnabledAt, UpdatedAt: row.UpdatedAt, Bindings: row.Bindings,
		}
		if item.Bindings == nil {
			item.Bindings = []PluginBinding{}
		}
		switch {
		case row.ConfigEncrypted == "":
		case b.encryptor == nil:
			item.ConfigError = "configuration decryptor is unavailable"
		default:
			plain, decryptErr := b.encryptor.Decrypt(row.ConfigEncrypted)
			switch {
			case decryptErr != nil:
				item.ConfigError = decryptErr.Error()
			case json.Valid([]byte(plain)):
				item.Config = json.RawMessage(plain)
			default:
				item.ConfigText = plain
			}
		}
		view.Installations = append(view.Installations, item)
	}
	return view, nil
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
		// responses_image_enabled (the removed Responses image bridge) is
		// accepted in a saved plugin configuration and dropped.
		if err := DecodeSwitchSettings(raw, &value, "studio_enabled", "responses_image_enabled"); err != nil {
			return nil, fmt.Errorf("invalid saved image tool configuration: %w", err)
		}
		enabled, err := nativeRetirementBindingEnabled(plugin, "extensions.request.v1", "*", "*")
		if err != nil {
			return nil, err
		}
		value.StudioEnabled = value.StudioEnabled && enabled
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
		// The provider plugin was removed; its saved switches have no native
		// equivalent and are dropped with the retired installation.
		return settings, nil
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
	case "codexrip.prompt-skills":
		return (global && capability == "extensions.admin.v1") || (capability == "extensions.request.v1" && platform == PlatformOpenAI && accountType == "*")
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
		return DecodeSwitchSettings(raw, &extensionv1.ImageToolsConfig{}, "studio_enabled")
	case SettingKeyAdminObservabilityConfig:
		return DecodeSwitchSettings(raw, &extensionv1.AdminObservabilityConfig{}, "telemetry_enabled", "theme_enabled")
	default:
		return errors.New("unsupported native feature setting")
	}
}
