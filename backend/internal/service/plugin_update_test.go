package service

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
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
	artifact     []byte
	commits      int
	pendingReads int
}

func (r *pendingResumePluginRepository) PendingPluginArtifact(context.Context, int64) ([]byte, error) {
	r.pendingReads++
	return r.artifact, nil
}

func (*pendingResumePluginRepository) StagePluginUpdate(context.Context, *PluginInstallation, *PluginInstallation, string) error {
	return errors.New("unexpected staging from a resume test")
}

func (*pendingResumePluginRepository) SetPluginUpdatePolicy(context.Context, int64, int64, string) error {
	return errors.New("unexpected policy change from a resume test")
}

var _ PluginUpdateRepository = (*pendingResumePluginRepository)(nil)

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
	require.ErrorContains(t, err, "启动插件进程", "a real candidate-start failure, not a missing update interface, must block promotion")
	require.Equal(t, 1, repo.pendingReads)
	require.Zero(t, repo.commits)
	require.Equal(t, old.PackageSHA256, repo.installation.PackageSHA256)
	require.Equal(t, old.ConfigEncrypted, repo.installation.ConfigEncrypted)
	require.Equal(t, old.Bindings, repo.installation.Bindings)
}

type pluginUpdateAdmissionRepository struct {
	*pluginTokenRepository
	artifact     []byte
	peers        []*PluginInstallation
	lists        int
	pendingReads int
	stages       int
	commits      int
}

func (r *pluginUpdateAdmissionRepository) List(ctx context.Context) ([]*PluginInstallation, error) {
	r.lists++
	all, err := r.pluginTokenRepository.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, peer := range r.peers {
		copy := *peer
		copy.Bindings = append([]PluginBinding(nil), peer.Bindings...)
		all = append(all, &copy)
	}
	return all, nil
}

func (r *pluginUpdateAdmissionRepository) StagePluginUpdate(context.Context, *PluginInstallation, *PluginInstallation, string) error {
	r.stages++
	return errors.New("unexpected plugin update stage")
}

func (r *pluginUpdateAdmissionRepository) PendingPluginArtifact(context.Context, int64) ([]byte, error) {
	r.pendingReads++
	return append([]byte(nil), r.artifact...), nil
}

func (r *pluginUpdateAdmissionRepository) CommitPluginUpdate(context.Context, *PluginInstallation, *PluginInstallation) error {
	r.commits++
	return errors.New("unexpected plugin update commit")
}

func (*pluginUpdateAdmissionRepository) SetPluginUpdatePolicy(context.Context, int64, int64, string) error {
	return errors.New("unexpected plugin update policy change")
}

var _ PluginUpdateRepository = (*pluginUpdateAdmissionRepository)(nil)

func newPluginUpdateAdmissionFixture(t *testing.T, state string) (*PluginManager, *pluginUpdateAdmissionRepository, *PluginInstallation, *PluginInstallation) {
	t.Helper()
	cfg := testPluginConfig(t.TempDir(), true)
	host := PluginHostInfo{Version: "0.1.179"}
	installer := NewPluginPackageInstaller(cfg, host)
	manifest := testPluginManifest(nil)
	manifest.Requires.ExtensionAPI = extensionv1.Version
	manifest.Capabilities = []PluginCapability{{ID: extensionv1.CapabilityAdmin, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth}}
	manifest.Operations = map[string][]string{extensionv1.CapabilityAdmin: {"admin.original"}}
	old, err := installer.Install(context.Background(), bytes.NewReader(buildPluginArchive(t, manifest, nil, "", nil)), nil)
	require.NoError(t, err)
	old.ID, old.Revision, old.RuntimeGeneration = 7, 4, 9
	old.State, old.ConfigEncrypted = state, `ENC:{"saved":true}`
	old.Bindings = []PluginBinding{{Capability: extensionv1.CapabilityAdmin, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true, RolloutPercent: 37}}

	manifest.Version = "0.1.1"
	manifest.Capabilities = append(manifest.Capabilities, PluginCapability{ID: extensionv1.CapabilityRecovery, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth})
	manifest.Dependencies = []extensionv1.Dependency{{Capability: extensionv1.CapabilityProvider, Platform: "cindy", AccountType: "apikey"}}
	manifest.Operations = map[string][]string{extensionv1.CapabilityAdmin: {"admin.replacement"}}
	candidate, err := installer.Install(context.Background(), bytes.NewReader(buildPluginArchive(t, manifest, nil, "", nil)), nil)
	require.NoError(t, err)
	repo := &pluginUpdateAdmissionRepository{
		pluginTokenRepository: &pluginTokenRepository{installation: old},
		artifact:              append([]byte(nil), candidate.ArtifactData...),
		peers: []*PluginInstallation{
			{ID: 8, PluginKey: "com.example.provider", State: PluginStateEnabled, Bindings: []PluginBinding{{Capability: extensionv1.CapabilityProvider, Platform: "cindy", AccountType: "apikey", Enabled: true, RolloutPercent: 100}}},
			{ID: 9, PluginKey: "com.example.admin", State: PluginStateEnabled,
				Manifest: PluginManifest{Operations: map[string][]string{extensionv1.CapabilityAdmin: {"admin.other"}}},
				Bindings: []PluginBinding{{Capability: extensionv1.CapabilityAdmin, Platform: "*", AccountType: "*", Enabled: true, RolloutPercent: 100}}},
		},
	}
	previous := *old
	previous.Bindings = append([]PluginBinding(nil), old.Bindings...)
	return NewPluginManager(repo, pluginTokenEncryptor{}, cfg, host, nil), repo, &previous, candidate
}

func TestPluginUpdateAdmissionRejectsChangedRegistry(t *testing.T) {
	for _, mode := range []string{"stage", "resume", "restart"} {
		t.Run(mode, func(t *testing.T) {
			for _, test := range []struct {
				name   string
				change func(*pluginUpdateAdmissionRepository)
				want   string
			}{
				{
					name: "dependency_disabled",
					change: func(repo *pluginUpdateAdmissionRepository) {
						repo.peers[0].State = PluginStateDisabled
						repo.peers[0].Bindings[0].Enabled = false
					},
					want: "missing enabled plugin dependency",
				},
				{
					name: "operation_owner_added",
					change: func(repo *pluginUpdateAdmissionRepository) {
						repo.peers[1].Manifest.Operations = map[string][]string{extensionv1.CapabilityAdmin: {"admin.replacement"}}
					},
					want: "extension operation has overlapping enabled owners",
				},
				{
					name: "registry_unavailable",
					change: func(repo *pluginUpdateAdmissionRepository) {
						repo.listErr = errors.New("current plugin registry unavailable")
					},
					want: "current plugin registry unavailable",
				},
			} {
				t.Run(test.name, func(t *testing.T) {
					state := PluginStateUpdating
					if mode == "stage" {
						state = PluginStateEnabled
					}
					manager, repo, previous, candidate := newPluginUpdateAdmissionFixture(t, state)
					require.NoError(t, manager.validatePluginReplacementRegistry(context.Background(), previous, candidate))
					require.Equal(t, 1, repo.lists)
					pending := append([]byte(nil), repo.artifact...)
					// Leave both local projections at the admitted state. Neither is
					// evidence that the database still permits this replacement.
					cached := &pluginExtensionRegistry{installations: map[int64]*PluginInstallation{previous.ID: previous}, runtimes: map[int64]*pluginRuntime{}}
					for _, peer := range repo.peers {
						copy := *peer
						copy.Bindings = append([]PluginBinding(nil), peer.Bindings...)
						cached.installations[copy.ID] = &copy
						manager.localInstallations[copy.ID] = &copy
					}
					manager.extensions.Store(cached)
					test.change(repo)
					if mode == "restart" {
						manager = NewPluginManager(repo, pluginTokenEncryptor{}, manager.cfg, manager.hostInfo, nil)
					}
					var err error
					if mode == "stage" {
						err = manager.stagePluginReplacement(context.Background(), previous, candidate, PluginUpdatePinned)
						require.Zero(t, repo.pendingReads)
					} else {
						var result *PluginInstallation
						result, err = manager.resumePluginUpdate(context.Background(), previous)
						require.Nil(t, result)
						require.Equal(t, 1, repo.pendingReads)
					}
					// The archive contains no runnable program. A matching admission
					// error proves the registry was checked before process startup.
					require.ErrorContains(t, err, test.want)
					require.Equal(t, 2, repo.lists)
					require.Zero(t, repo.stages)
					require.Zero(t, repo.commits)
					require.Equal(t, pending, repo.artifact)
					require.Equal(t, previous, repo.installation)
					require.FileExists(t, previous.ArtifactPath)
					require.FileExists(t, previous.BinaryPath)
				})
			}
		})
	}
}

func TestResumePluginUpdateAdmissionPreservesCurrentDisableIntent(t *testing.T) {
	manager, repo, previous, candidate := newPluginUpdateAdmissionFixture(t, PluginStateUpdating)
	require.NoError(t, manager.validatePluginReplacementRegistry(context.Background(), previous, candidate))
	require.Equal(t, previous.ID, candidate.ID)
	require.Equal(t, previous.ConfigEncrypted, candidate.ConfigEncrypted)
	bindings := make(map[string]PluginBinding)
	for _, binding := range candidate.Bindings {
		bindings[binding.Capability] = binding
	}
	require.True(t, bindings[extensionv1.CapabilityAdmin].Enabled)
	require.Equal(t, 37, bindings[extensionv1.CapabilityAdmin].RolloutPercent)
	require.False(t, bindings[extensionv1.CapabilityRecovery].Enabled, "new capabilities remain disabled")

	// A disable during draining changes the revision but leaves the pending
	// package in place. An older reconcile snapshot must not commit over it.
	repo.installation.Revision++
	repo.installation.Bindings[0].Enabled = false
	result, err := manager.resumePluginUpdate(context.Background(), previous)
	require.Nil(t, result)
	require.ErrorIs(t, err, ErrPluginStateChanged)
	require.Zero(t, repo.commits)
	require.Equal(t, previous.PackageSHA256, repo.installation.PackageSHA256)
	require.Equal(t, previous.ConfigEncrypted, repo.installation.ConfigEncrypted)
	require.Equal(t, previous.RuntimeGeneration, repo.installation.RuntimeGeneration)
	require.Equal(t, PluginStateUpdating, repo.installation.State)
	require.Equal(t, candidate.ArtifactData, repo.artifact)

	current, err := repo.GetByID(context.Background(), previous.ID)
	require.NoError(t, err)
	repo.peers[0].Bindings[0].Enabled = false
	repo.peers[1].Manifest.Operations = map[string][]string{extensionv1.CapabilityAdmin: {"admin.replacement"}}
	require.NoError(t, manager.validatePluginReplacementRegistry(context.Background(), current, candidate))
	require.False(t, hasEnabledPluginBinding(candidate.Bindings), "fresh admission must preserve the administrator's disable")
	require.Equal(t, current.ConfigEncrypted, candidate.ConfigEncrypted)
	require.True(t, previous.Bindings[0].Enabled, "the earlier snapshot is not rewritten")
	require.Equal(t, int64(5), repo.installation.Revision)
	require.FileExists(t, previous.ArtifactPath)
	require.FileExists(t, previous.BinaryPath)
}
