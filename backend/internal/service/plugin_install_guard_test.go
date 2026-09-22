package service

import (
	"bytes"
	"context"
	"database/sql"
	"io/fs"
	"net/http"
	"path/filepath"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type pluginInstallGuardRepository struct {
	*pluginTokenRepository
	lookupError error
	installErr  error
	installs    int
	candidate   *PluginInstallation
}

func (r *pluginInstallGuardRepository) GetByKey(ctx context.Context, _ string) (*PluginInstallation, error) {
	if r.lookupError != nil {
		return nil, r.lookupError
	}
	return r.GetByID(ctx, 7)
}

func (r *pluginInstallGuardRepository) Install(_ context.Context, candidate *PluginInstallation, bindings []PluginBinding) (*PluginInstallation, error) {
	r.installs++
	r.candidate = candidate
	if r.installErr != nil {
		return nil, r.installErr
	}
	installed := *candidate
	installed.ID, installed.RuntimeGeneration, installed.Revision = 7, 1, 1
	installed.UpdatePolicy = PluginUpdatePinned
	installed.Bindings = append([]PluginBinding(nil), bindings...)
	r.installation = &installed
	return &installed, nil
}

func pluginInstallGuardFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	}))
	return files
}

func TestPluginInstallRejectsExistingIdentity(t *testing.T) {
	for _, state := range []string{PluginStateDisabled, PluginStateStarting, PluginStateEnabled, PluginStateError, PluginStateIncompatible, PluginStateUpdating} {
		t.Run(state, func(t *testing.T) {
			cfg := testPluginConfig(t.TempDir(), true)
			host := PluginHostInfo{Version: "0.1.179"}
			installer := NewPluginPackageInstaller(cfg, host)
			old, err := installer.Install(context.Background(), bytes.NewReader(buildTestPluginArchive(t, nil, "")), nil)
			require.NoError(t, err)
			old.ID, old.RuntimeGeneration, old.Revision = 7, 9, 4
			old.State, old.ConfigEncrypted, old.UpdatePolicy = state, `ENC:{"saved":true}`, PluginUpdateBundled
			old.Bindings = []PluginBinding{{Capability: PluginCapabilityOpenAIOAuthOutbound, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, RolloutPercent: 37, Enabled: state == PluginStateEnabled}}
			before := *old
			before.Bindings = append([]PluginBinding(nil), old.Bindings...)
			files := pluginInstallGuardFiles(t, cfg.Plugins.DataDir)
			repo := &pluginInstallGuardRepository{pluginTokenRepository: &pluginTokenRepository{installation: old}}
			manager := NewPluginManager(repo, pluginTokenEncryptor{}, cfg, host, nil)
			manager.localInstallations[old.ID] = old
			manifest := old.Manifest
			manifest.Description = "same immutable version with changed content"
			result, err := manager.Install(context.Background(), bytes.NewReader(buildPluginArchive(t, manifest, nil, "", nil)), nil)
			require.Nil(t, result)
			require.ErrorIs(t, err, ErrPluginAlreadyInstalled)
			require.Equal(t, http.StatusConflict, infraerrors.Code(err))
			require.Contains(t, infraerrors.Message(err), "独立更新")
			require.Zero(t, repo.installs)
			require.Equal(t, &before, repo.installation)
			require.Same(t, old, manager.localInstallations[old.ID])
			require.Equal(t, files, pluginInstallGuardFiles(t, cfg.Plugins.DataDir), "only the rejected candidate extraction is removed")
		})
	}
}

func TestPluginInstallDatabaseConflictCleansOnlyCandidate(t *testing.T) {
	cfg := testPluginConfig(t.TempDir(), true)
	repo := &pluginInstallGuardRepository{pluginTokenRepository: &pluginTokenRepository{}, lookupError: sql.ErrNoRows, installErr: ErrPluginAlreadyInstalled}
	manager := NewPluginManager(repo, pluginTokenEncryptor{}, cfg, PluginHostInfo{Version: "0.1.179"}, nil)
	result, err := manager.Install(context.Background(), bytes.NewReader(buildTestPluginArchive(t, nil, "")), nil)
	require.Nil(t, result)
	require.ErrorIs(t, err, ErrPluginAlreadyInstalled)
	require.Equal(t, 1, repo.installs)
	require.NotNil(t, repo.candidate)
	require.NoFileExists(t, repo.candidate.ArtifactPath)
	require.NoDirExists(t, repo.candidate.InstallPath)
	require.Empty(t, manager.localInstallations)
}

func TestPluginInstallInitialPackageRemainsDisabled(t *testing.T) {
	cfg := testPluginConfig(t.TempDir(), true)
	repo := &pluginInstallGuardRepository{pluginTokenRepository: &pluginTokenRepository{}, lookupError: sql.ErrNoRows}
	manager := NewPluginManager(repo, pluginTokenEncryptor{}, cfg, PluginHostInfo{Version: "0.1.179"}, nil)
	result, err := manager.Install(context.Background(), bytes.NewReader(buildTestPluginArchive(t, nil, "")), nil)
	require.NoError(t, err)
	require.Equal(t, 1, repo.installs)
	require.Equal(t, PluginStateDisabled, result.State)
	require.Equal(t, PluginUpdatePinned, result.UpdatePolicy)
	require.Len(t, result.Bindings, 1)
	require.False(t, result.Bindings[0].Enabled)
	require.Equal(t, 100, result.Bindings[0].RolloutPercent)
	require.FileExists(t, result.ArtifactPath)
	require.FileExists(t, result.BinaryPath)
}
