package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBusinessSystemPromptBundleLoaderRejectsTraversalAndSymlink(t *testing.T) {
	root := t.TempDir()
	writeBundleFixture(t, root)
	manifestPath := filepath.Join(root, businessSystemPromptBundleManifestName)
	var manifest BusinessSystemPromptBundleManifest
	data, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &manifest))
	manifest.Files[0].Path = "../outside.md"
	bad, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifestPath, bad, 0o600))
	_, err = LoadBusinessSystemPromptBundle(root)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)

	root = t.TempDir()
	writeBundleFixture(t, root)
	outside := filepath.Join(t.TempDir(), "outside.md")
	require.NoError(t, os.WriteFile(outside, []byte("outside"), 0o600))
	link := filepath.Join(root, "links")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	data, err = os.ReadFile(filepath.Join(root, businessSystemPromptBundleManifestName))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &manifest))
	manifest.Files[0].Path = "links"
	bad, err = json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, businessSystemPromptBundleManifestName), bad, 0o600))
	_, err = LoadBusinessSystemPromptBundle(root)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
}

func TestBusinessSystemPromptBundlePathNormalizationRejectsPlatformAliases(t *testing.T) {
	for _, value := range []string{"../escape", `dir\\escape`, "file.md:stream", "file?.md", "NUL", "dir/COM1.txt", "trailing. ", "/absolute"} {
		t.Run(value, func(t *testing.T) {
			_, err := normalizeBundleRelativePath(value)
			require.Error(t, err)
		})
	}
	require.Equal(t, "skills/api-security/SKILL.md", mustNormalizeBundlePath(t, "skills/api-security/SKILL.md"))
}

func mustNormalizeBundlePath(t *testing.T, value string) string {
	t.Helper()
	got, err := normalizeBundleRelativePath(value)
	require.NoError(t, err)
	return got
}

func TestBusinessSystemPromptBundleLoaderVerifiesUTF8HashAndLength(t *testing.T) {
	root := t.TempDir()
	writeBundleFixture(t, root)
	bundle, err := LoadBusinessSystemPromptBundle(root)
	require.NoError(t, err)
	require.Equal(t, "fixture-reverse-skill", bundle.Manifest.BundleID)
	require.NotEmpty(t, bundle.ManifestSHA256)
	text, err := bundle.ReadText("core.md")
	require.NoError(t, err)
	require.Equal(t, "core instructions", text)

	require.NoError(t, os.WriteFile(filepath.Join(root, "core.md"), []byte("tampered"), 0o600))
	_, err = LoadBusinessSystemPromptBundle(root)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
}

func TestBusinessSystemPromptBundleLoaderRejectsInvalidUTF8AndAllowsMissingOptional(t *testing.T) {
	root := t.TempDir()
	writeBundleFixture(t, root)
	invalid := []byte{0xff, 0xfe}
	require.NoError(t, os.WriteFile(filepath.Join(root, "core.md"), invalid, 0o600))
	updateFixtureFileHash(t, root, "core.md", invalid)
	_, err := LoadBusinessSystemPromptBundle(root)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)

	root = t.TempDir()
	writeBundleFixture(t, root)
	require.NoError(t, os.Remove(filepath.Join(root, "ref-optional.md")))
	bundle, err := LoadBusinessSystemPromptBundle(root)
	require.NoError(t, err)
	require.True(t, bundle.Degraded)
	require.Equal(t, []string{"ref-optional.md"}, bundle.MissingOptional)
}

func writeBundleFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string][]byte{
		"core.md":          []byte("core instructions"),
		"api.md":           []byte("api entry"),
		"malware.md":       []byte("malware entry"),
		"forensics.md":     []byte("forensics entry"),
		"ref-auth.md":      []byte("auth reference"),
		"ref-http.md":      []byte("http reference"),
		"ref-malware.md":   []byte("malware reference"),
		"ref-forensics.md": []byte("forensics reference"),
		"ref-optional.md":  []byte("optional reference"),
		"script.ps1":       []byte("Write-Host unsafe"),
	}
	entries := make([]BusinessSystemPromptBundleFile, 0, len(files))
	for name, body := range files {
		digest := sha256.Sum256(body)
		kind := "text"
		if strings.HasSuffix(name, ".ps1") {
			kind = "script"
		}
		entries = append(entries, BusinessSystemPromptBundleFile{Path: name, SHA256: hex.EncodeToString(digest[:]), ByteLength: len(body), Kind: kind, Required: name != "ref-optional.md"})
		require.NoError(t, os.WriteFile(filepath.Join(root, name), body, 0o600))
	}
	manifest := BusinessSystemPromptBundleManifest{
		SchemaVersion: 1,
		BundleID:      "fixture-reverse-skill",
		CoreFiles:     []string{"core.md"},
		Files:         entries,
		Domains: []BusinessSystemPromptBundleDomain{
			{ID: "api-security", Keywords: []string{"api", "http", "authentication", "security", "接口安全", "鉴权"}, Entry: "api.md", References: []string{"ref-auth.md", "ref-http.md", "ref-optional.md"}},
			{ID: "malware-analysis", Keywords: []string{"malware", "reverse engineering"}, Entry: "malware.md", References: []string{"ref-malware.md"}},
			{ID: "digital-forensics", Keywords: []string{"forensic"}, Entry: "forensics.md", References: []string{"ref-forensics.md"}},
		},
	}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, businessSystemPromptBundleManifestName), data, 0o600))
}

func updateFixtureFileHash(t *testing.T, root, name string, body []byte) {
	t.Helper()
	manifestPath := filepath.Join(root, businessSystemPromptBundleManifestName)
	data, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	var manifest BusinessSystemPromptBundleManifest
	require.NoError(t, json.Unmarshal(data, &manifest))
	digest := sha256.Sum256(body)
	for i := range manifest.Files {
		if manifest.Files[i].Path == name {
			manifest.Files[i].SHA256 = hex.EncodeToString(digest[:])
			manifest.Files[i].ByteLength = len(body)
		}
	}
	data, err = json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifestPath, data, 0o600))
}
