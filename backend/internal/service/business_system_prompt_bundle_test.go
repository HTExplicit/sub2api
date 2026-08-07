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
	for _, value := range []string{"../escape", `dir\\escape`, "file.md:stream", "NUL", "dir/COM1.txt", "trailing. ", "/absolute"} {
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

func TestBusinessSystemPromptBundleRegistryValidatesIdentityAndRetainsLastGood(t *testing.T) {
	base := t.TempDir()
	pending := filepath.Join(base, "pending")
	require.NoError(t, os.MkdirAll(pending, 0o700))
	writeBundleFixture(t, pending)
	loaded, err := LoadBusinessSystemPromptBundle(pending)
	require.NoError(t, err)
	versioned := filepath.Join(base, loaded.Manifest.BundleID, loaded.ManifestSHA256)
	require.NoError(t, os.MkdirAll(filepath.Dir(versioned), 0o700))
	require.NoError(t, os.Rename(pending, versioned))

	registry := NewBusinessSystemPromptBundleRegistry(base)
	got, err := registry.Load(loaded.Manifest.BundleID, loaded.ManifestSHA256)
	require.NoError(t, err)
	require.Equal(t, loaded.ManifestSHA256, got.ManifestSHA256)
	_, err = registry.Load(loaded.Manifest.BundleID, strings.Repeat("0", 64))
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleUnavailable)

	require.NoError(t, os.WriteFile(filepath.Join(versioned, "core.md"), []byte("tampered"), 0o600))
	_, err = registry.Reload(loaded.Manifest.BundleID, loaded.ManifestSHA256)
	require.Error(t, err)
	lastGood, ok := registry.Current(loaded.Manifest.BundleID, loaded.ManifestSHA256)
	require.True(t, ok)
	text, err := lastGood.ReadText("core.md")
	require.NoError(t, err)
	require.Equal(t, "core instructions", text)
}

func TestDefaultBusinessSystemPromptBundlePathUsesEnvironmentOverride(t *testing.T) {
	t.Setenv(BusinessSystemPromptBundlePathEnv, `D:\verified\bundle`)
	require.Equal(t, `D:\verified\bundle`, DefaultBusinessSystemPromptBundlePath())
	t.Setenv(BusinessSystemPromptBundlePathEnv, " ")
	t.Setenv("SUB2API_BUSINESS_SYSTEM_PROMPT_BUNDLE_PATH", "")
	require.Equal(t, BusinessSystemPromptBundleDefaultPath, DefaultBusinessSystemPromptBundlePath())
}

func TestBusinessSystemPromptBundleCompilerRoutesDeterministicallyAndStripsNetworkMarkers(t *testing.T) {
	root := t.TempDir()
	writeBundleFixture(t, root)
	bundle, err := LoadBusinessSystemPromptBundle(root)
	require.NoError(t, err)
	compiler := NewBusinessSystemPromptBundleCompiler(bundle)
	base := "<!-- BEGIN 模型港 REVERSE-SKILL -->\nC:\\Users\\Administrator\\AppData\\Local\\模型港\\reverse-skill\\RULES.md\n<!-- END 模型港 REVERSE-SKILL -->\nCore seed at C:\\Program Files\\Reverse Tool\\tool.exe with https://example.test/reference and https:\\/\\/escaped.example.test/reference\n<!-- BEGIN 模型港 SECURITY-RESEARCH ROUTING -->\nREMOTE_ROOT = https://moxinggang.com/skills/security-research/current\n<!-- END 模型港 SECURITY-RESEARCH ROUTING -->"
	compiled, err := compiler.Compile(BusinessSystemPromptBundleCompileInput{BasePrompt: base, RequestText: "Please audit this HTTP API authentication flow"})
	require.NoError(t, err)
	require.Equal(t, []string{"api-security"}, compiled.RouteIDs)
	require.LessOrEqual(t, len(compiled.Metadata.DocumentPaths), 5) // core + entry + <= 3 references
	require.NotContains(t, compiled.Body, "moxinggang.com")
	require.NotContains(t, compiled.Body, `C:\`)
	require.NotContains(t, compiled.Body, "https://")
	require.NotContains(t, compiled.Body, "http://")
	require.Contains(t, compiled.Body, "LOCAL_BUNDLE_PATH")
	require.Contains(t, compiled.Body, "LOCAL_BUNDLE_URL")
	require.NotEmpty(t, compiled.Metadata.EffectiveSHA256)
	require.Equal(t, len([]byte(compiled.Body)), compiled.Metadata.ByteLength)

	repeat, err := compiler.Compile(BusinessSystemPromptBundleCompileInput{BasePrompt: base, RequestText: "Please audit this HTTP API authentication flow"})
	require.NoError(t, err)
	require.Equal(t, compiled.Body, repeat.Body)
	require.Equal(t, compiled.Metadata, repeat.Metadata)
	require.Equal(t, compiled.Metadata.CacheKey(9), repeat.Metadata.CacheKey(9))
}

func TestBusinessSystemPromptBundleCompilerLimitsRoutesAndRetainsPreviousOnContinuation(t *testing.T) {
	root := t.TempDir()
	writeBundleFixture(t, root)
	bundle, err := LoadBusinessSystemPromptBundle(root)
	require.NoError(t, err)
	compiler := NewBusinessSystemPromptBundleCompiler(bundle)
	first, err := compiler.Compile(BusinessSystemPromptBundleCompileInput{BasePrompt: "seed", RequestText: "api security, malware reverse engineering, and forensic disk analysis"})
	require.NoError(t, err)
	require.Equal(t, []string{"api-security", "malware-analysis"}, first.RouteIDs)
	require.LessOrEqual(t, len(first.RouteIDs), BusinessSystemPromptBundleMaxDomains)
	require.LessOrEqual(t, len(first.Metadata.ReferencePaths), 3)
	require.NotEmpty(t, first.RouteIDs)

	continued, err := compiler.Compile(BusinessSystemPromptBundleCompileInput{
		BasePrompt:       "seed",
		RequestText:      "ordinary follow-up",
		Continuation:     true,
		PreviousMetadata: &first.Metadata,
	})
	require.NoError(t, err)
	require.Equal(t, first.RouteIDs, continued.RouteIDs)
	require.Equal(t, first.Metadata.DocumentPaths, continued.Metadata.DocumentPaths)
}

func TestBusinessSystemPromptBundleCompilerPreservesLegacyBaseProvenanceOffline(t *testing.T) {
	root := t.TempDir()
	writeBundleFixture(t, root)
	bundle, err := LoadBusinessSystemPromptBundle(root)
	require.NoError(t, err)
	legacyBase := "legacy captured seed"
	compiled, err := NewBusinessSystemPromptBundleCompiler(bundle).Compile(BusinessSystemPromptBundleCompileInput{
		BasePrompt:  legacyBase,
		RequestText: "ordinary weather question",
	})
	require.NoError(t, err)
	require.Equal(t, hashBusinessSystemPromptBundleBytes([]byte(legacyBase)), compiled.Metadata.BaseSHA256)
	require.Empty(t, compiled.RouteIDs)
	require.Contains(t, compiled.Body, "[BUSINESS SYSTEM PROMPT: OFFLINE SKILL BUNDLE]")
	require.Contains(t, compiled.Body, "[core/core.md]")
	require.NotContains(t, compiled.Body, `C:\`)
	require.NotContains(t, compiled.Body, "https://moxinggang.com")
	require.NotEqual(t, compiled.Metadata.CacheKey(1), compiled.Metadata.CacheKey(2))
}

func TestBusinessSystemPromptBundleCompilerMarksOptionalOverflowDegraded(t *testing.T) {
	root := t.TempDir()
	writeBundleFixture(t, root)
	// Make the optional reference larger than the compiler cap without changing
	// the fixture's route shape; it should be omitted rather than fail the request.
	large := strings.Repeat("x", BusinessSystemPromptBundleMaxBytes)
	path := filepath.Join(root, "ref-optional.md")
	require.NoError(t, os.WriteFile(path, []byte(large), 0o600))
	updateFixtureFileHash(t, root, "ref-optional.md", []byte(large))
	bundle, err := LoadBusinessSystemPromptBundle(root)
	require.NoError(t, err)
	compiler := NewBusinessSystemPromptBundleCompilerWithLimit(bundle, 512)
	compiled, err := compiler.Compile(BusinessSystemPromptBundleCompileInput{BasePrompt: "seed", RequestText: "api security"})
	require.NoError(t, err)
	require.True(t, compiled.Metadata.Degraded)
	require.NotEmpty(t, compiled.Body)
}

func TestBusinessSystemPromptBundleCompilerNeverUsesScriptEntries(t *testing.T) {
	root := t.TempDir()
	writeBundleFixture(t, root)
	manifestPath := filepath.Join(root, businessSystemPromptBundleManifestName)
	data, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	var manifest BusinessSystemPromptBundleManifest
	require.NoError(t, json.Unmarshal(data, &manifest))
	manifest.Domains = append(manifest.Domains, BusinessSystemPromptBundleDomain{
		ID: "unsafe-script", Keywords: []string{"powershell"}, Entry: "script.ps1",
	})
	data, err = json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifestPath, data, 0o600))
	bundle, err := LoadBusinessSystemPromptBundle(root)
	require.NoError(t, err)
	_, err = NewBusinessSystemPromptBundleCompiler(bundle).Compile(BusinessSystemPromptBundleCompileInput{
		BasePrompt: "seed", RequestText: "run this powershell task",
	})
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
}

func TestBusinessSystemPromptBundleReconstructedArtifact(t *testing.T) {
	root := os.Getenv("SUB2API_TEST_RECONSTRUCTED_BUNDLE_PATH")
	if root == "" {
		t.Skip("set SUB2API_TEST_RECONSTRUCTED_BUNDLE_PATH for reconstructed artifact validation")
	}
	bundle, err := LoadBusinessSystemPromptBundle(root)
	require.NoError(t, err)
	require.Equal(t, "moxinggang-reverse-skill", bundle.Manifest.BundleID)
	require.Equal(t, "22c227128165afbbcbda0175eb5e991ddb51d105b7d1e704572c625c64b626d7", bundle.ManifestSHA256)
	require.Len(t, bundle.Manifest.Files, 538)
	require.Len(t, bundle.Manifest.Domains, 39)

	compiled, err := NewBusinessSystemPromptBundleCompiler(bundle).Compile(BusinessSystemPromptBundleCompileInput{
		BasePrompt:  embeddedBusinessSystemPrompt,
		RequestText: "analyze this Android APK with jadx and Frida",
	})
	require.NoError(t, err)
	require.Contains(t, compiled.RouteIDs, "apk-reverse")
	require.LessOrEqual(t, len(compiled.RouteIDs), BusinessSystemPromptBundleMaxDomains)
	require.LessOrEqual(t, len(compiled.Metadata.ReferencePaths), BusinessSystemPromptBundleMaxReferences)
	require.LessOrEqual(t, compiled.Metadata.ByteLength, BusinessSystemPromptBundleMaxBytes)
	require.False(t, compiled.Metadata.Degraded)
	require.NotContains(t, compiled.Body, "https://moxinggang.com")
	require.NotContains(t, compiled.Body, "moxinggang.com")
	require.NotContains(t, compiled.Body, `C:\`)
	require.NotContains(t, compiled.Body, "https://")
	require.NotContains(t, compiled.Body, "http://")
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
			{ID: "api-security", Keywords: []string{"api", "http", "authentication", "security"}, Entry: "api.md", References: []string{"ref-auth.md", "ref-http.md", "ref-optional.md"}},
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
