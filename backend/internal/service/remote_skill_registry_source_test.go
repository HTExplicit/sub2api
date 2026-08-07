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
	baseZIP      []byte
	overlayCalls int
}

func (f *fakeRemoteSkillHTTPClient) Do(req *http.Request) (*http.Response, error) {
	var body []byte
	switch req.URL.Hostname() {
	case "api.github.com":
		body = []byte(`{"sha":"0123456789abcdef0123456789abcdef01234567"}`)
	case "codeload.github.com":
		body = f.baseZIP
	case "moxinggang.com":
		f.overlayCalls++
		body = []byte("# 模型港\nREMOTE_ROOT = https://moxinggang.com/skills/security-research/current\nC:\\Users\\Administrator\\AppData\\Local\\模型港\\reverse-skill\n")
	default:
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)), Header: make(http.Header), Request: req,
	}, nil
}

func TestRemoteSkillCandidateSourceNormalizesCoreDocumentsAndBuildsVerifiedArchive(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
		"reverse-skill-commit/README.md":                    "base",
		"reverse-skill-commit/gradlew":                      "#!/bin/sh\nexit 0\n",
		"reverse-skill-commit/skills/api-security/SKILL.md": "# API security",
	})}
	candidate, err := NewGitHubRemoteSkillCandidateSource(client).Build(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, len(remoteSkillOverlayPaths), client.overlayCalls)
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

	for _, name := range candidate.Manifest.CoreFiles {
		body := string(candidate.Files[name])
		require.NotContains(t, strings.ToLower(body), "moxinggang.com")
		require.NotContains(t, body, `C:\Users\Administrator`)
		require.NotContains(t, body, "模型港")
		require.Contains(t, body, "codexrip-overlay/security-research")
	}
}

func TestRemoteSkillCandidateSourceRejectsPathTraversalBeforeOverlayFetch(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
		"reverse-skill-commit/../../escape.txt": "bad",
	})}
	_, err := NewGitHubRemoteSkillCandidateSource(client).Build(context.Background(), nil)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
	require.Zero(t, client.overlayCalls)
}

func TestRemoteSkillCandidateSourceRejectsNonCanonicalPathBeforeOverlayFetch(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
		"reverse-skill-commit/nested/../payload.txt": "bad",
	})}
	_, err := NewGitHubRemoteSkillCandidateSource(client).Build(context.Background(), nil)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
	require.Zero(t, client.overlayCalls)
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
