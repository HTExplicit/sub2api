package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	remoteSkillManifestSchemaVersion    = 1
	remoteSkillExpectedFiles            = 458
	remoteSkillExpectedUpstreamFiles    = 457
	remoteSkillExpectedPinnedFiles      = 1
	remoteSkillPinnedWAFPath            = "skills/sec-assessment-tooling/pentest-tools/src-hunter/references/payloader/waf-bypass.md"
	remoteSkillPinnedWAFEmbeddedPath    = "pinned/waf-bypass.md"
	remoteSkillPinnedWAFSHA256          = "0273517455962bb9908264f82e4708b31d541c91c2ec715e8032d6c1376728b5"
	remoteSkillPinnedWAFSourceCommit    = "d8bf34540cbc1aa34052e1b142576fc36a1f1437"
	remoteSkillPinnedWAFManifestSHA256  = "07bf0d71dfb687ff3ced0befa39081453c51ce85ae54a02bdb1e1f6fc34d3313"
	remoteSkillPinnedWAFArchiveSHA256   = "c6920445c55f46c2a30e8a2fe398e7c1cf0b22dcbe4c53ed0cfc105d9c8a5f3e"
	remoteSkillManifestEmbeddedPath     = "remote_skill_seed/manifest.json"
	remoteSkillUpstreamTreeEmbeddedRoot = "remote_skill_seed/tree"
	remoteSkillPinnedAssetsEmbeddedRoot = "remote_skill_seed/pinned"
)

type remoteSkillManifest struct {
	SchemaVersion     int                        `json:"schema_version"`
	BundleID          string                     `json:"bundle_id"`
	UpstreamSourceID  string                     `json:"upstream_source_id"`
	UpstreamRoot      string                     `json:"upstream_root"`
	ExpectedFileCount int                        `json:"expected_file_count"`
	UpstreamFileCount int                        `json:"upstream_file_count"`
	PinnedFileCount   int                        `json:"pinned_file_count"`
	Files             []remoteSkillManifestEntry `json:"files"`
}

type remoteSkillManifestEntry struct {
	Path         string                            `json:"path"`
	SourceKind   string                            `json:"source_kind"`
	EmbeddedPath string                            `json:"embedded_path,omitempty"`
	ByteLength   int                               `json:"byte_length"`
	SHA256       string                            `json:"sha256"`
	Provenance   *remoteSkillPinnedAssetProvenance `json:"provenance,omitempty"`
}

type remoteSkillPinnedAssetProvenance struct {
	SourceCommit             string `json:"source_commit"`
	HistoricalManifestSHA256 string `json:"historical_manifest_sha256"`
	HistoricalArchiveSHA256  string `json:"historical_archive_sha256"`
}

func loadRemoteSkillManifest() (remoteSkillManifest, error) {
	raw, err := fs.ReadFile(remoteSkillSeedFS, remoteSkillManifestEmbeddedPath)
	if err != nil {
		return remoteSkillManifest{}, err
	}
	if len(raw) == 0 || !utf8.Valid(raw) {
		return remoteSkillManifest{}, fmt.Errorf("%w: embedded manifest is empty or not UTF-8", ErrBusinessSystemPromptBundleInvalid)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest remoteSkillManifest
	if err := decoder.Decode(&manifest); err != nil {
		return remoteSkillManifest{}, fmt.Errorf("%w: embedded manifest invalid", ErrBusinessSystemPromptBundleInvalid)
	}
	if err := ensureRemoteSkillJSONEOF(decoder); err != nil {
		return remoteSkillManifest{}, err
	}
	if err := validateRemoteSkillManifest(manifest); err != nil {
		return remoteSkillManifest{}, err
	}
	return manifest, nil
}

func ensureRemoteSkillJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("%w: embedded manifest has trailing content", ErrBusinessSystemPromptBundleInvalid)
	}
	return nil
}

func validateRemoteSkillManifest(manifest remoteSkillManifest) error {
	if manifest.SchemaVersion != remoteSkillManifestSchemaVersion || manifest.BundleID != "security-research" ||
		manifest.UpstreamSourceID != RemoteSkillUpstreamSourceID || manifest.UpstreamRoot != RemoteSkillUpstreamRoot ||
		manifest.ExpectedFileCount != remoteSkillExpectedFiles || manifest.UpstreamFileCount != remoteSkillExpectedUpstreamFiles ||
		manifest.PinnedFileCount != remoteSkillExpectedPinnedFiles || len(manifest.Files) != remoteSkillExpectedFiles ||
		manifest.UpstreamFileCount+manifest.PinnedFileCount != manifest.ExpectedFileCount {
		return fmt.Errorf("%w: embedded manifest identity mismatch", ErrBusinessSystemPromptBundleInvalid)
	}

	seen := make(map[string]struct{}, len(manifest.Files))
	portable := make(map[string]string, len(manifest.Files))
	ordered := make([]string, 0, len(manifest.Files))
	upstreamCount := 0
	pinnedCount := 0
	for _, entry := range manifest.Files {
		normalized, err := normalizeBundleRelativePath(entry.Path)
		if err != nil || normalized != entry.Path || !norm.NFC.IsNormalString(entry.Path) || entry.ByteLength < 1 || entry.ByteLength > businessSystemPromptBundleMaxFileBytes ||
			!validRemoteSkillSHA256(entry.SHA256) || entry.SHA256 != strings.ToLower(entry.SHA256) {
			return fmt.Errorf("%w: embedded manifest entry invalid", ErrBusinessSystemPromptBundleInvalid)
		}
		if _, exists := seen[entry.Path]; exists {
			return fmt.Errorf("%w: embedded manifest path duplicated", ErrBusinessSystemPromptBundleInvalid)
		}
		seen[entry.Path] = struct{}{}
		portableKey := portableRemoteSkillPathKey(entry.Path)
		if previous, exists := portable[portableKey]; exists && previous != entry.Path {
			return fmt.Errorf("%w: embedded manifest portable path collision", ErrBusinessSystemPromptBundleInvalid)
		}
		portable[portableKey] = entry.Path
		ordered = append(ordered, entry.Path)

		switch entry.SourceKind {
		case "upstream":
			upstreamCount++
			if entry.EmbeddedPath != "" || entry.Provenance != nil {
				return fmt.Errorf("%w: upstream manifest entry has pinned metadata", ErrBusinessSystemPromptBundleInvalid)
			}
		case "pinned":
			pinnedCount++
			if entry.Path != remoteSkillPinnedWAFPath || entry.EmbeddedPath != remoteSkillPinnedWAFEmbeddedPath ||
				entry.SHA256 != remoteSkillPinnedWAFSHA256 || entry.Provenance == nil ||
				entry.Provenance.SourceCommit != remoteSkillPinnedWAFSourceCommit ||
				entry.Provenance.HistoricalManifestSHA256 != remoteSkillPinnedWAFManifestSHA256 ||
				entry.Provenance.HistoricalArchiveSHA256 != remoteSkillPinnedWAFArchiveSHA256 {
				return fmt.Errorf("%w: pinned WAF manifest identity mismatch", ErrBusinessSystemPromptBundleInvalid)
			}
		default:
			return fmt.Errorf("%w: embedded manifest source kind invalid", ErrBusinessSystemPromptBundleInvalid)
		}
	}
	if upstreamCount != remoteSkillExpectedUpstreamFiles || pinnedCount != remoteSkillExpectedPinnedFiles {
		return fmt.Errorf("%w: embedded manifest source counts mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	expectedOrder := append([]string(nil), ordered...)
	sortRemoteSkillPaths(expectedOrder)
	for index := range ordered {
		if ordered[index] != expectedOrder[index] {
			return fmt.Errorf("%w: embedded manifest entries are not sorted", ErrBusinessSystemPromptBundleInvalid)
		}
	}
	return nil
}

func loadRemoteSkillSeedFiles() (remoteSkillManifest, map[string][]byte, error) {
	manifest, err := loadRemoteSkillManifest()
	if err != nil {
		return remoteSkillManifest{}, nil, err
	}
	upstreamFiles, err := readRemoteSkillTreeFS(remoteSkillSeedFS, remoteSkillUpstreamTreeEmbeddedRoot)
	if err != nil {
		return remoteSkillManifest{}, nil, err
	}
	pinnedFiles, err := readRemoteSkillTreeFS(remoteSkillSeedFS, remoteSkillPinnedAssetsEmbeddedRoot)
	if err != nil {
		return remoteSkillManifest{}, nil, err
	}
	if len(upstreamFiles) != remoteSkillExpectedUpstreamFiles || len(pinnedFiles) != remoteSkillExpectedPinnedFiles {
		return remoteSkillManifest{}, nil, fmt.Errorf("%w: embedded seed file counts mismatch", ErrBusinessSystemPromptBundleInvalid)
	}

	files := make(map[string][]byte, len(manifest.Files))
	for _, entry := range manifest.Files {
		var body []byte
		var ok bool
		if entry.SourceKind == "upstream" {
			body, ok = upstreamFiles[entry.Path]
		} else {
			body, ok = pinnedFiles[strings.TrimPrefix(entry.EmbeddedPath, "pinned/")]
		}
		if !ok || !remoteSkillManifestEntryMatches(entry, body) {
			return remoteSkillManifest{}, nil, fmt.Errorf("%w: embedded seed content mismatch", ErrBusinessSystemPromptBundleInvalid)
		}
		files[entry.Path] = append([]byte(nil), body...)
	}
	for name := range upstreamFiles {
		if _, ok := files[name]; !ok {
			return remoteSkillManifest{}, nil, fmt.Errorf("%w: undeclared embedded upstream file", ErrBusinessSystemPromptBundleInvalid)
		}
	}
	if _, ok := pinnedFiles["waf-bypass.md"]; !ok {
		return remoteSkillManifest{}, nil, fmt.Errorf("%w: pinned WAF asset missing", ErrBusinessSystemPromptBundleInvalid)
	}
	return manifest, files, nil
}

func remoteSkillManifestEntryMatches(entry remoteSkillManifestEntry, body []byte) bool {
	return len(body) == entry.ByteLength && len(body) > 0 && utf8.Valid(body) && hashBusinessSystemPromptBundleBytes(body) == entry.SHA256
}

func loadRemoteSkillPinnedAsset(entry remoteSkillManifestEntry) ([]byte, error) {
	if entry.SourceKind != "pinned" || entry.EmbeddedPath != remoteSkillPinnedWAFEmbeddedPath {
		return nil, fmt.Errorf("%w: pinned manifest entry invalid", ErrBusinessSystemPromptBundleInvalid)
	}
	body, err := fs.ReadFile(remoteSkillSeedFS, "remote_skill_seed/"+entry.EmbeddedPath)
	if err != nil {
		return nil, err
	}
	if !remoteSkillManifestEntryMatches(entry, body) {
		return nil, fmt.Errorf("%w: pinned asset content mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	return append([]byte(nil), body...), nil
}

func sortRemoteSkillPaths(paths []string) {
	sort.Slice(paths, func(i, j int) bool {
		left := portableRemoteSkillPathKey(paths[i])
		right := portableRemoteSkillPathKey(paths[j])
		if left == right {
			return paths[i] < paths[j]
		}
		return left < right
	})
}
