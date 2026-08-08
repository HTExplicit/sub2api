package service

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type scriptedPromptSourceHTTP struct {
	responses map[string][]byte
	requests  []string
}

type timeoutPromptSourceHTTP struct{}

func (timeoutPromptSourceHTTP) Do(*http.Request) (*http.Response, error) {
	return nil, context.DeadlineExceeded
}

func (s *scriptedPromptSourceHTTP) Do(req *http.Request) (*http.Response, error) {
	s.requests = append(s.requests, req.URL.String())
	raw, ok := s.responses[req.URL.String()]
	if !ok {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}
	return &http.Response{
		StatusCode:    http.StatusOK,
		Body:          io.NopCloser(bytes.NewReader(raw)),
		Header:        make(http.Header),
		ContentLength: int64(len(raw)),
		Request:       req,
	}, nil
}

func TestGitHubGPT56PromptSourceFetchesVerifiedCandidate(t *testing.T) {
	commit := strings.Repeat("a", 40)
	body := []byte("verified prompt\n")
	archive := buildGPT56PromptTestZIP(t, []gpt56PromptTestEntry{{
		name: "gpt-5.6-sol-unrestricted-v45.md", body: body, mode: 0o644,
	}})
	archiveDigest := sha256.Sum256(archive)
	archiveSHA := hex.EncodeToString(archiveDigest[:])
	readme := []byte("当前生产版本为 `v45`\n\n当前发布 ZIP 的 SHA256：\n\n```text\nv45  " + archiveSHA + "\n```\n")
	license := []byte(gpt56PromptTestLicenseLF)

	client := &scriptedPromptSourceHTTP{responses: map[string][]byte{
		gpt56PromptCommitURL:                                          []byte(`{"sha":"` + commit + `"}`),
		gpt56PromptRawURL(commit, "README.md"):                        readme,
		gpt56PromptRawURL(commit, "LICENSE"):                          license,
		gpt56PromptRawURL(commit, "gpt-5.6-sol-unrestricted-v45.zip"): archive,
	}}
	source := NewGitHubGPT56PromptSource(client)
	candidate, err := source.Fetch(context.Background())
	require.NoError(t, err)
	require.Equal(t, BusinessSystemPromptManagedSourceGPT56, candidate.ManagedSource)
	require.Equal(t, "MDX-Tom/gpt-5.6-instruct", candidate.SourceRepository)
	require.Equal(t, commit, candidate.SourceCommit)
	require.Equal(t, "v45", candidate.SourceVersion)
	require.Equal(t, "gpt-5.6-sol-unrestricted-v45.zip", candidate.SourceArtifact)
	require.Equal(t, archiveSHA, candidate.SourceArtifactSHA256)
	require.Equal(t, GPT56PromptLicenseSHA256, candidate.SourceLicenseSHA256)
	require.Equal(t, string(body), candidate.Body)
	require.Equal(t, "82e48be49cf325ca5c62c7382f3738fe1d53528b0a9c0362606a3f6ecf3ffa61", candidate.SHA256)
	require.Equal(t, len(body), candidate.ByteLength)
	require.Len(t, client.requests, 4)
}

func TestGPT56PromptLicenseHashCanonicalizesLineEndings(t *testing.T) {
	lfSHA, err := hashGPT56PromptLicense([]byte(gpt56PromptTestLicenseLF))
	require.NoError(t, err)
	crlfSHA, err := hashGPT56PromptLicense([]byte(strings.ReplaceAll(gpt56PromptTestLicenseLF, "\n", "\r\n")))
	require.NoError(t, err)
	require.Equal(t, GPT56PromptLicenseSHA256, lfSHA)
	require.Equal(t, GPT56PromptLicenseSHA256, crlfSHA)

	_, err = hashGPT56PromptLicense([]byte("bad\x00license"))
	require.ErrorIs(t, err, ErrBusinessSystemPromptSourceInvalid)
}

func TestGitHubGPT56PromptSourceRejectsChangedLicenseBeforeArchive(t *testing.T) {
	commit := strings.Repeat("b", 40)
	client := &scriptedPromptSourceHTTP{responses: map[string][]byte{
		gpt56PromptCommitURL:                   []byte(`{"sha":"` + commit + `"}`),
		gpt56PromptRawURL(commit, "README.md"): []byte("当前生产版本为 `v45`\n\n当前发布 ZIP 的 SHA256：\n\n```text\nv45  " + strings.Repeat("c", 64) + "\n```\n"),
		gpt56PromptRawURL(commit, "LICENSE"):   []byte("changed"),
	}}
	_, err := NewGitHubGPT56PromptSource(client).Fetch(context.Background())
	require.ErrorIs(t, err, ErrBusinessSystemPromptSourceLicenseChanged)
	require.Len(t, client.requests, 3)
}

func TestExtractGPT56PromptArchiveRejectsUnsafeEntries(t *testing.T) {
	oversized := bytes.Repeat([]byte("a"), BusinessSystemPromptMaxBytes+1)
	for name, entries := range map[string][]gpt56PromptTestEntry{
		"extra file": {
			{name: "gpt-5.6-sol-unrestricted-v45.md", body: []byte("body"), mode: 0o644},
			{name: "extra.txt", body: []byte("extra"), mode: 0o644},
		},
		"path traversal": {{name: "../gpt-5.6-sol-unrestricted-v45.md", body: []byte("body"), mode: 0o644}},
		"symlink":        {{name: "gpt-5.6-sol-unrestricted-v45.md", body: []byte("target"), mode: os.ModeSymlink | 0o777}},
		"nul":            {{name: "gpt-5.6-sol-unrestricted-v45.md", body: []byte("bad\x00body"), mode: 0o644}},
		"invalid utf8":   {{name: "gpt-5.6-sol-unrestricted-v45.md", body: []byte{0xff, 0xfe}, mode: 0o644}},
		"oversized":      {{name: "gpt-5.6-sol-unrestricted-v45.md", body: oversized, mode: 0o644}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := extractGPT56PromptArchive(buildGPT56PromptTestZIP(t, entries), "v45")
			require.ErrorIs(t, err, ErrBusinessSystemPromptSourceInvalid)
		})
	}
}

func TestGPT56PromptHTTPClientRejectsForeignRedirect(t *testing.T) {
	client := newGPT56PromptHTTPClient()
	req, err := http.NewRequest(http.MethodGet, "https://example.com/file", nil)
	require.NoError(t, err)
	err = client.CheckRedirect(req, []*http.Request{{URL: req.URL}})
	require.ErrorIs(t, err, ErrBusinessSystemPromptSourceInvalid)
}

func TestGitHubGPT56PromptSourceMapsTimeoutToUnavailable(t *testing.T) {
	_, err := NewGitHubGPT56PromptSource(timeoutPromptSourceHTTP{}).Fetch(context.Background())
	require.ErrorIs(t, err, ErrBusinessSystemPromptSourceUnavailable)
}

type gpt56PromptTestEntry struct {
	name string
	body []byte
	mode os.FileMode
}

func buildGPT56PromptTestZIP(t *testing.T, entries []gpt56PromptTestEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		stream, err := writer.CreateHeader(header)
		require.NoError(t, err)
		_, err = stream.Write(entry.body)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}

const gpt56PromptTestLicenseLF = `MIT License

Copyright (c) 2026 li lingbo
Copyright (c) 2026 yynxxxxx

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
`
