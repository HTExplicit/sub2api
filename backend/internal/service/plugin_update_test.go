package service

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"github.com/stretchr/testify/require"
)

func TestPluginUpdateRejectsChangedVersionAndSignatureBeforeExecution(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	cfg := testPluginConfig(t.TempDir(), false)
	cfg.Plugins.TrustedPublishers["update-test"] = base64.StdEncoding.EncodeToString(public)
	archive := buildTestPluginArchive(t, private, "update-test")
	installer := NewPluginPackageInstaller(cfg, PluginHostInfo{Version: "0.1.179"})
	installed, err := installer.Install(context.Background(), bytes.NewReader(archive), nil)
	require.NoError(t, err)
	installed.ID, installed.Revision = 7, 4
	repo := &pluginTokenRepository{installation: installed}
	manager := NewPluginManager(repo, pluginTokenEncryptor{}, cfg, PluginHostInfo{Version: "0.1.179"}, nil)
	// Repeating exactly the accepted package is a read, even with an old revision.
	result, err := manager.Update(context.Background(), 7, 3, "previous-digest", bytes.NewReader(archive), nil)
	require.NoError(t, err)
	require.Equal(t, installed.PackageSHA256, result.PackageSHA256)
	manifest := installed.Manifest
	manifest.Description = "changed content without a version increment"
	changed := buildPluginArchive(t, manifest, private, "update-test", nil)
	_, err = manager.Update(context.Background(), 7, 4, installed.PackageSHA256, bytes.NewReader(changed), nil)
	require.ErrorContains(t, err, "newer immutable version")
	manifest.Version = "1.0.1"
	changed = buildPluginArchive(t, manifest, private, "update-test", nil)
	_, err = manager.Update(context.Background(), 7, 3, installed.PackageSHA256, bytes.NewReader(changed), nil)
	require.ErrorIs(t, err, ErrPluginStateChanged)
	_, otherKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	invalid := buildPluginArchive(t, manifest, otherKey, "update-test", nil)
	_, err = manager.Update(context.Background(), 7, 4, installed.PackageSHA256, bytes.NewReader(invalid), nil)
	require.ErrorContains(t, err, "签名校验失败")
	require.Equal(t, installed, repo.installation)
}

type conflictingPluginConfigRepository struct{ *pluginConfigRepository }

func (*conflictingPluginConfigRepository) UpdateConfig(context.Context, int64, string, string) error {
	return ErrPluginStateChanged
}

func TestPluginConfigurationConflictDoesNotApplyOrCancelWork(t *testing.T) {
	installation := &PluginInstallation{ID: 9, Revision: 3}
	repo := &conflictingPluginConfigRepository{&pluginConfigRepository{installation: installation}}
	client := &normalizingPluginClient{normalized: []byte(`{"enabled":true}`)}
	runtime := &pluginRuntime{installation: installation, api: client}
	work, release, err := runtime.bindPolicyContext(context.Background())
	require.NoError(t, err)
	defer release()
	manager := &PluginManager{repo: repo, encryptor: pluginTokenEncryptor{}, runtimes: map[int64]*pluginRuntime{9: runtime}}
	_, err = manager.SaveConfig(context.Background(), 9, []byte(`{}`))
	require.ErrorIs(t, err, ErrPluginStateChanged)
	require.Empty(t, client.applied)
	require.NoError(t, work.Err())
}

type pendingResumePluginRepository struct {
	*pluginTokenRepository
	artifact []byte
	commits  int
}

func (r *pendingResumePluginRepository) PendingPluginArtifact(context.Context, int64) ([]byte, error) {
	return r.artifact, nil
}

func (r *pendingResumePluginRepository) CommitPluginUpdate(context.Context, *PluginInstallation, *PluginInstallation) error {
	r.commits++
	return nil
}

func TestResumePluginUpdateRevalidatesCandidateBeforeCommit(t *testing.T) {
	cfg := testPluginConfig(t.TempDir(), true)
	installer := NewPluginPackageInstaller(cfg, PluginHostInfo{Version: "0.1.179"})
	oldArchive := buildTestPluginArchive(t, nil, "")
	old, err := installer.Install(context.Background(), bytes.NewReader(oldArchive), nil)
	require.NoError(t, err)
	old.ID = 7
	old.State = PluginStateUpdating
	old.Revision = 4
	old.RuntimeGeneration = 9
	old.ConfigEncrypted = "ENC:{}"
	old.Bindings = []PluginBinding{{Capability: extensionv1.CapabilityAdmin, Platform: "*", AccountType: "*", Enabled: true, RolloutPercent: 100}}

	manifest := testPluginManifest(nil)
	manifest.Version = "0.1.1"
	pending := buildPluginArchive(t, manifest, nil, "", nil)
	repo := &pendingResumePluginRepository{
		pluginTokenRepository: &pluginTokenRepository{installation: old},
		artifact:              pending,
	}
	manager := NewPluginManager(repo, pluginTokenEncryptor{}, cfg, PluginHostInfo{Version: "0.1.179"}, nil)

	_, err = manager.resumePluginUpdate(context.Background(), old)
	require.Error(t, err, "the fixture candidate cannot start and must fail candidate validation")
	require.Zero(t, repo.commits)
	require.Equal(t, old.PackageSHA256, repo.installation.PackageSHA256)
	require.Equal(t, old.ConfigEncrypted, repo.installation.ConfigEncrypted)
	require.Equal(t, old.Bindings, repo.installation.Bindings)
}
