package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	RemoteSkillRegistryRootEnv       = "SUB2API_REMOTE_SKILL_REGISTRY_ROOT"
	RemoteSkillRegistryDefaultRoot   = "/app/skill-registry"
	RemoteSkillRegistryReleaseEnv    = "SUB2API_REMOTE_SKILL_RELEASE_ROOT"
	RemoteSkillRegistryReleaseRoot   = "/app/skill-registry-release"
	remoteSkillSeedDescriptorName    = "seed-descriptor.json"
	remoteSkillPublicDescriptorLimit = 256 << 10
	remoteSkillArchiveMaxBytes       = 128 << 20
)

type RemoteSkillPublicDescriptor struct {
	SchemaVersion   int                         `json:"schema_version"`
	BundleID        string                      `json:"bundle_id"`
	Revision        int64                       `json:"revision"`
	SourceCommit    string                      `json:"source_commit"`
	OverlaySHA256   string                      `json:"overlay_sha256"`
	ManifestSHA256  string                      `json:"manifest_sha256"`
	ArchiveSHA256   string                      `json:"archive_sha256"`
	ManifestURL     string                      `json:"manifest_url"`
	ArchiveURL      string                      `json:"archive_url"`
	FilesBaseURL    string                      `json:"files_base_url"`
	CoreFiles       []string                    `json:"core_files"`
	FileCount       int                         `json:"file_count"`
	TotalBytes      int64                       `json:"total_bytes"`
	PublishedAt     time.Time                   `json:"published_at"`
	BootstrapPolicy string                      `json:"bootstrap_policy"`
	Bootstraps      RemoteSkillPublicBootstraps `json:"bootstraps"`
}

type RemoteSkillPublicBootstrap struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type RemoteSkillPublicBootstraps struct {
	PowerShell RemoteSkillPublicBootstrap `json:"powershell"`
	Python     RemoteSkillPublicBootstrap `json:"python"`
}

type RemoteSkillRegistryFilesystem struct {
	root        string
	releaseRoot string
}

func DefaultRemoteSkillRegistryRoot() string {
	if value := strings.TrimSpace(os.Getenv(RemoteSkillRegistryRootEnv)); value != "" {
		return value
	}
	return RemoteSkillRegistryDefaultRoot
}

func NewRemoteSkillRegistryFilesystem(root string) *RemoteSkillRegistryFilesystem {
	return NewRemoteSkillRegistryFilesystemWithReleaseRoot(root, "")
}

func NewRemoteSkillRegistryFilesystemWithReleaseRoot(root, releaseRoot string) *RemoteSkillRegistryFilesystem {
	if strings.TrimSpace(root) == "" {
		root = DefaultRemoteSkillRegistryRoot()
	}
	if strings.TrimSpace(releaseRoot) == "" {
		releaseRoot = strings.TrimSpace(os.Getenv(RemoteSkillRegistryReleaseEnv))
		if releaseRoot == "" {
			releaseRoot = RemoteSkillRegistryReleaseRoot
		}
	}
	return &RemoteSkillRegistryFilesystem{root: filepath.Clean(root), releaseRoot: filepath.Clean(releaseRoot)}
}

func (f *RemoteSkillRegistryFilesystem) LoadSeed(ctx context.Context) (RemoteSkillBundleVersion, error) {
	if err := ctx.Err(); err != nil {
		return RemoteSkillBundleVersion{}, err
	}
	if err := f.installReleaseBootstraps(ctx); err != nil {
		return RemoteSkillBundleVersion{}, fmt.Errorf("install release bootstraps: %w", err)
	}
	if err := f.installReleaseSeedPackage(ctx); err != nil {
		return RemoteSkillBundleVersion{}, fmt.Errorf("install release seed package: %w", err)
	}
	seedRoot := filepath.Join(f.root, "private", "seed")
	version, err := validateRemoteSkillSeedPackageRoot(seedRoot)
	if errors.Is(err, os.ErrNotExist) {
		return RemoteSkillBundleVersion{}, ErrRemoteSkillSeedUnavailable
	}
	if err != nil {
		return RemoteSkillBundleVersion{}, err
	}
	if err := f.materializeSeedVersion(ctx, seedRoot, version); err != nil {
		return RemoteSkillBundleVersion{}, err
	}
	if err := f.ValidateVersion(ctx, version); err != nil {
		return RemoteSkillBundleVersion{}, err
	}
	return version, nil
}

func (f *RemoteSkillRegistryFilesystem) installReleaseSeedPackage(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	destination := filepath.Join(f.root, "private", "seed")
	source := filepath.Join(f.releaseRoot, "seed")
	if _, err := os.Lstat(filepath.Join(source, remoteSkillSeedDescriptorName)); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := validateRemoteSkillSeedPackageRoot(source); err != nil {
		return fmt.Errorf("validate release seed: %w", err)
	}
	sourceDescriptor, err := readRemoteSkillBoundedFile(filepath.Join(source, remoteSkillSeedDescriptorName), remoteSkillPublicDescriptorLimit)
	if err != nil {
		return err
	}
	if currentDescriptor, currentErr := readRemoteSkillBoundedFile(filepath.Join(destination, remoteSkillSeedDescriptorName), remoteSkillPublicDescriptorLimit); currentErr == nil {
		if bytes.Equal(currentDescriptor, sourceDescriptor) {
			return nil
		}
	} else if !errors.Is(currentErr, os.ErrNotExist) {
		return currentErr
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".seed-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := copyRemoteSkillTree(source, staging); err != nil {
		return err
	}
	if _, err := validateRemoteSkillSeedPackageRoot(staging); err != nil {
		return fmt.Errorf("validate staged release seed: %w", err)
	}
	if _, err := os.Lstat(destination); errors.Is(err, os.ErrNotExist) {
		return os.Rename(staging, destination)
	} else if err != nil {
		return err
	}
	backup, err := os.MkdirTemp(parent, ".seed-old-")
	if err != nil {
		return err
	}
	if err := os.Remove(backup); err != nil {
		return err
	}
	if err := os.Rename(destination, backup); err != nil {
		return err
	}
	if err := os.Rename(staging, destination); err != nil {
		if restoreErr := os.Rename(backup, destination); restoreErr != nil {
			return fmt.Errorf("replace release seed: %v (restore failed: %v)", err, restoreErr)
		}
		return err
	}
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("remove replaced release seed: %w", err)
	}
	return nil
}

func validateRemoteSkillSeedPackageRoot(root string) (RemoteSkillBundleVersion, error) {
	descriptorRaw, err := readRemoteSkillBoundedFile(filepath.Join(root, remoteSkillSeedDescriptorName), remoteSkillPublicDescriptorLimit)
	if err != nil {
		return RemoteSkillBundleVersion{}, err
	}
	var descriptor RemoteSkillPublicDescriptor
	if err := json.Unmarshal(descriptorRaw, &descriptor); err != nil {
		return RemoteSkillBundleVersion{}, fmt.Errorf("%w: invalid seed descriptor", ErrBusinessSystemPromptBundleInvalid)
	}
	version := remoteSkillVersionFromDescriptor(descriptor)
	if err := validateRemoteSkillVersionMetadata(version); err != nil {
		return RemoteSkillBundleVersion{}, err
	}
	if err := validateRemoteSkillPublicBootstraps(descriptor.Bootstraps); err != nil {
		return RemoteSkillBundleVersion{}, err
	}
	manifestRaw, err := readRemoteSkillBoundedFile(filepath.Join(root, BusinessSystemPromptBundleManifestName), businessSystemPromptBundleMaxManifestBytes)
	if err != nil || hashBusinessSystemPromptBundleBytes(manifestRaw) != version.ManifestSHA256 {
		return RemoteSkillBundleVersion{}, fmt.Errorf("%w: seed manifest mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	var manifest BusinessSystemPromptBundleManifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return RemoteSkillBundleVersion{}, fmt.Errorf("%w: invalid seed manifest", ErrBusinessSystemPromptBundleInvalid)
	}
	archiveRaw, err := readRemoteSkillBoundedFile(filepath.Join(root, remoteSkillArchiveName(version.ManifestSHA256)), remoteSkillArchiveMaxBytes)
	if err != nil || hashBusinessSystemPromptBundleBytes(archiveRaw) != version.ArchiveSHA256 {
		return RemoteSkillBundleVersion{}, fmt.Errorf("%w: seed archive mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	files, err := remoteSkillFilesFromArchive(archiveRaw, manifestRaw, manifest)
	if err != nil {
		return RemoteSkillBundleVersion{}, err
	}
	for _, required := range []string{remoteSkillClientSkillPath, remoteSkillClientOpenAIPath} {
		if _, ok := files[required]; !ok {
			return RemoteSkillBundleVersion{}, fmt.Errorf("%w: native Skill entry missing", ErrBusinessSystemPromptBundleInvalid)
		}
	}
	return version, nil
}

func (f *RemoteSkillRegistryFilesystem) materializeSeedVersion(ctx context.Context, seedRoot string, version RemoteSkillBundleVersion) error {
	destination := f.privateVersionRoot(version.ManifestSHA256)
	if _, err := os.Lstat(destination); err == nil {
		return f.ValidateVersion(ctx, version)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	manifestBytes, err := readRemoteSkillBoundedFile(filepath.Join(seedRoot, BusinessSystemPromptBundleManifestName), businessSystemPromptBundleMaxManifestBytes)
	if err != nil {
		return err
	}
	archiveBytes, err := readRemoteSkillBoundedFile(filepath.Join(seedRoot, remoteSkillArchiveName(version.ManifestSHA256)), remoteSkillArchiveMaxBytes)
	if err != nil {
		return err
	}
	var manifest BusinessSystemPromptBundleManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return fmt.Errorf("%w: invalid seed manifest", ErrBusinessSystemPromptBundleInvalid)
	}
	files, err := remoteSkillFilesFromArchive(archiveBytes, manifestBytes, manifest)
	if err != nil {
		return err
	}
	candidate := RemoteSkillCandidate{
		Version: version, Manifest: manifest, ManifestBytes: manifestBytes,
		ArchiveBytes: archiveBytes, Files: files,
	}
	return f.InstallCandidate(ctx, candidate)
}

func (f *RemoteSkillRegistryFilesystem) installReleaseBootstraps(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sourceRoot := filepath.Join(f.releaseRoot, "bootstrap")
	entries, err := os.ReadDir(sourceRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	installed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			return fmt.Errorf("%w: bootstrap root contains a file", ErrBusinessSystemPromptBundleInvalid)
		}
		directory := filepath.Join(sourceRoot, entry.Name())
		files, err := os.ReadDir(directory)
		if err != nil {
			return err
		}
		if len(files) == 0 {
			continue
		}
		if len(files) != 1 || files[0].IsDir() || (files[0].Name() != "bootstrap-reverse-skill.ps1" && files[0].Name() != "bootstrap-reverse-skill.py") {
			return fmt.Errorf("%w: bootstrap directory shape invalid", ErrBusinessSystemPromptBundleInvalid)
		}
		raw, err := readRemoteSkillBoundedFile(filepath.Join(directory, files[0].Name()), 1<<20)
		if err != nil {
			return err
		}
		if entry.Name() != hashBusinessSystemPromptBundleBytes(raw) {
			return fmt.Errorf("%w: bootstrap digest mismatch", ErrBusinessSystemPromptBundleInvalid)
		}
		destination := filepath.Join(f.root, "public", "bootstrap", entry.Name())
		if err := installRemoteSkillBootstrap(directory, destination, entry.Name(), files[0].Name()); err != nil {
			return err
		}
		installed++
	}
	if installed != 2 {
		return fmt.Errorf("%w: release must contain exactly two bootstraps", ErrBusinessSystemPromptBundleInvalid)
	}
	return nil
}

func (f *RemoteSkillRegistryFilesystem) InstallCandidate(ctx context.Context, candidate RemoteSkillCandidate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRemoteSkillCandidate(candidate); err != nil {
		return err
	}
	destination := f.privateVersionRoot(candidate.Version.ManifestSHA256)
	if _, err := os.Stat(destination); err == nil {
		return f.ValidateVersion(ctx, candidate.Version)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(destination), ".candidate-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := writeRemoteSkillCandidate(staging, candidate); err != nil {
		return err
	}
	if err := validateRemoteSkillVersionRoot(staging, candidate.Version); err != nil {
		return err
	}
	if err := os.Rename(staging, destination); err != nil {
		if _, statErr := os.Stat(destination); statErr == nil {
			return f.ValidateVersion(ctx, candidate.Version)
		}
		return err
	}
	return nil
}

func (f *RemoteSkillRegistryFilesystem) ValidateVersion(ctx context.Context, version RemoteSkillBundleVersion) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRemoteSkillVersionMetadata(version); err != nil {
		return err
	}
	return validateRemoteSkillVersionRoot(f.privateVersionRoot(version.ManifestSHA256), version)
}

func (f *RemoteSkillRegistryFilesystem) PreparePublic(ctx context.Context, version RemoteSkillBundleVersion) error {
	if err := f.ValidateVersion(ctx, version); err != nil {
		return err
	}
	source := f.privateVersionRoot(version.ManifestSHA256)
	destination := f.publicVersionRoot(version.ManifestSHA256)
	if _, err := os.Stat(destination); err == nil {
		return validateRemoteSkillVersionRoot(destination, version)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(destination), ".publish-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := copyRemoteSkillTree(source, staging); err != nil {
		return err
	}
	if err := validateRemoteSkillVersionRoot(staging, version); err != nil {
		return err
	}
	if err := os.Chmod(staging, 0o755); err != nil {
		return err
	}
	if err := os.Rename(staging, destination); err != nil {
		if _, statErr := os.Stat(destination); statErr == nil {
			return validateRemoteSkillVersionRoot(destination, version)
		}
		return err
	}
	return nil
}

func (f *RemoteSkillRegistryFilesystem) Activate(ctx context.Context, snapshot RemoteSkillRegistrySnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot.Active == nil {
		return fmt.Errorf("%w: no active remote skill bundle", ErrBusinessSystemPromptBundleUnavailable)
	}
	if err := validateRemoteSkillVersionRoot(f.publicVersionRoot(snapshot.Active.ManifestSHA256), *snapshot.Active); err != nil {
		return err
	}
	manifest, err := f.LoadManifest(ctx, *snapshot.Active)
	if err != nil {
		return err
	}
	publishedAt := snapshot.UpdatedAt.UTC()
	if snapshot.Active.PublishedAt != nil {
		publishedAt = snapshot.Active.PublishedAt.UTC()
	}
	baseURL := "https://codexrip.vip/skills/reverse-skill/versions/" + snapshot.Active.ManifestSHA256
	descriptor := RemoteSkillPublicDescriptor{
		SchemaVersion: 1, BundleID: snapshot.Active.BundleID, Revision: snapshot.Revision,
		SourceCommit: snapshot.Active.SourceCommit, OverlaySHA256: snapshot.Active.OverlaySHA256,
		ManifestSHA256: snapshot.Active.ManifestSHA256, ArchiveSHA256: snapshot.Active.ArchiveSHA256,
		ManifestURL:  baseURL + "/" + BusinessSystemPromptBundleManifestName,
		ArchiveURL:   baseURL + "/" + remoteSkillArchiveName(snapshot.Active.ManifestSHA256),
		FilesBaseURL: baseURL + "/", CoreFiles: append([]string(nil), manifest.CoreFiles...),
		FileCount: snapshot.Active.FileCount, TotalBytes: snapshot.Active.TotalBytes,
		PublishedAt: publishedAt, BootstrapPolicy: "download_verify_native_skill_atomic_replace",
		Bootstraps: remoteSkillPublicBootstraps(),
	}
	raw, err := json.Marshal(descriptor)
	if err != nil {
		return err
	}
	root := filepath.Join(f.root, "public", "reverse-skill")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(root, ".current-*.json")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if _, err := temp.Write(raw); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	current := filepath.Join(root, "current.json")
	if err := os.Rename(tempName, current); err != nil {
		// Windows cannot replace an existing file with os.Rename. Production is
		// Linux; this fallback keeps local contract tests portable.
		if removeErr := os.Remove(current); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return err
		}
		if retryErr := os.Rename(tempName, current); retryErr != nil {
			return retryErr
		}
	}
	return nil
}

func (f *RemoteSkillRegistryFilesystem) LoadManifest(ctx context.Context, version RemoteSkillBundleVersion) (BusinessSystemPromptBundleManifest, error) {
	if err := ctx.Err(); err != nil {
		return BusinessSystemPromptBundleManifest{}, err
	}
	raw, err := readRemoteSkillBoundedFile(filepath.Join(f.privateVersionRoot(version.ManifestSHA256), BusinessSystemPromptBundleManifestName), businessSystemPromptBundleMaxManifestBytes)
	if err != nil {
		return BusinessSystemPromptBundleManifest{}, err
	}
	if hashBusinessSystemPromptBundleBytes(raw) != version.ManifestSHA256 {
		return BusinessSystemPromptBundleManifest{}, fmt.Errorf("%w: manifest digest mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	var manifest BusinessSystemPromptBundleManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return BusinessSystemPromptBundleManifest{}, fmt.Errorf("%w: parse manifest", ErrBusinessSystemPromptBundleInvalid)
	}
	if err := validateBusinessSystemPromptBundleManifest(manifest); err != nil {
		return BusinessSystemPromptBundleManifest{}, err
	}
	return cloneBusinessSystemPromptBundleManifest(manifest), nil
}

func (f *RemoteSkillRegistryFilesystem) LoadBundle(ctx context.Context, version RemoteSkillBundleVersion) (*BusinessSystemPromptBundle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := f.ValidateVersion(ctx, version); err != nil {
		return nil, err
	}
	return loadBusinessSystemPromptBundleIdentity(f.privateVersionRoot(version.ManifestSHA256), version.BundleID, version.ManifestSHA256)
}

func (f *RemoteSkillRegistryFilesystem) privateVersionRoot(hash string) string {
	return filepath.Join(f.root, "private", "versions", strings.ToLower(strings.TrimSpace(hash)))
}

func (f *RemoteSkillRegistryFilesystem) publicVersionRoot(hash string) string {
	return filepath.Join(f.root, "public", "reverse-skill", "versions", strings.ToLower(strings.TrimSpace(hash)))
}

func validateRemoteSkillCandidate(candidate RemoteSkillCandidate) error {
	if err := validateRemoteSkillVersionMetadata(candidate.Version); err != nil {
		return err
	}
	if candidate.Manifest.BundleID != BusinessSystemPromptRemoteSkillBundleID || candidate.Version.BundleID != candidate.Manifest.BundleID {
		return fmt.Errorf("%w: candidate bundle id mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	if err := validateBusinessSystemPromptBundleManifest(candidate.Manifest); err != nil {
		return err
	}
	if hashBusinessSystemPromptBundleBytes(candidate.ManifestBytes) != candidate.Version.ManifestSHA256 ||
		hashBusinessSystemPromptBundleBytes(candidate.ArchiveBytes) != candidate.Version.ArchiveSHA256 {
		return fmt.Errorf("%w: candidate digest mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	if len(candidate.ArchiveBytes) <= 0 || len(candidate.ArchiveBytes) > remoteSkillArchiveMaxBytes {
		return fmt.Errorf("%w: candidate archive size invalid", ErrBusinessSystemPromptBundleInvalid)
	}
	if len(candidate.Files) != len(candidate.Manifest.Files) {
		return fmt.Errorf("%w: candidate file set mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	for _, entry := range candidate.Manifest.Files {
		data, ok := candidate.Files[entry.Path]
		if !ok || len(data) != entry.ByteLength || !equalHexDigest(entry.SHA256, data) {
			return fmt.Errorf("%w: candidate file mismatch", ErrBusinessSystemPromptBundleInvalid)
		}
	}
	return verifyRemoteSkillArchive(candidate.ArchiveBytes, candidate.ManifestBytes, candidate.Manifest)
}

func validateRemoteSkillVersionMetadata(version RemoteSkillBundleVersion) error {
	if version.BundleID != BusinessSystemPromptRemoteSkillBundleID || len(version.SourceCommit) != 40 || !isLowerHexSHA256(version.SourceCommit+strings.Repeat("0", 24)) ||
		len(version.OverlaySHA256) != 64 || !isLowerHexSHA256(version.OverlaySHA256) ||
		len(version.ManifestSHA256) != 64 || !isLowerHexSHA256(version.ManifestSHA256) ||
		len(version.ArchiveSHA256) != 64 || !isLowerHexSHA256(version.ArchiveSHA256) ||
		version.FileCount <= 0 || version.FileCount > 2000 || version.TotalBytes <= 0 || version.TotalBytes > 256<<20 {
		return fmt.Errorf("%w: invalid remote skill version metadata", ErrBusinessSystemPromptBundleInvalid)
	}
	return nil
}

func validateRemoteSkillVersionRoot(root string, version RemoteSkillBundleVersion) error {
	bundle, err := loadBusinessSystemPromptBundleIdentity(root, version.BundleID, version.ManifestSHA256)
	if err != nil {
		return err
	}
	if len(bundle.Manifest.Files) != version.FileCount {
		return fmt.Errorf("%w: file count mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	var total int64
	for _, entry := range bundle.Manifest.Files {
		total += int64(entry.ByteLength)
	}
	if total != version.TotalBytes {
		return fmt.Errorf("%w: total bytes mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(root, BusinessSystemPromptBundleManifestName))
	if err != nil {
		return err
	}
	archiveBytes, err := readRemoteSkillBoundedFile(filepath.Join(root, remoteSkillArchiveName(version.ManifestSHA256)), remoteSkillArchiveMaxBytes)
	if err != nil {
		return err
	}
	if hashBusinessSystemPromptBundleBytes(archiveBytes) != version.ArchiveSHA256 {
		return fmt.Errorf("%w: archive digest mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	return verifyRemoteSkillArchive(archiveBytes, manifestBytes, bundle.Manifest)
}

func writeRemoteSkillCandidate(root string, candidate RemoteSkillCandidate) error {
	if err := os.WriteFile(filepath.Join(root, BusinessSystemPromptBundleManifestName), candidate.ManifestBytes, 0o640); err != nil {
		return err
	}
	for _, entry := range candidate.Manifest.Files {
		target := filepath.Join(root, filepath.FromSlash(entry.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(target, candidate.Files[entry.Path], 0o640); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(root, remoteSkillArchiveName(candidate.Version.ManifestSHA256)), candidate.ArchiveBytes, 0o640)
}

func verifyRemoteSkillArchive(archiveBytes, manifestBytes []byte, manifest BusinessSystemPromptBundleManifest) error {
	reader, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		return fmt.Errorf("%w: invalid ZIP", ErrBusinessSystemPromptBundleInvalid)
	}
	expected := make(map[string]BusinessSystemPromptBundleFile, len(manifest.Files))
	for _, entry := range manifest.Files {
		expected[entry.Path] = entry
	}
	seen := make(map[string]struct{}, len(reader.File))
	portable := make(map[string]struct{}, len(reader.File))
	for _, entry := range reader.File {
		name, pathErr := normalizeBundleRelativePath(entry.Name)
		mode := entry.Mode()
		if pathErr != nil || name != entry.Name || entry.FileInfo().IsDir() || mode&os.ModeSymlink != 0 || (!mode.IsRegular() && mode != 0) {
			return fmt.Errorf("%w: unsafe ZIP entry", ErrBusinessSystemPromptBundleInvalid)
		}
		key := portableRemoteSkillPathKey(name)
		if _, ok := seen[name]; ok {
			return fmt.Errorf("%w: duplicate ZIP entry", ErrBusinessSystemPromptBundleInvalid)
		}
		if _, ok := portable[key]; ok {
			return fmt.Errorf("%w: portable ZIP path collision", ErrBusinessSystemPromptBundleInvalid)
		}
		seen[name] = struct{}{}
		portable[key] = struct{}{}
		var expectedLength int
		if name == BusinessSystemPromptBundleManifestName {
			expectedLength = len(manifestBytes)
		} else if declared, ok := expected[name]; ok {
			expectedLength = declared.ByteLength
		} else {
			return fmt.Errorf("%w: undeclared ZIP entry", ErrBusinessSystemPromptBundleInvalid)
		}
		if entry.UncompressedSize64 != uint64(expectedLength) || entry.UncompressedSize64 > businessSystemPromptBundleMaxFileBytes {
			return fmt.Errorf("%w: ZIP entry length mismatch", ErrBusinessSystemPromptBundleInvalid)
		}
		stream, err := entry.Open()
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(stream, businessSystemPromptBundleMaxFileBytes+1))
		closeErr := stream.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if name == BusinessSystemPromptBundleManifestName {
			if !bytes.Equal(data, manifestBytes) {
				return fmt.Errorf("%w: ZIP manifest mismatch", ErrBusinessSystemPromptBundleInvalid)
			}
		} else if !equalHexDigest(expected[name].SHA256, data) {
			return fmt.Errorf("%w: ZIP file digest mismatch", ErrBusinessSystemPromptBundleInvalid)
		}
	}
	if len(seen) != len(expected)+1 {
		return fmt.Errorf("%w: ZIP entry set mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	return nil
}

func remoteSkillFilesFromArchive(archiveBytes, manifestBytes []byte, manifest BusinessSystemPromptBundleManifest) (map[string][]byte, error) {
	if err := verifyRemoteSkillArchive(archiveBytes, manifestBytes, manifest); err != nil {
		return nil, err
	}
	reader, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid ZIP", ErrBusinessSystemPromptBundleInvalid)
	}
	files := make(map[string][]byte, len(manifest.Files))
	for _, entry := range reader.File {
		if entry.Name == BusinessSystemPromptBundleManifestName {
			continue
		}
		stream, err := entry.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(stream, businessSystemPromptBundleMaxFileBytes+1))
		closeErr := stream.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		files[entry.Name] = data
	}
	return files, nil
}

func installRemoteSkillBootstrap(source, destination, expectedHash, expectedName string) error {
	if _, err := os.Lstat(destination); err == nil {
		return validateRemoteSkillBootstrapRoot(destination, expectedHash, expectedName)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".bootstrap-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := copyRemoteSkillTree(source, staging); err != nil {
		return err
	}
	if err := validateRemoteSkillBootstrapRoot(staging, expectedHash, expectedName); err != nil {
		return err
	}
	if err := os.Chmod(staging, 0o755); err != nil {
		return err
	}
	if err := os.Rename(staging, destination); err != nil {
		if _, statErr := os.Lstat(destination); statErr == nil {
			return validateRemoteSkillBootstrapRoot(destination, expectedHash, expectedName)
		}
		return err
	}
	return nil
}

func validateRemoteSkillBootstrapRoot(root, expectedHash, expectedName string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: bootstrap target is not a directory", ErrBusinessSystemPromptBundleInvalid)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	if len(entries) != 1 || entries[0].IsDir() || entries[0].Name() != expectedName {
		return fmt.Errorf("%w: bootstrap target shape invalid", ErrBusinessSystemPromptBundleInvalid)
	}
	raw, err := readRemoteSkillBoundedFile(filepath.Join(root, expectedName), 1<<20)
	if err != nil {
		return err
	}
	if hashBusinessSystemPromptBundleBytes(raw) != expectedHash {
		return fmt.Errorf("%w: installed bootstrap digest mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	return nil
}

func copyRemoteSkillTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.Mode().IsRegular() && !info.IsDir()) {
			return fmt.Errorf("%w: special file in private registry", ErrBusinessSystemPromptBundleInvalid)
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func readRemoteSkillBoundedFile(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maximum {
		return nil, fmt.Errorf("%w: file size or type invalid", ErrBusinessSystemPromptBundleInvalid)
	}
	return os.ReadFile(path)
}

func remoteSkillArchiveName(manifestHash string) string {
	return BusinessSystemPromptRemoteSkillBundleID + "-" + strings.ToLower(strings.TrimSpace(manifestHash)) + ".zip"
}

func remoteSkillVersionFromDescriptor(descriptor RemoteSkillPublicDescriptor) RemoteSkillBundleVersion {
	return RemoteSkillBundleVersion{
		BundleID: descriptor.BundleID, SourceCommit: strings.ToLower(descriptor.SourceCommit),
		OverlaySHA256: strings.ToLower(descriptor.OverlaySHA256), ManifestSHA256: strings.ToLower(descriptor.ManifestSHA256),
		ArchiveSHA256: strings.ToLower(descriptor.ArchiveSHA256), FileCount: descriptor.FileCount,
		TotalBytes: descriptor.TotalBytes, PublishedAt: &descriptor.PublishedAt,
	}
}

func remoteSkillPublicBootstraps() RemoteSkillPublicBootstraps {
	return RemoteSkillPublicBootstraps{
		PowerShell: RemoteSkillPublicBootstrap{URL: RemoteSkillPowerShellBootstrapURL, SHA256: RemoteSkillPowerShellBootstrapSHA256},
		Python:     RemoteSkillPublicBootstrap{URL: RemoteSkillPythonBootstrapURL, SHA256: RemoteSkillPythonBootstrapSHA256},
	}
}

func validateRemoteSkillPublicBootstraps(value RemoteSkillPublicBootstraps) error {
	expected := remoteSkillPublicBootstraps()
	if value != expected {
		return fmt.Errorf("%w: bootstrap metadata is not content addressed", ErrBusinessSystemPromptBundleInvalid)
	}
	return nil
}

func portableRemoteSkillPathKey(value string) string {
	parts := strings.Split(value, "/")
	for i := range parts {
		parts[i] = strings.ToLower(strings.TrimRight(parts[i], " ."))
	}
	return strings.Join(parts, "/")
}

func sortedRemoteSkillFileNames(files map[string][]byte) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
