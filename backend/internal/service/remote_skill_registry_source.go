package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/sync/errgroup"
)

const (
	remoteSkillSyncTimeout   = 5 * time.Minute
	remoteSkillSyncWorkers   = 8
	remoteSkillMaxFileCount  = 2000
	remoteSkillMaxTotalBytes = 256 << 20
)

type RemoteSkillHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type MoxinggangRemoteSkillCandidateSource struct {
	client RemoteSkillHTTPDoer
	now    func() time.Time
}

func NewMoxinggangRemoteSkillCandidateSource(client RemoteSkillHTTPDoer) *MoxinggangRemoteSkillCandidateSource {
	if client == nil {
		client = newRemoteSkillHTTPClient()
	}
	return &MoxinggangRemoteSkillCandidateSource{client: client, now: time.Now}
}

func (s *MoxinggangRemoteSkillCandidateSource) Build(
	ctx context.Context,
	prompt RemoteSkillPromptCapture,
	active *RemoteSkillCandidate,
) (RemoteSkillCandidate, error) {
	if s == nil || s.client == nil {
		return RemoteSkillCandidate{}, fmt.Errorf("%w: sync HTTP client unavailable", ErrBusinessSystemPromptBundleUnavailable)
	}
	if prompt.RawSHA256 == "" || prompt.EffectiveSHA256 == "" {
		return RemoteSkillCandidate{}, fmt.Errorf("%w: prompt capture is required", ErrBusinessSystemPromptInvalid)
	}
	manifest, err := loadRemoteSkillManifest()
	if err != nil {
		return RemoteSkillCandidate{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, remoteSkillSyncTimeout)
	defer cancel()
	rawFiles := make(map[string][]byte, len(manifest.Files))
	upstreamEntries := make([]remoteSkillManifestEntry, 0, manifest.UpstreamFileCount)
	for _, entry := range manifest.Files {
		if entry.SourceKind == "upstream" {
			upstreamEntries = append(upstreamEntries, entry)
			continue
		}
		body, err := loadRemoteSkillPinnedAsset(entry)
		if err != nil {
			return RemoteSkillCandidate{}, err
		}
		rawFiles[entry.Path] = body
	}

	var rawFilesMu sync.Mutex
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(remoteSkillSyncWorkers)
	for _, manifestEntry := range upstreamEntries {
		entry := manifestEntry
		group.Go(func() error {
			body, err := s.downloadEntry(groupCtx, entry.Path)
			if err != nil {
				return err
			}
			if !remoteSkillManifestEntryMatches(entry, body) {
				return fmt.Errorf("%w: upstream content does not match manifest", ErrBusinessSystemPromptBundleInvalid)
			}
			rawFilesMu.Lock()
			rawFiles[entry.Path] = body
			rawFilesMu.Unlock()
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return RemoteSkillCandidate{}, err
	}
	if err := validateCurrentRemoteSkillTree(rawFiles); err != nil {
		return RemoteSkillCandidate{}, err
	}
	effectiveFiles := rewriteRemoteSkillPublishedFiles(rawFiles)
	fetchedAt := time.Now().UTC()
	if s.now != nil {
		fetchedAt = s.now().UTC()
	}
	return buildPairedRemoteSkillCandidate(rawFiles, effectiveFiles, prompt, active, fetchedAt)
}

func (s *MoxinggangRemoteSkillCandidateSource) downloadEntry(ctx context.Context, name string) ([]byte, error) {
	normalized, err := normalizeBundleRelativePath(name)
	if err != nil || normalized != name {
		return nil, fmt.Errorf("%w: upstream path rejected", ErrBusinessSystemPromptBundleInvalid)
	}
	rawURL := remoteSkillUpstreamEntryURL(name)
	parsed, err := url.Parse(rawURL)
	if err != nil || !validMoxinggangRemoteSkillURL(parsed) {
		return nil, fmt.Errorf("%w: upstream URL rejected", ErrBusinessSystemPromptBundleInvalid)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/markdown, text/plain;q=0.9, application/octet-stream;q=0.8")
	req.Header.Set("User-Agent", "Sub2API-Remote-Skill-Sync/3")
	response, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: upstream request failed", ErrBusinessSystemPromptBundleUnavailable)
	}
	defer func() { _ = response.Body.Close() }()
	if response.Request == nil || response.Request.URL == nil || response.Request.URL.String() != rawURL {
		return nil, fmt.Errorf("%w: upstream redirect rejected", ErrBusinessSystemPromptBundleInvalid)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: upstream returned status %d", ErrBusinessSystemPromptBundleUnavailable, response.StatusCode)
	}
	if response.ContentLength > businessSystemPromptBundleMaxFileBytes {
		return nil, fmt.Errorf("%w: upstream response exceeds limit", ErrBusinessSystemPromptBundleInvalid)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, businessSystemPromptBundleMaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: upstream response incomplete", ErrBusinessSystemPromptBundleUnavailable)
	}
	if len(raw) == 0 || len(raw) > businessSystemPromptBundleMaxFileBytes {
		return nil, fmt.Errorf("%w: upstream response size invalid", ErrBusinessSystemPromptBundleInvalid)
	}
	if response.ContentLength >= 0 && int64(len(raw)) != response.ContentLength {
		return nil, fmt.Errorf("%w: upstream response incomplete", ErrBusinessSystemPromptBundleUnavailable)
	}
	return raw, nil
}

func validMoxinggangRemoteSkillURL(parsed *url.URL) bool {
	if parsed == nil || parsed.Scheme != "https" || parsed.Hostname() != "moxinggang.com" || parsed.Port() != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	if parsed.Path != path.Clean(parsed.Path) || !strings.HasPrefix(parsed.Path, RemoteSkillMoxinggangPath+"/") {
		return false
	}
	relative := strings.TrimPrefix(parsed.Path, RemoteSkillMoxinggangPath+"/")
	normalized, err := normalizeBundleRelativePath(relative)
	return err == nil && normalized == relative
}

func remoteSkillUpstreamEntryURL(name string) string {
	parts := strings.Split(name, "/")
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}
	return RemoteSkillUpstreamRoot + "/" + strings.Join(parts, "/")
}

func newRemoteSkillHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 45 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return fmt.Errorf("%w: upstream redirect rejected", ErrBusinessSystemPromptBundleInvalid)
		},
	}
}

func buildPairedRemoteSkillCandidate(
	rawFiles map[string][]byte,
	effectiveFiles map[string][]byte,
	prompt RemoteSkillPromptCapture,
	active *RemoteSkillCandidate,
	fetchedAt time.Time,
) (RemoteSkillCandidate, error) {
	fetchedAt = fetchedAt.UTC().Truncate(time.Microsecond)
	if len(rawFiles) == 0 || len(rawFiles) != len(effectiveFiles) || len(rawFiles) > remoteSkillMaxFileCount {
		return RemoteSkillCandidate{}, fmt.Errorf("%w: paired tree file count invalid", ErrBusinessSystemPromptBundleInvalid)
	}
	var rawTotal, effectiveTotal int64
	for name, raw := range rawFiles {
		normalized, err := normalizeBundleRelativePath(name)
		if err != nil || normalized != name || len(raw) == 0 || len(raw) > businessSystemPromptBundleMaxFileBytes {
			return RemoteSkillCandidate{}, fmt.Errorf("%w: raw tree path or size invalid", ErrBusinessSystemPromptBundleInvalid)
		}
		effective, ok := effectiveFiles[name]
		if !ok || len(effective) == 0 || len(effective) > businessSystemPromptBundleMaxFileBytes {
			return RemoteSkillCandidate{}, fmt.Errorf("%w: published tree path or size invalid", ErrBusinessSystemPromptBundleInvalid)
		}
		rawTotal += int64(len(raw))
		effectiveTotal += int64(len(effective))
	}
	if rawTotal > remoteSkillMaxTotalBytes || effectiveTotal > remoteSkillMaxTotalBytes {
		return RemoteSkillCandidate{}, fmt.Errorf("%w: paired tree size invalid", ErrBusinessSystemPromptBundleInvalid)
	}
	promptVersion := RemoteSkillPromptVersion{
		RawSHA256:       prompt.RawSHA256,
		EffectiveSHA256: prompt.EffectiveSHA256,
		Diff:            prompt.Diff,
		RawBody:         string(prompt.RawBody),
		EffectiveBody:   string(prompt.EffectiveBody),
	}
	candidate := RemoteSkillCandidate{
		Version: RemoteSkillBundleVersion{
			UpstreamSourceID:    RemoteSkillUpstreamSourceID,
			UpstreamRoot:        RemoteSkillUpstreamRoot,
			PublicRoot:          RemoteSkillPublicRoot,
			RawTreeSHA256:       remoteSkillFileTreeSHA256(rawFiles),
			EffectiveTreeSHA256: remoteSkillFileTreeSHA256(effectiveFiles),
			FileCount:           len(rawFiles),
			RawTotalBytes:       rawTotal,
			EffectiveTotalBytes: effectiveTotal,
			FetchedAt:           fetchedAt,
		},
		Prompt:         promptVersion,
		RawFiles:       cloneRemoteSkillFiles(rawFiles),
		EffectiveFiles: cloneRemoteSkillFiles(effectiveFiles),
	}
	candidate.FileChanges = remoteSkillFileChanges(active, candidate)
	for _, change := range candidate.FileChanges {
		switch change.Change {
		case "added":
			candidate.Version.AddedFiles++
		case "modified":
			candidate.Version.ModifiedFiles++
		case "deleted":
			candidate.Version.DeletedFiles++
		}
		if change.Kind == "script" {
			candidate.Version.ScriptChanges++
		}
		if change.Kind == "binary" {
			candidate.Version.BinaryChanges++
		}
	}
	return candidate, nil
}

func remoteSkillFileChanges(active *RemoteSkillCandidate, candidate RemoteSkillCandidate) []RemoteSkillFileChange {
	oldFiles := map[string][]byte{}
	if active != nil {
		oldFiles = active.EffectiveFiles
	}
	names := make(map[string]struct{}, len(oldFiles)+len(candidate.EffectiveFiles))
	for name := range oldFiles {
		names[name] = struct{}{}
	}
	for name := range candidate.EffectiveFiles {
		names[name] = struct{}{}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	changes := make([]RemoteSkillFileChange, 0)
	for _, name := range ordered {
		oldBody, hadOld := oldFiles[name]
		newBody, hasNew := candidate.EffectiveFiles[name]
		change := RemoteSkillFileChange{Path: name}
		switch {
		case !hadOld:
			change.Change = "added"
		case !hasNew:
			change.Change = "deleted"
		case bytes.Equal(oldBody, newBody):
			continue
		default:
			change.Change = "modified"
		}
		if hasNew {
			change.Kind = remoteSkillFileKind(name, newBody)
			change.EffectiveSHA256 = hashBusinessSystemPromptBundleBytes(newBody)
			change.RawSHA256 = hashBusinessSystemPromptBundleBytes(candidate.RawFiles[name])
		} else {
			change.Kind = remoteSkillFileKind(name, oldBody)
		}
		if hadOld {
			change.PreviousEffectiveSHA256 = hashBusinessSystemPromptBundleBytes(oldBody)
		}
		changes = append(changes, change)
	}
	return changes
}

func remoteSkillMoxinggangReferences(raw []byte) []string {
	const marker = "https://moxinggang.com/skills/security-research/current/"
	seen := make(map[string]struct{})
	result := make([]string, 0)
	text := string(raw)
	for start := 0; ; {
		index := strings.Index(text[start:], marker)
		if index < 0 {
			break
		}
		index += start + len(marker)
		end := index
		for end < len(text) && !strings.ContainsRune(" \t\r\n<>()[]{}'\"`", rune(text[end])) {
			end++
		}
		name := strings.TrimRight(text[index:end], ".,;:!?")
		if normalized, err := normalizeBundleRelativePath(name); err == nil && normalized == name {
			if _, ok := seen[name]; !ok {
				seen[name] = struct{}{}
				result = append(result, name)
			}
		}
		start = end
	}
	sort.Strings(result)
	return result
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

func remoteSkillFileKind(name string, data []byte) string {
	if bytes.HasPrefix(data, []byte("#!")) {
		return "script"
	}
	switch strings.ToLower(path.Ext(name)) {
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
