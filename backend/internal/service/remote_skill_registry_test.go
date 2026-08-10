package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoteSkillClientInstallUsesContentAddressedHTTPSAcquisition(t *testing.T) {
	metadata := (&RemoteSkillRegistryService{}).ClientInstallMetadata()

	require.Equal(t, "codexrip-reverse-skill", metadata.SkillName)
	require.Equal(t, RemoteSkillSourceGitHubOfficial, metadata.SourceID)
	require.Equal(t, RemoteSkillGitHubRoot, metadata.RemoteRoot)
	require.Equal(t, "https://codexrip.vip/skills/reverse-skill/current.json", metadata.DescriptorURL)
	raw, err := json.Marshal(metadata)
	require.NoError(t, err)
	for _, forbidden := range []string{"repository_url", "repository_ref", "repository_commit", "bootstrap_path", "github.com"} {
		require.NotContains(t, strings.ToLower(string(raw)), forbidden)
	}

	for name, expected := range map[string]struct {
		installer RemoteSkillClientInstaller
		filename  string
	}{
		"powershell": {metadata.PowerShell, "bootstrap-reverse-skill.ps1"},
		"python":     {metadata.Python, "bootstrap-reverse-skill.py"},
	} {
		t.Run(name, func(t *testing.T) {
			installer := expected.installer
			require.Equal(t, "verified_https_content_addressed", installer.Strategy)
			parsed, err := url.Parse(installer.BootstrapURL)
			require.NoError(t, err)
			require.Equal(t, "https", parsed.Scheme)
			require.Equal(t, "codexrip.vip", parsed.Hostname())
			require.Equal(t, expected.filename, parsed.Path[strings.LastIndex(parsed.Path, "/")+1:])
			require.Contains(t, parsed.Path, installer.BootstrapSHA256)
			require.Contains(t, installer.AcquireCommand, installer.BootstrapURL)
			require.Contains(t, installer.AcquireCommand, installer.BootstrapSHA256)
			require.Contains(t, installer.ExecuteCommand, installer.BootstrapSHA256)
			require.Contains(t, installer.ExecuteCommand, metadata.DescriptorURL)
			for _, command := range []string{installer.AcquireCommand, installer.ExecuteCommand} {
				lower := strings.ToLower(command)
				require.NotContains(t, lower, "github.com")
				require.NotContains(t, lower, "git clone")
				require.NotContains(t, lower, "http://")
				require.NotContains(t, lower, "curl ")
				require.NotContains(t, lower, "wget ")
				require.NotContains(t, lower, "invoke-expression")
				require.NotContains(t, lower, "| sh")
				require.NotContains(t, lower, "| bash")
				require.NotContains(t, lower, "| pwsh")
			}
			if name == "powershell" {
				require.Contains(t, installer.AcquireCommand, "GetTempPath")
				require.Contains(t, installer.AcquireCommand, "RequestUri")
				require.Contains(t, installer.ExecuteCommand, "Get-FileHash")
			} else {
				require.Contains(t, installer.AcquireCommand, "geturl")
				for _, command := range []string{installer.AcquireCommand, installer.ExecuteCommand} {
					require.Contains(t, command, "os.geteuid()")
					require.Contains(t, command, "stat.S_IMODE")
					require.Contains(t, command, "0o700")
				}
				require.Contains(t, installer.ExecuteCommand, "os.O_NOFOLLOW")
				require.Contains(t, installer.ExecuteCommand, "os.fstat")
				require.Contains(t, installer.ExecuteCommand, "exec(compile(raw")
				require.NotContains(t, installer.ExecuteCommand, `python3 "$path"`)
			}
		})
	}
}

type fakeRemoteSkillRegistryStore struct {
	snapshot        RemoteSkillRegistrySnapshot
	loadErr         error
	job             RemoteSkillSyncJob
	completed       RemoteSkillBundleVersion
	published       RemoteSkillRegistrySnapshot
	publishErr      error
	failedCode      string
	ensureSeed      RemoteSkillBundleVersion
	stage           string
	createdSourceID string
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
func (f *fakeRemoteSkillRegistryStore) CreateRemoteSkillSyncJob(_ context.Context, sourceID string, _ int64, _ int64) (RemoteSkillSyncJob, error) {
	f.createdSourceID = sourceID
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
	sourceID  string
}

func (f *fakeRemoteSkillCandidateSource) Build(_ context.Context, sourceID string, _ *BusinessSystemPromptBundleManifest) (RemoteSkillCandidate, error) {
	f.sourceID = sourceID
	return f.candidate, f.err
}

func TestRemoteSkillRegistrySyncCreatesCandidateWithoutPublishing(t *testing.T) {
	store := &fakeRemoteSkillRegistryStore{
		snapshot: RemoteSkillRegistrySnapshot{Revision: 7, Active: &RemoteSkillBundleVersion{ID: 1, SourceID: RemoteSkillSourceGitHubOfficial, RemoteRoot: RemoteSkillGitHubRoot, ManifestSHA256: "old"}},
		job:      RemoteSkillSyncJob{ID: 9, Status: RemoteSkillSyncStatusQueued, SourceID: RemoteSkillSourceMoxinggang},
	}
	files := &fakeRemoteSkillRegistryFiles{seedErr: ErrRemoteSkillSeedUnavailable}
	source := &fakeRemoteSkillCandidateSource{candidate: RemoteSkillCandidate{Version: RemoteSkillBundleVersion{
		BundleID: BusinessSystemPromptRemoteSkillBundleID, SourceID: RemoteSkillSourceMoxinggang, RemoteRoot: RemoteSkillMoxinggangRoot, ManifestSHA256: "new", ArchiveSHA256: "archive",
	}}}
	svc := NewRemoteSkillRegistryService(store, nil, files, source)
	require.NoError(t, svc.Initialize(context.Background()))
	svc.runSyncJob(context.Background(), store.job)

	require.True(t, files.installed)
	require.Equal(t, RemoteSkillSourceMoxinggang, source.sourceID)
	require.Equal(t, "new", store.completed.ManifestSHA256)
	current := svc.CurrentSnapshot()
	require.Equal(t, int64(7), current.Revision)
	require.Equal(t, "old", current.Active.ManifestSHA256)
	require.Equal(t, RemoteSkillSourceGitHubOfficial, current.Active.SourceID)
	require.NotNil(t, files.activated.Active)
	require.Equal(t, "old", files.activated.Active.ManifestSHA256)
}

func TestRemoteSkillRegistryStartSyncDefaultsSourceAndQueuesRequestedSource(t *testing.T) {
	store := &fakeRemoteSkillRegistryStore{
		snapshot: RemoteSkillRegistrySnapshot{Revision: 7},
		job:      RemoteSkillSyncJob{ID: 9, Status: RemoteSkillSyncStatusQueued},
	}
	svc := NewRemoteSkillRegistryService(store, nil, &fakeRemoteSkillRegistryFiles{seedErr: ErrRemoteSkillSeedUnavailable}, &fakeRemoteSkillCandidateSource{})
	require.NoError(t, svc.Start(context.Background()))
	t.Cleanup(svc.Stop)

	job, err := svc.StartSync(context.Background(), "", 42, 7)
	require.NoError(t, err)
	require.Equal(t, int64(9), job.ID)
	require.Equal(t, RemoteSkillSourceGitHubOfficial, store.createdSourceID)
}

func TestRemoteSkillRegistryClientMetadataChangesOnlyAfterPublish(t *testing.T) {
	github := RemoteSkillBundleVersion{ID: 1, SourceID: RemoteSkillSourceGitHubOfficial, RemoteRoot: RemoteSkillGitHubRoot, ManifestSHA256: "old"}
	moxinggang := RemoteSkillBundleVersion{ID: 2, SourceID: RemoteSkillSourceMoxinggang, RemoteRoot: RemoteSkillMoxinggangRoot, ManifestSHA256: "new"}
	store := &fakeRemoteSkillRegistryStore{
		snapshot:  RemoteSkillRegistrySnapshot{Revision: 3, Active: &github},
		completed: moxinggang,
		published: RemoteSkillRegistrySnapshot{Revision: 4, Active: &moxinggang},
	}
	svc := NewRemoteSkillRegistryService(store, nil, &fakeRemoteSkillRegistryFiles{seedErr: ErrRemoteSkillSeedUnavailable}, &fakeRemoteSkillCandidateSource{})
	require.NoError(t, svc.Initialize(context.Background()))

	before := svc.ClientInstallMetadata()
	require.Equal(t, RemoteSkillSourceGitHubOfficial, before.SourceID)
	require.Equal(t, RemoteSkillGitHubRoot, before.RemoteRoot)

	_, err := svc.PublishVersion(context.Background(), 2, 3, 8)
	require.NoError(t, err)
	after := svc.ClientInstallMetadata()
	require.Equal(t, RemoteSkillSourceMoxinggang, after.SourceID)
	require.Equal(t, RemoteSkillMoxinggangRoot, after.RemoteRoot)
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
