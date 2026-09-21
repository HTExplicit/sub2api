package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type bootstrapCompletionRaceRepository struct {
	PluginRepository
	applied       bool
	readErr       error
	completionErr error
	appliedCalls  int
	imports       int
	completions   int
}

func (r *bootstrapCompletionRaceRepository) BundleApplied(context.Context, string, string) (bool, error) {
	r.appliedCalls++
	if r.appliedCalls == 1 {
		return false, nil
	}
	return r.applied, r.readErr
}

func (*bootstrapCompletionRaceRepository) PrepareBundledPlugin(_ context.Context, candidate *PluginInstallation, _, _ string, _ bool, config string) (*PluginInstallation, error) {
	copy := *candidate
	copy.ID, copy.Revision, copy.RuntimeGeneration = 7, 1, 1
	copy.ConfigEncrypted = config
	return &copy, nil
}

func (r *bootstrapCompletionRaceRepository) CompleteBundledPlugin(context.Context, int64, string) error {
	r.completions++
	return r.completionErr
}

func (*bootstrapCompletionRaceRepository) LegacyBundleSeed(_ context.Context, _, _ string, fallback PluginBundleSeed) (PluginBundleSeed, error) {
	return fallback, nil
}

func (r *bootstrapCompletionRaceRepository) ImportLegacyPluginState(context.Context, *PluginInstallation, string, json.RawMessage) error {
	r.imports++
	return nil
}

var _ PluginBundleRepository = (*bootstrapCompletionRaceRepository)(nil)

func TestBootstrapCompletionConflictRequiresPersistedTakeover(t *testing.T) {
	readFailure := errors.New("ownership read failed")
	otherFailure := errors.New("completion storage failed")
	for _, test := range []struct {
		name          string
		applied       bool
		readErr       error
		completionErr error
		wantErr       error
		wantReads     int
	}{
		{name: "confirmed_takeover", applied: true, completionErr: ErrPluginStateChanged, wantReads: 2},
		{name: "unconfirmed_state_change", completionErr: ErrPluginStateChanged, wantErr: ErrPluginStateChanged, wantReads: 2},
		{name: "takeover_read_failure", applied: true, readErr: readFailure, completionErr: ErrPluginStateChanged, wantErr: readFailure, wantReads: 2},
		{name: "other_completion_failure", applied: true, completionErr: otherFailure, wantErr: otherFailure, wantReads: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			cfg := testPluginConfig(root, true)
			host := PluginHostInfo{Version: "0.1.179"}
			manifest := testPluginManifest(nil)
			manifest.ID = "codexrip.bootstrap-race"
			archive := buildPluginArchive(t, manifest, nil, "", nil)
			digest := sha256.Sum256(archive)
			bundle := PluginBundle{SchemaVersion: 1, HostVersion: host.Version, PublisherKeyID: "completion-fixture", PublisherPublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32)), Plugins: []BundledPlugin{{ID: manifest.ID, Version: manifest.Version, File: "fixture.s2plugin", SHA256: hex.EncodeToString(digest[:])}}}
			raw, err := json.Marshal(bundle)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(root, "fixture.s2plugin"), archive, 0600))
			bundlePath := filepath.Join(root, "lock.json")
			require.NoError(t, os.WriteFile(bundlePath, raw, 0600))
			repo := &bootstrapCompletionRaceRepository{applied: test.applied, readErr: test.readErr, completionErr: test.completionErr}
			manager := NewPluginManager(repo, pluginTokenEncryptor{}, cfg, host, nil)
			manager.bundlePath = bundlePath
			err = manager.bootstrapBundle(context.Background())
			if test.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, test.wantErr)
			}
			require.Equal(t, test.wantReads, repo.appliedCalls)
			require.Equal(t, 1, repo.imports)
			require.Equal(t, 1, repo.completions)
		})
	}
}
