package service

import (
	"bytes"
	"context"
	"encoding/json"
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
	require.Equal(t, "c01ea5ce364caf52e28e214162fd36e6d733280aae0bf94fed7ac2ebe8bbb621", seed.Prompt.RawSHA256)
	require.Equal(t, "c56ef682bfae6b0c640148d56ec0a626e3a5cb1f35996caebf3a9c9d6da9c520", seed.Prompt.EffectiveSHA256)
	require.Equal(t, "5bd6df3cfa226c2d6c354dfb83dba8bb1036e0f3042e42590e6d0196a667ca71", seed.Version.RawTreeSHA256)
	require.Equal(t, "42f6b73618ec7c5a6f4d0794171e36db68068acd0fff3c861da24b11b65e7671", seed.Version.EffectiveTreeSHA256)
	require.Equal(t, int64(7_093_862), seed.Version.RawTotalBytes)
	require.Equal(t, int64(7_093_856), seed.Version.EffectiveTotalBytes)
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

func TestRemoteSkillFilesystemLoadsSelfConsistentHistorical73FilePair(t *testing.T) {
	files := NewRemoteSkillRegistryFilesystem(t.TempDir())
	seed, err := files.LoadSeed(context.Background())
	require.NoError(t, err)
	historical := historical73RemoteSkillCandidateForTest(t, seed)
	require.Equal(t, 73, historical.Version.FileCount)
	historical.Version.ID = 73
	historical.Version.PromptVersionID = 9
	historical.Prompt.ID = 9

	require.ErrorIs(t, validatePairedRemoteSkillCandidate(historical, true), ErrBusinessSystemPromptBundleInvalid)
	require.NoError(t, validateStoredPairedRemoteSkillCandidate(historical, true))
	writeRemoteSkillCandidateFixture(t, files, historical)
	loaded, err := files.LoadCandidate(context.Background(), historical.Version, historical.Prompt, historical.FileChanges)
	require.NoError(t, err)
	require.Equal(t, 73, loaded.Version.FileCount)
}

func historical73RemoteSkillCandidateForTest(t *testing.T, seed RemoteSkillCandidate) RemoteSkillCandidate {
	t.Helper()
	names := make([]string, 0, len(seed.RawFiles))
	for name := range seed.RawFiles {
		if name != "RULES.md" && name != "README_AI.md" && name != "SKILL.md" {
			names = append(names, name)
		}
	}
	sortRemoteSkillPaths(names)
	names = append([]string{"RULES.md", "README_AI.md", "SKILL.md"}, names[:70]...)
	rawFiles := make(map[string][]byte, 73)
	for _, name := range names {
		rawFiles[name] = bytes.Clone(seed.RawFiles[name])
	}
	prompt := RemoteSkillPromptCapture{
		RawBody: []byte(seed.Prompt.RawBody), EffectiveBody: []byte(seed.Prompt.EffectiveBody),
		RawSHA256: seed.Prompt.RawSHA256, EffectiveSHA256: seed.Prompt.EffectiveSHA256, Diff: seed.Prompt.Diff,
	}
	historical, err := buildPairedRemoteSkillCandidate(rawFiles, rewriteRemoteSkillPublishedFiles(rawFiles), prompt, nil, seed.Version.FetchedAt)
	require.NoError(t, err)
	return historical
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

func TestRemoteSkillFilesystemLoadsImmutableHistoricalPromptPair(t *testing.T) {
	files := NewRemoteSkillRegistryFilesystem(t.TempDir())
	seed, err := files.LoadSeed(context.Background())
	require.NoError(t, err)

	historicalPrompt := historicalRemoteSkillPromptCaptureForTest(t)
	historical, err := buildPairedRemoteSkillCandidate(
		seed.RawFiles,
		seed.EffectiveFiles,
		historicalPrompt,
		nil,
		seed.Version.FetchedAt,
	)
	require.NoError(t, err)
	historical.Version.ID = 12
	historical.Version.PromptVersionID = 1
	historical.Prompt.ID = 1

	require.ErrorIs(t, validatePairedRemoteSkillCandidate(historical, true), ErrBusinessSystemPromptBundleInvalid)
	require.NoError(t, validateStoredPairedRemoteSkillCandidate(historical, true))
	writeRemoteSkillCandidateFixture(t, files, historical)

	loaded, err := files.LoadCandidate(context.Background(), historical.Version, historical.Prompt, historical.FileChanges)
	require.NoError(t, err)
	require.Equal(t, "de259ce1b269f926e33232ff2ed628965d47c0d97d3a7c964d05b1e5d62c7c38", loaded.Prompt.EffectiveSHA256)
	require.Equal(t, historical.Prompt.EffectiveBody, loaded.Prompt.EffectiveBody)

	diffPath := filepath.Join(
		files.candidateRoot(historical.Version.EffectiveTreeSHA256, historical.Prompt.EffectiveSHA256),
		remoteSkillPromptDiffFile,
	)
	require.NoError(t, os.WriteFile(diffPath, []byte("tampered"), 0o640))
	_, err = files.LoadCandidate(context.Background(), historical.Version, historical.Prompt, historical.FileChanges)
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

func historicalRemoteSkillPromptCaptureForTest(t *testing.T) RemoteSkillPromptCapture {
	t.Helper()
	routingBlock := `<!-- BEGIN  REVERSE-SKILL -->
#  Codex reverse-skill routing
For reverse engineering, security research, and CTF tasks, use this remote Skill root:

` + "```text" + `
REMOTE_ROOT = https://codexrip.vip/skills/security-research/current
` + "```" + `

Fetch Skill content only from ` + "`REMOTE_ROOT`" + `. Do not install a local copy or use another Skill content source.
<!-- END  REVERSE-SKILL -->`
	securityResearchRoutingBlock := `<!-- BEGIN  SECURITY-RESEARCH ROUTING -->

#  Codex security-research routing

For reverse engineering, security research, and CTF tasks, fetch and read these cloud files first, in order:

` + "```text" + `
REMOTE_ROOT = https://codexrip.vip/skills/security-research/current
` + "```" + `

1. ` + "`REMOTE_ROOT/RULES.md`" + `
2. ` + "`REMOTE_ROOT/README_AI.md`" + `
3. ` + "`REMOTE_ROOT/SKILL.md`" + `

Resolve every relative path in those files against ` + "`REMOTE_ROOT`" + ` and fetch supporting files only from that same root.

<!-- END  SECURITY-RESEARCH ROUTING -->`
	raw, readErr := os.ReadFile(filepath.Join("testdata", "codexrip_reverse_skill_system_prompt_legacy_dual_marker.txt"))
	require.NoError(t, readErr)
	effective, err := rewriteRemoteSkillPromptBlocks(raw, routingBlock, securityResearchRoutingBlock)
	require.NoError(t, err)
	diff, err := remoteSkillPromptUnifiedDiff(raw, effective)
	require.NoError(t, err)
	capture := RemoteSkillPromptCapture{
		RawBody:         bytes.Clone(raw),
		EffectiveBody:   effective,
		RawSHA256:       hashBusinessSystemPromptBundleBytes(raw),
		EffectiveSHA256: hashBusinessSystemPromptBundleBytes(effective),
		Diff:            diff,
	}
	require.Equal(t, "74bd491260aaa23c45b82bd522b32c6b6dea7d5e76a2d8e3ab3607c6f1ab4e58", capture.RawSHA256)
	require.Equal(t, "de259ce1b269f926e33232ff2ed628965d47c0d97d3a7c964d05b1e5d62c7c38", capture.EffectiveSHA256)
	return capture
}

func writeRemoteSkillCandidateFixture(t *testing.T, files *RemoteSkillRegistryFilesystem, candidate RemoteSkillCandidate) {
	t.Helper()
	root := files.candidateRoot(candidate.Version.EffectiveTreeSHA256, candidate.Prompt.EffectiveSHA256)
	require.NoError(t, os.MkdirAll(root, 0o750))
	metadata := remoteSkillCandidateMetadata{
		Version: remoteSkillContentVersionMetadataFrom(candidate.Version),
		Prompt:  remoteSkillContentPromptMetadataFrom(candidate.Prompt),
	}
	metadataRaw, err := json.MarshalIndent(metadata, "", "  ")
	require.NoError(t, err)
	require.NoError(t, writeRemoteSkillRegularFile(filepath.Join(root, remoteSkillCandidateMetadataFile), metadataRaw, 0o640))
	require.NoError(t, writeRemoteSkillTree(filepath.Join(root, remoteSkillRawTreeDirectory), candidate.RawFiles))
	require.NoError(t, writeRemoteSkillTree(filepath.Join(root, remoteSkillEffectiveDirectory), candidate.EffectiveFiles))
	require.NoError(t, writeRemoteSkillRegularFile(filepath.Join(root, remoteSkillRawPromptFile), []byte(candidate.Prompt.RawBody), 0o640))
	require.NoError(t, writeRemoteSkillRegularFile(filepath.Join(root, remoteSkillEffectivePromptFile), []byte(candidate.Prompt.EffectiveBody), 0o640))
	require.NoError(t, writeRemoteSkillRegularFile(filepath.Join(root, remoteSkillPromptDiffFile), []byte(candidate.Prompt.Diff), 0o640))
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
