package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type PluginBundle struct {
	SchemaVersion      int             `json:"schema_version"`
	HostVersion        string          `json:"host_version"`
	PublisherKeyID     string          `json:"publisher_key_id"`
	PublisherPublicKey string          `json:"publisher_public_key"`
	Plugins            []BundledPlugin `json:"plugins"`
}

type BundledPlugin struct {
	DefaultEnabled bool   `json:"default_enabled,omitempty"`
	ID             string `json:"id"`
	Version        string `json:"version"`
	File           string `json:"file"`
	SHA256         string `json:"sha256"`
	Migration      string `json:"migration"`
}

type PluginBundleSeed struct {
	Enabled bool
	Config  json.RawMessage
}

type PluginBundleRepository interface {
	BundleApplied(context.Context, string, string) (bool, error)
	PrepareBundledPlugin(context.Context, *PluginInstallation, string, string, bool, string) (*PluginInstallation, error)
	CompleteBundledPlugin(context.Context, int64, string) error
	LegacyBundleSeed(context.Context, string, string, PluginBundleSeed) (PluginBundleSeed, error)
	ImportLegacyPluginState(context.Context, *PluginInstallation, string, json.RawMessage) error
}

// Bundles are read from the immutable application image. They never cause a
// remote download, and a completed bundle is not reapplied on process restart.
func (m *PluginManager) bootstrapBundle(ctx context.Context) error {
	path := m.bundlePath
	if path == "" {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		path = filepath.Join(filepath.Dir(executable), "bundled-plugins", "lock.json")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && m.bundlePath == "" {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return errors.New("invalid plugin bundle manifest")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var bundle PluginBundle
	if json.Unmarshal(raw, &bundle) != nil || bundle.SchemaVersion != 1 || len(bundle.Plugins) == 0 || bundle.PublisherKeyID == "" {
		return errors.New("invalid plugin bundle")
	}
	public, err := base64.StdEncoding.DecodeString(bundle.PublisherPublicKey)
	if err != nil || len(public) != 32 {
		return errors.New("invalid bundled publisher")
	}
	if normalizeSemver(bundle.HostVersion) != normalizeSemver(m.hostInfo.Version) {
		return errors.New("plugin bundle does not match host version")
	}
	store, ok := m.repo.(PluginBundleRepository)
	if !ok {
		return errors.New("plugin bundle repository unavailable")
	}
	if m.installer.bundledPublishers == nil {
		m.installer.bundledPublishers = make(map[string]string)
	}
	m.installer.bundledPublishers[bundle.PublisherKeyID] = bundle.PublisherPublicKey
	digest := sha256.Sum256(raw)
	bundleID := hex.EncodeToString(digest[:])
	seen := make(map[string]bool)
	for _, entry := range bundle.Plugins {
		if seen[entry.ID] || !strings.HasPrefix(entry.ID, "codexrip.") || !safePluginRelativePath(entry.File) || filepath.Base(entry.File) != entry.File {
			return errors.New("invalid first-party plugin bundle entry")
		}
		seen[entry.ID] = true
		applied, err := store.BundleApplied(ctx, entry.ID, bundleID)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		packagePath := filepath.Join(filepath.Dir(path), entry.File)
		info, err := os.Lstat(packagePath)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("bundled plugin must be a regular file")
		}
		content, err := os.ReadFile(packagePath)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		if hex.EncodeToString(sum[:]) != entry.SHA256 {
			return errors.New("bundled plugin digest mismatch")
		}
		file, err := os.Open(packagePath)
		if err != nil {
			return err
		}
		installation, installErr := m.installer.Install(ctx, file, nil)
		_ = file.Close()
		if installErr != nil {
			return installErr
		}
		if installation.PluginKey != entry.ID || installation.Version != entry.Version {
			return errors.New("bundled plugin identity mismatch")
		}
		seed := PluginBundleSeed{Enabled: entry.DefaultEnabled, Config: json.RawMessage(`{}`)}
		if entry.Migration == "codex-tickets-v1" {
			config := m.cfg.Gateway.OpenAICodexTicket
			models := config.Models
			if len(models) == 0 {
				models = []string{"gpt-6-astra", "gpt-5.6-sol"}
			}
			seed.Enabled = true
			seed.Config, _ = json.Marshal(map[string]any{"enabled": config.Enabled, "fail_closed": config.FailClosed, "proxy_url": config.HarvestProxyURL, "models": models, "request_zstd": m.cfg.Gateway.OpenAICodexRequestZstd})
		}
		if entry.Migration == "cindy-provider-v1" {
			seed.Config, _ = json.Marshal(LegacyCindyProviderConfig())
		}
		if entry.Migration == "image-tools-v1" {
			seed.Config, _ = json.Marshal(LegacyImageToolsConfig())
		}
		if entry.Migration == "admin-observability-v1" {
			seed.Config, _ = json.Marshal(LegacyAdminObservabilityConfig(m.cfg))
		}
		seed, err = store.LegacyBundleSeed(ctx, entry.ID, entry.Migration, seed)
		if err != nil {
			return fmt.Errorf("read plugin migration seed: %w", err)
		}
		cipher, err := m.encryptor.Encrypt(string(seed.Config))
		if err != nil {
			return errors.New("could not encrypt migrated plugin configuration")
		}
		uncommitted := installation
		installation, err = store.PrepareBundledPlugin(ctx, installation, bundleID, entry.Migration, seed.Enabled, cipher)
		if err != nil {
			return err
		}
		if installation == nil {
			if err := m.cleanupInstallationFiles(uncommitted); err != nil {
				return err
			}
			continue
		}
		migrationConfig, err := m.decryptConfig(installation)
		if err != nil {
			return err
		}
		if err := store.ImportLegacyPluginState(ctx, installation, entry.Migration, migrationConfig); err != nil {
			return err
		}
		if err := store.CompleteBundledPlugin(ctx, installation.ID, bundleID); err != nil {
			return err
		}
	}
	return nil
}
