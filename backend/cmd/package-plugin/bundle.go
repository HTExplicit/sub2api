package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"golang.org/x/mod/semver"
)

type bundleSource struct {
	SchemaVersion int `json:"schema_version"`
	Plugins       []struct {
		Directory      string `json:"directory"`
		DefaultEnabled bool   `json:"default_enabled"`
		Migration      string `json:"migration"`
	} `json:"plugins"`
}

// The source inventory is the only place that chooses bundled domains and
// initial activation. It accepts prebuilt binaries and never executes a plugin.
func packageBundle(source, binaryDirectory string, options packageOptions) error {
	if binaryDirectory == "" || options.output == "" || options.hostVersion == "" {
		return errors.New("bundle requires binary-dir, output directory and tested-host-version")
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	var inventory bundleSource
	if json.Unmarshal(raw, &inventory) != nil || inventory.SchemaVersion != 1 || len(inventory.Plugins) == 0 {
		return errors.New("invalid bundle source")
	}
	if existing, err := os.ReadDir(options.output); err == nil && len(existing) > 0 {
		return errors.New("bundle output directory must be empty")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(options.output, 0700); err != nil {
		return err
	}
	seen := map[string]bool{}
	matched := false
	valid := regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	for _, entry := range inventory.Plugins {
		if !valid.MatchString(entry.Directory) || seen[entry.Directory] {
			return errors.New("invalid or duplicate bundled module directory")
		}
		seen[entry.Directory] = true
		if options.domain != "" && options.domain != entry.Directory {
			continue
		}
		matched = true
		suffix := ""
		if strings.HasPrefix(options.platform, "windows-") {
			suffix = ".exe"
		}
		module := options
		module.source = filepath.Join(filepath.Dir(source), entry.Directory)
		module.binary = filepath.Join(binaryDirectory, entry.Directory+suffix)
		module.output = filepath.Join(options.output, entry.Directory+".s2plugin")
		module.bundleLock = filepath.Join(options.output, "lock.json")
		module.defaultEnabled, module.migration = entry.DefaultEnabled, entry.Migration
		if err := packagePlugin(module); err != nil {
			return err
		}
	}
	if !matched {
		return errors.New("plugin domain is not in the bundle source")
	}
	return nil
}

func buildBundleBinaries(source, destination string, options packageOptions) error {
	if source == "" || destination == "" {
		return errors.New("bundle-source and binary-dir are required")
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	var inventory bundleSource
	if json.Unmarshal(raw, &inventory) != nil || inventory.SchemaVersion != 1 {
		return errors.New("invalid bundle source")
	}
	platform := strings.Split(options.platform, "-")
	if len(platform) != 2 || !regexp.MustCompile(`^[a-z0-9]+$`).MatchString(platform[0]) || !regexp.MustCompile(`^[a-z0-9]+$`).MatchString(platform[1]) {
		return errors.New("invalid runtime platform")
	}
	if err = os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return err
	}
	matched := false
	seen := map[string]bool{}
	for _, entry := range inventory.Plugins {
		if !regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(entry.Directory) || seen[entry.Directory] {
			return errors.New("invalid domain directory")
		}
		seen[entry.Directory] = true
		if options.domain != "" && options.domain != entry.Directory {
			continue
		}
		matched = true
		module := filepath.Join(filepath.Dir(source), entry.Directory)
		raw, err := os.ReadFile(filepath.Join(module, "manifest.source.json"))
		if err != nil {
			return err
		}
		var manifest service.PluginManifest
		if json.Unmarshal(raw, &manifest) != nil || manifest.ID != "codexrip."+entry.Directory || !semver.IsValid("v"+manifest.Version) {
			return errors.New("invalid domain version or identity")
		}
		suffix := ""
		if platform[0] == "windows" {
			suffix = ".exe"
		}
		output := filepath.Join(destination, entry.Directory+suffix)
		if _, err = os.Stat(output); !errors.Is(err, os.ErrNotExist) {
			return errors.New("binary output already exists or cannot be inspected")
		}
		command := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w -X main.version="+manifest.Version, "-o", output, "./cmd/plugin")
		command.Dir = module
		for _, value := range os.Environ() {
			key, _, _ := strings.Cut(value, "=")
			if strings.EqualFold(key, "SUB2API_PLUGIN_SIGNING_KEY") || strings.EqualFold(key, "GOOS") || strings.EqualFold(key, "GOARCH") || strings.EqualFold(key, "CGO_ENABLED") {
				continue
			}
			command.Env = append(command.Env, value)
		}
		command.Env = append(command.Env, "GOOS="+platform[0], "GOARCH="+platform[1], "CGO_ENABLED=0")
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
		if err = command.Run(); err != nil {
			return fmt.Errorf("build domain %s: %w", entry.Directory, err)
		}
	}
	if !matched {
		return errors.New("plugin domain is not in the bundle source")
	}
	return nil
}
