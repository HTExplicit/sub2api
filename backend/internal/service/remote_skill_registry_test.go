package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoteSkillClientInstallUsesPinnedTwoStageGitAcquisition(t *testing.T) {
	metadata := (&RemoteSkillRegistryService{}).ClientInstallMetadata()

	require.Equal(t, "codexrip-reverse-skill", metadata.SkillName)
	require.Equal(t, "https://codexrip.vip/skills/reverse-skill/current.json", metadata.DescriptorURL)
	for name, installer := range map[string]RemoteSkillClientInstaller{
		"powershell": metadata.PowerShell,
		"python":     metadata.Python,
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, "verified_git_sparse_checkout", installer.Strategy)
			require.Equal(t, "https://github.com/HTExplicit/sub2api.git", installer.RepositoryURL)
			require.Equal(t, "v0.1.171-codexrip.7", installer.RepositoryRef)
			require.Equal(t, "176dad47dd049b34d45a032d889a0dc11405a39e", installer.RepositoryCommit)
			require.Contains(t, installer.BootstrapPath, installer.BootstrapSHA256)
			require.Contains(t, installer.AcquireCommand, installer.RepositoryURL)
			require.Contains(t, installer.AcquireCommand, installer.RepositoryRef)
			require.Contains(t, installer.AcquireCommand, installer.RepositoryCommit)
			require.Contains(t, installer.AcquireCommand, installer.BootstrapPath)
			require.Contains(t, installer.AcquireCommand, installer.BootstrapSHA256)
			require.Contains(t, installer.ExecuteCommand, installer.BootstrapPath)
			require.Contains(t, installer.ExecuteCommand, installer.BootstrapSHA256)
			require.Contains(t, installer.ExecuteCommand, metadata.DescriptorURL)
			require.NotContains(t, installer.ExecuteCommand, "& pwsh")
			require.NotContains(t, strings.ToLower(installer.AcquireCommand), "invoke-webrequest")
			require.NotContains(t, strings.ToLower(installer.AcquireCommand), "curl")
			require.NotContains(t, strings.ToLower(installer.AcquireCommand), "http://")
			require.NotContains(t, strings.ToLower(installer.AcquireCommand), "remove-item")
			require.NotContains(t, strings.ToLower(installer.AcquireCommand), "rm -rf")
			if name == "powershell" {
				require.Contains(t, installer.AcquireCommand, "GetTempPath")
				require.Contains(t, installer.AcquireCommand, "temporary checkout path rejected")
				require.Contains(t, installer.AcquireCommand, "NewGuid")
			} else {
				require.Contains(t, installer.AcquireCommand, "mktemp -d")
			}
		})
	}
}

type fakeRemoteSkillRegistryStore struct {
	snapshot   RemoteSkillRegistrySnapshot
	loadErr    error
	job        RemoteSkillSyncJob
	completed  RemoteSkillBundleVersion
	published  RemoteSkillRegistrySnapshot
	publishErr error
	failedCode string
	ensureSeed RemoteSkillBundleVersion
	stage      string
}

func (f *fakeRemoteSkillRegistryStore) EnsureRemoteSkillSeed(_ context.Context, version RemoteSkillBundleVersion) error {
	f.ensureSeed = version
	return nil
}
func (f *fakeRemoteSkillRegistryStore) LoadRemoteSkillSnapshot(context.Context) (RemoteSkillRegistrySnapshot, error) {
	return f.snapshot, f.loadErr
}
func (f *fakeRemoteSkillRegistryStore) ListRemoteSkillVersions(context.Context) ([]RemoteSkillBundleVersion, error) {
	return nil, nil
}
func (f *fakeRemoteSkillRegistryStore) GetRemoteSkillVersion(context.Context, int64) (RemoteSkillBundleVersion, error) {
	return f.completed, nil
}
func (f *fakeRemoteSkillRegistryStore) CreateRemoteSkillSyncJob(context.Context, int64, int64) (RemoteSkillSyncJob, error) {
	return f.job, nil
}
func (f *fakeRemoteSkillRegistryStore) UpdateRemoteSkillSyncJobStage(_ context.Context, _ int64, stage string) error {
	f.stage = stage
	return nil
}
func (f *fakeRemoteSkillRegistryStore) CompleteRemoteSkillSyncJob(_ context.Context, _ int64, version RemoteSkillBundleVersion) (RemoteSkillSyncJob, error) {
	f.completed = version
	return RemoteSkillSyncJob{ID: f.job.ID, Status: RemoteSkillSyncStatusSucceeded, CandidateBundleVersionID: 12}, nil
}
func (f *fakeRemoteSkillRegistryStore) FailRemoteSkillSyncJob(_ context.Context, _ int64, code string) error {
	f.failedCode = code
	return nil
}
func (f *fakeRemoteSkillRegistryStore) GetRemoteSkillSyncJob(context.Context, int64) (RemoteSkillSyncJob, error) {
	return f.job, nil
}
func (f *fakeRemoteSkillRegistryStore) PublishRemoteSkillVersion(context.Context, int64, int64, int64) (RemoteSkillRegistrySnapshot, error) {
	return f.published, f.publishErr
}

type fakeRemoteSkillRegistryFiles struct {
	seed        RemoteSkillBundleVersion
	seedErr     error
	installErr  error
	validateErr error
	activateErr error
	installed   bool
	activated   RemoteSkillRegistrySnapshot
	bundle      *BusinessSystemPromptBundle
}

func (f *fakeRemoteSkillRegistryFiles) LoadSeed(context.Context) (RemoteSkillBundleVersion, error) {
	return f.seed, f.seedErr
}
func (f *fakeRemoteSkillRegistryFiles) InstallCandidate(context.Context, RemoteSkillCandidate) error {
	f.installed = true
	return f.installErr
}
func (f *fakeRemoteSkillRegistryFiles) ValidateVersion(context.Context, RemoteSkillBundleVersion) error {
	return f.validateErr
}
func (f *fakeRemoteSkillRegistryFiles) PreparePublic(context.Context, RemoteSkillBundleVersion) error {
	return f.validateErr
}
func (f *fakeRemoteSkillRegistryFiles) Activate(_ context.Context, snapshot RemoteSkillRegistrySnapshot) error {
	f.activated = snapshot
	return f.activateErr
}
func (f *fakeRemoteSkillRegistryFiles) LoadManifest(context.Context, RemoteSkillBundleVersion) (BusinessSystemPromptBundleManifest, error) {
	return BusinessSystemPromptBundleManifest{}, nil
}
func (f *fakeRemoteSkillRegistryFiles) LoadBundle(context.Context, RemoteSkillBundleVersion) (*BusinessSystemPromptBundle, error) {
	return f.bundle, f.validateErr
}

type fakeRemoteSkillCandidateSource struct {
	candidate RemoteSkillCandidate
	err       error
}

func (f *fakeRemoteSkillCandidateSource) Build(context.Context, *BusinessSystemPromptBundleManifest) (RemoteSkillCandidate, error) {
	return f.candidate, f.err
}

func TestRemoteSkillRegistrySyncCreatesCandidateWithoutPublishing(t *testing.T) {
	store := &fakeRemoteSkillRegistryStore{
		snapshot: RemoteSkillRegistrySnapshot{Revision: 7, Active: &RemoteSkillBundleVersion{ID: 1, ManifestSHA256: "old"}},
		job:      RemoteSkillSyncJob{ID: 9, Status: RemoteSkillSyncStatusQueued},
	}
	files := &fakeRemoteSkillRegistryFiles{seedErr: ErrRemoteSkillSeedUnavailable}
	source := &fakeRemoteSkillCandidateSource{candidate: RemoteSkillCandidate{Version: RemoteSkillBundleVersion{
		BundleID: BusinessSystemPromptRemoteSkillBundleID, ManifestSHA256: "new", ArchiveSHA256: "archive",
	}}}
	svc := NewRemoteSkillRegistryService(store, nil, files, source)
	require.NoError(t, svc.Initialize(context.Background()))
	svc.runSyncJob(context.Background(), store.job)

	require.True(t, files.installed)
	require.Equal(t, "new", store.completed.ManifestSHA256)
	current := svc.CurrentSnapshot()
	require.Equal(t, int64(7), current.Revision)
	require.Equal(t, "old", current.Active.ManifestSHA256)
	require.NotNil(t, files.activated.Active)
	require.Equal(t, "old", files.activated.Active.ManifestSHA256)
}

func TestRemoteSkillRegistryPublishHonorsCASConflict(t *testing.T) {
	store := &fakeRemoteSkillRegistryStore{
		snapshot:   RemoteSkillRegistrySnapshot{Revision: 3, Active: &RemoteSkillBundleVersion{ID: 1}},
		completed:  RemoteSkillBundleVersion{ID: 4, ManifestSHA256: "candidate"},
		publishErr: ErrBusinessSystemPromptRevisionConflict,
	}
	svc := NewRemoteSkillRegistryService(store, nil, &fakeRemoteSkillRegistryFiles{seedErr: ErrRemoteSkillSeedUnavailable}, &fakeRemoteSkillCandidateSource{})
	require.NoError(t, svc.Initialize(context.Background()))
	_, err := svc.PublishVersion(context.Background(), 4, 2, 8)
	require.ErrorIs(t, err, ErrBusinessSystemPromptRevisionConflict)
	require.Equal(t, int64(3), svc.CurrentSnapshot().Revision)
}

func TestRemoteSkillRegistryReloadKeepsLastKnownGood(t *testing.T) {
	store := &fakeRemoteSkillRegistryStore{snapshot: RemoteSkillRegistrySnapshot{
		Revision: 5, Active: &RemoteSkillBundleVersion{ID: 1, ManifestSHA256: "good"},
	}}
	files := &fakeRemoteSkillRegistryFiles{seedErr: ErrRemoteSkillSeedUnavailable}
	svc := NewRemoteSkillRegistryService(store, nil, files, &fakeRemoteSkillCandidateSource{})
	require.NoError(t, svc.Initialize(context.Background()))

	store.snapshot = RemoteSkillRegistrySnapshot{Revision: 6, Active: &RemoteSkillBundleVersion{ID: 2, ManifestSHA256: "bad"}}
	files.validateErr = errors.New("candidate files missing")
	require.Error(t, svc.Reload(context.Background()))
	current := svc.CurrentSnapshot()
	require.Equal(t, int64(5), current.Revision)
	require.Equal(t, "good", current.Active.ManifestSHA256)
	require.True(t, current.Degraded)
}
