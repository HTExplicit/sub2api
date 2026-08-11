package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeRemoteSkillRegistryStore struct {
	snapshot        RemoteSkillRegistrySnapshot
	loadErr         error
	detail          RemoteSkillBundleVersionDetail
	job             RemoteSkillSyncJob
	completed       RemoteSkillCandidate
	published       RemoteSkillRegistrySnapshot
	publishErr      error
	publishCalls    int
	failedCode      string
	ensureSeed      RemoteSkillCandidate
	stage           string
	createdBy       int64
	createdProvided bool
	cleaned         bool
}

func (f *fakeRemoteSkillRegistryStore) EnsureRemoteSkillSeed(_ context.Context, candidate RemoteSkillCandidate) (RemoteSkillRegistrySnapshot, error) {
	f.ensureSeed = candidate
	return f.snapshot, nil
}
func (f *fakeRemoteSkillRegistryStore) LoadRemoteSkillSnapshot(context.Context) (RemoteSkillRegistrySnapshot, error) {
	return f.snapshot, f.loadErr
}
func (f *fakeRemoteSkillRegistryStore) ListRemoteSkillVersions(context.Context) ([]RemoteSkillBundleVersion, error) {
	if f.detail.ID != 0 {
		return []RemoteSkillBundleVersion{f.detail.RemoteSkillBundleVersion}, nil
	}
	return nil, nil
}
func (f *fakeRemoteSkillRegistryStore) GetRemoteSkillVersion(context.Context, int64) (RemoteSkillBundleVersionDetail, error) {
	if f.detail.ID == 0 {
		return RemoteSkillBundleVersionDetail{}, ErrRemoteSkillVersionNotFound
	}
	return f.detail, nil
}
func (f *fakeRemoteSkillRegistryStore) CreateRemoteSkillSyncJob(_ context.Context, actorID, _ int64, provided bool) (RemoteSkillSyncJob, error) {
	f.createdBy = actorID
	f.createdProvided = provided
	return f.job, nil
}
func (f *fakeRemoteSkillRegistryStore) UpdateRemoteSkillSyncJobStage(_ context.Context, _ int64, stage string) error {
	f.stage = stage
	return nil
}
func (f *fakeRemoteSkillRegistryStore) CompleteRemoteSkillSyncJob(_ context.Context, _ int64, candidate RemoteSkillCandidate) (RemoteSkillSyncJob, error) {
	f.completed = candidate
	return RemoteSkillSyncJob{ID: f.job.ID, Status: RemoteSkillSyncStatusSucceeded, CandidateBundleVersionID: candidate.Version.ID}, nil
}
func (f *fakeRemoteSkillRegistryStore) FailRemoteSkillSyncJob(_ context.Context, _ int64, code string) error {
	f.failedCode = code
	return nil
}
func (f *fakeRemoteSkillRegistryStore) GetRemoteSkillSyncJob(context.Context, int64) (RemoteSkillSyncJob, error) {
	return f.job, nil
}
func (f *fakeRemoteSkillRegistryStore) PublishRemoteSkillVersion(context.Context, int64, int64, int64) (RemoteSkillRegistrySnapshot, error) {
	f.publishCalls++
	return f.published, f.publishErr
}
func (f *fakeRemoteSkillRegistryStore) CleanupLegacyRemoteSkillData(context.Context) error {
	f.cleaned = true
	return nil
}

type fakeRemoteSkillRegistryFiles struct {
	seed       RemoteSkillCandidate
	seedErr    error
	installErr error
	loadErr    error
	installed  bool
	cleaned    bool
	candidates map[int64]RemoteSkillCandidate
}

func (f *fakeRemoteSkillRegistryFiles) LoadSeed(context.Context) (RemoteSkillCandidate, error) {
	return f.seed, f.seedErr
}
func (f *fakeRemoteSkillRegistryFiles) InstallCandidate(_ context.Context, _ RemoteSkillCandidate) error {
	f.installed = true
	return f.installErr
}
func (f *fakeRemoteSkillRegistryFiles) LoadCandidate(_ context.Context, version RemoteSkillBundleVersion, prompt RemoteSkillPromptVersion, changes []RemoteSkillFileChange) (RemoteSkillCandidate, error) {
	if f.loadErr != nil {
		return RemoteSkillCandidate{}, f.loadErr
	}
	if candidate, ok := f.candidates[version.ID]; ok {
		return candidate, nil
	}
	return RemoteSkillCandidate{Version: version, Prompt: prompt, FileChanges: changes, RawFiles: map[string][]byte{"SKILL.md": []byte("tree")}, EffectiveFiles: map[string][]byte{"SKILL.md": []byte("tree")}}, nil
}
func (f *fakeRemoteSkillRegistryFiles) CleanupLegacy(context.Context) error {
	f.cleaned = true
	return nil
}

type fakeRemoteSkillCandidateSource struct {
	candidate RemoteSkillCandidate
	err       error
	prompt    RemoteSkillPromptCapture
	active    *RemoteSkillCandidate
}

func (f *fakeRemoteSkillCandidateSource) Build(_ context.Context, prompt RemoteSkillPromptCapture, active *RemoteSkillCandidate) (RemoteSkillCandidate, error) {
	f.prompt = prompt
	f.active = active
	return f.candidate, f.err
}

func testRemoteSkillCandidate(t *testing.T, id, promptID int64, tree string) RemoteSkillCandidate {
	t.Helper()
	prompt, err := buildRemoteSkillPromptCapture([]byte(modelGangPromptCaptureFixture))
	require.NoError(t, err)
	raw := map[string][]byte{
		"RULES.md":     []byte(tree),
		"README_AI.md": []byte(tree),
		"SKILL.md":     []byte(tree),
	}
	candidate, err := buildPairedRemoteSkillCandidate(raw, rewriteRemoteSkillPublishedFiles(raw), prompt, nil, time.Unix(0, 0).UTC())
	require.NoError(t, err)
	candidate.Version.ID = id
	candidate.Version.PromptVersionID = promptID
	candidate.Prompt.ID = promptID
	return candidate
}

func testRemoteSkillRegistry(t *testing.T, active RemoteSkillCandidate) (*RemoteSkillRegistryService, *fakeRemoteSkillRegistryStore, *fakeRemoteSkillRegistryFiles) {
	t.Helper()
	active.Version.ID = 1
	active.Version.PromptVersionID = 1
	active.Prompt.ID = 1
	store := &fakeRemoteSkillRegistryStore{
		snapshot: RemoteSkillRegistrySnapshot{Revision: 7, Active: &active.Version, ActivePrompt: &active.Prompt, UpdatedAt: time.Now().UTC()},
		detail:   RemoteSkillBundleVersionDetail{RemoteSkillBundleVersion: active.Version, Prompt: active.Prompt, FileChanges: active.FileChanges},
		job:      RemoteSkillSyncJob{ID: 9, Status: RemoteSkillSyncStatusQueued},
	}
	files := &fakeRemoteSkillRegistryFiles{seed: active, candidates: map[int64]RemoteSkillCandidate{1: active}}
	source := &fakeRemoteSkillCandidateSource{}
	svc := NewRemoteSkillRegistryService(store, nil, files, source)
	require.NoError(t, svc.Initialize(context.Background()))
	return svc, store, files
}

func TestRemoteSkillRegistryStartupRequiresAndActivatesPairedSeed(t *testing.T) {
	seed := testRemoteSkillCandidate(t, 1, 1, "seed")
	svc, store, files := testRemoteSkillRegistry(t, seed)
	current := svc.CurrentSnapshot()
	require.Equal(t, int64(7), current.Revision)
	require.Equal(t, RemoteSkillUpstreamSourceID, current.Active.UpstreamSourceID)
	require.Equal(t, current.Active.PromptVersionID, current.ActivePrompt.ID)
	require.True(t, files.installed)
	require.True(t, files.cleaned)
	require.True(t, store.cleaned)
}

func TestRemoteSkillRegistrySyncCreatesCandidateWithoutPublishingAndReusesPromptByDefault(t *testing.T) {
	active := testRemoteSkillCandidate(t, 1, 1, "old")
	svc, store, _ := testRemoteSkillRegistry(t, active)
	candidate := testRemoteSkillCandidate(t, 2, 2, "new")
	source, ok := svc.source.(*fakeRemoteSkillCandidateSource)
	require.True(t, ok)
	source.candidate = candidate
	svc.runSyncJob(context.Background(), RemoteSkillSyncJob{ID: 9, CreatedBy: 42}, RemoteSkillPromptCapture{
		RawBody: []byte(active.Prompt.RawBody), EffectiveBody: []byte(active.Prompt.EffectiveBody), RawSHA256: active.Prompt.RawSHA256, EffectiveSHA256: active.Prompt.EffectiveSHA256,
	})
	require.Equal(t, "verifying_candidate", store.stage)
	require.Equal(t, int64(2), store.completed.Version.ID)
	require.Equal(t, int64(42), store.completed.Version.CreatedBy)
	require.Equal(t, int64(42), store.completed.Prompt.CreatedBy)
	require.Equal(t, active.Prompt.RawSHA256, source.prompt.RawSHA256)
	require.NotNil(t, source.active)
	require.Equal(t, "old", string(source.active.RawFiles["SKILL.md"]))
	require.Equal(t, int64(7), svc.CurrentSnapshot().Revision)
	require.Equal(t, int64(1), svc.CurrentSnapshot().Active.ID)
}

func TestRemoteSkillRegistryPromptUploadIsValidatedBeforeSyncJobCreation(t *testing.T) {
	active := testRemoteSkillCandidate(t, 1, 1, "old")
	svc, store, _ := testRemoteSkillRegistry(t, active)
	_, err := svc.StartSync(context.Background(), []byte("malformed"), 42, 7)
	require.ErrorIs(t, err, ErrBusinessSystemPromptInvalid)
	require.Equal(t, int64(0), store.createdBy)
}

func TestRemoteSkillRegistrySyncFailureDoesNotSwitchActivePair(t *testing.T) {
	active := testRemoteSkillCandidate(t, 1, 1, "active")
	svc, store, _ := testRemoteSkillRegistry(t, active)
	source, ok := svc.source.(*fakeRemoteSkillCandidateSource)
	require.True(t, ok)
	source.err = ErrBusinessSystemPromptBundleUnavailable

	svc.runSyncJob(context.Background(), RemoteSkillSyncJob{ID: 9, CreatedBy: 42}, RemoteSkillPromptCapture{
		RawBody: []byte(active.Prompt.RawBody), EffectiveBody: []byte(active.Prompt.EffectiveBody),
		RawSHA256: active.Prompt.RawSHA256, EffectiveSHA256: active.Prompt.EffectiveSHA256,
	})

	require.Equal(t, "source_unavailable", store.failedCode)
	require.Zero(t, store.completed.Version.ID)
	require.Equal(t, int64(7), svc.CurrentSnapshot().Revision)
	require.Equal(t, int64(1), svc.CurrentSnapshot().Active.ID)
}

func TestRemoteSkillRegistryPublishHonorsRevisionCASAndKeepsPreviousOnConflict(t *testing.T) {
	active := testRemoteSkillCandidate(t, 1, 1, "old")
	svc, store, files := testRemoteSkillRegistry(t, active)
	candidate := testRemoteSkillCandidate(t, 2, 2, "new")
	store.detail = RemoteSkillBundleVersionDetail{RemoteSkillBundleVersion: candidate.Version, Prompt: candidate.Prompt, FileChanges: candidate.FileChanges}
	files.candidates[2] = candidate
	store.published = RemoteSkillRegistrySnapshot{Revision: 8, Active: &candidate.Version, ActivePrompt: &candidate.Prompt, UpdatedAt: time.Now().UTC()}
	store.publishErr = ErrBusinessSystemPromptRevisionConflict
	_, err := svc.PublishVersion(context.Background(), 2, 7, 42)
	require.ErrorIs(t, err, ErrBusinessSystemPromptRevisionConflict)
	require.Equal(t, int64(7), svc.CurrentSnapshot().Revision)

	store.publishErr = nil
	published, err := svc.PublishVersion(context.Background(), 2, 7, 42)
	require.NoError(t, err)
	require.Equal(t, int64(8), published.Revision)
	require.Equal(t, int64(2), svc.CurrentSnapshot().Active.ID)
}

func TestRemoteSkillRegistryPublishValidatesPairBeforeDatabaseCAS(t *testing.T) {
	active := testRemoteSkillCandidate(t, 1, 1, "old")
	svc, store, files := testRemoteSkillRegistry(t, active)
	candidate := testRemoteSkillCandidate(t, 2, 2, "new")
	candidate.Prompt.EffectiveSHA256 = strings.Repeat("f", 64)
	store.detail = RemoteSkillBundleVersionDetail{RemoteSkillBundleVersion: candidate.Version, Prompt: candidate.Prompt, FileChanges: candidate.FileChanges}
	files.candidates[2] = candidate

	_, err := svc.PublishVersion(context.Background(), 2, 7, 42)
	require.ErrorIs(t, err, ErrBusinessSystemPromptUnavailable)
	require.Zero(t, store.publishCalls)
	require.Equal(t, int64(1), svc.CurrentSnapshot().Active.ID)
}

func TestRemoteSkillRegistryReloadRetainsLastGoodPairedPublication(t *testing.T) {
	active := testRemoteSkillCandidate(t, 1, 1, "good")
	svc, store, files := testRemoteSkillRegistry(t, active)
	bad := testRemoteSkillCandidate(t, 2, 2, "bad")
	store.snapshot = RemoteSkillRegistrySnapshot{Revision: 8, Active: &bad.Version, ActivePrompt: &bad.Prompt, UpdatedAt: time.Now().UTC()}
	store.detail = RemoteSkillBundleVersionDetail{RemoteSkillBundleVersion: bad.Version, Prompt: bad.Prompt, FileChanges: bad.FileChanges}
	files.loadErr = errors.New("candidate missing")
	require.Error(t, svc.Reload(context.Background()))
	current := svc.CurrentSnapshot()
	require.Equal(t, int64(7), current.Revision)
	require.Equal(t, int64(1), current.Active.ID)
	require.True(t, current.Degraded)
}
