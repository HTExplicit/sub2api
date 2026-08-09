package service

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeRemoteSkillHTTPClient struct {
	baseZIP []byte
	hosts   []string
	commit  string
}

func (f *fakeRemoteSkillHTTPClient) Do(req *http.Request) (*http.Response, error) {
	f.hosts = append(f.hosts, req.URL.Hostname())
	var body []byte
	switch req.URL.Hostname() {
	case "api.github.com":
		commit := f.commit
		if commit == "" {
			commit = remoteSkillPinnedCommit
		}
		body = []byte(`{"sha":"` + commit + `"}`)
	case "codeload.github.com":
		body = f.baseZIP
	default:
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)), Header: make(http.Header), Request: req,
	}, nil
}

func newFakeNativeClientSource() *fakeRemoteSkillClientSource {
	return &fakeRemoteSkillClientSource{files: map[string][]byte{
		remoteSkillClientSkillPath:  validRemoteSkillClientDocument(),
		remoteSkillClientOpenAIPath: []byte("interface:\n  display_name: CodexRip Reverse Skill\n"),
	}}
}

func newTestPinnedCandidateSource(client *fakeRemoteSkillHTTPClient, clientSource RemoteSkillClientSource) *GitHubRemoteSkillCandidateSource {
	source := newGitHubRemoteSkillCandidateSource(client, clientSource)
	source.sourcePin = remoteSkillSourcePin{
		Commit:        remoteSkillPinnedCommit,
		ArchiveSHA256: hashBusinessSystemPromptBundleBytes(client.baseZIP),
		CoreSHA256: map[string]string{
			"RULES.md":        hashBusinessSystemPromptBundleBytes([]byte("# Rules\n")),
			"README_AI.md":    hashBusinessSystemPromptBundleBytes([]byte("# AI README\n")),
			"skills/SKILL.md": hashBusinessSystemPromptBundleBytes([]byte("# Upstream skill\n")),
		},
	}
	return source
}

func validRemoteSkillClientDocument() []byte {
	return []byte(`---
name: codexrip-reverse-skill
description: Use for reverse engineering, security research, and CTF tasks.
---

On the first matching task in each conversation, perform exactly one version check through https://codexrip.vip/skills/reverse-skill/current.json.
Use bootstraps.powershell or bootstraps.python from the descriptor, verify its SHA-256, and execute it with CODEX_HOME.
If acquisition or update fails, report the failed stage and continue only when the existing local installation verifies successfully.
Read bundle/RULES.md, bundle/README_AI.md, and bundle/skills/SKILL.md completely in that order.
Later matching tasks in the same conversation do not repeat the check or reads; never load Skill content remotely at runtime.
`)
}

func TestRemoteSkillClientDocumentRequiresLocalOnlyLifecycleContract(t *testing.T) {
	require.NoError(t, validateRemoteSkillClientDocument(remoteSkillClientSkillPath, validRemoteSkillClientDocument()))

	tests := map[string]func(string) string{
		"remote root": func(value string) string { return value + "\nREMOTE_ROOT=https://codexrip.vip/bundle\n" },
		"legacy overlay": func(value string) string {
			return value + "\nRead moxinggang-overlay/security-research/SKILL.md.\n"
		},
		"GitHub download": func(value string) string {
			return value + "\nDownload https://github.com/zhaoxuya520/reverse-skill at runtime.\n"
		},
		"foreign URL": func(value string) string { return value + "\nDownload https://example.com/skill.zip.\n" },
		"similar-domain URL": func(value string) string {
			return strings.Replace(value, "https://codexrip.vip", "https://codexrip.vip.evil.example", 1)
		},
		"missing registry URL": func(value string) string {
			return strings.Replace(value, "https://codexrip.vip", "the registry", 1)
		},
		"duplicate local reference": func(value string) string { return value + "\nbundle/RULES.md\n" },
		"wrong local order": func(value string) string {
			return strings.Replace(value,
				"bundle/RULES.md, bundle/README_AI.md, and bundle/skills/SKILL.md",
				"bundle/README_AI.md, bundle/RULES.md, and bundle/skills/SKILL.md", 1)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			err := validateRemoteSkillClientDocument(remoteSkillClientSkillPath, []byte(mutate(string(validRemoteSkillClientDocument()))))
			require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
		})
	}
}

func TestRemoteSkillCandidateUsesPinnedNativeCorePaths(t *testing.T) {
	const pinnedCommit = "a5d8c9233b98c52df387d5b1a0ef669fcaa51374"
	client := &fakeRemoteSkillHTTPClient{
		commit: pinnedCommit,
		baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
			"reverse-skill-commit/RULES.md":                     "# Rules\n",
			"reverse-skill-commit/README_AI.md":                 "# AI README\n",
			"reverse-skill-commit/skills/SKILL.md":              "# Upstream skill\n",
			"reverse-skill-commit/skills/api-security/SKILL.md": "# API\n",
		}),
	}
	candidate, err := newTestPinnedCandidateSource(client, newFakeNativeClientSource()).Build(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, pinnedCommit, candidate.Version.SourceCommit)
	require.Equal(t, []string{"RULES.md", "README_AI.md", "skills/SKILL.md"}, candidate.Manifest.CoreFiles)
	for _, name := range candidate.Manifest.CoreFiles {
		require.Contains(t, candidate.Files, name)
	}
	for name := range candidate.Files {
		require.False(t, strings.HasPrefix(name, "codexrip-overlay/security-research/"), name)
	}
}

func TestReleaseRemoteSkillSourcePinMatchesReviewedArchiveAndCore(t *testing.T) {
	pin := releaseRemoteSkillSourcePin()
	require.Equal(t, "a5d8c9233b98c52df387d5b1a0ef669fcaa51374", pin.Commit)
	require.Equal(t, "c6cc4a531b62ded1fae92cc8cdace9cf7833fe23978350161d90dedff77f80df", pin.ArchiveSHA256)
	require.Equal(t, map[string]string{
		"RULES.md":        "2d86efa38f8a8b9ef23fa71edcae35cf111a8fef9027a8893ff66e7e4086afa0",
		"README_AI.md":    "d79c9b34beba0160c1a290763ce40ddf9f4027d2086f575a1b396188ddef87c9",
		"skills/SKILL.md": "2c7994642ae2cd97a15fffc0d6e119e07e83582ca70cc9a7a5d212aa9a947a56",
	}, pin.CoreSHA256)
}

func TestRemoteSkillCandidateAcceptsReviewedPinnedArchive(t *testing.T) {
	archivePath := strings.TrimSpace(os.Getenv("CODEXRIP_REMOTE_SKILL_SOURCE_ZIP"))
	if archivePath == "" {
		t.Skip("set CODEXRIP_REMOTE_SKILL_SOURCE_ZIP to run the reviewed-source integration")
	}
	raw, err := os.ReadFile(archivePath)
	require.NoError(t, err)
	client := &fakeRemoteSkillHTTPClient{baseZIP: raw}
	candidate, err := newGitHubRemoteSkillCandidateSource(client, newFakeNativeClientSource()).Build(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, remoteSkillPinnedCommit, candidate.Version.SourceCommit)
	require.Equal(t, []string{"codeload.github.com"}, client.hosts)
	for name, expected := range releaseRemoteSkillSourcePin().CoreSHA256 {
		require.Equal(t, expected, hashBusinessSystemPromptBundleBytes(candidate.Files[name]))
	}
}

func TestRemoteSkillCandidateRejectsSourceArchiveDigestMismatch(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{
		baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
			"reverse-skill-commit/RULES.md":        "# Rules\n",
			"reverse-skill-commit/README_AI.md":    "# AI README\n",
			"reverse-skill-commit/skills/SKILL.md": "# Upstream skill\n",
		}),
	}
	_, err := newGitHubRemoteSkillCandidateSource(client, newFakeNativeClientSource()).Build(context.Background(), nil)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
}

func TestRemoteSkillCandidateRejectsPinnedCoreMismatch(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
		"reverse-skill-commit/RULES.md":        "# Rules\n",
		"reverse-skill-commit/README_AI.md":    "# AI README\n",
		"reverse-skill-commit/skills/SKILL.md": "# Upstream skill\n",
	})}
	source := newTestPinnedCandidateSource(client, newFakeNativeClientSource())
	source.sourcePin.CoreSHA256["RULES.md"] = strings.Repeat("0", 64)
	_, err := source.Build(context.Background(), nil)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
}

func TestRemoteSkillCandidateRejectsRemoteSkillAcquisitionInstructions(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
		"reverse-skill-commit/RULES.md":        "# Rules\n",
		"reverse-skill-commit/README_AI.md":    "# AI README\n",
		"reverse-skill-commit/skills/SKILL.md": "# Upstream skill\n",
		"reverse-skill-commit/docs/install.md": "curl https://example.com/skills/SKILL.md\n",
	})}
	_, err := newTestPinnedCandidateSource(client, newFakeNativeClientSource()).Build(context.Background(), nil)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
}

func TestRemoteSkillCandidateRejectsUnreviewedPackageAcquisitionRewrite(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
		"reverse-skill-commit/RULES.md":        "# Rules\n",
		"reverse-skill-commit/README_AI.md":    "# AI README\n",
		"reverse-skill-commit/skills/SKILL.md": "# Upstream skill\n",
		"reverse-skill-commit/docs/install.md": remoteSkillUpstreamCloneCommand + "\n",
	})}
	clientSource := newFakeNativeClientSource()
	_, err := newTestPinnedCandidateSource(client, clientSource).Build(context.Background(), nil)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
	require.Zero(t, clientSource.calls)
}

func TestRemoteSkillCandidateRejectsChangedApprovedPackageAcquisition(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
		"reverse-skill-commit/RULES.md":        "# Rules\n",
		"reverse-skill-commit/README_AI.md":    "# AI README\n",
		"reverse-skill-commit/skills/SKILL.md": "# Upstream skill\n",
		"reverse-skill-commit/README.md":       remoteSkillUpstreamCloneCommand + "\n" + remoteSkillUpstreamCloneCommand + "\n",
	})}
	clientSource := newFakeNativeClientSource()
	_, err := newTestPinnedCandidateSource(client, clientSource).Build(context.Background(), nil)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
	require.Zero(t, clientSource.calls)
}

func TestRemoteSkillCandidateRejectsPackageAcquisitionSuffixes(t *testing.T) {
	for name, suffix := range map[string]string{
		"branch argument": " --branch attacker\n",
		"chained command": " && echo attacker\n",
	} {
		t.Run(name, func(t *testing.T) {
			client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
				"reverse-skill-commit/RULES.md":        "# Rules\n",
				"reverse-skill-commit/README_AI.md":    "# AI README\n",
				"reverse-skill-commit/skills/SKILL.md": "# Upstream skill\n",
				"reverse-skill-commit/README.md":       remoteSkillUpstreamCloneCommand + suffix,
			})}
			clientSource := newFakeNativeClientSource()
			_, err := newTestPinnedCandidateSource(client, clientSource).Build(context.Background(), nil)
			require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
			require.Zero(t, clientSource.calls)
		})
	}
}

func TestRemoteSkillCandidateRejectsGitPullInstruction(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
		"reverse-skill-commit/RULES.md":        "# Rules\n",
		"reverse-skill-commit/README_AI.md":    "# AI README\n",
		"reverse-skill-commit/skills/SKILL.md": "# Upstream skill\n",
		"reverse-skill-commit/docs/install.md": "Run git pull and retry.\n",
	})}
	clientSource := newFakeNativeClientSource()
	_, err := newTestPinnedCandidateSource(client, clientSource).Build(context.Background(), nil)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
	require.Zero(t, clientSource.calls)
}

func TestRemoteSkillCandidateRejectsLegacyOverlayTextReference(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
		"reverse-skill-commit/RULES.md":        "# Rules\n",
		"reverse-skill-commit/README_AI.md":    "# AI README\n",
		"reverse-skill-commit/skills/SKILL.md": "# Upstream skill\n",
		"reverse-skill-commit/docs/install.md": "Read MoxingGang-Overlay/Security-Research/SKILL.md.\n",
	})}
	clientSource := newFakeNativeClientSource()
	_, err := newTestPinnedCandidateSource(client, clientSource).Build(context.Background(), nil)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
	require.Zero(t, clientSource.calls)
}

func TestRemoteSkillCandidateRejectsLegacyOverlayPath(t *testing.T) {
	for _, legacyPath := range []string{
		"codexrip-overlay/security-research/SKILL.md",
		"CodexRip-Overlay/Security-Research/SKILL.md",
		"MoxingGang-Overlay/Security-Research/SKILL.md",
	} {
		t.Run(legacyPath, func(t *testing.T) {
			client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
				"reverse-skill-commit/RULES.md":        "# Rules\n",
				"reverse-skill-commit/README_AI.md":    "# AI README\n",
				"reverse-skill-commit/skills/SKILL.md": "# Upstream skill\n",
				"reverse-skill-commit/" + legacyPath:   "# Duplicate route\n",
			})}
			clientSource := newFakeNativeClientSource()
			_, err := newTestPinnedCandidateSource(client, clientSource).Build(context.Background(), nil)
			require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
			require.Zero(t, clientSource.calls)
		})
	}
}

type fakeRemoteSkillClientSource struct {
	files map[string][]byte
	calls int
}

func (f *fakeRemoteSkillClientSource) Load(context.Context) (map[string][]byte, error) {
	f.calls++
	result := make(map[string][]byte, len(f.files))
	for name, body := range f.files {
		result[name] = append([]byte(nil), body...)
	}
	return result, nil
}

func TestRemoteSkillCandidateSourceNormalizesCoreDocumentsAndBuildsVerifiedArchive(t *testing.T) {
	files := map[string]string{}
	files["reverse-skill-commit/README.md"] = "git clone https://github.com/zhaoxuya520/reverse-skill.git\n"
	files["reverse-skill-commit/README_RECONSTRUCTED.md"] = "captured provenance"
	files["reverse-skill-commit/SOURCE-MANIFEST.json"] = "captured source"
	files["reverse-skill-commit/moxinggang-overlay/inline-system-instructions.txt"] = "legacy prompt"
	files["reverse-skill-commit/gradlew"] = "#!/bin/sh\nexit 0\n"
	files["reverse-skill-commit/RULES.md"] = "# Rules\n"
	files["reverse-skill-commit/README_AI.md"] = "# AI README\n"
	files["reverse-skill-commit/skills/SKILL.md"] = "# Upstream skill\n"
	files["reverse-skill-commit/skills/api-security/SKILL.md"] = "# API security"
	files["reverse-skill-commit/skills/scripts/master-route.ps1"] = remoteSkillRouterRecoveryLine + "\r\n"
	client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, files)}
	clientSource := newFakeNativeClientSource()
	candidate, err := newTestPinnedCandidateSource(client, clientSource).Build(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, 1, clientSource.calls)
	require.Equal(t, []string{"codeload.github.com"}, client.hosts)
	require.Equal(t, BusinessSystemPromptRemoteSkillBundleID, candidate.Version.BundleID)
	require.Equal(t, candidate.Version.ManifestSHA256, hashBusinessSystemPromptBundleBytes(candidate.ManifestBytes))
	require.Equal(t, candidate.Version.ArchiveSHA256, hashBusinessSystemPromptBundleBytes(candidate.ArchiveBytes))
	require.NoError(t, verifyRemoteSkillArchive(candidate.ArchiveBytes, candidate.ManifestBytes, candidate.Manifest))
	// Native PowerShell and Python installers require an explicit empty list;
	// an omitted/null references field is not a portable manifest contract.
	require.Contains(t, string(candidate.ManifestBytes), `"references": []`)
	archive, err := zip.NewReader(bytes.NewReader(candidate.ArchiveBytes), int64(len(candidate.ArchiveBytes)))
	require.NoError(t, err)
	for _, entry := range archive.File {
		require.Equal(t, uint16(zip.Store), entry.Method)
	}
	foundGradleWrapper := false
	for _, entry := range candidate.Manifest.Files {
		if entry.Path == "gradlew" {
			foundGradleWrapper = true
			require.Equal(t, "script", entry.Kind)
		}
	}
	require.True(t, foundGradleWrapper)
	require.NotContains(t, string(candidate.Files["README.md"]), "github.com/zhaoxuya520/reverse-skill")
	require.Contains(t, string(candidate.Files["README.md"]), remoteSkillLocalPackageContractLine)
	require.Contains(t, string(candidate.Files["skills/scripts/master-route.ps1"]), remoteSkillLocalRecoveryLine)
	require.NotContains(t, string(candidate.Files["skills/scripts/master-route.ps1"]), "git pull")
	for _, excluded := range []string{"README_RECONSTRUCTED.md", "SOURCE-MANIFEST.json", "moxinggang-overlay/inline-system-instructions.txt"} {
		_, present := candidate.Files[excluded]
		require.False(t, present, excluded)
	}

	for _, name := range candidate.Manifest.CoreFiles {
		body := string(candidate.Files[name])
		require.NotContains(t, strings.ToLower(body), "moxinggang.com")
		require.NotContains(t, body, `C:\Users\Administrator`)
		require.NotEmpty(t, body)
	}
}

func TestRemoteSkillCandidateSourceRejectsPathTraversalBeforeClientFetch(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
		"reverse-skill-commit/../../escape.txt": "bad",
	})}
	clientSource := newFakeNativeClientSource()
	_, err := newTestPinnedCandidateSource(client, clientSource).Build(context.Background(), nil)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
	require.Zero(t, clientSource.calls)
}

func TestRemoteSkillCandidateSourceRejectsNonCanonicalPathBeforeClientFetch(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
		"reverse-skill-commit/nested/../payload.txt": "bad",
	})}
	clientSource := newFakeNativeClientSource()
	_, err := newTestPinnedCandidateSource(client, clientSource).Build(context.Background(), nil)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
	require.Zero(t, clientSource.calls)
}

func TestRemoteSkillCandidateSourceRejectsPortableCollisionWithReleaseClient(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
		"reverse-skill-commit/CODEXRIP-CLIENT/SKILL.md": "conflict",
		"reverse-skill-commit/RULES.md":                 "# Rules\n",
		"reverse-skill-commit/README_AI.md":             "# AI README\n",
		"reverse-skill-commit/skills/SKILL.md":          "# Upstream skill\n",
	})}
	clientSource := newFakeNativeClientSource()
	_, err := newTestPinnedCandidateSource(client, clientSource).Build(context.Background(), nil)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
	require.Equal(t, 1, clientSource.calls)
}

func makeRemoteSkillSourceZIP(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range files {
		name = strings.Replace(name, "reverse-skill-commit/", "reverse-skill-"+remoteSkillPinnedCommit+"/", 1)
		entry, err := writer.Create(name)
		require.NoError(t, err)
		_, err = io.WriteString(entry, body)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}
