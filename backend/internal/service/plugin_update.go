package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"golang.org/x/mod/semver"
)

func samePluginRuntime(a, b *PluginInstallation) bool {
	return a != nil && b != nil && a.ID == b.ID && a.RuntimeGeneration == b.RuntimeGeneration && samePluginPackage(a, b)
}

type stagedPluginHost struct{}

func (stagedPluginHost) Call(context.Context, extensionv1.HostInvocation) (extensionv1.Result, error) {
	return extensionv1.Result{}, errors.New("candidate validation does not grant host operations")
}

// Validation runs without storage, credentials, jobs or an activation binding.
// ApplyConfig is intentionally deferred until after the atomic promotion.
func (m *PluginManager) validatePluginCandidate(ctx context.Context, candidate *PluginInstallation) error {
	compatibility := EvaluatePluginCompatibility(candidate.Manifest, m.hostInfo)
	if !compatibility.Compatible {
		return errors.New(compatibility.Message)
	}
	socketDir := filepath.Join(m.installer.RootDir(), "runtime")
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		return err
	}
	host := newPluginHostServiceServer(candidate.PluginKey, nil, nil)
	host.extension = stagedPluginHost{}
	call, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	runtime, err := startPluginRuntime(call, candidate, time.Duration(m.cfg.Plugins.StartTimeoutSeconds)*time.Second, socketDir, host)
	if err != nil {
		return err
	}
	defer runtime.kill()
	config, err := m.decryptConfig(candidate)
	if err != nil {
		return err
	}
	result, err := runtime.api.ValidateConfig(call, &pluginv1.ValidateConfigRequest{ConfigJson: config})
	if err != nil || result == nil || !result.Valid {
		return errors.New("replacement plugin does not accept the saved configuration")
	}
	if len(result.NormalizedConfigJson) > pluginConfigMaxBytes || (len(result.NormalizedConfigJson) > 0 && !json.Valid(result.NormalizedConfigJson)) {
		return errors.New("invalid replacement configuration result")
	}
	return runtime.checkHealth(call)
}

func (m *PluginManager) Update(ctx context.Context, id, revision int64, digest string, reader io.Reader, actor *int64) (*PluginInstallation, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	previous, err := m.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	candidate, err := m.installer.Install(ctx, reader, actor)
	if err != nil {
		return nil, err
	}
	defer func() { _ = m.cleanupInstallationFiles(candidate) }()
	if candidate.PluginKey != previous.PluginKey {
		return nil, errors.New("replacement plugin identity differs")
	}
	if previous.State == PluginStateUpdating {
		if store, ok := m.repo.(PluginUpdateRepository); ok {
			pending, err := store.PendingPluginArtifact(ctx, id)
			if err != nil {
				return nil, err
			}
			sum := sha256.Sum256(pending)
			if hex.EncodeToString(sum[:]) == candidate.PackageSHA256 {
				return m.Get(ctx, id)
			}
		}
		return nil, ErrPluginStateChanged
	}
	if previous.PackageSHA256 == candidate.PackageSHA256 {
		// A retry after the first response was lost must not stage another update.
		return m.Get(ctx, id)
	}
	if revision <= 0 || previous.Revision != revision || digest == "" || previous.PackageSHA256 != digest {
		return nil, ErrPluginStateChanged
	}
	if semver.Compare(normalizeSemver(candidate.Version), normalizeSemver(previous.Version)) <= 0 {
		return nil, errors.New("plugin update requires a newer immutable version")
	}
	if err = m.stagePluginReplacement(ctx, previous, candidate, PluginUpdatePinned); err != nil {
		return nil, err
	}
	m.pausePluginForUpdate(id)
	return m.Get(ctx, id)
}

func (m *PluginManager) stagePluginReplacement(ctx context.Context, previous, candidate *PluginInstallation, policy string) error {
	if candidate.Version == previous.Version && candidate.PackageSHA256 != previous.PackageSHA256 {
		return errors.New("an immutable plugin version cannot contain different packages")
	}
	store, ok := m.repo.(PluginUpdateRepository)
	if !ok {
		return errors.New("independent plugin updates are unavailable")
	}
	if previous.State == PluginStateUpdating {
		return ErrPluginStateChanged
	}
	if err := m.validatePluginReplacementRegistry(ctx, previous, candidate); err != nil {
		return err
	}
	if err := m.validatePluginCandidate(ctx, candidate); err != nil {
		return err
	}
	// Other hosts can change bindings while the candidate process is checked.
	if err := m.validatePluginReplacementRegistry(ctx, previous, candidate); err != nil {
		return err
	}
	return store.StagePluginUpdate(ctx, previous, candidate, policy)
}

// Admission must use persisted desired bindings, not the local runtime or UI
// registry. Keep the caller's revision/config fence so a concurrent disable is
// retried with its new intent instead of silently enabling the replacement.
func (m *PluginManager) validatePluginReplacementRegistry(ctx context.Context, previous, candidate *PluginInstallation) error {
	all, err := m.repo.List(ctx)
	if err != nil {
		return err
	}
	for index, installation := range all {
		if installation.ID == previous.ID {
			if installation.Revision != previous.Revision || installation.State != previous.State ||
				installation.ConfigEncrypted != previous.ConfigEncrypted || !samePluginRuntime(installation, previous) {
				return ErrPluginStateChanged
			}
			candidate.ID, candidate.ConfigEncrypted = installation.ID, installation.ConfigEncrypted
			candidate.Bindings = PluginReplacementBindings(installation.Bindings, candidate.Manifest)
			all[index] = candidate
			return validatePluginRegistry(all)
		}
	}
	return ErrPluginStateChanged
}

// Keep the desired contributions visible as unavailable while only this
// process drains. Other plugins and the host continue serving requests.
func (m *PluginManager) pausePluginForUpdate(id int64) {
	m.mu.Lock()
	runtime := m.runtimes[id]
	delete(m.runtimes, id)
	if registry := m.extensions.Load(); registry != nil {
		if installation := registry.installations[id]; installation != nil {
			m.replaceExtensionRegistrationLocked(installation, nil)
		}
	}
	if route := m.route.Load(); route != nil && route.pluginID == id {
		m.route.Store(&pluginRoute{pluginID: id, rolloutPercent: route.rolloutPercent, unavailable: "plugin update in progress"})
	}
	if runtime != nil {
		runtime.beginDrain()
	}
	m.mu.Unlock()
	if runtime != nil {
		go runtime.drain(10 * time.Second)
	}
}

func (m *PluginManager) resumePluginUpdate(ctx context.Context, previous *PluginInstallation) (*PluginInstallation, error) {
	m.pausePluginForUpdate(previous.ID)
	store, ok := m.repo.(PluginUpdateRepository)
	if !ok {
		return nil, errors.New("plugin update repository unavailable")
	}
	artifact, err := store.PendingPluginArtifact(ctx, previous.ID)
	if err != nil {
		return nil, err
	}
	candidate, err := m.installer.Install(ctx, bytes.NewReader(artifact), previous.InstalledBy)
	if err != nil {
		return nil, err
	}
	retained := false
	defer func() {
		if !retained {
			_ = m.cleanupInstallationFiles(candidate)
		}
	}()
	if candidate.PluginKey != previous.PluginKey {
		return nil, errors.New("pending plugin identity differs")
	}
	if !candidate.Compatibility.Compatible {
		return nil, errors.New(candidate.Compatibility.Message)
	}
	if err = m.validatePluginReplacementRegistry(ctx, previous, candidate); err != nil {
		return nil, err
	}
	if err = m.validatePluginCandidate(ctx, candidate); err != nil {
		return nil, err
	}
	if err = m.validatePluginReplacementRegistry(ctx, previous, candidate); err != nil {
		return nil, err
	}
	if err = store.CommitPluginUpdate(ctx, previous, candidate); err != nil {
		if errors.Is(err, ErrPluginUpdateWaiting) || errors.Is(err, ErrPluginStateChanged) {
			return nil, nil
		}
		return nil, err
	}
	retained = true
	current, err := m.repo.GetByID(ctx, previous.ID)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.localInstallations[current.ID] = mergeLocalInstallation(candidate, current)
	m.mu.Unlock()
	return current, nil
}

// Following the image is explicit: selecting it may replace a newer pinned
// version with the image's compatible package. No account data is rolled back.
func (m *PluginManager) FollowBundledVersion(ctx context.Context, id, revision int64, digest string) (*PluginInstallation, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	previous, err := m.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if revision <= 0 || previous.Revision != revision || previous.PackageSHA256 != digest {
		return nil, ErrPluginStateChanged
	}
	store, ok := m.repo.(PluginUpdateRepository)
	if !ok {
		return nil, errors.New("plugin updates unavailable")
	}
	path := m.bundlePath
	if path == "" {
		executable, err := os.Executable()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(filepath.Dir(executable), "bundled-plugins", "lock.json")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("this plugin has no bundled version")
	}
	var bundle PluginBundle
	if json.Unmarshal(raw, &bundle) != nil || bundle.SchemaVersion != 1 || normalizeSemver(bundle.HostVersion) != normalizeSemver(m.hostInfo.Version) {
		return nil, errors.New("invalid host bundle")
	}
	for _, entry := range bundle.Plugins {
		if entry.ID != previous.PluginKey {
			continue
		}
		if !safePluginRelativePath(entry.File) || filepath.Base(entry.File) != entry.File {
			return nil, errors.New("invalid bundled package path")
		}
		artifact, err := os.ReadFile(filepath.Join(filepath.Dir(path), entry.File))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(artifact)
		if hex.EncodeToString(sum[:]) != entry.SHA256 {
			return nil, errors.New("bundled package digest mismatch")
		}
		if previous.PackageSHA256 == entry.SHA256 {
			if err = store.SetPluginUpdatePolicy(ctx, id, revision, PluginUpdateBundled); err != nil {
				return nil, err
			}
			return m.Get(ctx, id)
		}
		candidate, err := m.installer.Install(ctx, bytes.NewReader(artifact), previous.InstalledBy)
		if err != nil {
			return nil, err
		}
		defer func() { _ = m.cleanupInstallationFiles(candidate) }()
		if candidate.PluginKey != previous.PluginKey || candidate.Version != entry.Version {
			return nil, errors.New("bundled plugin identity mismatch")
		}
		if err = m.stagePluginReplacement(ctx, previous, candidate, PluginUpdateBundled); err != nil {
			return nil, err
		}
		m.pausePluginForUpdate(id)
		return m.Get(ctx, id)
	}
	return nil, errors.New("this plugin has no bundled version")
}
