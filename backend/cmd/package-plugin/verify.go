package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// Verification uses the production package inspector, including path, file,
// signature and compatibility checks. It never starts a plugin executable.
func verifyPluginBundle(path, hostVersion, runtimeKey string, development bool) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var bundle service.PluginBundle
	if json.Unmarshal(raw, &bundle) != nil || bundle.SchemaVersion != 1 || len(bundle.Plugins) == 0 || hostVersion == "" || bundle.HostVersion != hostVersion {
		return errors.New("bundle does not identify this host version")
	}
	publisher := extensionv1.FirstPartyPublisher()
	if !development && (bundle.PublisherKeyID != publisher.KeyID || bundle.PublisherPublicKey != publisher.PublicKey) {
		return errors.New("bundle is not signed by the fixed release publisher")
	}
	directory, err := os.MkdirTemp("", "sub2api-package-verification-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	cfg := &config.Config{}
	cfg.Plugins.DataDir = directory
	cfg.Plugins.MaxUploadBytes = 128 << 20
	cfg.Plugins.MaxUncompressedBytes = 512 << 20
	cfg.Plugins.TrustedPublishers = map[string]string{bundle.PublisherKeyID: bundle.PublisherPublicKey}
	installer := service.NewPluginPackageInstaller(cfg, service.PluginHostInfo{Version: hostVersion, BuildType: "artifact"})
	seen := map[string]bool{}
	for _, entry := range bundle.Plugins {
		if seen[entry.ID] || entry.File == "" || filepath.Base(entry.File) != entry.File || filepath.IsAbs(entry.File) {
			return errors.New("invalid or repeated bundle entry")
		}
		seen[entry.ID] = true
		file := filepath.Join(filepath.Dir(path), entry.File)
		info, err := os.Lstat(file)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > cfg.Plugins.MaxUploadBytes {
			return errors.New("invalid bundled artifact")
		}
		artifact, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(artifact)
		if hex.EncodeToString(sum[:]) != entry.SHA256 {
			return errors.New("bundled package checksum differs")
		}
		manifest, err := installer.VerifyPackage(context.Background(), artifact, runtimeKey)
		if err != nil {
			return err
		}
		if manifest.ID != entry.ID || manifest.Version != entry.Version || !service.EvaluatePluginCompatibility(manifest, service.PluginHostInfo{Version: hostVersion}).Compatible {
			return errors.New("bundled identity or compatibility differs")
		}
	}
	fmt.Printf("verified_plugin_packages=%d\n", len(bundle.Plugins))
	return nil
}
