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
}

func (f *fakeRemoteSkillHTTPClient) Do(req *http.Request) (*http.Response, error) {
	f.hosts = append(f.hosts, req.URL.Hostname())
	var body []byte
	switch req.URL.Hostname() {
	case "api.github.com":
		body = []byte(`{"sha":"0123456789abcdef0123456789abcdef01234567"}`)
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

type fakeRemoteSkillOverlaySource struct {
	files map[string][]byte
	calls int
}

func (f *fakeRemoteSkillOverlaySource) Load(context.Context) (map[string][]byte, error) {
	f.calls++
	result := make(map[string][]byte, len(f.files))
	for name, body := range f.files {
		result[name] = append([]byte(nil), body...)
	}
	return result, nil
}

func newFakeRemoteSkillOverlaySource() *fakeRemoteSkillOverlaySource {
	files := make(map[string][]byte, len(remoteSkillOverlayPaths)+2)
	for _, relative := range remoteSkillOverlayPaths {
		files["codexrip-overlay/security-research/"+relative] = []byte("# CodexRip\nLOCAL_ROOT = codexrip-overlay/security-research\n")
	}
	files[remoteSkillClientSkillPath] = []byte("---\nname: codexrip-reverse-skill\ndescription: reverse and security routing\n---\nRead bundle/RULES.md.\n")
	files[remoteSkillClientOpenAIPath] = []byte("interface:\n  display_name: CodexRip Reverse Skill\n")
	return &fakeRemoteSkillOverlaySource{files: files}
}

func TestRemoteSkillCandidateSourceNormalizesCoreDocumentsAndBuildsVerifiedArchive(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
		"reverse-skill-commit/README.md":                    "base",
		"reverse-skill-commit/README_RECONSTRUCTED.md":      "captured provenance",
		"reverse-skill-commit/SOURCE-MANIFEST.json":         "captured source",
		"reverse-skill-commit/moxinggang-overlay/inline-system-instructions.txt": "legacy prompt",
		"reverse-skill-commit/gradlew":                      "#!/bin/sh\nexit 0\n",
		"reverse-skill-commit/skills/api-security/SKILL.md": "# API security",
	})}
	overlay := newFakeRemoteSkillOverlaySource()
	candidate, err := newGitHubRemoteSkillCandidateSource(client, overlay).Build(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, 1, overlay.calls)
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
		require.Contains(t, body, "codexrip-overlay/security-research")
	}
}

func TestRemoteSkillCandidateSourceRejectsPathTraversalBeforeOverlayFetch(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
		"reverse-skill-commit/../../escape.txt": "bad",
	})}
	overlay := newFakeRemoteSkillOverlaySource()
	_, err := newGitHubRemoteSkillCandidateSource(client, overlay).Build(context.Background(), nil)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
	require.Zero(t, overlay.calls)
}

func TestRemoteSkillCandidateSourceRejectsNonCanonicalPathBeforeOverlayFetch(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
		"reverse-skill-commit/nested/../payload.txt": "bad",
	})}
	overlay := newFakeRemoteSkillOverlaySource()
	_, err := newGitHubRemoteSkillCandidateSource(client, overlay).Build(context.Background(), nil)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
	require.Zero(t, overlay.calls)
}

func TestRemoteSkillCandidateSourceRejectsPortableCollisionWithReleaseOverlay(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
		"reverse-skill-commit/CODEXRIP-OVERLAY/security-research/RULES.md": "conflict",
	})}
	overlay := newFakeRemoteSkillOverlaySource()
	_, err := newGitHubRemoteSkillCandidateSource(client, overlay).Build(context.Background(), nil)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
	require.Equal(t, 1, overlay.calls)
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
