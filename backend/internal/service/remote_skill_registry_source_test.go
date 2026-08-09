package service

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
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

func validRemoteSkillClientDocument() []byte {
	return []byte(`---
name: codexrip-reverse-skill
description: Use for reverse engineering, security research, and CTF tasks.
---

On the first matching task in each conversation, perform exactly one version check through https://codexrip.vip.
If acquisition or update fails, report the failed stage and continue only when the existing local installation verifies successfully.
Read bundle/RULES.md, bundle/README_AI.md, and bundle/skills/SKILL.md completely in that order.
Later matching tasks in the same conversation do not repeat the check or reads; never load Skill content remotely at runtime.
`)
}

func TestRemoteSkillClientDocumentRequiresLocalOnlyLifecycleContract(t *testing.T) {
	require.NoError(t, validateRemoteSkillClientDocument(remoteSkillClientSkillPath, validRemoteSkillClientDocument()))

	tests := map[string]func(string) string{
		"remote root": func(value string) string { return value + "\nREMOTE_ROOT=https://codexrip.vip/bundle\n" },
		"GitHub download": func(value string) string {
			return value + "\nDownload https://github.com/zhaoxuya520/reverse-skill at runtime.\n"
		},
		"foreign URL": func(value string) string { return value + "\nDownload https://example.com/skill.zip.\n" },
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
	candidate, err := newGitHubRemoteSkillCandidateSource(client, newFakeNativeClientSource()).Build(context.Background(), nil)
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

func TestRemoteSkillCandidateRejectsUnpinnedUpstreamCommit(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{
		commit: "0123456789abcdef0123456789abcdef01234567",
		baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
			"reverse-skill-commit/RULES.md":        "# Rules\n",
			"reverse-skill-commit/README_AI.md":    "# AI README\n",
			"reverse-skill-commit/skills/SKILL.md": "# Upstream skill\n",
		}),
	}
	_, err := newGitHubRemoteSkillCandidateSource(client, newFakeNativeClientSource()).Build(context.Background(), nil)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
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
	files["reverse-skill-commit/README.md"] = "base"
	files["reverse-skill-commit/README_RECONSTRUCTED.md"] = "captured provenance"
	files["reverse-skill-commit/SOURCE-MANIFEST.json"] = "captured source"
	files["reverse-skill-commit/moxinggang-overlay/inline-system-instructions.txt"] = "legacy prompt"
	files["reverse-skill-commit/gradlew"] = "#!/bin/sh\nexit 0\n"
	files["reverse-skill-commit/RULES.md"] = "# Rules\n"
	files["reverse-skill-commit/README_AI.md"] = "# AI README\n"
	files["reverse-skill-commit/skills/SKILL.md"] = "# Upstream skill\n"
	files["reverse-skill-commit/skills/api-security/SKILL.md"] = "# API security"
	client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, files)}
	clientSource := newFakeNativeClientSource()
	candidate, err := newGitHubRemoteSkillCandidateSource(client, clientSource).Build(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, 1, clientSource.calls)
	require.ElementsMatch(t, []string{"api.github.com", "codeload.github.com"}, client.hosts)
	require.Equal(t, BusinessSystemPromptRemoteSkillBundleID, candidate.Version.BundleID)
	require.Equal(t, candidate.Version.ManifestSHA256, hashBusinessSystemPromptBundleBytes(candidate.ManifestBytes))
	require.Equal(t, candidate.Version.ArchiveSHA256, hashBusinessSystemPromptBundleBytes(candidate.ArchiveBytes))
	require.NoError(t, verifyRemoteSkillArchive(candidate.ArchiveBytes, candidate.ManifestBytes, candidate.Manifest))
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
	_, err := newGitHubRemoteSkillCandidateSource(client, clientSource).Build(context.Background(), nil)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
	require.Zero(t, clientSource.calls)
}

func TestRemoteSkillCandidateSourceRejectsNonCanonicalPathBeforeClientFetch(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
		"reverse-skill-commit/nested/../payload.txt": "bad",
	})}
	clientSource := newFakeNativeClientSource()
	_, err := newGitHubRemoteSkillCandidateSource(client, clientSource).Build(context.Background(), nil)
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
	_, err := newGitHubRemoteSkillCandidateSource(client, clientSource).Build(context.Background(), nil)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
	require.Equal(t, 1, clientSource.calls)
}

func makeRemoteSkillSourceZIP(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range files {
		entry, err := writer.Create(name)
		require.NoError(t, err)
		_, err = io.WriteString(entry, body)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}
