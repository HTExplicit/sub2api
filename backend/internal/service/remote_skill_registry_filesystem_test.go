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
	require.Equal(t, "33f081a5bd9f2499dc818f53e3ed069dd0d30ebefe6f7cc47022840553aecb27", version.ManifestSHA256)
	require.Equal(t, "c6920445c55f46c2a30e8a2fe398e7c1cf0b22dcbe4c53ed0cfc105d9c8a5f3e", version.ArchiveSHA256)
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
		{"8595884159988ff653c1d66be66d25acc62a359009c85a7924a23dbaf45d4246", "bootstrap-reverse-skill.ps1"},
		{"2db6ff2d1a5182b73920aabe701d914cca83643aeab89443c0561b1a67430b42", "bootstrap-reverse-skill.py"},
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
			filepath.Join(runtimeRoot, "public", "bootstrap", "8595884159988ff653c1d66be66d25acc62a359009c85a7924a23dbaf45d4246"),
			filepath.Join(runtimeRoot, "public", "bootstrap", "2db6ff2d1a5182b73920aabe701d914cca83643aeab89443c0561b1a67430b42"),
		} {
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.NotZero(t, info.Mode().Perm()&0o001, "host Nginx must be able to traverse %s", path)
		}
	}
}
