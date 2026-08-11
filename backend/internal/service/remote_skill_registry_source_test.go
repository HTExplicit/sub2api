package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeRemoteSkillHTTPClient struct {
	responses     map[string][]byte
	finalURLs     map[string]string
	status        map[string]int
	contentLength map[string]int64
	requests      []string
	err           error
}

type blockingRemoteSkillHTTPClient struct{}

func (blockingRemoteSkillHTTPClient) Do(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func (f *fakeRemoteSkillHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.requests = append(f.requests, req.URL.String())
	body, ok := f.responses[req.URL.Path]
	status := f.status[req.URL.Path]
	if status == 0 {
		if ok {
			status = http.StatusOK
		} else {
			status = http.StatusNotFound
		}
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
	length := int64(len(body))
	if declared, exists := f.contentLength[req.URL.Path]; exists {
		length = declared
	}
	return &http.Response{
		StatusCode: status, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: length,
		Header: make(http.Header), Request: finalRequest,
	}, nil
}

func modelGangSeedResponses(t *testing.T) map[string][]byte {
	t.Helper()
	files, err := readRemoteSkillTreeFS(remoteSkillSeedFS, "remote_skill_seed/tree")
	require.NoError(t, err)
	responses := make(map[string][]byte, len(files))
	for name, body := range files {
		responses[RemoteSkillMoxinggangPath+"/"+name] = body
	}
	return responses
}

func TestMoxinggangRemoteSkillSourceFetchesFixedApprovedTreeAndBuildsPairedCandidate(t *testing.T) {
	client := &fakeRemoteSkillHTTPClient{responses: modelGangSeedResponses(t), finalURLs: map[string]string{}, status: map[string]int{}, contentLength: map[string]int64{}}
	source := NewMoxinggangRemoteSkillCandidateSource(client)
	source.now = func() time.Time { return time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC) }
	prompt, err := buildRemoteSkillPromptCapture([]byte(modelGangPromptCaptureFixture))
	require.NoError(t, err)

	candidate, err := source.Build(context.Background(), prompt, nil)
	require.NoError(t, err)
	require.Equal(t, remoteSkillExpectedFiles, candidate.Version.FileCount)
	require.Len(t, client.requests, remoteSkillExpectedFiles)
	require.Equal(t, RemoteSkillUpstreamSourceID, candidate.Version.UpstreamSourceID)
	require.Equal(t, RemoteSkillUpstreamRoot, candidate.Version.UpstreamRoot)
	require.Equal(t, RemoteSkillPublicRoot, candidate.Version.PublicRoot)
	require.Equal(t, prompt.RawSHA256, candidate.Prompt.RawSHA256)
	require.Equal(t, prompt.EffectiveSHA256, candidate.Prompt.EffectiveSHA256)
	require.NotEqual(t, candidate.Version.RawTreeSHA256, candidate.Version.EffectiveTreeSHA256)
	require.Contains(t, string(candidate.RawFiles["SKILL.md"]), RemoteSkillUpstreamRoot)
	require.NotContains(t, string(candidate.EffectiveFiles["SKILL.md"]), RemoteSkillUpstreamRoot)
	require.Contains(t, string(candidate.EffectiveFiles["SKILL.md"]), RemoteSkillPublicRoot)

	foundGitHubLink := false
	for name, raw := range candidate.RawFiles {
		if strings.Contains(string(raw), "https://github.com/") {
			foundGitHubLink = true
			require.Contains(t, string(candidate.EffectiveFiles[name]), "https://github.com/")
		}
	}
	require.True(t, foundGitHubLink)
}

func TestMoxinggangRemoteSkillSourceRejectsRedirectMissingPartialAndTransportFailure(t *testing.T) {
	prompt, err := buildRemoteSkillPromptCapture([]byte(modelGangPromptCaptureFixture))
	require.NoError(t, err)
	tests := map[string]func(*fakeRemoteSkillHTTPClient){
		"cross-domain redirect": func(client *fakeRemoteSkillHTTPClient) {
			client.finalURLs[RemoteSkillMoxinggangPath+"/RULES.md"] = "https://example.com/RULES.md"
		},
		"same-domain redirect": func(client *fakeRemoteSkillHTTPClient) {
			client.finalURLs[RemoteSkillMoxinggangPath+"/RULES.md"] = RemoteSkillUpstreamRoot + "/README_AI.md"
		},
		"missing file": func(client *fakeRemoteSkillHTTPClient) {
			delete(client.responses, RemoteSkillMoxinggangPath+"/RULES.md")
		},
		"partial response": func(client *fakeRemoteSkillHTTPClient) {
			client.contentLength[RemoteSkillMoxinggangPath+"/RULES.md"] = int64(len(client.responses[RemoteSkillMoxinggangPath+"/RULES.md"]) + 1)
		},
		"oversized response": func(client *fakeRemoteSkillHTTPClient) {
			client.contentLength[RemoteSkillMoxinggangPath+"/RULES.md"] = businessSystemPromptBundleMaxFileBytes + 1
		},
		"transport failure": func(client *fakeRemoteSkillHTTPClient) {
			client.err = errors.New("network down")
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			client := &fakeRemoteSkillHTTPClient{responses: modelGangSeedResponses(t), finalURLs: map[string]string{}, status: map[string]int{}, contentLength: map[string]int64{}}
			mutate(client)
			_, err := NewMoxinggangRemoteSkillCandidateSource(client).Build(context.Background(), prompt, nil)
			require.Error(t, err)
		})
	}
}

func TestMoxinggangRemoteSkillSourceRejectsUnapprovedSameRootReference(t *testing.T) {
	prompt, err := buildRemoteSkillPromptCapture([]byte(modelGangPromptCaptureFixture))
	require.NoError(t, err)
	responses := modelGangSeedResponses(t)
	rulesPath := RemoteSkillMoxinggangPath + "/RULES.md"
	responses[rulesPath] = append(bytes.Clone(responses[rulesPath]), []byte("\n"+RemoteSkillUpstreamRoot+"/unapproved.md\n")...)
	client := &fakeRemoteSkillHTTPClient{responses: responses, finalURLs: map[string]string{}, status: map[string]int{}, contentLength: map[string]int64{}}

	_, err = NewMoxinggangRemoteSkillCandidateSource(client).Build(context.Background(), prompt, nil)
	require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
}

func TestMoxinggangRemoteSkillSourceHonorsCallerTimeout(t *testing.T) {
	prompt, err := buildRemoteSkillPromptCapture([]byte(modelGangPromptCaptureFixture))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

	_, err = NewMoxinggangRemoteSkillCandidateSource(blockingRemoteSkillHTTPClient{}).Build(ctx, prompt, nil)
	require.Error(t, err)
}
