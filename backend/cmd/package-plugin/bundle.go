package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	valid := regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	for _, entry := range inventory.Plugins {
		if !valid.MatchString(entry.Directory) || seen[entry.Directory] {
			return errors.New("invalid or duplicate bundled module directory")
		}
		seen[entry.Directory] = true
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
	return nil
}
