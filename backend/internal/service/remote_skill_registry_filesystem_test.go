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
