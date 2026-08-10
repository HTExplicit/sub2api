package service

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	remoteSkillGitHubZipPrefix        = "https://codeload.github.com/zhaoxuya520/reverse-skill/zip/"
	remoteSkillPinnedCommit           = "a5d8c9233b98c52df387d5b1a0ef669fcaa51374"
	remoteSkillPinnedSourceArchiveSHA = "c6cc4a531b62ded1fae92cc8cdace9cf7833fe23978350161d90dedff77f80df"
	remoteSkillSyncTimeout            = 2 * time.Minute
	remoteSkillSourceMaxBytes         = 128 << 20
	remoteSkillMaxFileCount           = 2000
	remoteSkillMaxTotalBytes          = 256 << 20
)

var remoteSkillMoxinggangReferencePattern = regexp.MustCompile(`(?i)(?:REMOTE_ROOT|https://moxinggang\.com/skills/security-research/current)/([a-z0-9][a-z0-9._/-]*)`)

type remoteSkillSourcePin struct {
	Commit        string
	ArchiveSHA256 string
	CoreSHA256    map[string]string
}

func releaseRemoteSkillSourcePin() remoteSkillSourcePin {
	return remoteSkillSourcePin{
		Commit:        remoteSkillPinnedCommit,
		ArchiveSHA256: remoteSkillPinnedSourceArchiveSHA,
		CoreSHA256: map[string]string{
			"RULES.md":        "2d86efa38f8a8b9ef23fa71edcae35cf111a8fef9027a8893ff66e7e4086afa0",
			"README_AI.md":    "d79c9b34beba0160c1a290763ce40ddf9f4027d2086f575a1b396188ddef87c9",
			"skills/SKILL.md": "2c7994642ae2cd97a15fffc0d6e119e07e83582ca70cc9a7a5d212aa9a947a56",
		},
	}
}

type RemoteSkillHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type GitHubRemoteSkillCandidateSource struct {
	client    RemoteSkillHTTPDoer
	sourcePin remoteSkillSourcePin
}

type MoxinggangRemoteSkillCandidateSource struct {
	client RemoteSkillHTTPDoer
}

type remoteSkillProvider interface {
	Build(context.Context, *BusinessSystemPromptBundleManifest) (RemoteSkillCandidate, error)
}

type remoteSkillCandidateSourceSelector struct {
	providers map[string]remoteSkillProvider
}

func NewGitHubRemoteSkillCandidateSource(client RemoteSkillHTTPDoer) *GitHubRemoteSkillCandidateSource {
	if client == nil {
		client = newRemoteSkillHTTPClient()
	}
	return &GitHubRemoteSkillCandidateSource{client: client, sourcePin: releaseRemoteSkillSourcePin()}
}

func NewMoxinggangRemoteSkillCandidateSource(client RemoteSkillHTTPDoer) *MoxinggangRemoteSkillCandidateSource {
	if client == nil {
		client = newRemoteSkillHTTPClient()
	}
	return &MoxinggangRemoteSkillCandidateSource{client: client}
}

func newRemoteSkillCandidateSourceSelector(providers map[string]remoteSkillProvider) *remoteSkillCandidateSourceSelector {
	cloned := make(map[string]remoteSkillProvider, len(providers))
	for sourceID, provider := range providers {
		cloned[sourceID] = provider
	}
	return &remoteSkillCandidateSourceSelector{providers: cloned}
}

func NewRemoteSkillCandidateSourceSelector(client RemoteSkillHTTPDoer) RemoteSkillCandidateSource {
	return newRemoteSkillCandidateSourceSelector(map[string]remoteSkillProvider{
		RemoteSkillSourceGitHubOfficial: NewGitHubRemoteSkillCandidateSource(client),
		RemoteSkillSourceMoxinggang:     NewMoxinggangRemoteSkillCandidateSource(client),
	})
}

func (s *remoteSkillCandidateSourceSelector) Build(ctx context.Context, sourceID string, active *BusinessSystemPromptBundleManifest) (RemoteSkillCandidate, error) {
	sourceID, err := NormalizeRemoteSkillSourceID(sourceID)
	if err != nil {
		return RemoteSkillCandidate{}, err
	}
	provider := s.providers[sourceID]
	if provider == nil {
		return RemoteSkillCandidate{}, fmt.Errorf("%w: remote skill source unavailable", ErrBusinessSystemPromptBundleUnavailable)
	}
	candidate, err := provider.Build(ctx, active)
	if err != nil {
		return RemoteSkillCandidate{}, err
	}
	if candidate.Version.SourceID != sourceID || candidate.Version.RemoteRoot != remoteSkillSourceRoot(sourceID) {
		return RemoteSkillCandidate{}, fmt.Errorf("%w: source identity mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	return candidate, nil
}

func (s *GitHubRemoteSkillCandidateSource) Build(ctx context.Context, active *BusinessSystemPromptBundleManifest) (RemoteSkillCandidate, error) {
	if s == nil || s.client == nil {
		return RemoteSkillCandidate{}, fmt.Errorf("%w: sync HTTP client unavailable", ErrBusinessSystemPromptBundleUnavailable)
	}
	ctx, cancel := context.WithTimeout(ctx, remoteSkillSyncTimeout)
	defer cancel()
	pin := s.sourcePin
	if pin.Commit == "" {
		pin = releaseRemoteSkillSourcePin()
	}
	commit := pin.Commit
	baseArchive, err := s.download(ctx, remoteSkillGitHubZipPrefix+commit, remoteSkillSourceMaxBytes, "codeload.github.com")
	if err != nil {
		return RemoteSkillCandidate{}, err
	}
	if hashBusinessSystemPromptBundleBytes(baseArchive) != pin.ArchiveSHA256 {
		return RemoteSkillCandidate{}, fmt.Errorf("%w: pinned source archive digest mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	files, err := extractRemoteSkillBaseArchive(baseArchive, "reverse-skill-"+commit)
	if err != nil {
		return RemoteSkillCandidate{}, err
	}
	if err := validateRemoteSkillPinnedSource(files, pin); err != nil {
		return RemoteSkillCandidate{}, err
	}
	return buildRemoteSkillCandidate(RemoteSkillSourceGitHubOfficial, RemoteSkillGitHubRoot, commit, pin.ArchiveSHA256, []string{
		"RULES.md", "README_AI.md", "skills/SKILL.md",
	}, files, active)
}

func (s *MoxinggangRemoteSkillCandidateSource) Build(ctx context.Context, active *BusinessSystemPromptBundleManifest) (RemoteSkillCandidate, error) {
	if s == nil || s.client == nil {
		return RemoteSkillCandidate{}, fmt.Errorf("%w: sync HTTP client unavailable", ErrBusinessSystemPromptBundleUnavailable)
	}
	ctx, cancel := context.WithTimeout(ctx, remoteSkillSyncTimeout)
	defer cancel()
	coreFiles := []string{"RULES.md", "README_AI.md", "SKILL.md"}
	queue := append([]string(nil), coreFiles...)
	seen := make(map[string]struct{})
	files := make(map[string][]byte)
	var total int64
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		if len(seen) > remoteSkillMaxFileCount {
			return RemoteSkillCandidate{}, fmt.Errorf("%w: remote entry tree exceeds file limit", ErrBusinessSystemPromptBundleInvalid)
		}
		raw, err := s.downloadEntry(ctx, name)
		if err != nil {
			return RemoteSkillCandidate{}, err
		}
		total += int64(len(raw))
		if total > remoteSkillMaxTotalBytes {
			return RemoteSkillCandidate{}, fmt.Errorf("%w: remote entry tree exceeds size limit", ErrBusinessSystemPromptBundleInvalid)
		}
		files[name] = raw
		if remoteSkillFileKind(name, raw) == "binary" {
			continue
		}
		queue = append(queue, remoteSkillMoxinggangReferences(raw)...)
	}
	hashes := make(map[string]string, len(files))
	for name, raw := range files {
		hashes[name] = hashBusinessSystemPromptBundleBytes(raw)
	}
	sourceSHA := hashRemoteSkillFileSet(hashes)
	return buildRemoteSkillCandidate(RemoteSkillSourceMoxinggang, RemoteSkillMoxinggangRoot, sourceSHA[:40], sourceSHA, coreFiles, files, active)
}

func (s *MoxinggangRemoteSkillCandidateSource) downloadEntry(ctx context.Context, name string) ([]byte, error) {
	normalized, err := normalizeBundleRelativePath(name)
	if err != nil || normalized != name {
		return nil, fmt.Errorf("%w: remote entry path rejected", ErrBusinessSystemPromptBundleInvalid)
	}
	rawURL := RemoteSkillMoxinggangRoot + "/" + name
	parsed, err := url.Parse(rawURL)
	if err != nil || !validMoxinggangRemoteSkillURL(parsed) {
		return nil, fmt.Errorf("%w: remote entry URL rejected", ErrBusinessSystemPromptBundleInvalid)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/markdown, text/plain;q=0.9, application/octet-stream;q=0.8")
	req.Header.Set("User-Agent", "Sub2API-CodexRip-Skill-Sync/1")
	response, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: source request failed", ErrBusinessSystemPromptBundleUnavailable)
	}
	defer func() { _ = response.Body.Close() }()
	if response.Request == nil || response.Request.URL == nil || !validMoxinggangRemoteSkillURL(response.Request.URL) || response.Request.URL.Path != parsed.Path {
		return nil, fmt.Errorf("%w: source final URL rejected", ErrBusinessSystemPromptBundleInvalid)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: source returned status %d", ErrBusinessSystemPromptBundleUnavailable, response.StatusCode)
	}
	if response.ContentLength > businessSystemPromptBundleMaxFileBytes {
		return nil, fmt.Errorf("%w: source response exceeds limit", ErrBusinessSystemPromptBundleInvalid)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, businessSystemPromptBundleMaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: source read failed", ErrBusinessSystemPromptBundleUnavailable)
	}
	if len(raw) == 0 || len(raw) > businessSystemPromptBundleMaxFileBytes {
		return nil, fmt.Errorf("%w: source response exceeds limit", ErrBusinessSystemPromptBundleInvalid)
	}
	return raw, nil
}

func validMoxinggangRemoteSkillURL(parsed *url.URL) bool {
	if parsed == nil || parsed.Scheme != "https" || parsed.Hostname() != "moxinggang.com" || parsed.Port() != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" {
		return false
	}
	if parsed.Path != path.Clean(parsed.Path) || !strings.HasPrefix(parsed.Path, RemoteSkillMoxinggangPath+"/") {
		return false
	}
	relative := strings.TrimPrefix(parsed.Path, RemoteSkillMoxinggangPath+"/")
	normalized, err := normalizeBundleRelativePath(relative)
	return err == nil && normalized == relative
}

func remoteSkillMoxinggangReferences(raw []byte) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, match := range remoteSkillMoxinggangReferencePattern.FindAllSubmatch(raw, -1) {
		name := strings.TrimRight(string(match[1]), ".,;:!?)]}`'\"")
		if strings.HasSuffix(name, "/") {
			continue
		}
		normalized, err := normalizeBundleRelativePath(name)
		if err != nil || normalized != name {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func (s *GitHubRemoteSkillCandidateSource) download(ctx context.Context, rawURL string, maximum int64, expectedHost string) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != expectedHost || parsed.Port() != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("%w: source URL rejected", ErrBusinessSystemPromptBundleInvalid)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json, application/octet-stream;q=0.9, text/plain;q=0.8")
	req.Header.Set("User-Agent", "Sub2API-CodexRip-Skill-Sync/1")
	response, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: source request failed", ErrBusinessSystemPromptBundleUnavailable)
	}
	defer func() { _ = response.Body.Close() }()
	if response.Request == nil || response.Request.URL == nil || response.Request.URL.Scheme != "https" || response.Request.URL.Hostname() != expectedHost || response.Request.URL.Port() != "" || response.Request.URL.Path != parsed.Path || response.Request.URL.RawQuery != "" || response.Request.URL.Fragment != "" {
		return nil, fmt.Errorf("%w: source final URL rejected", ErrBusinessSystemPromptBundleInvalid)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: source returned status %d", ErrBusinessSystemPromptBundleUnavailable, response.StatusCode)
	}
	if response.ContentLength > maximum {
		return nil, fmt.Errorf("%w: source response exceeds limit", ErrBusinessSystemPromptBundleInvalid)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return nil, fmt.Errorf("%w: source read failed", ErrBusinessSystemPromptBundleUnavailable)
	}
	if int64(len(raw)) > maximum {
		return nil, fmt.Errorf("%w: source response exceeds limit", ErrBusinessSystemPromptBundleInvalid)
	}
	return raw, nil
}

func newRemoteSkillHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 45 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) == 0 || len(via) >= 3 || req.URL.Scheme != "https" || req.URL.Hostname() != via[0].URL.Hostname() {
				return errorsNewRemoteSkillRedirect()
			}
			switch req.URL.Hostname() {
			case "codeload.github.com":
				if !strings.HasPrefix(req.URL.Path, "/zhaoxuya520/reverse-skill/zip/") || req.URL.RawQuery != "" || req.URL.Fragment != "" {
					return errorsNewRemoteSkillRedirect()
				}
			case "moxinggang.com":
				if !validMoxinggangRemoteSkillURL(req.URL) {
					return errorsNewRemoteSkillRedirect()
				}
			default:
				return errorsNewRemoteSkillRedirect()
			}
			return nil
		},
	}
}

func errorsNewRemoteSkillRedirect() error {
	return fmt.Errorf("%w: redirect target rejected", ErrBusinessSystemPromptBundleInvalid)
}

func validateRemoteSkillPinnedSource(files map[string][]byte, pin remoteSkillSourcePin) error {
	if len(pin.Commit) != 40 || !isLowerHexSHA256(pin.Commit+strings.Repeat("0", 24)) || !isLowerHexSHA256(pin.ArchiveSHA256) || len(pin.CoreSHA256) != 3 {
		return fmt.Errorf("%w: pinned source metadata invalid", ErrBusinessSystemPromptBundleInvalid)
	}
	for name, expected := range pin.CoreSHA256 {
		raw, ok := files[name]
		if !ok || !isLowerHexSHA256(expected) || hashBusinessSystemPromptBundleBytes(raw) != expected {
			return fmt.Errorf("%w: pinned source core mismatch", ErrBusinessSystemPromptBundleInvalid)
		}
	}
	return nil
}

func extractRemoteSkillBaseArchive(raw []byte, expectedRoot string) (map[string][]byte, error) {
	if expectedRoot == "" {
		return nil, fmt.Errorf("%w: source ZIP root is not pinned", ErrBusinessSystemPromptBundleInvalid)
	}
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid source ZIP", ErrBusinessSystemPromptBundleInvalid)
	}
	files := make(map[string][]byte)
	portable := make(map[string]struct{})
	var total int64
	for _, entry := range reader.File {
		clean := path.Clean(entry.Name)
		parts := strings.Split(clean, "/")
		if clean == "." || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "../") || strings.Contains(entry.Name, "\\") || len(parts) < 1 {
			return nil, fmt.Errorf("%w: source ZIP path traversal", ErrBusinessSystemPromptBundleInvalid)
		}
		if parts[0] != expectedRoot {
			return nil, fmt.Errorf("%w: source ZIP root does not match the pinned commit", ErrBusinessSystemPromptBundleInvalid)
		}
		if len(parts) == 1 || entry.FileInfo().IsDir() {
			continue
		}
		if clean != entry.Name {
			return nil, fmt.Errorf("%w: source ZIP path is not canonical", ErrBusinessSystemPromptBundleInvalid)
		}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || (!mode.IsRegular() && mode != 0) {
			return nil, fmt.Errorf("%w: source ZIP contains a link or special file", ErrBusinessSystemPromptBundleInvalid)
		}
		relative := strings.Join(parts[1:], "/")
		normalized, err := normalizeBundleRelativePath(relative)
		if err != nil || normalized != relative || relative == BusinessSystemPromptBundleManifestName {
			return nil, fmt.Errorf("%w: source ZIP path rejected", ErrBusinessSystemPromptBundleInvalid)
		}
		key := portableRemoteSkillPathKey(relative)
		if _, exists := files[relative]; exists {
			return nil, fmt.Errorf("%w: duplicate source path", ErrBusinessSystemPromptBundleInvalid)
		}
		if _, exists := portable[key]; exists {
			return nil, fmt.Errorf("%w: portable source path collision", ErrBusinessSystemPromptBundleInvalid)
		}
		if entry.UncompressedSize64 > businessSystemPromptBundleMaxFileBytes {
			return nil, fmt.Errorf("%w: source file exceeds limit", ErrBusinessSystemPromptBundleInvalid)
		}
		stream, err := entry.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(stream, businessSystemPromptBundleMaxFileBytes+1))
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil || len(data) > businessSystemPromptBundleMaxFileBytes {
			return nil, fmt.Errorf("%w: source file read failed", ErrBusinessSystemPromptBundleInvalid)
		}
		files[relative] = data
		portable[key] = struct{}{}
		total += int64(len(data))
		if len(files) > remoteSkillMaxFileCount || total > remoteSkillMaxTotalBytes {
			return nil, fmt.Errorf("%w: source archive limits exceeded", ErrBusinessSystemPromptBundleInvalid)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: source archive is empty", ErrBusinessSystemPromptBundleInvalid)
	}
	return files, nil
}

func hashRemoteSkillFileSet(entries map[string]string) string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		_, _ = io.WriteString(hash, name)
		_, _ = hash.Write([]byte{0})
		_, _ = io.WriteString(hash, entries[name])
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func buildRemoteSkillCandidate(sourceID, remoteRoot, commit, sourceSHA string, coreFiles []string, files map[string][]byte, active *BusinessSystemPromptBundleManifest) (RemoteSkillCandidate, error) {
	if len(files) == 0 || len(files) > remoteSkillMaxFileCount {
		return RemoteSkillCandidate{}, fmt.Errorf("%w: candidate file count invalid", ErrBusinessSystemPromptBundleInvalid)
	}
	names := sortedRemoteSkillFileNames(files)
	entries := make([]BusinessSystemPromptBundleFile, 0, len(names))
	portable := make(map[string]string, len(names))
	var total int64
	for _, name := range names {
		data := files[name]
		normalized, err := normalizeBundleRelativePath(name)
		if err != nil || normalized != name || len(data) > businessSystemPromptBundleMaxFileBytes {
			return RemoteSkillCandidate{}, fmt.Errorf("%w: candidate path or size invalid", ErrBusinessSystemPromptBundleInvalid)
		}
		portableKey := portableRemoteSkillPathKey(name)
		if previous, exists := portable[portableKey]; exists && previous != name {
			return RemoteSkillCandidate{}, fmt.Errorf("%w: portable candidate path collision", ErrBusinessSystemPromptBundleInvalid)
		}
		portable[portableKey] = name
		kind := remoteSkillFileKind(name, data)
		entries = append(entries, BusinessSystemPromptBundleFile{
			Path: name, SHA256: hashBusinessSystemPromptBundleBytes(data), ByteLength: len(data), Kind: kind, Required: true,
		})
		total += int64(len(data))
		if total > remoteSkillMaxTotalBytes {
			return RemoteSkillCandidate{}, fmt.Errorf("%w: candidate total size invalid", ErrBusinessSystemPromptBundleInvalid)
		}
	}
	manifest := BusinessSystemPromptBundleManifest{
		SchemaVersion: 1, BundleID: BusinessSystemPromptRemoteSkillBundleID,
		Version: commit, CoreFiles: append([]string(nil), coreFiles...),
		Files: entries, Domains: buildRemoteSkillRoutes(files),
	}
	if err := validateBusinessSystemPromptBundleManifest(manifest); err != nil {
		return RemoteSkillCandidate{}, err
	}
	manifestBytes, err := marshalRemoteSkillManifest(manifest)
	if err != nil {
		return RemoteSkillCandidate{}, err
	}
	manifestSHA := hashBusinessSystemPromptBundleBytes(manifestBytes)
	archiveBytes, err := buildRemoteSkillArchive(manifestBytes, files)
	if err != nil {
		return RemoteSkillCandidate{}, err
	}
	version := RemoteSkillBundleVersion{
		BundleID: BusinessSystemPromptRemoteSkillBundleID, SourceID: sourceID, RemoteRoot: remoteRoot, SourceCommit: commit,
		OverlaySHA256: sourceSHA, ManifestSHA256: manifestSHA,
		ArchiveSHA256: hashBusinessSystemPromptBundleBytes(archiveBytes),
		FileCount:     len(entries), TotalBytes: total,
	}
	applyRemoteSkillDiff(&version, active, manifest)
	candidate := RemoteSkillCandidate{Version: version, Manifest: manifest, ManifestBytes: manifestBytes, ArchiveBytes: archiveBytes, Files: files}
	if err := validateRemoteSkillCandidate(candidate); err != nil {
		return RemoteSkillCandidate{}, err
	}
	return candidate, nil
}

// Keep empty reference lists explicit in the wire manifest. The native
// installers consume the JSON directly and distinguish [] from an omitted
// field; the generic manifest type intentionally uses omitempty for legacy
// offline bundles, so this normalization stays remote-candidate specific.
func marshalRemoteSkillManifest(manifest BusinessSystemPromptBundleManifest) ([]byte, error) {
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	if domains, ok := value["domains"].([]any); ok {
		for _, item := range domains {
			if domain, ok := item.(map[string]any); ok {
				if _, exists := domain["references"]; !exists {
					domain["references"] = []any{}
				}
			}
		}
	}
	return json.MarshalIndent(value, "", "  ")
}

func buildRemoteSkillArchive(manifestBytes []byte, files map[string][]byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	write := func(name string, data []byte) error {
		// Store canonical bytes instead of relying on compressor output that can
		// change across Go/zlib releases for an otherwise identical manifest.
		header := &zip.FileHeader{Name: name, Method: zip.Store}
		header.Modified = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
		header.SetMode(0o644)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		_, err = entry.Write(data)
		return err
	}
	if err := write(BusinessSystemPromptBundleManifestName, manifestBytes); err != nil {
		_ = writer.Close()
		return nil, err
	}
	for _, name := range sortedRemoteSkillFileNames(files) {
		if err := write(name, files[name]); err != nil {
			_ = writer.Close()
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if buffer.Len() <= 0 || buffer.Len() > remoteSkillArchiveMaxBytes {
		return nil, fmt.Errorf("%w: generated archive size invalid", ErrBusinessSystemPromptBundleInvalid)
	}
	return buffer.Bytes(), nil
}

func buildRemoteSkillRoutes(files map[string][]byte) []BusinessSystemPromptBundleDomain {
	ids := make(map[string]struct{})
	for name := range files {
		parts := strings.Split(name, "/")
		if len(parts) == 3 && parts[0] == "skills" && parts[2] == "SKILL.md" && validBundleID(parts[1]) {
			ids[parts[1]] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	routes := make([]BusinessSystemPromptBundleDomain, 0, len(ordered))
	for _, id := range ordered {
		prefix := "skills/" + id + "/references/"
		references := make([]string, 0, 8)
		for name := range files {
			if strings.HasPrefix(name, prefix) && strings.HasSuffix(strings.ToLower(name), ".md") {
				references = append(references, name)
			}
		}
		sort.Strings(references)
		if len(references) > 8 {
			references = references[:8]
		}
		keywords := remoteSkillKeywordsFromFrontmatter(id, files["skills/"+id+"/SKILL.md"])
		keywords = append(keywords, remoteSkillRouteKeywords[id]...)
		keywords = stableUniqueRemoteSkillKeywords(keywords)
		routes = append(routes, BusinessSystemPromptBundleDomain{
			ID: id, Keywords: append([]string(nil), keywords...), Entry: "skills/" + id + "/SKILL.md", References: references,
		})
	}
	return routes
}

type remoteSkillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func remoteSkillKeywordsFromFrontmatter(id string, raw []byte) []string {
	keywords := []string{id, strings.ReplaceAll(id, "-", " ")}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return keywords
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return keywords
	}
	var frontmatter remoteSkillFrontmatter
	if yaml.Unmarshal([]byte(text[4:4+end]), &frontmatter) != nil {
		return keywords
	}
	keywords = append(keywords, strings.TrimSpace(frontmatter.Name))
	for _, token := range strings.FieldsFunc(frontmatter.Description, func(r rune) bool {
		switch r {
		case ',', '.', ';', ':', '/', '\\', '|', '(', ')', '[', ']', '{', '}', '\n', '\r', '\t', '\u3001', '\u3002', '\uff0c', '\uff1b', '\uff1a':
			return true
		default:
			return false
		}
	}) {
		token = strings.TrimSpace(token)
		if len([]rune(token)) >= 2 && len([]rune(token)) <= 48 {
			keywords = append(keywords, token)
		}
	}
	return keywords
}

func stableUniqueRemoteSkillKeywords(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || len([]rune(value)) > 96 {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

var remoteSkillRouteKeywords = map[string][]string{
	"api-security":              {"api", "http", "rest", "graphql", "jwt", "oauth", "authentication", "authorization", "接口安全", "鉴权", "认证", "越权"},
	"apk-reverse":               {"apk", "android", "smali", "frida", "jadx", "安卓逆向", "应用逆向"},
	"attack-chain":              {"attack chain", "exploit chain", "kill chain", "lateral movement", "攻击链", "利用链", "横向移动"},
	"binary-diff":               {"binary diff", "bindiff", "patch diff", "二进制对比", "补丁对比"},
	"browser-automation":        {"browser automation", "playwright", "selenium", "cdp", "浏览器自动化"},
	"browser-extension-reverse": {"browser extension", "chrome extension", "firefox extension", "浏览器扩展逆向", "插件逆向"},
	"cloud-k8s":                 {"cloud", "kubernetes", "k8s", "container", "docker", "云安全", "容器安全"},
	"code-audit":                {"code audit", "source audit", "sast", "code review", "代码审计", "源码审计"},
	"database-security":         {"database", "sql", "postgresql", "mysql", "redis", "数据库安全", "数据库审计"},
	"digital-forensics":         {"forensics", "disk image", "memory dump", "pcap", "数字取证", "内存取证", "流量取证"},
	"dotnet-reverse":            {".net", "dotnet", "c#", "dnspy", "ilspy", ".net逆向", "c#逆向"},
	"edr-bypass-re":             {"edr", "endpoint detection", "unhook", "telemetry", "edr逆向", "端点检测"},
	"email-security":            {"email security", "smtp", "spf", "dkim", "dmarc", "邮件安全"},
	"firmware-pentest":          {"firmware", "embedded", "binwalk", "uart", "固件安全", "固件逆向", "嵌入式安全"},
	"ghidra-reverse":            {"ghidra", "decompiler", "ghidra逆向", "反编译器"},
	"go-rust-reverse":           {"golang binary", "go binary", "rust binary", "go逆向", "rust逆向"},
	"hardware-security":         {"hardware security", "jtag", "spi", "side channel", "硬件安全", "侧信道"},
	"ida-reverse":               {"ida pro", "idapython", "disassembly", "ida逆向", "反汇编"},
	"identity-federation":       {"saml", "oidc", "identity federation", "single sign-on", "sso", "身份联合", "单点登录"},
	"js-reverse":                {"javascript reverse", "js reverse", "web reverse", "wasm", "obfuscation", "js逆向", "网页逆向", "前端逆向", "反混淆"},
	"llm-security":              {"llm security", "prompt injection", "jailbreak", "agent security", "大模型安全", "提示词注入", "越狱", "智能体安全"},
	"macos-reverse":             {"macos", "mach-o", "objective-c", "swift binary", "macos逆向", "苹果电脑逆向"},
	"malware-analysis":          {"malware", "ransomware", "trojan", "sandbox", "yara", "恶意软件", "恶意样本", "勒索软件", "木马分析"},
	"mobile-reverse":            {"mobile reverse", "ios reverse", "android reverse", "移动端逆向", "ios逆向", "安卓逆向"},
	"ot-ics":                    {"ot security", "ics", "scada", "modbus", "工控安全", "工业控制"},
	"patch-diff-exploit":        {"patch diff", "cve patch", "vulnerability patch", "补丁分析", "补丁差分", "漏洞补丁"},
	"protocol-reverse":          {"protocol reverse", "packet format", "protobuf", "websocket", "协议逆向", "协议分析", "数据包格式"},
	"pwn-chain":                 {"pwn", "buffer overflow", "rop", "heap exploit", "shellcode", "二进制利用", "缓冲区溢出", "堆利用"},
	"radare2":                   {"radare2", "r2", "rizin", "radare2逆向"},
	"radio-sdr":                 {"radio", "sdr", "rf", "signal", "无线电安全", "软件无线电", "信号分析"},
	"reverse-engineering":       {"reverse engineering", "decompile", "disassemble", "binary analysis", "逆向工程", "反编译", "反汇编", "二进制分析"},
	"supply-chain-security":     {"supply chain", "dependency confusion", "sbom", "供应链安全", "依赖混淆"},
	"thick-client":              {"thick client", "desktop client", "windows client", "桌面客户端", "胖客户端"},
	"threat-hunting":            {"threat hunting", "ioc", "sigma", "mitre attack", "威胁狩猎", "威胁猎杀", "指标分析"},
	"wifi-wireless":             {"wifi", "wireless", "802.11", "wpa", "无线安全", "wifi安全"},
	"windows-ad":                {"active directory", "windows ad", "kerberos", "ldap", "域安全", "活动目录", "域渗透"},
}

func remoteSkillFileKind(name string, data []byte) string {
	if bytes.HasPrefix(data, []byte("#!")) {
		return "script"
	}
	extension := strings.ToLower(path.Ext(name))
	switch extension {
	case ".ps1", ".psm1", ".sh", ".bash", ".zsh", ".fish", ".py", ".rb", ".pl", ".lua", ".js", ".mjs", ".cjs", ".ts", ".bat", ".cmd":
		return "script"
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".jar", ".zip", ".gz", ".7z", ".exe", ".dll", ".so", ".pdf", ".docx":
		return "binary"
	}
	if !utf8.Valid(data) {
		return "binary"
	}
	return "text"
}

func applyRemoteSkillDiff(version *RemoteSkillBundleVersion, active *BusinessSystemPromptBundleManifest, candidate BusinessSystemPromptBundleManifest) {
	if version == nil {
		return
	}
	oldFiles := make(map[string]BusinessSystemPromptBundleFile)
	if active != nil {
		for _, entry := range active.Files {
			oldFiles[entry.Path] = entry
		}
	}
	newFiles := make(map[string]BusinessSystemPromptBundleFile, len(candidate.Files))
	for _, entry := range candidate.Files {
		newFiles[entry.Path] = entry
		old, exists := oldFiles[entry.Path]
		if !exists {
			version.AddedFiles++
			if bundleFileKind(entry) == "script" {
				version.ScriptChanges++
			}
			if bundleFileKind(entry) == "binary" {
				version.BinaryChanges++
			}
			continue
		}
		if old.SHA256 != entry.SHA256 || old.ByteLength != entry.ByteLength || bundleFileKind(old) != bundleFileKind(entry) {
			version.ModifiedFiles++
			if bundleFileKind(old) == "script" || bundleFileKind(entry) == "script" {
				version.ScriptChanges++
			}
			if bundleFileKind(old) == "binary" || bundleFileKind(entry) == "binary" {
				version.BinaryChanges++
			}
		}
	}
	for name, entry := range oldFiles {
		if _, exists := newFiles[name]; exists {
			continue
		}
		version.DeletedFiles++
		if bundleFileKind(entry) == "script" {
			version.ScriptChanges++
		}
		if bundleFileKind(entry) == "binary" {
			version.BinaryChanges++
		}
	}
}
