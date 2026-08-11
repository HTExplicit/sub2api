package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRemoteSkillFilesystemSeedContainsExactCurrentModelGangTreeAndApprovedPrompt(t *testing.T) {
	files := NewRemoteSkillRegistryFilesystem(t.TempDir())
	seed, err := files.LoadSeed(context.Background())
	require.NoError(t, err)
	require.Equal(t, remoteSkillExpectedFiles, seed.Version.FileCount)
	require.Len(t, seed.RawFiles, remoteSkillExpectedFiles)
	require.Len(t, seed.EffectiveFiles, remoteSkillExpectedFiles)
	require.Len(t, seed.FileChanges, remoteSkillExpectedFiles)
	require.Equal(t, "74bd491260aaa23c45b82bd522b32c6b6dea7d5e76a2d8e3ab3607c6f1ab4e58", seed.Prompt.RawSHA256)
	require.Contains(t, seed.Prompt.EffectiveBody, RemoteSkillPublicRoot)
	require.NotContains(t, seed.Prompt.EffectiveBody, "you are codexrip")
	require.Contains(t, seed.Prompt.EffectiveBody, "宝宝")
	for _, core := range []string{"RULES.md", "README_AI.md", "SKILL.md"} {
		require.NotEmpty(t, seed.RawFiles[core])
	}
	changes := make(map[string]RemoteSkillFileChange, len(seed.FileChanges))
	for _, change := range seed.FileChanges {
		changes[change.Path] = change
	}
	for name, raw := range seed.RawFiles {
		effective, ok := seed.EffectiveFiles[name]
		require.True(t, ok, "path=%s", name)
		change, ok := changes[name]
		require.True(t, ok, "path=%s", name)
		require.Equal(t, "added", change.Change, "path=%s", name)
		require.Equal(t, hashBusinessSystemPromptBundleBytes(raw), change.RawSHA256, "path=%s", name)
		require.Equal(t, hashBusinessSystemPromptBundleBytes(effective), change.EffectiveSHA256, "path=%s", name)
		require.Equal(t, rewriteRemoteSkillPublishedFiles(map[string][]byte{name: raw})[name], effective, "path=%s", name)
	}
}

func TestRemoteSkillFilesystemRoundTripsImmutablePairedCandidate(t *testing.T) {
	root := t.TempDir()
	files := NewRemoteSkillRegistryFilesystem(root)
	seed, err := files.LoadSeed(context.Background())
	require.NoError(t, err)
	seed.Version.ID = 7
	seed.Version.PromptVersionID = 9
	seed.Prompt.ID = 9
	require.NoError(t, files.InstallCandidate(context.Background(), seed))

	loaded, err := files.LoadCandidate(context.Background(), seed.Version, seed.Prompt, seed.FileChanges)
	require.NoError(t, err)
	require.Equal(t, seed.Version.RawTreeSHA256, loaded.Version.RawTreeSHA256)
	require.Equal(t, seed.Prompt.EffectiveSHA256, loaded.Prompt.EffectiveSHA256)
	require.Equal(t, seed.EffectiveFiles["SKILL.md"], loaded.EffectiveFiles["SKILL.md"])

	path := filepath.Join(files.candidateRoot(seed.Version.EffectiveTreeSHA256, seed.Prompt.EffectiveSHA256), remoteSkillEffectiveDirectory, "SKILL.md")
	require.NoError(t, os.WriteFile(path, []byte("tampered"), 0o640))
	_, err = files.LoadCandidate(context.Background(), seed.Version, seed.Prompt, seed.FileChanges)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
}

func TestRemoteSkillFilesystemRejectsCandidateMetadataAndDeterministicRewriteDrift(t *testing.T) {
	files := NewRemoteSkillRegistryFilesystem(t.TempDir())
	seed, err := files.LoadSeed(context.Background())
	require.NoError(t, err)

	tests := map[string]func(*RemoteSkillCandidate){
		"path traversal": func(candidate *RemoteSkillCandidate) {
			removed := removeRemoteSkillTestFile(candidate.RawFiles)
			delete(candidate.EffectiveFiles, removed)
			candidate.RawFiles["../../escaped.md"] = []byte("escaped")
			candidate.EffectiveFiles = rewriteRemoteSkillPublishedFiles(candidate.RawFiles)
			refreshRemoteSkillCandidateTestMetadata(candidate)
		},
		"portable duplicate": func(candidate *RemoteSkillCandidate) {
			removed := removeRemoteSkillTestFile(candidate.RawFiles)
			delete(candidate.EffectiveFiles, removed)
			candidate.RawFiles["skill.md"] = bytes.Clone(candidate.RawFiles["SKILL.md"])
			candidate.EffectiveFiles = rewriteRemoteSkillPublishedFiles(candidate.RawFiles)
			refreshRemoteSkillCandidateTestMetadata(candidate)
		},
		"published tree rewrite": func(candidate *RemoteSkillCandidate) {
			candidate.EffectiveFiles["SKILL.md"] = append(bytes.Clone(candidate.EffectiveFiles["SKILL.md"]), []byte("\nchanged")...)
			refreshRemoteSkillCandidateTestMetadata(candidate)
		},
		"raw byte total": func(candidate *RemoteSkillCandidate) {
			candidate.Version.RawTotalBytes++
		},
		"file change counters": func(candidate *RemoteSkillCandidate) {
			candidate.Version.AddedFiles++
		},
		"prompt rewrite": func(candidate *RemoteSkillCandidate) {
			candidate.Prompt.EffectiveBody = candidate.Prompt.RawBody
			candidate.Prompt.EffectiveSHA256 = candidate.Prompt.RawSHA256
			candidate.Prompt.Diff = ""
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := cloneRemoteSkillCandidateForTest(seed)
			mutate(&candidate)
			err := validatePairedRemoteSkillCandidate(candidate, false)
			require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
		})
	}
}

func TestRemoteSkillFilesystemSharesContentAcrossDistinctCandidateAudits(t *testing.T) {
	files := NewRemoteSkillRegistryFilesystem(t.TempDir())
	seed, err := files.LoadSeed(context.Background())
	require.NoError(t, err)
	seed.Version.ID = 1
	seed.Version.PromptVersionID = 1
	seed.Prompt.ID = 1
	require.NoError(t, files.InstallCandidate(context.Background(), seed))

	second := cloneRemoteSkillCandidateForTest(seed)
	second.Version.ID = 2
	second.Version.FetchedAt = second.Version.FetchedAt.Add(time.Minute)
	second.Version.CreatedBy = 42
	second.Version.AddedFiles = 0
	second.Version.ModifiedFiles = 0
	second.Version.DeletedFiles = 0
	second.Version.ScriptChanges = 0
	second.Version.BinaryChanges = 0
	second.FileChanges = []RemoteSkillFileChange{}
	require.NoError(t, files.InstallCandidate(context.Background(), second))

	loadedSeed, err := files.LoadCandidate(context.Background(), seed.Version, seed.Prompt, seed.FileChanges)
	require.NoError(t, err)
	loadedSecond, err := files.LoadCandidate(context.Background(), second.Version, second.Prompt, second.FileChanges)
	require.NoError(t, err)
	require.Equal(t, seed.Version.FetchedAt, loadedSeed.Version.FetchedAt)
	require.Equal(t, second.Version.FetchedAt, loadedSecond.Version.FetchedAt)
	require.Empty(t, loadedSecond.FileChanges)
	require.Equal(t, seed.Version.EffectiveTreeSHA256, second.Version.EffectiveTreeSHA256)
}

func TestRemoteSkillFilesystemRejectsLinkedMetadataAndPromptFiles(t *testing.T) {
	for _, name := range []string{remoteSkillCandidateMetadataFile, remoteSkillEffectivePromptFile} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			files := NewRemoteSkillRegistryFilesystem(root)
			seed, err := files.LoadSeed(context.Background())
			require.NoError(t, err)
			seed.Version.ID = 7
			seed.Version.PromptVersionID = 9
			seed.Prompt.ID = 9
			require.NoError(t, files.InstallCandidate(context.Background(), seed))

			candidateRoot := files.candidateRoot(seed.Version.EffectiveTreeSHA256, seed.Prompt.EffectiveSHA256)
			target := filepath.Join(candidateRoot, name)
			linkedTarget := filepath.Join(candidateRoot, remoteSkillRawPromptFile)
			require.NoError(t, os.Remove(target))
			if err := os.Symlink(linkedTarget, target); err != nil {
				t.Skipf("symbolic links are unavailable: %v", err)
			}
			_, err = files.LoadCandidate(context.Background(), seed.Version, seed.Prompt, seed.FileChanges)
			require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
		})
	}
}

func TestRemoteSkillFilesystemLegacyCleanupLeavesPairedCandidates(t *testing.T) {
	root := t.TempDir()
	files := NewRemoteSkillRegistryFilesystem(root)
	seed, err := files.LoadSeed(context.Background())
	require.NoError(t, err)
	require.NoError(t, files.InstallCandidate(context.Background(), seed))
	for _, name := range []string{"private/seed", "private/versions", "public/reverse-skill", "public/bootstrap", "public/versions", "staging/incomplete"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(name)), 0o750))
	}
	require.NoError(t, files.CleanupLegacy(context.Background()))
	for _, name := range []string{"private", "public", "staging"} {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(name)))
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	_, err = os.Stat(files.candidateRoot(seed.Version.EffectiveTreeSHA256, seed.Prompt.EffectiveSHA256))
	require.NoError(t, err)
}

func cloneRemoteSkillCandidateForTest(candidate RemoteSkillCandidate) RemoteSkillCandidate {
	candidate.RawFiles = cloneRemoteSkillFiles(candidate.RawFiles)
	candidate.EffectiveFiles = cloneRemoteSkillFiles(candidate.EffectiveFiles)
	candidate.FileChanges = append([]RemoteSkillFileChange(nil), candidate.FileChanges...)
	return candidate
}

func removeRemoteSkillTestFile(files map[string][]byte) string {
	for name := range files {
		if name != "RULES.md" && name != "README_AI.md" && name != "SKILL.md" {
			delete(files, name)
			return name
		}
	}
	return ""
}

func refreshRemoteSkillCandidateTestMetadata(candidate *RemoteSkillCandidate) {
	candidate.Version.FileCount = len(candidate.RawFiles)
	candidate.Version.RawTotalBytes = 0
	candidate.Version.EffectiveTotalBytes = 0
	for _, body := range candidate.RawFiles {
		candidate.Version.RawTotalBytes += int64(len(body))
	}
	for _, body := range candidate.EffectiveFiles {
		candidate.Version.EffectiveTotalBytes += int64(len(body))
	}
	candidate.Version.RawTreeSHA256 = remoteSkillFileTreeSHA256(candidate.RawFiles)
	candidate.Version.EffectiveTreeSHA256 = remoteSkillFileTreeSHA256(candidate.EffectiveFiles)
	candidate.Version.AddedFiles = 0
	candidate.Version.ModifiedFiles = 0
	candidate.Version.DeletedFiles = 0
	candidate.Version.ScriptChanges = 0
	candidate.Version.BinaryChanges = 0
	candidate.FileChanges = remoteSkillFileChanges(nil, *candidate)
	for _, change := range candidate.FileChanges {
		switch change.Change {
		case "added":
			candidate.Version.AddedFiles++
		case "modified":
			candidate.Version.ModifiedFiles++
		case "deleted":
			candidate.Version.DeletedFiles++
		}
		if change.Kind == "script" {
			candidate.Version.ScriptChanges++
		}
		if change.Kind == "binary" {
			candidate.Version.BinaryChanges++
		}
	}
}
