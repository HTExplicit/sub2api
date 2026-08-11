package service

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
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
	remoteSkillCandidateMetadataFile = "candidate.json"
	remoteSkillRawTreeDirectory      = "raw"
	remoteSkillEffectiveDirectory    = "effective"
	remoteSkillRawPromptFile         = "prompt.raw.txt"
	remoteSkillEffectivePromptFile   = "prompt.effective.txt"
	remoteSkillPromptDiffFile        = "prompt.diff"
	remoteSkillSeedFetchedAt         = "2026-08-11T00:00:00Z"
)

//go:embed all:remote_skill_seed/tree
var remoteSkillSeedFS embed.FS

type remoteSkillCandidateMetadata struct {
	Version remoteSkillContentVersionMetadata `json:"version"`
	Prompt  remoteSkillContentPromptMetadata  `json:"prompt"`
}

type remoteSkillContentVersionMetadata struct {
	UpstreamSourceID    string `json:"upstream_source_id"`
	UpstreamRoot        string `json:"upstream_root"`
	PublicRoot          string `json:"public_root"`
	RawTreeSHA256       string `json:"raw_tree_sha256"`
	EffectiveTreeSHA256 string `json:"effective_tree_sha256"`
	FileCount           int    `json:"file_count"`
	RawTotalBytes       int64  `json:"raw_total_bytes"`
	EffectiveTotalBytes int64  `json:"effective_total_bytes"`
}

type remoteSkillContentPromptMetadata struct {
	RawSHA256       string `json:"raw_sha256"`
	EffectiveSHA256 string `json:"effective_sha256"`
}

type RemoteSkillRegistryFilesystem struct {
	root string
}

func DefaultRemoteSkillRegistryRoot() string {
	if value := strings.TrimSpace(os.Getenv(RemoteSkillRegistryRootEnv)); value != "" {
		return value
	}
	return RemoteSkillRegistryDefaultRoot
}

func NewRemoteSkillRegistryFilesystem(root string) *RemoteSkillRegistryFilesystem {
	if strings.TrimSpace(root) == "" {
		root = DefaultRemoteSkillRegistryRoot()
	}
	return &RemoteSkillRegistryFilesystem{root: filepath.Clean(root)}
}

func (f *RemoteSkillRegistryFilesystem) LoadSeed(ctx context.Context) (RemoteSkillCandidate, error) {
	if err := ctx.Err(); err != nil {
		return RemoteSkillCandidate{}, err
	}
	rawFiles, err := readRemoteSkillTreeFS(remoteSkillSeedFS, "remote_skill_seed/tree")
	if err != nil {
		return RemoteSkillCandidate{}, err
	}
	paths, err := remoteSkillSeedPaths()
	if err != nil {
		return RemoteSkillCandidate{}, err
	}
	if err := validateRemoteSkillTreeShape(rawFiles, paths); err != nil {
		return RemoteSkillCandidate{}, err
	}
	prompt, err := buildRemoteSkillPromptCapture([]byte(embeddedBusinessSystemPrompt))
	if err != nil {
		return RemoteSkillCandidate{}, err
	}
	fetchedAt, err := time.Parse(time.RFC3339, remoteSkillSeedFetchedAt)
	if err != nil {
		return RemoteSkillCandidate{}, err
	}
	return buildPairedRemoteSkillCandidate(rawFiles, rewriteRemoteSkillPublishedFiles(rawFiles), prompt, nil, fetchedAt)
}

func remoteSkillSeedPaths() ([]string, error) {
	files := make([]string, 0, remoteSkillExpectedFiles)
	err := fs.WalkDir(remoteSkillSeedFS, "remote_skill_seed/tree", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(name, "remote_skill_seed/tree"), "/")
		normalized, normalizeErr := normalizeBundleRelativePath(relative)
		if normalizeErr != nil || normalized != relative {
			return fmt.Errorf("%w: embedded seed path rejected", ErrBusinessSystemPromptBundleInvalid)
		}
		files = append(files, relative)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortRemoteSkillPaths(files)
	return files, nil
}

func sortRemoteSkillPaths(paths []string) {
	sort.Strings(paths)
}

func readRemoteSkillTreeFS(tree fs.FS, root string) (map[string][]byte, error) {
	files := make(map[string][]byte)
	err := fs.WalkDir(tree, root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(name, root), "/")
		raw, readErr := fs.ReadFile(tree, name)
		if readErr != nil {
			return readErr
		}
		files[relative] = raw
		return nil
	})
	return files, err
}

func (f *RemoteSkillRegistryFilesystem) InstallCandidate(ctx context.Context, candidate RemoteSkillCandidate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	requireIDs := candidate.Version.ID != 0 || candidate.Version.PromptVersionID != 0 || candidate.Prompt.ID != 0
	if err := validatePairedRemoteSkillCandidate(candidate, requireIDs); err != nil {
		return err
	}
	destination := f.candidateRoot(candidate.Version.EffectiveTreeSHA256, candidate.Prompt.EffectiveSHA256)
	if _, err := os.Lstat(destination); err == nil {
		_, loadErr := f.loadCandidateRoot(destination, candidate.Version, candidate.Prompt, candidate.FileChanges, requireIDs)
		return loadErr
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".candidate-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	metadata := remoteSkillCandidateMetadata{
		Version: remoteSkillContentVersionMetadataFrom(candidate.Version),
		Prompt:  remoteSkillContentPromptMetadataFrom(candidate.Prompt),
	}
	metadataRaw, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	if err := writeRemoteSkillRegularFile(filepath.Join(staging, remoteSkillCandidateMetadataFile), metadataRaw, 0o640); err != nil {
		return err
	}
	if err := writeRemoteSkillTree(filepath.Join(staging, remoteSkillRawTreeDirectory), candidate.RawFiles); err != nil {
		return err
	}
	if err := writeRemoteSkillTree(filepath.Join(staging, remoteSkillEffectiveDirectory), candidate.EffectiveFiles); err != nil {
		return err
	}
	if err := writeRemoteSkillRegularFile(filepath.Join(staging, remoteSkillRawPromptFile), []byte(candidate.Prompt.RawBody), 0o640); err != nil {
		return err
	}
	if err := writeRemoteSkillRegularFile(filepath.Join(staging, remoteSkillEffectivePromptFile), []byte(candidate.Prompt.EffectiveBody), 0o640); err != nil {
		return err
	}
	if err := writeRemoteSkillRegularFile(filepath.Join(staging, remoteSkillPromptDiffFile), []byte(candidate.Prompt.Diff), 0o640); err != nil {
		return err
	}
	if _, err := f.loadCandidateRoot(staging, candidate.Version, candidate.Prompt, candidate.FileChanges, requireIDs); err != nil {
		return err
	}
	if err := os.Rename(staging, destination); err != nil {
		if _, statErr := os.Lstat(destination); statErr == nil {
			_, loadErr := f.loadCandidateRoot(destination, candidate.Version, candidate.Prompt, candidate.FileChanges, requireIDs)
			return loadErr
		}
		return err
	}
	return nil
}

func (f *RemoteSkillRegistryFilesystem) LoadCandidate(
	ctx context.Context,
	version RemoteSkillBundleVersion,
	prompt RemoteSkillPromptVersion,
	changes []RemoteSkillFileChange,
) (RemoteSkillCandidate, error) {
	if err := ctx.Err(); err != nil {
		return RemoteSkillCandidate{}, err
	}
	return f.loadCandidateRoot(f.candidateRoot(version.EffectiveTreeSHA256, prompt.EffectiveSHA256), version, prompt, changes, true)
}

func (f *RemoteSkillRegistryFilesystem) loadCandidateRoot(
	root string,
	version RemoteSkillBundleVersion,
	prompt RemoteSkillPromptVersion,
	changes []RemoteSkillFileChange,
	requireIDs bool,
) (RemoteSkillCandidate, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return RemoteSkillCandidate{}, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return RemoteSkillCandidate{}, fmt.Errorf("%w: candidate root is not a regular directory", ErrBusinessSystemPromptBundleInvalid)
	}
	metadataRaw, err := readRemoteSkillRegularFile(filepath.Join(root, remoteSkillCandidateMetadataFile), businessSystemPromptBundleMaxFileBytes)
	if err != nil {
		return RemoteSkillCandidate{}, err
	}
	var metadata remoteSkillCandidateMetadata
	if err := json.Unmarshal(metadataRaw, &metadata); err != nil {
		return RemoteSkillCandidate{}, fmt.Errorf("%w: candidate metadata invalid", ErrBusinessSystemPromptBundleInvalid)
	}
	if metadata.Version != remoteSkillContentVersionMetadataFrom(version) || metadata.Prompt != remoteSkillContentPromptMetadataFrom(prompt) {
		return RemoteSkillCandidate{}, fmt.Errorf("%w: candidate metadata mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	rawPrompt, err := readRemoteSkillRegularFile(filepath.Join(root, remoteSkillRawPromptFile), BusinessSystemPromptMaxBytes)
	if err != nil {
		return RemoteSkillCandidate{}, err
	}
	effectivePrompt, err := readRemoteSkillRegularFile(filepath.Join(root, remoteSkillEffectivePromptFile), BusinessSystemPromptMaxBytes)
	if err != nil {
		return RemoteSkillCandidate{}, err
	}
	promptDiff, err := readRemoteSkillRegularFile(filepath.Join(root, remoteSkillPromptDiffFile), businessSystemPromptBundleMaxFileBytes)
	if err != nil {
		return RemoteSkillCandidate{}, err
	}
	if string(rawPrompt) != prompt.RawBody || string(effectivePrompt) != prompt.EffectiveBody || string(promptDiff) != prompt.Diff {
		return RemoteSkillCandidate{}, fmt.Errorf("%w: stored prompt capture mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	rawFiles, err := readRemoteSkillDiskTree(filepath.Join(root, remoteSkillRawTreeDirectory))
	if err != nil {
		return RemoteSkillCandidate{}, err
	}
	effectiveFiles, err := readRemoteSkillDiskTree(filepath.Join(root, remoteSkillEffectiveDirectory))
	if err != nil {
		return RemoteSkillCandidate{}, err
	}
	candidate := RemoteSkillCandidate{Version: version, Prompt: prompt, RawFiles: rawFiles, EffectiveFiles: effectiveFiles, FileChanges: changes}
	if err := validatePairedRemoteSkillCandidate(candidate, requireIDs); err != nil {
		return RemoteSkillCandidate{}, err
	}
	return candidate, nil
}

func validatePairedRemoteSkillCandidate(candidate RemoteSkillCandidate, requireIDs bool) error {
	version := candidate.Version
	prompt := candidate.Prompt
	if version.UpstreamSourceID != RemoteSkillUpstreamSourceID || version.UpstreamRoot != RemoteSkillUpstreamRoot || version.PublicRoot != RemoteSkillPublicRoot ||
		!validRemoteSkillSHA256(version.RawTreeSHA256) || !validRemoteSkillSHA256(version.EffectiveTreeSHA256) ||
		!validRemoteSkillSHA256(prompt.RawSHA256) || !validRemoteSkillSHA256(prompt.EffectiveSHA256) || version.FetchedAt.IsZero() {
		return fmt.Errorf("%w: paired candidate identity mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	idsPresent := version.ID != 0 || version.PromptVersionID != 0 || prompt.ID != 0
	if (requireIDs && !idsPresent) || (idsPresent && (version.ID < 1 || prompt.ID < 1 || version.PromptVersionID != prompt.ID)) {
		return fmt.Errorf("%w: paired candidate database identity missing", ErrBusinessSystemPromptBundleInvalid)
	}
	approved, err := remoteSkillSeedPaths()
	if err != nil {
		return err
	}
	if err := validateRemoteSkillTreeShape(candidate.RawFiles, approved); err != nil {
		return err
	}
	expectedEffective := rewriteRemoteSkillPublishedFiles(candidate.RawFiles)
	if len(expectedEffective) != len(candidate.EffectiveFiles) {
		return fmt.Errorf("%w: paired candidate published tree shape mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	var rawTotal, effectiveTotal int64
	for name, rawBody := range candidate.RawFiles {
		effectiveBody, ok := candidate.EffectiveFiles[name]
		if !ok || len(rawBody) == 0 || len(rawBody) > businessSystemPromptBundleMaxFileBytes ||
			len(effectiveBody) == 0 || len(effectiveBody) > businessSystemPromptBundleMaxFileBytes ||
			!bytes.Equal(effectiveBody, expectedEffective[name]) {
			return fmt.Errorf("%w: paired candidate deterministic tree rewrite mismatch", ErrBusinessSystemPromptBundleInvalid)
		}
		rawTotal += int64(len(rawBody))
		effectiveTotal += int64(len(effectiveBody))
	}
	if version.FileCount != remoteSkillExpectedFiles || version.FileCount != len(candidate.RawFiles) ||
		version.RawTotalBytes != rawTotal || version.EffectiveTotalBytes != effectiveTotal ||
		rawTotal > remoteSkillMaxTotalBytes || effectiveTotal > remoteSkillMaxTotalBytes ||
		version.RawTreeSHA256 != remoteSkillFileTreeSHA256(candidate.RawFiles) ||
		version.EffectiveTreeSHA256 != remoteSkillFileTreeSHA256(candidate.EffectiveFiles) {
		return fmt.Errorf("%w: paired candidate tree identity mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	expectedPrompt, err := buildRemoteSkillPromptCapture([]byte(prompt.RawBody))
	if err != nil || prompt.RawSHA256 != expectedPrompt.RawSHA256 ||
		prompt.EffectiveSHA256 != expectedPrompt.EffectiveSHA256 ||
		prompt.EffectiveBody != string(expectedPrompt.EffectiveBody) || prompt.Diff != expectedPrompt.Diff {
		return fmt.Errorf("%w: paired candidate deterministic prompt rewrite mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	if err := validateRemoteSkillFileChanges(candidate); err != nil {
		return err
	}
	return nil
}

func validateRemoteSkillFileChanges(candidate RemoteSkillCandidate) error {
	seen := make(map[string]struct{}, len(candidate.FileChanges))
	var added, modified, deleted, scripts, binaries int
	for _, change := range candidate.FileChanges {
		normalized, err := normalizeBundleRelativePath(change.Path)
		if err != nil || normalized != change.Path {
			return fmt.Errorf("%w: candidate file change path invalid", ErrBusinessSystemPromptBundleInvalid)
		}
		if _, exists := seen[change.Path]; exists {
			return fmt.Errorf("%w: duplicate candidate file change", ErrBusinessSystemPromptBundleInvalid)
		}
		seen[change.Path] = struct{}{}
		if change.Kind != "text" && change.Kind != "script" && change.Kind != "binary" {
			return fmt.Errorf("%w: candidate file change kind invalid", ErrBusinessSystemPromptBundleInvalid)
		}
		rawBody, hasRaw := candidate.RawFiles[change.Path]
		effectiveBody, hasEffective := candidate.EffectiveFiles[change.Path]
		switch change.Change {
		case "added":
			added++
			if change.PreviousEffectiveSHA256 != "" || !hasRaw || !hasEffective {
				return fmt.Errorf("%w: added file change identity invalid", ErrBusinessSystemPromptBundleInvalid)
			}
		case "modified":
			modified++
			if !validRemoteSkillSHA256(change.PreviousEffectiveSHA256) || !hasRaw || !hasEffective ||
				change.PreviousEffectiveSHA256 == change.EffectiveSHA256 {
				return fmt.Errorf("%w: modified file change identity invalid", ErrBusinessSystemPromptBundleInvalid)
			}
		case "deleted":
			deleted++
			if !validRemoteSkillSHA256(change.PreviousEffectiveSHA256) || hasRaw || hasEffective ||
				change.RawSHA256 != "" || change.EffectiveSHA256 != "" {
				return fmt.Errorf("%w: deleted file change identity invalid", ErrBusinessSystemPromptBundleInvalid)
			}
		default:
			return fmt.Errorf("%w: candidate file change type invalid", ErrBusinessSystemPromptBundleInvalid)
		}
		if hasRaw && (change.RawSHA256 != hashBusinessSystemPromptBundleBytes(rawBody) ||
			change.EffectiveSHA256 != hashBusinessSystemPromptBundleBytes(effectiveBody) ||
			change.Kind != remoteSkillFileKind(change.Path, effectiveBody)) {
			return fmt.Errorf("%w: candidate file change hash mismatch", ErrBusinessSystemPromptBundleInvalid)
		}
		if change.Kind == "script" {
			scripts++
		}
		if change.Kind == "binary" {
			binaries++
		}
	}
	version := candidate.Version
	if version.AddedFiles != added || version.ModifiedFiles != modified || version.DeletedFiles != deleted ||
		version.ScriptChanges != scripts || version.BinaryChanges != binaries {
		return fmt.Errorf("%w: candidate file change counters mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	return nil
}

func writeRemoteSkillTree(root string, files map[string][]byte) error {
	for name, body := range files {
		destination := filepath.Join(root, filepath.FromSlash(name))
		if err := writeRemoteSkillRegularFile(destination, body, 0o640); err != nil {
			return err
		}
	}
	return nil
}

func writeRemoteSkillRegularFile(name string, body []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o750); err != nil {
		return err
	}
	return os.WriteFile(name, body, mode)
}

func readRemoteSkillRegularFile(name string, maxBytes int64) ([]byte, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxBytes {
		return nil, fmt.Errorf("%w: candidate file is not a bounded regular file", ErrBusinessSystemPromptBundleInvalid)
	}
	raw, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) != info.Size() {
		return nil, fmt.Errorf("%w: candidate file changed while reading", ErrBusinessSystemPromptBundleInvalid)
	}
	return raw, nil
}

func readRemoteSkillDiskTree(root string) (map[string][]byte, error) {
	files := make(map[string][]byte)
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: candidate symlink rejected", ErrBusinessSystemPromptBundleInvalid)
		}
		if entry.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > businessSystemPromptBundleMaxFileBytes {
			return fmt.Errorf("%w: candidate file rejected", ErrBusinessSystemPromptBundleInvalid)
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		raw, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		files[relative] = raw
		return nil
	})
	return files, err
}

func remoteSkillContentVersionMetadataFrom(version RemoteSkillBundleVersion) remoteSkillContentVersionMetadata {
	return remoteSkillContentVersionMetadata{
		UpstreamSourceID:    version.UpstreamSourceID,
		UpstreamRoot:        version.UpstreamRoot,
		PublicRoot:          version.PublicRoot,
		RawTreeSHA256:       version.RawTreeSHA256,
		EffectiveTreeSHA256: version.EffectiveTreeSHA256,
		FileCount:           version.FileCount,
		RawTotalBytes:       version.RawTotalBytes,
		EffectiveTotalBytes: version.EffectiveTotalBytes,
	}
}

func remoteSkillContentPromptMetadataFrom(prompt RemoteSkillPromptVersion) remoteSkillContentPromptMetadata {
	return remoteSkillContentPromptMetadata{RawSHA256: prompt.RawSHA256, EffectiveSHA256: prompt.EffectiveSHA256}
}

func (f *RemoteSkillRegistryFilesystem) CleanupLegacy(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, name := range []string{"private", "public", "staging"} {
		target := filepath.Clean(filepath.Join(f.root, filepath.FromSlash(name)))
		if !strings.HasPrefix(target, f.root+string(os.PathSeparator)) {
			return fmt.Errorf("legacy cleanup target escaped registry root")
		}
		if err := os.RemoveAll(target); err != nil {
			return err
		}
	}
	return nil
}

func (f *RemoteSkillRegistryFilesystem) candidateRoot(treeSHA, promptSHA string) string {
	return filepath.Join(f.root, "paired", treeSHA+"-"+promptSHA)
}
