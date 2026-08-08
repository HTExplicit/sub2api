package service

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	BusinessSystemPromptManagedSourceGPT56 = "mdx_tom_gpt_5_6_instruct"
	GPT56PromptLicenseSHA256               = "edb45f4cb24498171bc9a748bea6eabba5ed8176213522822128622623413eeb"

	gpt56PromptRepository = "MDX-Tom/gpt-5.6-instruct"
	gpt56PromptCommitURL  = "https://api.github.com/repos/MDX-Tom/gpt-5.6-instruct/commits/main"
	gpt56PromptMaxText    = 128 << 10
	gpt56PromptMaxLicense = 16 << 10
	gpt56PromptMaxArchive = 128 << 10
)

var (
	ErrBusinessSystemPromptSourceUnavailable    = errors.New("business system prompt source unavailable")
	ErrBusinessSystemPromptSourceInvalid        = errors.New("business system prompt source invalid")
	ErrBusinessSystemPromptSourceLicenseChanged = errors.New("business system prompt source license changed")

	gpt56PromptVersionPattern = regexp.MustCompile("(?m)^当前生产版本为 `(v[1-9][0-9]*)`(?:，|,|。|\\s|$)")
	gpt56PromptHashPattern    = regexp.MustCompile(`(?m)^(v[1-9][0-9]*)[ \t]+([0-9a-fA-F]{64})[ \t]*$`)
)

type BusinessSystemPromptSourceCandidate struct {
	ManagedSource        string `json:"managed_source"`
	SourceRepository     string `json:"source_repository"`
	SourceCommit         string `json:"source_commit"`
	SourceVersion        string `json:"source_version"`
	SourceArtifact       string `json:"source_artifact"`
	SourceArtifactSHA256 string `json:"source_artifact_sha256"`
	SourceLicenseSHA256  string `json:"source_license_sha256"`
	Body                 string `json:"-"`
	SHA256               string `json:"sha256"`
	ByteLength           int    `json:"byte_length"`
}

type BusinessSystemPromptSource interface {
	Fetch(context.Context) (BusinessSystemPromptSourceCandidate, error)
}

func ValidateBusinessSystemPromptSourceCandidate(candidate BusinessSystemPromptSourceCandidate) error {
	if candidate.ManagedSource != BusinessSystemPromptManagedSourceGPT56 ||
		candidate.SourceRepository != gpt56PromptRepository ||
		candidate.SourceLicenseSHA256 != GPT56PromptLicenseSHA256 ||
		!regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(candidate.SourceCommit) ||
		!regexp.MustCompile(`^v[1-9][0-9]*$`).MatchString(candidate.SourceVersion) ||
		candidate.SourceArtifact != "gpt-5.6-sol-unrestricted-"+candidate.SourceVersion+".zip" ||
		!isLowerHexSHA256(candidate.SourceArtifactSHA256) {
		return fmt.Errorf("%w: source provenance rejected", ErrBusinessSystemPromptSourceInvalid)
	}
	hash, byteLength, err := ValidateBusinessSystemPromptBody(candidate.Body)
	if err != nil || hash != candidate.SHA256 || byteLength != candidate.ByteLength {
		return fmt.Errorf("%w: source prompt digest mismatch", ErrBusinessSystemPromptSourceInvalid)
	}
	return nil
}

type GitHubGPT56PromptSource struct {
	client RemoteSkillHTTPDoer
}

func NewGitHubGPT56PromptSource(client RemoteSkillHTTPDoer) *GitHubGPT56PromptSource {
	if client == nil {
		client = newGPT56PromptHTTPClient()
	}
	return &GitHubGPT56PromptSource{client: client}
}

func (s *GitHubGPT56PromptSource) Fetch(ctx context.Context) (BusinessSystemPromptSourceCandidate, error) {
	if s == nil || s.client == nil {
		return BusinessSystemPromptSourceCandidate{}, ErrBusinessSystemPromptSourceUnavailable
	}
	commit, err := s.resolveCommit(ctx)
	if err != nil {
		return BusinessSystemPromptSourceCandidate{}, err
	}
	readme, err := s.download(ctx, gpt56PromptRawURL(commit, "README.md"), gpt56PromptMaxText, "raw.githubusercontent.com")
	if err != nil {
		return BusinessSystemPromptSourceCandidate{}, err
	}
	version, expectedArchiveSHA, err := parseGPT56PromptREADME(readme)
	if err != nil {
		return BusinessSystemPromptSourceCandidate{}, err
	}
	license, err := s.download(ctx, gpt56PromptRawURL(commit, "LICENSE"), gpt56PromptMaxLicense, "raw.githubusercontent.com")
	if err != nil {
		return BusinessSystemPromptSourceCandidate{}, err
	}
	licenseSHA, err := hashGPT56PromptLicense(license)
	if err != nil {
		return BusinessSystemPromptSourceCandidate{}, err
	}
	if licenseSHA != GPT56PromptLicenseSHA256 {
		return BusinessSystemPromptSourceCandidate{}, ErrBusinessSystemPromptSourceLicenseChanged
	}
	artifact := "gpt-5.6-sol-unrestricted-" + version + ".zip"
	archive, err := s.download(ctx, gpt56PromptRawURL(commit, artifact), gpt56PromptMaxArchive, "raw.githubusercontent.com")
	if err != nil {
		return BusinessSystemPromptSourceCandidate{}, err
	}
	archiveSHA := hashGPT56PromptBytes(archive)
	if archiveSHA != expectedArchiveSHA {
		return BusinessSystemPromptSourceCandidate{}, fmt.Errorf("%w: archive digest mismatch", ErrBusinessSystemPromptSourceInvalid)
	}
	body, err := extractGPT56PromptArchive(archive, version)
	if err != nil {
		return BusinessSystemPromptSourceCandidate{}, err
	}
	bodySHA, byteLength, err := ValidateBusinessSystemPromptBody(body)
	if err != nil {
		return BusinessSystemPromptSourceCandidate{}, fmt.Errorf("%w: prompt body rejected", ErrBusinessSystemPromptSourceInvalid)
	}
	return BusinessSystemPromptSourceCandidate{
		ManagedSource:        BusinessSystemPromptManagedSourceGPT56,
		SourceRepository:     gpt56PromptRepository,
		SourceCommit:         commit,
		SourceVersion:        version,
		SourceArtifact:       artifact,
		SourceArtifactSHA256: archiveSHA,
		SourceLicenseSHA256:  GPT56PromptLicenseSHA256,
		Body:                 body,
		SHA256:               bodySHA,
		ByteLength:           byteLength,
	}, nil
}

func (s *GitHubGPT56PromptSource) resolveCommit(ctx context.Context) (string, error) {
	raw, err := s.download(ctx, gpt56PromptCommitURL, 128<<10, "api.github.com")
	if err != nil {
		return "", err
	}
	var response struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", fmt.Errorf("%w: invalid commit response", ErrBusinessSystemPromptSourceInvalid)
	}
	commit := strings.ToLower(strings.TrimSpace(response.SHA))
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(commit) {
		return "", fmt.Errorf("%w: invalid source commit", ErrBusinessSystemPromptSourceInvalid)
	}
	return commit, nil
}

func (s *GitHubGPT56PromptSource) download(ctx context.Context, rawURL string, maximum int64, expectedHost string) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != expectedHost || parsed.User != nil || parsed.Fragment != "" {
		return nil, fmt.Errorf("%w: source URL rejected", ErrBusinessSystemPromptSourceInvalid)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, ErrBusinessSystemPromptSourceUnavailable
	}
	req.Header.Set("Accept", "application/vnd.github+json, application/octet-stream;q=0.9, text/plain;q=0.8")
	req.Header.Set("User-Agent", "Sub2API-GPT56-Prompt-Sync/1")
	response, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: source request failed", ErrBusinessSystemPromptSourceUnavailable)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: source returned status %d", ErrBusinessSystemPromptSourceUnavailable, response.StatusCode)
	}
	if response.ContentLength > maximum {
		return nil, fmt.Errorf("%w: source response exceeds limit", ErrBusinessSystemPromptSourceInvalid)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return nil, fmt.Errorf("%w: source read failed", ErrBusinessSystemPromptSourceUnavailable)
	}
	if int64(len(raw)) > maximum {
		return nil, fmt.Errorf("%w: source response exceeds limit", ErrBusinessSystemPromptSourceInvalid)
	}
	return raw, nil
}

func gpt56PromptRawURL(commit, name string) string {
	return "https://raw.githubusercontent.com/MDX-Tom/gpt-5.6-instruct/" + commit + "/" + name
}

func parseGPT56PromptREADME(raw []byte) (string, string, error) {
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return "", "", fmt.Errorf("%w: README encoding rejected", ErrBusinessSystemPromptSourceInvalid)
	}
	versionMatches := gpt56PromptVersionPattern.FindAllSubmatch(raw, -1)
	if len(versionMatches) != 1 {
		return "", "", fmt.Errorf("%w: production version is ambiguous", ErrBusinessSystemPromptSourceInvalid)
	}
	version := string(versionMatches[0][1])
	hashMatches := gpt56PromptHashPattern.FindAllSubmatch(raw, -1)
	archiveSHA := ""
	for _, match := range hashMatches {
		if string(match[1]) != version {
			continue
		}
		if archiveSHA != "" {
			return "", "", fmt.Errorf("%w: production digest is ambiguous", ErrBusinessSystemPromptSourceInvalid)
		}
		archiveSHA = strings.ToLower(string(match[2]))
	}
	if archiveSHA == "" {
		return "", "", fmt.Errorf("%w: production digest missing", ErrBusinessSystemPromptSourceInvalid)
	}
	return version, archiveSHA, nil
}

func extractGPT56PromptArchive(raw []byte, version string) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil || len(reader.File) != 1 {
		return "", fmt.Errorf("%w: archive must contain one file", ErrBusinessSystemPromptSourceInvalid)
	}
	entry := reader.File[0]
	expectedName := "gpt-5.6-sol-unrestricted-" + version + ".md"
	if entry.Name != expectedName || entry.Flags&1 != 0 {
		return "", fmt.Errorf("%w: archive entry rejected", ErrBusinessSystemPromptSourceInvalid)
	}
	mode := entry.Mode()
	if mode&os.ModeSymlink != 0 || !mode.IsRegular() || entry.UncompressedSize64 > BusinessSystemPromptMaxBytes {
		return "", fmt.Errorf("%w: archive entry type or size rejected", ErrBusinessSystemPromptSourceInvalid)
	}
	stream, err := entry.Open()
	if err != nil {
		return "", fmt.Errorf("%w: archive entry open failed", ErrBusinessSystemPromptSourceInvalid)
	}
	body, readErr := io.ReadAll(io.LimitReader(stream, BusinessSystemPromptMaxBytes+1))
	closeErr := stream.Close()
	if readErr != nil || closeErr != nil || len(body) > BusinessSystemPromptMaxBytes {
		return "", fmt.Errorf("%w: archive entry read failed", ErrBusinessSystemPromptSourceInvalid)
	}
	if !utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0 || bytes.HasPrefix(body, []byte{0xef, 0xbb, 0xbf}) {
		return "", fmt.Errorf("%w: prompt encoding rejected", ErrBusinessSystemPromptSourceInvalid)
	}
	return string(body), nil
}

func newGPT56PromptHTTPClient() *http.Client {
	allowed := map[string]struct{}{
		"api.github.com":            {},
		"raw.githubusercontent.com": {},
	}
	return &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 2 || req.URL.Scheme != "https" {
				return fmt.Errorf("%w: redirect target rejected", ErrBusinessSystemPromptSourceInvalid)
			}
			if _, ok := allowed[req.URL.Hostname()]; !ok {
				return fmt.Errorf("%w: redirect target rejected", ErrBusinessSystemPromptSourceInvalid)
			}
			return nil
		},
	}
}

func hashGPT56PromptBytes(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func hashGPT56PromptLicense(raw []byte) (string, error) {
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 || bytes.HasPrefix(raw, []byte{0xef, 0xbb, 0xbf}) {
		return "", fmt.Errorf("%w: license encoding rejected", ErrBusinessSystemPromptSourceInvalid)
	}
	// The pinned license digest was captured from the repository's Windows
	// checkout. Canonicalizing line endings keeps that identity stable while
	// still rejecting any license text change from GitHub's LF raw endpoint.
	canonical := strings.ReplaceAll(string(raw), "\r\n", "\n")
	canonical = strings.ReplaceAll(canonical, "\r", "\n")
	canonical = strings.ReplaceAll(canonical, "\n", "\r\n")
	return hashGPT56PromptBytes([]byte(canonical)), nil
}
