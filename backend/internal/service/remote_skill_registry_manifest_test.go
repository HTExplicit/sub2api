package service

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode"

	"github.com/stretchr/testify/require"
)

func TestRemoteSkillManifestIsFixedCompleteAndMatchesEmbeddedSeed(t *testing.T) {
	manifest, files, err := loadRemoteSkillSeedFiles()
	require.NoError(t, err)
	require.Equal(t, remoteSkillExpectedFiles, manifest.ExpectedFileCount)
	require.Equal(t, remoteSkillExpectedUpstreamFiles, manifest.UpstreamFileCount)
	require.Equal(t, remoteSkillExpectedPinnedFiles, manifest.PinnedFileCount)
	require.Len(t, manifest.Files, remoteSkillExpectedFiles)
	require.Len(t, files, remoteSkillExpectedFiles)

	var totalBytes int64
	markdownCount := 0
	pythonCount := 0
	hasUnicodePath := false
	for _, entry := range manifest.Files {
		totalBytes += int64(entry.ByteLength)
		markdownCount += boolInt(strings.HasSuffix(strings.ToLower(entry.Path), ".md"))
		pythonCount += boolInt(strings.HasSuffix(strings.ToLower(entry.Path), ".py"))
		hasUnicodePath = hasUnicodePath || strings.IndexFunc(entry.Path, func(r rune) bool { return r > unicode.MaxASCII }) >= 0
		require.True(t, remoteSkillManifestEntryMatches(entry, files[entry.Path]), "path=%s", entry.Path)
	}
	require.Equal(t, 388, markdownCount)
	require.Equal(t, 11, pythonCount)
	require.Greater(t, totalBytes, int64(7_000_000))
	require.True(t, hasUnicodePath)
	require.NotEmpty(t, files["references/ctf/web/auth-and-access.md"])
	require.Equal(t, remoteSkillPinnedWAFSHA256, hashBusinessSystemPromptBundleBytes(files[remoteSkillPinnedWAFPath]))
	require.NoError(t, validateCurrentRemoteSkillTree(files))
}

func TestRemoteSkillManifestRejectsMissingExtraDuplicateHashAndPinnedDrift(t *testing.T) {
	manifest, err := loadRemoteSkillManifest()
	require.NoError(t, err)

	tests := map[string]func(*remoteSkillManifest){
		"missing": func(candidate *remoteSkillManifest) {
			candidate.Files = candidate.Files[:len(candidate.Files)-1]
		},
		"extra": func(candidate *remoteSkillManifest) {
			candidate.Files = append(candidate.Files, candidate.Files[len(candidate.Files)-1])
			candidate.Files[len(candidate.Files)-1].Path = "zz-extra.md"
		},
		"duplicate": func(candidate *remoteSkillManifest) {
			candidate.Files[1] = candidate.Files[0]
		},
		"hash": func(candidate *remoteSkillManifest) {
			candidate.Files[0].SHA256 = "bad"
		},
		"unsorted": func(candidate *remoteSkillManifest) {
			candidate.Files[0], candidate.Files[1] = candidate.Files[1], candidate.Files[0]
		},
		"pinned provenance": func(candidate *remoteSkillManifest) {
			for index := range candidate.Files {
				if candidate.Files[index].SourceKind == "pinned" {
					candidate.Files[index].Provenance.HistoricalArchiveSHA256 = strings.Repeat("0", 64)
					return
				}
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := cloneRemoteSkillManifestForTest(manifest)
			mutate(&candidate)
			require.ErrorIs(t, validateRemoteSkillManifest(candidate), ErrBusinessSystemPromptBundleInvalid)
		})
	}
}

func TestCurrentRemoteSkillTreeRejectsManifestHashDrift(t *testing.T) {
	_, files, err := loadRemoteSkillSeedFiles()
	require.NoError(t, err)
	files["RULES.md"] = append(files["RULES.md"], byte('\n'))
	require.ErrorIs(t, validateCurrentRemoteSkillTree(files), ErrBusinessSystemPromptBundleInvalid)
}

func TestRemoteSkillMarkdownClosureValidatesFilesDirectoriesUnicodeAndRoutes(t *testing.T) {
	tests := map[string]struct {
		name    string
		content string
		files   map[string][]byte
		wantErr bool
	}{
		"file": {
			name: "docs/index.md", content: "[topic](topic.md)",
			files: map[string][]byte{"docs/index.md": []byte("index"), "docs/topic.md": []byte("topic")},
		},
		"directory": {
			name: "docs/index.md", content: "[topic](topic)",
			files: map[string][]byte{"docs/index.md": []byte("index"), "docs/topic/child.md": []byte("child")},
		},
		"unicode": {
			name: "docs/index.md", content: "[topic](认证漏洞.md)",
			files: map[string][]byte{"docs/index.md": []byte("index"), "docs/认证漏洞.md": []byte("topic")},
		},
		"relative image": {
			name: "docs/index.md", content: "![](diagram.txt)",
			files: map[string][]byte{"docs/index.md": []byte("index"), "docs/diagram.txt": []byte("diagram")},
		},
		"local literal route": {
			name: "skills/sec-web-api/INSTRUCTIONS.md", content: "`references/topic.md`",
			files: map[string][]byte{
				"skills/sec-web-api/INSTRUCTIONS.md":     []byte("entry"),
				"skills/sec-web-api/references/topic.md": []byte("topic"),
			},
		},
		"missing": {
			name: "docs/index.md", content: "[topic](missing.md)",
			files: map[string][]byte{"docs/index.md": []byte("index")}, wantErr: true,
		},
		"missing relative image": {
			name: "docs/index.md", content: "![](missing.txt)",
			files: map[string][]byte{"docs/index.md": []byte("index")}, wantErr: true,
		},
		"escaping": {
			name: "docs/index.md", content: "[topic](../../missing.md)",
			files: map[string][]byte{"docs/index.md": []byte("index")}, wantErr: true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			err := validateRemoteSkillMarkdownClosure(test.name, test.content, test.files)
			if test.wantErr {
				require.ErrorIs(t, err, ErrBusinessSystemPromptBundleInvalid)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestGenericRemoteSkillTreeRejectsPortableAndUnicodeNormalizationCollisions(t *testing.T) {
	base := map[string][]byte{
		"RULES.md":     []byte("rules"),
		"README_AI.md": []byte("readme"),
		"SKILL.md":     []byte("skill"),
	}
	caseCollision := cloneRemoteSkillFiles(base)
	caseCollision["Alpha.md"] = []byte("a")
	caseCollision["alpha.md"] = []byte("b")
	require.ErrorIs(t, validateGenericRemoteSkillTree(caseCollision), ErrBusinessSystemPromptBundleInvalid)

	nonNFC := cloneRemoteSkillFiles(base)
	nonNFC["references/cafe\u0301.md"] = []byte("topic")
	require.ErrorIs(t, validateGenericRemoteSkillTree(nonNFC), ErrBusinessSystemPromptBundleInvalid)
}

type concurrencyRemoteSkillHTTPClient struct {
	delegate *fakeRemoteSkillHTTPClient
	active   atomic.Int64
	maximum  atomic.Int64
}

func (c *concurrencyRemoteSkillHTTPClient) Do(request *http.Request) (*http.Response, error) {
	active := c.active.Add(1)
	defer c.active.Add(-1)
	for {
		maximum := c.maximum.Load()
		if active <= maximum || c.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	time.Sleep(time.Millisecond)
	return c.delegate.Do(request)
}

func TestMoxinggangRemoteSkillSourceUsesBoundedConcurrencyAndCanonicalUnicodeURLs(t *testing.T) {
	delegate := &fakeRemoteSkillHTTPClient{
		responses: modelGangSeedResponses(t), finalURLs: map[string]string{}, status: map[string]int{}, contentLength: map[string]int64{},
	}
	client := &concurrencyRemoteSkillHTTPClient{delegate: delegate}
	prompt, err := buildRemoteSkillPromptCapture([]byte(modelGangPromptCaptureFixture))
	require.NoError(t, err)
	_, err = NewMoxinggangRemoteSkillCandidateSource(client).Build(context.Background(), prompt, nil)
	require.NoError(t, err)
	require.Greater(t, client.maximum.Load(), int64(1))
	require.LessOrEqual(t, client.maximum.Load(), int64(remoteSkillSyncWorkers))

	manifest, err := loadRemoteSkillManifest()
	require.NoError(t, err)
	seen := make(map[string]int, remoteSkillExpectedUpstreamFiles)
	delegate.mu.Lock()
	for _, request := range delegate.requests {
		seen[request]++
	}
	delegate.mu.Unlock()
	require.Len(t, seen, remoteSkillExpectedUpstreamFiles)
	for request, count := range seen {
		require.Equal(t, 1, count, "url=%s", request)
	}

	for _, entry := range manifest.Files {
		if strings.IndexFunc(entry.Path, func(r rune) bool { return r > unicode.MaxASCII }) < 0 {
			continue
		}
		rawURL := remoteSkillUpstreamEntryURL(entry.Path)
		require.Contains(t, rawURL, "%")
		parsed, parseErr := url.Parse(rawURL)
		require.NoError(t, parseErr)
		require.True(t, validMoxinggangRemoteSkillURL(parsed))
		break
	}
}

func cloneRemoteSkillManifestForTest(manifest remoteSkillManifest) remoteSkillManifest {
	clone := manifest
	clone.Files = append([]remoteSkillManifestEntry(nil), manifest.Files...)
	for index := range clone.Files {
		if manifest.Files[index].Provenance != nil {
			provenance := *manifest.Files[index].Provenance
			clone.Files[index].Provenance = &provenance
		}
	}
	return clone
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
