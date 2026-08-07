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
	require.Equal(t, "510fed48ae78a2580548d27290259bab1848639538af0dd53acaa3f71c855fea", version.ManifestSHA256)
	require.Equal(t, "1b676ba6e12ffa7c4d16b95e94f82a8330a3afa34f664aa98c3ac808927a60bd", version.ArchiveSHA256)
	require.NoError(t, files.ValidateVersion(context.Background(), version))

	for _, hashAndName := range [][2]string{
		{"e3dfee2e99fad9c890295a9de6fd1d2882c428971579049c3038b94d10668edd", "bootstrap-reverse-skill.ps1"},
		{"6bd6f94cb552f979443303c34883b12b475e724dcaf0b77843420f991459cf9c", "bootstrap-reverse-skill.py"},
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
			filepath.Join(runtimeRoot, "public", "bootstrap", "e3dfee2e99fad9c890295a9de6fd1d2882c428971579049c3038b94d10668edd"),
			filepath.Join(runtimeRoot, "public", "bootstrap", "6bd6f94cb552f979443303c34883b12b475e724dcaf0b77843420f991459cf9c"),
		} {
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.NotZero(t, info.Mode().Perm()&0o001, "host Nginx must be able to traverse %s", path)
		}
	}
}
