package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Every interface used by the real bootstrap/stage path is implemented. An
// unexpected write fails this fixture instead of dispatching through a nil port.
type completedBundleRaceRepository struct {
	t                             *testing.T
	current, onJournal, afterGet  *PluginInstallation
	prepared                      *PluginInstallation
	completed, removed, confirmed bool
	journalErr, getErr, listErr   error
	confirmationErr, importErr    error
	bundleReads, journalReads     int
	gets, lists, stages           int
	prepares, imports, completes  int
}

func cloneCompletedInstallation(value *PluginInstallation) *PluginInstallation {
	if value == nil {
		return nil
	}
	copy := *value
	copy.Bindings = append([]PluginBinding(nil), value.Bindings...)
	copy.ArtifactData = append([]byte(nil), value.ArtifactData...)
	return &copy
}

func (r *completedBundleRaceRepository) List(context.Context) ([]*PluginInstallation, error) {
	r.lists++
	if r.listErr != nil {
		return nil, r.listErr
	}
	if r.current == nil {
		return nil, nil
	}
	return []*PluginInstallation{cloneCompletedInstallation(r.current)}, nil
}
func (r *completedBundleRaceRepository) GetByKey(context.Context, string) (*PluginInstallation, error) {
	r.gets++
	if r.getErr != nil {
		return nil, r.getErr
	}
	if r.current == nil {
		return nil, sql.ErrNoRows
	}
	snapshot := cloneCompletedInstallation(r.current)
	if r.afterGet != nil {
		r.current = cloneCompletedInstallation(r.afterGet)
	}
	return snapshot, nil
}
func (r *completedBundleRaceRepository) GetByID(context.Context, int64) (*PluginInstallation, error) {
	return cloneCompletedInstallation(r.current), nil
}
func (r *completedBundleRaceRepository) BundleApplied(context.Context, string, string) (bool, error) {
	r.bundleReads++
	if r.bundleReads == 1 {
		return false, nil
	}
	return r.confirmed, r.confirmationErr
}
func (r *completedBundleRaceRepository) CompletedBundledPlugin(context.Context, string) (bool, error) {
	r.journalReads++
	if r.onJournal != nil {
		r.current = cloneCompletedInstallation(r.onJournal)
	}
	if r.removed {
		r.current = nil
	}
	return r.completed, r.journalErr
}
func (r *completedBundleRaceRepository) PrepareBundledPlugin(_ context.Context, candidate *PluginInstallation, _, _ string, _ bool, config string) (*PluginInstallation, error) {
	r.prepares++
	r.prepared = cloneCompletedInstallation(candidate)
	r.prepared.ID, r.prepared.Revision, r.prepared.RuntimeGeneration = 7, 5, 10
	r.prepared.ConfigEncrypted, r.prepared.UpdatePolicy = config, PluginUpdateBundled
	return cloneCompletedInstallation(r.prepared), nil
}
func (r *completedBundleRaceRepository) CompleteBundledPlugin(context.Context, int64, string) error {
	r.completes++
	return nil
}
func (*completedBundleRaceRepository) LegacyBundleSeed(_ context.Context, _, _ string, fallback PluginBundleSeed) (PluginBundleSeed, error) {
	return fallback, nil
}
func (r *completedBundleRaceRepository) ImportLegacyPluginState(context.Context, *PluginInstallation, string, json.RawMessage) error {
	r.imports++
	return r.importErr
}
func (r *completedBundleRaceRepository) StagePluginUpdate(context.Context, *PluginInstallation, *PluginInstallation, string) error {
	r.stages++
	r.t.Error("candidate process validation must not be reached in this metadata-only fixture")
	return errors.New("unexpected stage write")
}
func (r *completedBundleRaceRepository) unexpectedWrite() error {
	r.t.Error("unexpected repository mutation")
	return errors.New("unexpected repository mutation")
}
func (r *completedBundleRaceRepository) PendingPluginArtifact(context.Context, int64) ([]byte, error) {
	return nil, r.unexpectedWrite()
}
func (r *completedBundleRaceRepository) CommitPluginUpdate(context.Context, *PluginInstallation, *PluginInstallation) error {
	return r.unexpectedWrite()
}
func (r *completedBundleRaceRepository) SetPluginUpdatePolicy(context.Context, int64, int64, string) error {
	return r.unexpectedWrite()
}
func (r *completedBundleRaceRepository) Install(context.Context, *PluginInstallation, []PluginBinding) (*PluginInstallation, error) {
	return nil, r.unexpectedWrite()
}
func (r *completedBundleRaceRepository) GetArtifact(context.Context, int64) ([]byte, error) {
	return nil, r.unexpectedWrite()
}
func (r *completedBundleRaceRepository) Delete(context.Context, int64, string) error {
	return r.unexpectedWrite()
}
func (r *completedBundleRaceRepository) BeginEnable(context.Context, int64, string, string) error {
	return r.unexpectedWrite()
}
func (r *completedBundleRaceRepository) MarkRuntimeHealthy(context.Context, int64, string, string) error {
	return r.unexpectedWrite()
}
func (r *completedBundleRaceRepository) UpdateState(context.Context, int64, string, string, *time.Time, string, string) error {
	return r.unexpectedWrite()
}
func (r *completedBundleRaceRepository) UpdateConfig(context.Context, int64, string, string) error {
	return r.unexpectedWrite()
}
func (r *completedBundleRaceRepository) UpdateBindingsAndState(context.Context, int64, []PluginBinding, string, string, *time.Time, string, string) error {
	return r.unexpectedWrite()
}

var _ PluginRepository = (*completedBundleRaceRepository)(nil)
var _ PluginBundleRepository = (*completedBundleRaceRepository)(nil)
var _ PluginUpdateRepository = (*completedBundleRaceRepository)(nil)

func completedBundleFileSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		digest := sha256.Sum256(raw)
		files[path] = hex.EncodeToString(digest[:])
		return nil
	}))
	return files
}

func completedBundleFixture(t *testing.T, samePackage bool) (*PluginManager, *completedBundleRaceRepository, *PluginInstallation) {
	t.Helper()
	root := t.TempDir()
	cfg := testPluginConfig(root, true)
	host := PluginHostInfo{Version: "0.1.179"}
	manifest := testPluginManifest(nil)
	manifest.ID = "codexrip.completed-race"
	archive := buildPluginArchive(t, manifest, nil, "", nil)
	old, err := NewPluginPackageInstaller(cfg, host).Install(context.Background(), bytes.NewReader(archive), nil)
	require.NoError(t, err)
	old.ID, old.Revision, old.RuntimeGeneration = 7, 4, 9
	old.State, old.UpdatePolicy, old.ConfigEncrypted = PluginStateEnabled, PluginUpdateBundled, `ENC:{"saved":true}`
	old.Bindings = []PluginBinding{{Capability: PluginCapabilityOpenAIOAuthOutbound, Platform: PlatformOpenAI,
		AccountType: AccountTypeOAuth, Enabled: true, RolloutPercent: 37}}
	if !samePackage {
		manifest.Version = "0.1.1"
		// This rejection is after the real registry/List check but before any
		// runtime directory or process startup. It is not a missing-interface error.
		manifest.Requires.Sub2API = ">=9.0.0"
		archive = buildPluginArchive(t, manifest, nil, "", nil)
	}
	digest := sha256.Sum256(archive)
	bundle := PluginBundle{SchemaVersion: 1, HostVersion: host.Version, PublisherKeyID: "completed-fixture",
		PublisherPublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32)),
		Plugins:            []BundledPlugin{{ID: manifest.ID, Version: manifest.Version, File: "fixture.s2plugin", SHA256: hex.EncodeToString(digest[:])}}}
	raw, err := json.Marshal(bundle)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "fixture.s2plugin"), archive, 0600))
	path := filepath.Join(root, "lock.json")
	require.NoError(t, os.WriteFile(path, raw, 0600))
	repo := &completedBundleRaceRepository{t: t, current: cloneCompletedInstallation(old), completed: true}
	manager := NewPluginManager(repo, pluginTokenEncryptor{}, cfg, host, nil)
	manager.bundlePath = path
	return manager, repo, old
}

func assertCompletedCandidateCleaned(t *testing.T, manager *PluginManager, old *PluginInstallation, before map[string]string) {
	t.Helper()
	require.Equal(t, before, completedBundleFileSnapshot(t, manager.installer.RootDir()), "only the task's candidate files may disappear")
	require.FileExists(t, old.ArtifactPath)
	require.FileExists(t, old.BinaryPath)
	installations, err := filepath.Glob(filepath.Join(manager.installer.RootDir(), "installed", "*", "*"))
	require.NoError(t, err)
	require.Equal(t, []string{old.InstallPath}, installations)
	_, err = os.Stat(filepath.Join(manager.installer.RootDir(), "runtime"))
	require.ErrorIs(t, err, os.ErrNotExist, "no candidate process or socket directory may be started")
}

func TestCompletedBundleFreshPinAndUpdatingIntentCannotStage(t *testing.T) {
	for _, mode := range []string{"pinned", "updating", "unknown-policy"} {
		t.Run(mode, func(t *testing.T) {
			manager, repo, old := completedBundleFixture(t, false)
			before := completedBundleFileSnapshot(t, manager.installer.RootDir())
			fresh := cloneCompletedInstallation(old)
			fresh.Revision++
			if mode == "updating" {
				fresh.State = PluginStateUpdating
			} else {
				fresh.UpdatePolicy = mode
			}
			repo.onJournal = fresh
			err := manager.bootstrapBundle(context.Background())
			require.Zero(t, repo.lists, "a fresh non-bundled/updating snapshot must stop before real stage admission")
			require.NoError(t, err)
			require.Zero(t, repo.stages)
			require.Zero(t, repo.prepares)
			require.Equal(t, 1, repo.bundleReads)
			require.Equal(t, fresh, repo.current, "late operator intent must remain exactly unchanged")
			assertCompletedCandidateCleaned(t, manager, old, before)
		})
	}
}

func TestCompletedBundleFailuresCleanOnlyTheirCandidate(t *testing.T) {
	journalFailure := errors.New("fixture journal read failure")
	getFailure := errors.New("fixture installation read failure")
	registryFailure := errors.New("fixture registry read failure")
	for _, test := range []struct {
		name      string
		configure func(*completedBundleRaceRepository)
		want      error
		wantLists int
	}{
		{"journal", func(r *completedBundleRaceRepository) { r.journalErr = journalFailure }, journalFailure, 0},
		{"get", func(r *completedBundleRaceRepository) { r.getErr = getFailure }, getFailure, 0},
		{"registry", func(r *completedBundleRaceRepository) { r.listErr = registryFailure }, registryFailure, 1},
		{"eligible-validation", func(*completedBundleRaceRepository) {}, nil, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, repo, old := completedBundleFixture(t, false)
			before := completedBundleFileSnapshot(t, manager.installer.RootDir())
			test.configure(repo)
			err := manager.bootstrapBundle(context.Background())
			if test.want != nil {
				require.ErrorIs(t, err, test.want)
			} else {
				require.ErrorContains(t, err, "不满足插件要求", "eligible bundles must still take the real stage validation path")
			}
			require.Equal(t, test.wantLists, repo.lists)
			require.Zero(t, repo.stages)
			require.Zero(t, repo.prepares)
			require.Equal(t, 1, repo.bundleReads, "unknown failures are not treated as takeover")
			require.Equal(t, old, repo.current)
			assertCompletedCandidateCleaned(t, manager, old, before)
		})
	}
}

func TestCompletedBundleRemovalOrStageConflictNeedsConfirmedTakeover(t *testing.T) {
	readFailure := errors.New("fixture takeover read failure")
	for _, phase := range []string{"removed", "stage-conflict"} {
		for _, outcome := range []string{"confirmed", "unconfirmed", "read-failed"} {
			t.Run(phase+"/"+outcome, func(t *testing.T) {
				manager, repo, old := completedBundleFixture(t, false)
				before := completedBundleFileSnapshot(t, manager.installer.RootDir())
				if phase == "removed" {
					repo.removed = true
				} else {
					repo.afterGet = cloneCompletedInstallation(old)
					repo.afterGet.Revision++
					if outcome != "unconfirmed" {
						repo.afterGet.UpdatePolicy = PluginUpdatePinned
					}
				}
				repo.confirmed = outcome != "unconfirmed"
				if outcome == "read-failed" {
					repo.confirmationErr = readFailure
				}
				err := manager.bootstrapBundle(context.Background())
				switch outcome {
				case "confirmed":
					require.NoError(t, err)
				case "read-failed":
					require.ErrorIs(t, err, readFailure)
				default:
					if phase == "removed" {
						require.ErrorIs(t, err, sql.ErrNoRows)
					} else {
						require.ErrorIs(t, err, ErrPluginStateChanged)
					}
				}
				require.Equal(t, 2, repo.bundleReads)
				if phase == "removed" {
					require.Zero(t, repo.lists)
					require.Nil(t, repo.current)
				} else {
					require.Equal(t, 1, repo.lists)
					require.Equal(t, repo.afterGet, repo.current)
				}
				require.Zero(t, repo.stages)
				require.Zero(t, repo.prepares)
				assertCompletedCandidateCleaned(t, manager, old, before)
			})
		}
	}
}

func TestCompletedBundleSameDigestCleanupKeepsOldNonceFiles(t *testing.T) {
	manager, repo, old := completedBundleFixture(t, true)
	before := completedBundleFileSnapshot(t, manager.installer.RootDir())
	require.NoError(t, manager.bootstrapBundle(context.Background()))
	require.Zero(t, repo.lists)
	require.Zero(t, repo.stages)
	require.Equal(t, old, repo.current)
	assertCompletedCandidateCleaned(t, manager, old, before)
}

func TestIncompleteBundlePreparedPathsRemainOwnedAfterImportFailure(t *testing.T) {
	manager, repo, old := completedBundleFixture(t, false)
	repo.completed = false
	repo.importErr = errors.New("fixture interrupted migration")
	err := manager.bootstrapBundle(context.Background())
	require.ErrorIs(t, err, repo.importErr)
	require.Equal(t, 1, repo.prepares)
	require.Equal(t, 1, repo.imports)
	require.Zero(t, repo.completes)
	require.Zero(t, repo.gets)
	require.NotNil(t, repo.prepared)
	require.NotEqual(t, old.InstallPath, repo.prepared.InstallPath)
	require.FileExists(t, repo.prepared.ArtifactPath)
	require.FileExists(t, repo.prepared.BinaryPath)
	require.FileExists(t, old.ArtifactPath)
	require.FileExists(t, old.BinaryPath)
}

func TestCompletedBundleIdentityMismatchCleansUnownedCandidate(t *testing.T) {
	manager, repo, old := completedBundleFixture(t, false)
	raw, err := os.ReadFile(manager.bundlePath)
	require.NoError(t, err)
	var bundle PluginBundle
	require.NoError(t, json.Unmarshal(raw, &bundle))
	bundle.Plugins[0].Version = "0.1.2"
	raw, err = json.Marshal(bundle)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manager.bundlePath, raw, 0600))
	before := completedBundleFileSnapshot(t, manager.installer.RootDir())
	err = manager.bootstrapBundle(context.Background())
	require.ErrorContains(t, err, "bundled plugin identity mismatch")
	require.Zero(t, repo.journalReads)
	require.Zero(t, repo.gets)
	require.Zero(t, repo.lists)
	require.Zero(t, repo.stages)
	require.Zero(t, repo.prepares)
	require.Equal(t, old, repo.current)
	assertCompletedCandidateCleaned(t, manager, old, before)
}

func TestCompletedBundleCleanupErrorDoesNotErasePrimaryFailure(t *testing.T) {
	manager, repo, old := completedBundleFixture(t, false)
	outside := filepath.Join(t.TempDir(), "must-remain")
	require.NoError(t, os.WriteFile(outside, []byte("outside managed root"), 0600))
	original := errors.New("fixture journal failure")
	repo.journalErr = original
	// The production caller supplies unique installer paths. This direct helper
	// boundary fixture additionally verifies cleanup refusal is not swallowed.
	candidate := &PluginInstallation{ArtifactPath: outside, InstallPath: outside}
	handled, err := manager.handleCompletedBundledCandidate(context.Background(), repo, BundledPlugin{ID: old.PluginKey}, "fixture-bundle", candidate)
	require.True(t, handled)
	require.ErrorIs(t, err, original)
	require.ErrorContains(t, err, "拒绝删除插件根目录之外的路径")
	require.FileExists(t, outside)
	require.FileExists(t, old.ArtifactPath)
	require.FileExists(t, old.BinaryPath)
}
