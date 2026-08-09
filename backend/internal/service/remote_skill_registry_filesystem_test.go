package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRemoteSkillRegistryFilesystemInstallsReleaseSeedAndPublicAssets(t *testing.T) {
	releaseRoot := filepath.Join("..", "..", "..", "deploy", "skill-registry")
	runtimeRoot := t.TempDir()
	files := NewRemoteSkillRegistryFilesystemWithReleaseRoot(runtimeRoot, releaseRoot)

	version, err := files.LoadSeed(context.Background())
	require.NoError(t, err)
	require.Equal(t, BusinessSystemPromptRemoteSkillBundleID, version.BundleID)
	require.Equal(t, "8c72ca9a3fbccb1af90152ab4ed00f3369bd4cc9c84c279a3f3e4208492e69bd", version.ManifestSHA256)
	require.Equal(t, "30d2b2d152a5456b7abcded6c2c823ec21b08113d5c15f4913802afb6742d20b", version.ArchiveSHA256)
	require.NoError(t, files.ValidateVersion(context.Background(), version))

	seedRoot := filepath.Join(runtimeRoot, "private", "seed")
	require.NoError(t, os.WriteFile(filepath.Join(seedRoot, remoteSkillSeedDescriptorName), []byte("{}"), 0o640))
	require.NoError(t, os.WriteFile(filepath.Join(seedRoot, "STALE"), []byte("old"), 0o640))
	reloaded, err := files.LoadSeed(context.Background())
	require.NoError(t, err)
	require.Equal(t, version.ManifestSHA256, reloaded.ManifestSHA256)
	_, err = os.Stat(filepath.Join(seedRoot, "STALE"))
	require.ErrorIs(t, err, os.ErrNotExist)

	for _, hashAndName := range [][2]string{
		{"2199e8c4e8a09278c9b79e17b05e5457308db0a7d593e0f933ad6bd0712845f9", "bootstrap-reverse-skill.ps1"},
		{"353878272c8972c00817cc7171d7a4a087b4203fa2758b7ba1d040ededde7dc9", "bootstrap-reverse-skill.py"},
	} {
		_, err := os.Stat(filepath.Join(runtimeRoot, "public", "bootstrap", hashAndName[0], hashAndName[1]))
		require.NoError(t, err)
	}

	now := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	version.PublishedAt = &now
	snapshot := RemoteSkillRegistrySnapshot{Revision: 1, Active: &version, UpdatedAt: now}
	require.NoError(t, files.PreparePublic(context.Background(), version))
	require.NoError(t, files.Activate(context.Background(), snapshot))

	raw, err := os.ReadFile(filepath.Join(runtimeRoot, "public", "reverse-skill", "current.json"))
	require.NoError(t, err)
	var descriptor RemoteSkillPublicDescriptor
	require.NoError(t, json.Unmarshal(raw, &descriptor))
	baseURL := "https://codexrip.vip/skills/reverse-skill/versions/" + version.ManifestSHA256 + "/"
	require.Equal(t, baseURL, descriptor.FilesBaseURL)
	require.Equal(t, baseURL+BusinessSystemPromptBundleManifestName, descriptor.ManifestURL)
	require.Equal(t, baseURL+remoteSkillArchiveName(version.ManifestSHA256), descriptor.ArchiveURL)
	require.Equal(t, RemoteSkillPowerShellBootstrapURL, descriptor.Bootstraps.PowerShell.URL)
	require.Equal(t, RemoteSkillPowerShellBootstrapSHA256, descriptor.Bootstraps.PowerShell.SHA256)
	require.Equal(t, RemoteSkillPythonBootstrapURL, descriptor.Bootstraps.Python.URL)
	require.Equal(t, RemoteSkillPythonBootstrapSHA256, descriptor.Bootstraps.Python.SHA256)
	require.NotContains(t, strings.ToLower(string(raw)), "moxinggang")
	require.NotContains(t, string(raw), `C:\Users\Administrator`)
	if runtime.GOOS != "windows" {
		for _, path := range []string{
			filepath.Join(runtimeRoot, "public", "reverse-skill", "current.json"),
			filepath.Join(runtimeRoot, "public", "reverse-skill", "versions", version.ManifestSHA256, BusinessSystemPromptBundleManifestName),
		} {
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.NotZero(t, info.Mode().Perm()&0o004, "host Nginx must be able to read %s", path)
		}
		for _, path := range []string{
			filepath.Join(runtimeRoot, "public", "reverse-skill", "versions", version.ManifestSHA256),
			filepath.Join(runtimeRoot, "public", "bootstrap", "2199e8c4e8a09278c9b79e17b05e5457308db0a7d593e0f933ad6bd0712845f9"),
			filepath.Join(runtimeRoot, "public", "bootstrap", "353878272c8972c00817cc7171d7a4a087b4203fa2758b7ba1d040ededde7dc9"),
		} {
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.NotZero(t, info.Mode().Perm()&0o001, "host Nginx must be able to traverse %s", path)
		}
	}
}

func TestRemoteSkillRegistryFilesystemRejectsUnexpectedReleaseBootstrapPairs(t *testing.T) {
	releaseBootstrapRoot := filepath.Join("..", "..", "..", "deploy", "skill-registry", "bootstrap")
	powershellRaw, err := os.ReadFile(filepath.Join(releaseBootstrapRoot, RemoteSkillPowerShellBootstrapSHA256, "bootstrap-reverse-skill.ps1"))
	require.NoError(t, err)
	pythonRaw, err := os.ReadFile(filepath.Join(releaseBootstrapRoot, RemoteSkillPythonBootstrapSHA256, "bootstrap-reverse-skill.py"))
	require.NoError(t, err)

	tests := map[string]map[string]struct {
		name string
		raw  []byte
	}{
		"hash paired with the wrong language": {
			RemoteSkillPowerShellBootstrapSHA256: {name: "bootstrap-reverse-skill.ps1", raw: powershellRaw},
			RemoteSkillPythonBootstrapSHA256:     {name: "bootstrap-reverse-skill.ps1", raw: pythonRaw},
		},
		"duplicate language with self addressed bytes": {
			RemoteSkillPowerShellBootstrapSHA256: {name: "bootstrap-reverse-skill.ps1", raw: powershellRaw},
			hashBusinessSystemPromptBundleBytes([]byte("Write-Host 'unexpected'\n")): {
				name: "bootstrap-reverse-skill.ps1", raw: []byte("Write-Host 'unexpected'\n"),
			},
		},
	}
	for name, assets := range tests {
		t.Run(name, func(t *testing.T) {
			releaseRoot := t.TempDir()
			for hash, asset := range assets {
				directory := filepath.Join(releaseRoot, "bootstrap", hash)
				require.NoError(t, os.MkdirAll(directory, 0o750))
				require.NoError(t, os.WriteFile(filepath.Join(directory, asset.name), asset.raw, 0o640))
			}
			files := NewRemoteSkillRegistryFilesystemWithReleaseRoot(t.TempDir(), releaseRoot)
			require.ErrorIs(t, files.installReleaseBootstraps(context.Background()), ErrBusinessSystemPromptBundleInvalid)
		})
	}
}

func TestRemoteSkillRegistryInitializeKeepsHistoricalOverlayActiveDuringNativeSeedUpgrade(t *testing.T) {
	releaseRoot := filepath.Join("..", "..", "..", "deploy", "skill-registry")
	runtimeRoot := t.TempDir()
	files := NewRemoteSkillRegistryFilesystemWithReleaseRoot(runtimeRoot, releaseRoot)
	legacyFiles := map[string][]byte{
		"codexrip-overlay/security-research/RULES.md":     []byte("legacy rules\n"),
		"codexrip-overlay/security-research/README_AI.md": []byte("legacy readme\n"),
		"codexrip-overlay/security-research/SKILL.md":     []byte("legacy skill\n"),
	}
	entries := make([]BusinessSystemPromptBundleFile, 0, len(legacyFiles))
	var total int64
	for _, name := range sortedRemoteSkillFileNames(legacyFiles) {
		raw := legacyFiles[name]
		entries = append(entries, BusinessSystemPromptBundleFile{
			Path: name, SHA256: hashBusinessSystemPromptBundleBytes(raw), ByteLength: len(raw), Kind: "text", Required: true,
		})
		total += int64(len(raw))
	}
	manifest := BusinessSystemPromptBundleManifest{
		SchemaVersion: 1,
		BundleID:      BusinessSystemPromptRemoteSkillBundleID,
		Version:       "historical-revision-2",
		CoreFiles: []string{
			"codexrip-overlay/security-research/RULES.md",
			"codexrip-overlay/security-research/README_AI.md",
			"codexrip-overlay/security-research/SKILL.md",
		},
		Files: entries,
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	require.NoError(t, err)
	archiveBytes, err := buildRemoteSkillArchive(manifestBytes, legacyFiles)
	require.NoError(t, err)
	legacyVersion := RemoteSkillBundleVersion{
		BundleID:       BusinessSystemPromptRemoteSkillBundleID,
		SourceCommit:   strings.Repeat("1", 40),
		OverlaySHA256:  strings.Repeat("2", 64),
		ManifestSHA256: hashBusinessSystemPromptBundleBytes(manifestBytes),
		ArchiveSHA256:  hashBusinessSystemPromptBundleBytes(archiveBytes),
		FileCount:      len(entries),
		TotalBytes:     total,
	}
	legacyRoot := files.privateVersionRoot(legacyVersion.ManifestSHA256)
	require.NoError(t, os.MkdirAll(legacyRoot, 0o750))
	require.NoError(t, writeRemoteSkillCandidate(legacyRoot, RemoteSkillCandidate{
		Version: legacyVersion, Manifest: manifest, ManifestBytes: manifestBytes, ArchiveBytes: archiveBytes, Files: legacyFiles,
	}))

	store := &fakeRemoteSkillRegistryStore{snapshot: RemoteSkillRegistrySnapshot{
		Revision:  2,
		Active:    &legacyVersion,
		UpdatedAt: time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC),
	}}
	svc := NewRemoteSkillRegistryService(store, nil, files, nil)
	require.NoError(t, svc.Initialize(context.Background()))
	require.NotEqual(t, legacyVersion.ManifestSHA256, store.ensureSeed.ManifestSHA256)
	require.Equal(t, int64(2), svc.CurrentSnapshot().Revision)
	require.Equal(t, legacyVersion.ManifestSHA256, svc.CurrentSnapshot().Active.ManifestSHA256)
	require.False(t, svc.CurrentSnapshot().Degraded)
}

func TestValidateRemoteSkillPublicBootstrapsKeepsSchemaOneLegacyDescriptorsReadable(t *testing.T) {
	require.NoError(t, validateRemoteSkillPublicBootstraps(RemoteSkillPublicBootstraps{}))
	partial := RemoteSkillPublicBootstraps{PowerShell: remoteSkillPublicBootstraps().PowerShell}
	require.ErrorIs(t, validateRemoteSkillPublicBootstraps(partial), ErrBusinessSystemPromptBundleInvalid)
	require.NoError(t, validateRemoteSkillPublicBootstraps(remoteSkillPublicBootstraps()))
}

func TestLegacyRemoteSkillOverlayPathGuardIsRemoteSpecificAndCaseInsensitive(t *testing.T) {
	for _, path := range []string{
		"codexrip-overlay/security-research",
		"CodexRip-Overlay/Security-Research/SKILL.md",
		"moxinggang-overlay/security-research",
		"MoxingGang-Overlay/Security-Research/SKILL.md",
	} {
		require.True(t, isLegacyRemoteSkillOverlayPath(path), path)
	}
	require.False(t, isLegacyRemoteSkillOverlayPath("skills/security-research/SKILL.md"))
}
