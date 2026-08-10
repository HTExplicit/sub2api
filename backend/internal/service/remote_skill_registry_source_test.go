package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeRemoteSkillHTTPClient struct {
	baseZIP   []byte
	responses map[string]string
	finalURLs map[string]string
	requests  []string
	err       error
}

func (f *fakeRemoteSkillHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.requests = append(f.requests, req.URL.String())
	var body []byte
	switch req.URL.Hostname() {
	case "codeload.github.com":
		body = f.baseZIP
	case "moxinggang.com":
		value, ok := f.responses[req.URL.Path]
		if !ok {
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: req}, nil
		}
		body = []byte(value)
	default:
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: req}, nil
	}
	finalRequest := req
	if raw := f.finalURLs[req.URL.Path]; raw != "" {
		parsed, err := url.Parse(raw)
		if err != nil {
			return nil, err
		}
		finalRequest = req.Clone(req.Context())
		finalRequest.URL = parsed
	}
	return &http.Response{
		StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)), Header: make(http.Header), Request: finalRequest,
	}, nil
}

func TestNormalizeRemoteSkillSourceIDDefaultsAndRejectsUnknownValues(t *testing.T) {
	for _, value := range []string{"", "  ", " GITHUB_OFFICIAL "} {
		got, err := NormalizeRemoteSkillSourceID(value)
		require.NoError(t, err)
		require.Equal(t, RemoteSkillSourceGitHubOfficial, got)
	}
	got, err := NormalizeRemoteSkillSourceID(" MOXINGGANG ")
	require.NoError(t, err)
	require.Equal(t, RemoteSkillSourceMoxinggang, got)
	_, err = NormalizeRemoteSkillSourceID("other")
	require.ErrorIs(t, err, ErrBusinessSystemPromptInvalid)
}

func TestRemoteSkillSourceRootsExposeResolvableSingleSkillEntry(t *testing.T) {
	require.Equal(t,
		"https://raw.githubusercontent.com/zhaoxuya520/reverse-skill/"+remoteSkillPinnedCommit+"/skills/SKILL.md",
		remoteSkillSourceEntryURL(RemoteSkillSourceGitHubOfficial),
	)
	require.Equal(t,
		"https://moxinggang.com/skills/security-research/current/SKILL.md",
		remoteSkillSourceEntryURL(RemoteSkillSourceMoxinggang),
	)
	for _, sourceID := range []string{RemoteSkillSourceGitHubOfficial, RemoteSkillSourceMoxinggang} {
		entry := remoteSkillSourceEntryURL(sourceID)
		parsed, err := url.ParseRequestURI(entry)
		require.NoError(t, err)
		require.Equal(t, "https", parsed.Scheme)
		require.NotEmpty(t, parsed.Host)
		require.Equal(t, remoteSkillSourceRoot(sourceID)+"/SKILL.md", entry)
	}
}

func TestRemoteSkillSourceSelectorUsesRequestedProvider(t *testing.T) {
	github := &fakeRemoteSkillProvider{candidate: RemoteSkillCandidate{Version: RemoteSkillBundleVersion{SourceID: RemoteSkillSourceGitHubOfficial, RemoteRoot: RemoteSkillGitHubRoot}}}
	moxinggang := &fakeRemoteSkillProvider{candidate: RemoteSkillCandidate{Version: RemoteSkillBundleVersion{SourceID: RemoteSkillSourceMoxinggang, RemoteRoot: RemoteSkillMoxinggangRoot}}}
	selector := newRemoteSkillCandidateSourceSelector(map[string]remoteSkillProvider{
		RemoteSkillSourceGitHubOfficial: github,
		RemoteSkillSourceMoxinggang:     moxinggang,
	})

	candidate, err := selector.Build(context.Background(), RemoteSkillSourceMoxinggang, nil)
	require.NoError(t, err)
	require.Equal(t, RemoteSkillSourceMoxinggang, candidate.Version.SourceID)
	require.Equal(t, 0, github.calls)
	require.Equal(t, 1, moxinggang.calls)
}

type fakeRemoteSkillProvider struct {
	candidate RemoteSkillCandidate
	err       error
	calls     int
}

func (f *fakeRemoteSkillProvider) Build(context.Context, *BusinessSystemPromptBundleManifest) (RemoteSkillCandidate, error) {
	f.calls++
	return f.candidate, f.err
}

func TestGitHubRemoteSkillCandidateKeepsReviewedArchiveBytes(t *testing.T) {
	files := map[string]string{
		"reverse-skill-commit/RULES.md":        "# Rules\n",
		"reverse-skill-commit/README_AI.md":    "# AI README\n",
		"reverse-skill-commit/skills/SKILL.md": "# Upstream skill\n",
		"reverse-skill-commit/README.md":       "git clone https://github.com/zhaoxuya520/reverse-skill.git\n",
	}
	client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, files)}
	source := NewGitHubRemoteSkillCandidateSource(client)
	source.sourcePin = remoteSkillSourcePin{
		Commit: remoteSkillPinnedCommit, ArchiveSHA256: hashBusinessSystemPromptBundleBytes(client.baseZIP),
		CoreSHA256: map[string]string{
			"RULES.md": hashBusinessSystemPromptBundleBytes([]byte("# Rules\n")), "README_AI.md": hashBusinessSystemPromptBundleBytes([]byte("# AI README\n")),
			"skills/SKILL.md": hashBusinessSystemPromptBundleBytes([]byte("# Upstream skill\n")),
		},
	}

	candidate, err := source.Build(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, RemoteSkillSourceGitHubOfficial, candidate.Version.SourceID)
	require.Equal(t, RemoteSkillGitHubRoot, candidate.Version.RemoteRoot)
	require.Equal(t, RemoteSkillGitHubRoot+"/SKILL.md", remoteSkillSourceEntryURL(candidate.Version.SourceID))
	require.Equal(t, files["reverse-skill-commit/README.md"], string(candidate.Files["README.md"]))
	require.Equal(t, []string{"RULES.md", "README_AI.md", "skills/SKILL.md"}, candidate.Manifest.CoreFiles)
	require.NoError(t, verifyRemoteSkillArchive(candidate.ArchiveBytes, candidate.ManifestBytes, candidate.Manifest))
}

func TestGitHubRemoteSkillCandidateRejectsArchiveDigestMismatch(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{baseZIP: makeRemoteSkillSourceZIP(t, map[string]string{
		"reverse-skill-commit/RULES.md": "# Rules\n", "reverse-skill-commit/README_AI.md": "# AI\n", "reverse-skill-commit/skills/SKILL.md": "# Skill\n",
	})}
	_, err := NewGitHubRemoteSkillCandidateSource(client).Build(context.Background(), nil)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
}

func TestMoxinggangRemoteSkillCandidateLoadsFullReferencedEntryTree(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{responses: map[string]string{
		RemoteSkillMoxinggangPath + "/RULES.md":                       "# Rules\nRead REMOTE_ROOT/references/scope.md.\n",
		RemoteSkillMoxinggangPath + "/README_AI.md":                   "# AI\n",
		RemoteSkillMoxinggangPath + "/SKILL.md":                       "# Skill\nRead REMOTE_ROOT/skills/sec-web/INSTRUCTIONS.md.\n",
		RemoteSkillMoxinggangPath + "/references/scope.md":            "# Scope\nRead https://moxinggang.com/skills/security-research/current/scripts/check.py.\n",
		RemoteSkillMoxinggangPath + "/skills/sec-web/INSTRUCTIONS.md": "# Web\n",
		RemoteSkillMoxinggangPath + "/scripts/check.py":               "#!/usr/bin/env python3\n",
	}}

	candidate, err := NewMoxinggangRemoteSkillCandidateSource(client).Build(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, RemoteSkillSourceMoxinggang, candidate.Version.SourceID)
	require.Equal(t, RemoteSkillMoxinggangRoot, candidate.Version.RemoteRoot)
	require.Equal(t, RemoteSkillMoxinggangRoot+"/SKILL.md", remoteSkillSourceEntryURL(candidate.Version.SourceID))
	require.Equal(t, []string{"RULES.md", "README_AI.md", "SKILL.md"}, candidate.Manifest.CoreFiles)
	for _, name := range []string{"RULES.md", "README_AI.md", "SKILL.md", "references/scope.md", "skills/sec-web/INSTRUCTIONS.md", "scripts/check.py"} {
		require.Contains(t, candidate.Files, name)
	}
	require.Len(t, client.requests, 6)
	require.NoError(t, verifyRemoteSkillArchive(candidate.ArchiveBytes, candidate.ManifestBytes, candidate.Manifest))
}

func TestMoxinggangRemoteSkillCandidateRejectsCrossHostOrPathRedirects(t *testing.T) {
	for name, finalURL := range map[string]string{
		"cross host":   "https://example.com/skills/security-research/current/RULES.md",
		"outside root": "https://moxinggang.com/skills/other/RULES.md",
		"query":        "https://moxinggang.com/skills/security-research/current/RULES.md?v=1",
	} {
		t.Run(name, func(t *testing.T) {
			client := &fakeRemoteSkillHTTPClient{
				responses: map[string]string{RemoteSkillMoxinggangPath + "/RULES.md": "# Rules\n"},
				finalURLs: map[string]string{RemoteSkillMoxinggangPath + "/RULES.md": finalURL},
			}
			_, err := NewMoxinggangRemoteSkillCandidateSource(client).Build(context.Background(), nil)
			require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
		})
	}
}

func TestMoxinggangRemoteSkillCandidateRejectsRemoteFailure(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{err: errors.New("offline")}
	_, err := NewMoxinggangRemoteSkillCandidateSource(client).Build(context.Background(), nil)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleUnavailable)
}

func TestRemoteSkillSourceHTTPClientRejectsCrossHostRedirect(t *testing.T) {
	client := newRemoteSkillHTTPClient()
	require.NotNil(t, client.CheckRedirect)
	request, err := http.NewRequest(http.MethodGet, "https://example.com/skills/security-research/current/SKILL.md", nil)
	require.NoError(t, err)
	err = client.CheckRedirect(request, []*http.Request{{URL: &url.URL{Scheme: "https", Host: "moxinggang.com", Path: RemoteSkillMoxinggangPath + "/SKILL.md"}}})
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
}

func TestExtractRemoteSkillBaseArchiveRejectsPathTraversal(t *testing.T) {
	raw := makeRemoteSkillSourceZIP(t, map[string]string{"reverse-skill-commit/../../escape.txt": "bad"})
	_, err := extractRemoteSkillBaseArchive(raw, "reverse-skill-"+remoteSkillPinnedCommit)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
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
