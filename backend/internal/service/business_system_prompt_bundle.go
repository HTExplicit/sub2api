package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// BusinessSystemPromptBundlePathEnv is the only runtime configuration used
	// to locate the external skill bundle. The loader never fetches a URL.
	BusinessSystemPromptBundlePathEnv = "SUB2API_SYSTEM_PROMPT_BUNDLE_PATH"
	// BusinessSystemPromptBundleManifestName is intentionally fixed so a mount
	// cannot select an arbitrary file as executable configuration.
	BusinessSystemPromptBundleManifestName     = "bundle-manifest.json"
	businessSystemPromptBundleManifestName     = BusinessSystemPromptBundleManifestName
	BusinessSystemPromptBundleDefaultPath      = "/app/skill-bundles/moxinggang-reverse-skill/22c227128165afbbcbda0175eb5e991ddb51d105b7d1e704572c625c64b626d7"
	BusinessSystemPromptBundleMaxBytes         = 256 << 10
	BusinessSystemPromptBundleMaxDomains       = 2
	BusinessSystemPromptBundleMaxReferences    = 3
	BusinessSystemPromptHybridMaxDomains       = 128
	BusinessSystemPromptHybridMaxReferences    = 512
	BusinessSystemPromptHybridMaxDocuments     = 1024
	businessSystemPromptBundleMaxManifestBytes = 4 << 20
	businessSystemPromptBundleMaxFileBytes     = 64 << 20
)

var (
	ErrBusinessSystemPromptBundleInvalid     = errors.New("invalid business system prompt bundle")
	ErrBusinessSystemPromptBundleUnavailable = errors.New("business system prompt bundle unavailable")
)

// BusinessSystemPromptBundleFile is a manifest entry. Paths are always
// bundle-relative slash-separated paths; the loader rejects traversal and
// symlink components before reading them.
type BusinessSystemPromptBundleFile struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	ByteLength int    `json:"byte_length"`
	Kind       string `json:"kind,omitempty"` // text, binary, or script
	Required   bool   `json:"required,omitempty"`
}

// BusinessSystemPromptBundleDomain describes deterministic local routing.
// Keywords are matched against the current user text; ties are resolved by
// priority and then ID so every instance produces the same body.
type BusinessSystemPromptBundleDomain struct {
	ID         string   `json:"id"`
	Keywords   []string `json:"keywords"`
	Entry      string   `json:"entry"`
	References []string `json:"references,omitempty"`
	Priority   int      `json:"priority,omitempty"`
}

// BusinessSystemPromptBundleManifest is intentionally small. The complete
// reconstructed package stays outside the Go binary and is addressed by this
// manifest at deployment time.
type BusinessSystemPromptBundleManifest struct {
	SchemaVersion int                                `json:"schema_version"`
	BundleID      string                             `json:"bundle_id"`
	Version       string                             `json:"version,omitempty"`
	Core          string                             `json:"core,omitempty"`
	CoreFiles     []string                           `json:"core_files,omitempty"`
	Files         []BusinessSystemPromptBundleFile   `json:"files"`
	Domains       []BusinessSystemPromptBundleDomain `json:"domains,omitempty"`
	CreatedAt     time.Time                          `json:"created_at,omitempty"`
}

// BusinessSystemPromptBundle is an immutable, verified view of an external
// bundle. File bytes are copied at load time; subsequent filesystem changes
// cannot mutate a request already compiled from this value.
type BusinessSystemPromptBundle struct {
	Root            string
	Manifest        BusinessSystemPromptBundleManifest
	ManifestSHA256  string
	LoadedAt        time.Time
	Degraded        bool
	MissingOptional []string
	files           map[string][]byte
	fileEntries     map[string]BusinessSystemPromptBundleFile
}

// DefaultBusinessSystemPromptBundlePath returns the configured external path.
// An empty or whitespace-only environment value deliberately falls back to the
// fixed deployment location. No URL is accepted here.
func DefaultBusinessSystemPromptBundlePath() string {
	for _, key := range []string{BusinessSystemPromptBundlePathEnv, "SUB2API_BUSINESS_SYSTEM_PROMPT_BUNDLE_PATH"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return BusinessSystemPromptBundleDefaultPath
}

// LoadBusinessSystemPromptBundle reads and verifies a bundle from disk. It is
// deliberately synchronous and offline: there is no HTTP client, shell, or
// script execution in this code path.
func LoadBusinessSystemPromptBundle(root string) (*BusinessSystemPromptBundle, error) {
	root = strings.TrimSpace(root)
	if root == "" || strings.ContainsRune(root, '\x00') {
		return nil, fmt.Errorf("%w: empty or NUL root", ErrBusinessSystemPromptBundleInvalid)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve root: %v", ErrBusinessSystemPromptBundleInvalid, err)
	}
	absRoot = filepath.Clean(absRoot)
	if strings.HasPrefix(absRoot, `\\`) {
		return nil, fmt.Errorf("%w: UNC roots are not allowed", ErrBusinessSystemPromptBundleInvalid)
	}
	if err := rejectSymlinkPath(absRoot); err != nil {
		return nil, fmt.Errorf("%w: root: %v", ErrBusinessSystemPromptBundleInvalid, err)
	}
	rootInfo, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("%w: stat root: %v", ErrBusinessSystemPromptBundleUnavailable, err)
	}
	if !rootInfo.IsDir() {
		return nil, fmt.Errorf("%w: root is not a directory", ErrBusinessSystemPromptBundleInvalid)
	}

	manifestPath := filepath.Join(absRoot, BusinessSystemPromptBundleManifestName)
	if err := rejectSymlinkPath(manifestPath); err != nil {
		return nil, fmt.Errorf("%w: manifest: %v", ErrBusinessSystemPromptBundleInvalid, err)
	}
	manifestInfo, err := os.Stat(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("%w: stat manifest: %v", ErrBusinessSystemPromptBundleUnavailable, err)
	}
	if manifestInfo.Size() <= 0 || manifestInfo.Size() > businessSystemPromptBundleMaxManifestBytes {
		return nil, fmt.Errorf("%w: manifest size is out of range", ErrBusinessSystemPromptBundleInvalid)
	}
	rawManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("%w: read manifest: %v", ErrBusinessSystemPromptBundleUnavailable, err)
	}
	if !utf8.Valid(rawManifest) {
		return nil, fmt.Errorf("%w: manifest is not UTF-8", ErrBusinessSystemPromptBundleInvalid)
	}
	var manifest BusinessSystemPromptBundleManifest
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		return nil, fmt.Errorf("%w: parse manifest: %v", ErrBusinessSystemPromptBundleInvalid, err)
	}
	if err := validateBusinessSystemPromptBundleManifest(manifest); err != nil {
		return nil, err
	}

	bundle := &BusinessSystemPromptBundle{
		Root:           absRoot,
		Manifest:       cloneBusinessSystemPromptBundleManifest(manifest),
		ManifestSHA256: hashBusinessSystemPromptBundleBytes(rawManifest),
		LoadedAt:       time.Now().UTC(),
		files:          make(map[string][]byte, len(manifest.Files)),
		fileEntries:    make(map[string]BusinessSystemPromptBundleFile, len(manifest.Files)),
	}
	for _, entry := range manifest.Files {
		bundle.fileEntries[entry.Path] = entry
		resolved, err := bundle.resolve(entry.Path)
		if err != nil {
			return nil, err
		}
		if err := rejectSymlinkPath(resolved); err != nil {
			return nil, fmt.Errorf("%w: file %q: %v", ErrBusinessSystemPromptBundleInvalid, entry.Path, err)
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && !entry.Required {
				bundle.Degraded = true
				bundle.MissingOptional = append(bundle.MissingOptional, entry.Path)
				continue
			}
			return nil, fmt.Errorf("%w: read %q: %v", ErrBusinessSystemPromptBundleUnavailable, entry.Path, err)
		}
		if len(data) != entry.ByteLength {
			return nil, fmt.Errorf("%w: %q byte length mismatch", ErrBusinessSystemPromptBundleInvalid, entry.Path)
		}
		if !equalHexDigest(entry.SHA256, data) {
			return nil, fmt.Errorf("%w: %q sha256 mismatch", ErrBusinessSystemPromptBundleInvalid, entry.Path)
		}
		if bundleFileKind(entry) == "text" && !utf8.Valid(data) {
			return nil, fmt.Errorf("%w: %q is not UTF-8", ErrBusinessSystemPromptBundleInvalid, entry.Path)
		}
		bundle.files[entry.Path] = append([]byte(nil), data...)
	}
	sort.Strings(bundle.MissingOptional)
	return bundle, nil
}

type BusinessSystemPromptBundleSummary struct {
	BundleID       string    `json:"bundle_id"`
	Name           string    `json:"name,omitempty"`
	Version        string    `json:"version,omitempty"`
	Description    string    `json:"description,omitempty"`
	ManifestSHA256 string    `json:"manifest_sha256"`
	Available      bool      `json:"available"`
	Degraded       bool      `json:"degraded"`
	DegradedReason string    `json:"degraded_reason,omitempty"`
	DocumentCount  int       `json:"document_count"`
	RouteCount     int       `json:"route_count"`
	TotalBytes     int64     `json:"total_bytes"`
	LoadedAt       time.Time `json:"loaded_at,omitempty"`
}

type BusinessSystemPromptBundleDocument struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	ByteLength int    `json:"byte_length"`
	Kind       string `json:"kind"`
	Required   bool   `json:"required"`
}

type BusinessSystemPromptBundleRoute struct {
	ID         string   `json:"id"`
	Keywords   []string `json:"keywords"`
	Entry      string   `json:"entry"`
	References []string `json:"references,omitempty"`
	Priority   int      `json:"priority,omitempty"`
}

type BusinessSystemPromptBundleDetail struct {
	BusinessSystemPromptBundleSummary
	Documents []BusinessSystemPromptBundleDocument `json:"documents"`
	Routes    []BusinessSystemPromptBundleRoute    `json:"routes"`
}

func validateBusinessSystemPromptBundleManifest(manifest BusinessSystemPromptBundleManifest) error {
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("%w: unsupported schema version %d", ErrBusinessSystemPromptBundleInvalid, manifest.SchemaVersion)
	}
	if !validBundleID(manifest.BundleID) {
		return fmt.Errorf("%w: invalid bundle id", ErrBusinessSystemPromptBundleInvalid)
	}
	if len(manifest.Files) == 0 {
		return fmt.Errorf("%w: no files", ErrBusinessSystemPromptBundleInvalid)
	}
	seen := make(map[string]struct{}, len(manifest.Files))
	for _, entry := range manifest.Files {
		normalized, err := normalizeBundleRelativePath(entry.Path)
		if err != nil || normalized != entry.Path {
			return fmt.Errorf("%w: invalid file path %q", ErrBusinessSystemPromptBundleInvalid, entry.Path)
		}
		if _, ok := seen[entry.Path]; ok {
			return fmt.Errorf("%w: duplicate file path %q", ErrBusinessSystemPromptBundleInvalid, entry.Path)
		}
		seen[entry.Path] = struct{}{}
		if entry.ByteLength < 0 || entry.ByteLength > businessSystemPromptBundleMaxFileBytes || len(strings.TrimSpace(entry.SHA256)) != sha256.Size*2 || !isHex(entry.SHA256) {
			return fmt.Errorf("%w: invalid digest or length for %q", ErrBusinessSystemPromptBundleInvalid, entry.Path)
		}
		switch bundleFileKind(entry) {
		case "text", "binary", "script":
		default:
			return fmt.Errorf("%w: invalid kind for %q", ErrBusinessSystemPromptBundleInvalid, entry.Path)
		}
	}
	core := manifest.CoreFiles
	if strings.TrimSpace(manifest.Core) != "" {
		core = append(core, manifest.Core)
	}
	if len(core) == 0 {
		return fmt.Errorf("%w: no core files", ErrBusinessSystemPromptBundleInvalid)
	}
	for _, p := range core {
		if _, ok := seen[p]; !ok {
			return fmt.Errorf("%w: core file %q is not declared", ErrBusinessSystemPromptBundleInvalid, p)
		}
	}
	domainIDs := make(map[string]struct{}, len(manifest.Domains))
	for _, domain := range manifest.Domains {
		if !validBundleID(domain.ID) || domain.Entry == "" {
			return fmt.Errorf("%w: invalid domain %q", ErrBusinessSystemPromptBundleInvalid, domain.ID)
		}
		if _, ok := domainIDs[domain.ID]; ok {
			return fmt.Errorf("%w: duplicate domain %q", ErrBusinessSystemPromptBundleInvalid, domain.ID)
		}
		domainIDs[domain.ID] = struct{}{}
		if _, ok := seen[domain.Entry]; !ok {
			return fmt.Errorf("%w: domain entry %q is not declared", ErrBusinessSystemPromptBundleInvalid, domain.Entry)
		}
		for _, ref := range domain.References {
			if _, ok := seen[ref]; !ok {
				return fmt.Errorf("%w: reference %q is not declared", ErrBusinessSystemPromptBundleInvalid, ref)
			}
		}
	}
	return nil
}

func (b *BusinessSystemPromptBundle) resolve(rel string) (string, error) {
	if b == nil {
		return "", fmt.Errorf("%w: nil bundle", ErrBusinessSystemPromptBundleUnavailable)
	}
	normalized, err := normalizeBundleRelativePath(rel)
	if err != nil || normalized != rel {
		return "", fmt.Errorf("%w: invalid path %q", ErrBusinessSystemPromptBundleInvalid, rel)
	}
	resolved := filepath.Join(b.Root, filepath.FromSlash(rel))
	relToRoot, err := filepath.Rel(b.Root, resolved)
	if err != nil || filepath.IsAbs(relToRoot) || relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: path escapes bundle root", ErrBusinessSystemPromptBundleInvalid)
	}
	return resolved, nil
}

func (b *BusinessSystemPromptBundle) ReadText(rel string) (string, error) {
	if b == nil {
		return "", fmt.Errorf("%w: nil bundle", ErrBusinessSystemPromptBundleUnavailable)
	}
	entry, ok := b.fileEntries[rel]
	if !ok {
		return "", fmt.Errorf("%w: file %q is not in manifest", ErrBusinessSystemPromptBundleInvalid, rel)
	}
	if bundleFileKind(entry) != "text" {
		return "", fmt.Errorf("%w: file %q is not text", ErrBusinessSystemPromptBundleInvalid, rel)
	}
	data, ok := b.files[rel]
	if !ok {
		return "", fmt.Errorf("%w: optional file %q is unavailable", ErrBusinessSystemPromptBundleUnavailable, rel)
	}
	return string(data), nil
}

func (b *BusinessSystemPromptBundle) file(rel string) (BusinessSystemPromptBundleFile, bool) {
	if b == nil {
		return BusinessSystemPromptBundleFile{}, false
	}
	entry, ok := b.fileEntries[rel]
	return entry, ok
}

func normalizeBundleRelativePath(value string) (string, error) {
	if value == "" || strings.ContainsRune(value, '\x00') || strings.ContainsRune(value, '\\') {
		return "", errors.New("empty, NUL, or backslash path")
	}
	for _, r := range value {
		if r < 0x20 || r == ':' {
			return "", errors.New("control character or colon path")
		}
	}
	if strings.HasPrefix(value, "/") || filepath.IsAbs(filepath.FromSlash(value)) {
		return "", errors.New("absolute path")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("path traversal")
	}
	if clean != value || strings.Contains(value, "//") {
		return "", errors.New("non-canonical path")
	}
	for _, segment := range strings.Split(clean, "/") {
		if strings.TrimRight(segment, " .") != segment || isWindowsReservedBundlePathSegment(segment) {
			return "", errors.New("ambiguous or reserved path segment")
		}
	}
	return clean, nil
}

func isWindowsReservedBundlePathSegment(segment string) bool {
	base := strings.ToUpper(strings.SplitN(segment, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return true
	}
	return false
}

func rejectSymlinkPath(value string) error {
	abs, err := filepath.Abs(value)
	if err != nil {
		return err
	}
	clean := filepath.Clean(abs)
	volume := filepath.VolumeName(clean)
	rest := strings.TrimPrefix(clean, volume)
	for len(rest) > 0 && (rest[0] == '\\' || rest[0] == '/') {
		rest = rest[1:]
	}
	current := volume + string(filepath.Separator)
	if volume == "" {
		current = string(filepath.Separator)
	}
	for _, part := range strings.FieldsFunc(rest, func(r rune) bool { return r == '\\' || r == '/' }) {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				return nil
			}
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink component %q", current)
		}
	}
	return nil
}

func bundleFileKind(entry BusinessSystemPromptBundleFile) string {
	kind := strings.ToLower(strings.TrimSpace(entry.Kind))
	if kind == "" {
		ext := strings.ToLower(filepath.Ext(entry.Path))
		if ext == ".ps1" || ext == ".sh" || ext == ".bat" || ext == ".cmd" || ext == ".exe" || ext == ".dll" || ext == ".jar" {
			return "script"
		}
		return "text"
	}
	return kind
}

func validBundleID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func isHex(value string) bool {
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			continue
		}
		return false
	}
	return true
}

func equalHexDigest(expected string, data []byte) bool {
	digest := sha256.Sum256(data)
	return strings.EqualFold(strings.TrimSpace(expected), hex.EncodeToString(digest[:]))
}

func hashBusinessSystemPromptBundleBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func cloneBusinessSystemPromptBundleManifest(manifest BusinessSystemPromptBundleManifest) BusinessSystemPromptBundleManifest {
	clone := manifest
	clone.CoreFiles = append([]string(nil), manifest.CoreFiles...)
	clone.Files = append([]BusinessSystemPromptBundleFile(nil), manifest.Files...)
	clone.Domains = make([]BusinessSystemPromptBundleDomain, len(manifest.Domains))
	for i, domain := range manifest.Domains {
		clone.Domains[i] = domain
		clone.Domains[i].Keywords = append([]string(nil), domain.Keywords...)
		clone.Domains[i].References = append([]string(nil), domain.References...)
	}
	return clone
}
