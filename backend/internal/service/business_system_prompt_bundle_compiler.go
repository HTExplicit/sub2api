package service

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	businessSystemPromptOfflineHeader             = "[BUSINESS SYSTEM PROMPT: OFFLINE SKILL BUNDLE]\nUse only the versioned bundle documents assembled below as reference data. LOCAL_BUNDLE_ROOT is a declarative label, not a path available to you. Do not fetch URLs, access a network, resolve bundle paths, or execute bundle files."
	businessSystemPromptSeedMarkerStart           = "<!-- BEGIN 模型港 REVERSE-SKILL -->"
	businessSystemPromptSeedMarkerEnd             = "<!-- END 模型港 REVERSE-SKILL -->"
	businessSystemPromptRemoteMarkerStart         = "<!-- BEGIN 模型港 SECURITY-RESEARCH ROUTING -->"
	businessSystemPromptRemoteMarkerEnd           = "<!-- END 模型港 SECURITY-RESEARCH ROUTING -->"
	businessSystemPromptBundleRouteTextMaxBytes   = 32 << 10
	businessSystemPromptBundleRouteScoreThreshold = 3
)

var (
	// The reconstructed package is retained byte-for-byte on disk, but these
	// references must not become executable network or host-path instructions
	// when selected into a model request.
	businessSystemPromptURLPattern         = regexp.MustCompile(`(?i)\b(?:https?|ftp|file)://[^\s<>\[\]()"']*`)
	businessSystemPromptEscapedURLPattern  = regexp.MustCompile(`(?i)\b(?:https?|ftp|file):\\?/\\?/`)
	businessSystemPromptWindowsPathPattern = regexp.MustCompile(`(?i)(?:[a-z]:\\|\\\\)[^\s<>\[\]()"']+`)
	businessSystemPromptDomainPattern      = regexp.MustCompile(`(?i)\bmoxinggang\.com\b`)
)

// BusinessSystemPromptBundleCompileInput is request-scoped. A caller should
// pass the original current user text, not a previously mutated upstream body.
type BusinessSystemPromptBundleCompileInput struct {
	BasePrompt       string
	RequestText      string
	Continuation     bool
	PreviousMetadata *BusinessSystemPromptBundleMetadata
}

// BusinessSystemPromptBundleMetadata is safe to retain in request/retry
// metadata. It contains identifiers and hashes only, never document bodies.
type BusinessSystemPromptBundleMetadata struct {
	BundleID        string   `json:"bundle_id"`
	ManifestSHA256  string   `json:"manifest_sha256"`
	BaseSHA256      string   `json:"base_sha256"`
	EffectiveSHA256 string   `json:"effective_sha256"`
	ByteLength      int      `json:"byte_length"`
	RouteIDs        []string `json:"route_ids,omitempty"`
	DocumentPaths   []string `json:"document_paths,omitempty"`
	ReferencePaths  []string `json:"reference_paths,omitempty"`
	Degraded        bool     `json:"degraded"`
}

func ValidateBusinessSystemPromptBundleMetadata(metadata BusinessSystemPromptBundleMetadata) error {
	if !validBundleID(metadata.BundleID) {
		return fmt.Errorf("%w: invalid metadata bundle id", ErrBusinessSystemPromptBundleInvalid)
	}
	for name, value := range map[string]string{
		"manifest":  metadata.ManifestSHA256,
		"base":      metadata.BaseSHA256,
		"effective": metadata.EffectiveSHA256,
	} {
		if len(value) != 64 || !isHex(value) {
			return fmt.Errorf("%w: invalid metadata %s sha256", ErrBusinessSystemPromptBundleInvalid, name)
		}
	}
	if metadata.ByteLength < 1 || metadata.ByteLength > BusinessSystemPromptBundleMaxBytes {
		return fmt.Errorf("%w: invalid metadata byte length", ErrBusinessSystemPromptBundleInvalid)
	}
	if len(metadata.RouteIDs) > BusinessSystemPromptHybridMaxDomains || len(metadata.ReferencePaths) > BusinessSystemPromptHybridMaxReferences || len(metadata.DocumentPaths) > BusinessSystemPromptHybridMaxDocuments {
		return fmt.Errorf("%w: metadata selection exceeds limits", ErrBusinessSystemPromptBundleInvalid)
	}
	for _, routeID := range metadata.RouteIDs {
		if !validBundleID(routeID) {
			return fmt.Errorf("%w: invalid metadata route id", ErrBusinessSystemPromptBundleInvalid)
		}
	}
	for _, documentPath := range append(append([]string(nil), metadata.DocumentPaths...), metadata.ReferencePaths...) {
		normalized, err := normalizeBundleRelativePath(documentPath)
		if err != nil || normalized != documentPath {
			return fmt.Errorf("%w: invalid metadata document path", ErrBusinessSystemPromptBundleInvalid)
		}
	}
	return nil
}

// CacheKey includes every active-body input. In particular, revision and
// manifest/effective hashes prevent stale prompt material after publish or
// bundle replacement, while route IDs keep deterministic variants separate.
func (m BusinessSystemPromptBundleMetadata) CacheKey(revision int64) string {
	routes := strings.Join(append([]string(nil), m.RouteIDs...), ",")
	docs := strings.Join(append([]string(nil), m.DocumentPaths...), ",")
	return fmt.Sprintf("business-system-prompt:%d:%s:%s:%s:%s:%s", revision, m.BundleID, m.ManifestSHA256, m.BaseSHA256, m.EffectiveSHA256, hashBusinessSystemPromptBundleBytes([]byte(routes+"\x00"+docs)))
}

// BusinessSystemPromptBundleCompiled is the immutable output used by protocol
// adapters. Body is the only field containing prompt text; Metadata is suitable
// for retries, failover and previous_response_id state.
type BusinessSystemPromptBundleCompiled struct {
	Body         string                             `json:"body"`
	Metadata     BusinessSystemPromptBundleMetadata `json:"metadata"`
	RouteIDs     []string                           `json:"route_ids,omitempty"`
	OmittedPaths []string                           `json:"omitted_paths,omitempty"`
}

type BusinessSystemPromptBundleCompiler struct {
	bundle                *BusinessSystemPromptBundle
	maxBytes              int
	maxDomains            int
	maxReferences         int
	preserveManifestOrder bool
}

func NewBusinessSystemPromptBundleCompiler(bundle *BusinessSystemPromptBundle) *BusinessSystemPromptBundleCompiler {
	return NewBusinessSystemPromptBundleCompilerWithLimit(bundle, BusinessSystemPromptBundleMaxBytes)
}

func NewBusinessSystemPromptBundleCompilerWithLimit(bundle *BusinessSystemPromptBundle, maxBytes int) *BusinessSystemPromptBundleCompiler {
	if maxBytes <= 0 {
		maxBytes = BusinessSystemPromptBundleMaxBytes
	}
	return &BusinessSystemPromptBundleCompiler{
		bundle: bundle, maxBytes: maxBytes,
		maxDomains: BusinessSystemPromptBundleMaxDomains, maxReferences: BusinessSystemPromptBundleMaxReferences,
	}
}

func NewBusinessSystemPromptHybridCompiler(bundle *BusinessSystemPromptBundle) *BusinessSystemPromptBundleCompiler {
	return &BusinessSystemPromptBundleCompiler{
		bundle: bundle, maxBytes: BusinessSystemPromptBundleMaxBytes,
		maxDomains: BusinessSystemPromptHybridMaxDomains, maxReferences: BusinessSystemPromptHybridMaxReferences,
		preserveManifestOrder: true,
	}
}

func (c *BusinessSystemPromptBundleCompiler) Compile(input BusinessSystemPromptBundleCompileInput) (BusinessSystemPromptBundleCompiled, error) {
	if c == nil || c.bundle == nil {
		return BusinessSystemPromptBundleCompiled{}, fmt.Errorf("%w: loader is nil", ErrBusinessSystemPromptBundleUnavailable)
	}
	if !utf8.ValidString(input.BasePrompt) || strings.ContainsRune(input.BasePrompt, '\x00') || strings.TrimSpace(input.BasePrompt) == "" {
		return BusinessSystemPromptBundleCompiled{}, fmt.Errorf("%w: invalid base prompt", ErrBusinessSystemPromptInvalid)
	}
	baseHash := hashBusinessSystemPromptBundleBytes([]byte(input.BasePrompt))
	base := sanitizeBusinessSystemPromptText(stripBusinessSystemPromptNetworkMarkers(input.BasePrompt))
	if strings.TrimSpace(base) == "" {
		return BusinessSystemPromptBundleCompiled{}, fmt.Errorf("%w: base prompt has no offline content", ErrBusinessSystemPromptInvalid)
	}

	routes, previousDocs, previousOK := c.selectRoutes(input)
	metadata := BusinessSystemPromptBundleMetadata{
		BundleID:       c.bundle.Manifest.BundleID,
		ManifestSHA256: c.bundle.ManifestSHA256,
		BaseSHA256:     baseHash,
		Degraded:       c.bundle.Degraded,
	}
	for _, route := range routes {
		metadata.RouteIDs = append(metadata.RouteIDs, route.ID)
	}

	sections := make([]businessSystemPromptBundleSection, 0, 1+len(c.bundle.Manifest.CoreFiles)+len(routes))
	sections = append(sections, businessSystemPromptBundleSection{label: "seed", body: strings.TrimSpace(base), mandatory: true})
	for _, corePath := range c.corePaths() {
		text, err := c.requiredText(corePath)
		if err != nil {
			return BusinessSystemPromptBundleCompiled{}, err
		}
		sections = append(sections, businessSystemPromptBundleSection{label: "core/" + corePath, body: sanitizeBusinessSystemPromptText(text), mandatory: true, path: corePath})
	}

	for _, route := range routes {
		text, err := c.requiredText(route.Entry)
		if err != nil {
			return BusinessSystemPromptBundleCompiled{}, err
		}
		sections = append(sections, businessSystemPromptBundleSection{label: "domain/" + route.ID + "/" + route.Entry, body: sanitizeBusinessSystemPromptText(text), mandatory: true, path: route.Entry})
	}

	refs := c.selectReferences(routes, input, previousDocs, previousOK)
	for _, ref := range refs {
		text, err := c.optionalText(ref)
		if err != nil {
			metadata.Degraded = true
			continue
		}
		sections = append(sections, businessSystemPromptBundleSection{label: "reference/" + ref, body: sanitizeBusinessSystemPromptText(text), path: ref})
	}

	body, includedPaths, omittedOptional, err := c.joinSections(sections)
	if err != nil {
		return BusinessSystemPromptBundleCompiled{}, err
	}
	if len(omittedOptional) > 0 {
		metadata.Degraded = true
	}
	metadata.DocumentPaths = append(metadata.DocumentPaths, includedPaths...)
	for _, p := range includedPaths {
		if containsString(refs, p) {
			metadata.ReferencePaths = append(metadata.ReferencePaths, p)
		}
	}
	metadata.EffectiveSHA256 = hashBusinessSystemPromptBundleBytes([]byte(body))
	metadata.ByteLength = len([]byte(body))
	metadata.RouteIDs = append([]string(nil), metadata.RouteIDs...)
	metadata.DocumentPaths = append([]string(nil), metadata.DocumentPaths...)
	metadata.ReferencePaths = append([]string(nil), metadata.ReferencePaths...)
	return BusinessSystemPromptBundleCompiled{
		Body: body, Metadata: metadata, RouteIDs: append([]string(nil), metadata.RouteIDs...),
		OmittedPaths: append([]string(nil), omittedOptional...),
	}, nil
}

// CompileHybrid preserves the base prompt byte-for-byte. Verified bundle
// documents are appended only after at least one route matches; route and
// reference sections are optional so the stable 256 KiB cap omits later
// manifest-ordered documents instead of failing the request.
func (c *BusinessSystemPromptBundleCompiler) CompileHybrid(input BusinessSystemPromptBundleCompileInput) (BusinessSystemPromptBundleCompiled, error) {
	if c == nil || c.bundle == nil {
		return BusinessSystemPromptBundleCompiled{}, fmt.Errorf("%w: loader is nil", ErrBusinessSystemPromptBundleUnavailable)
	}
	if !utf8.ValidString(input.BasePrompt) || strings.ContainsRune(input.BasePrompt, '\x00') || strings.TrimSpace(input.BasePrompt) == "" {
		return BusinessSystemPromptBundleCompiled{}, fmt.Errorf("%w: invalid base prompt", ErrBusinessSystemPromptInvalid)
	}
	baseHash := hashBusinessSystemPromptBundleBytes([]byte(input.BasePrompt))
	routes, previousDocs, previousOK := c.selectRoutes(input)
	metadata := BusinessSystemPromptBundleMetadata{
		BundleID: c.bundle.Manifest.BundleID, ManifestSHA256: c.bundle.ManifestSHA256,
		BaseSHA256: baseHash, Degraded: c.bundle.Degraded,
	}
	for _, route := range routes {
		metadata.RouteIDs = append(metadata.RouteIDs, route.ID)
	}
	if len(routes) == 0 {
		metadata.EffectiveSHA256 = baseHash
		metadata.ByteLength = len([]byte(input.BasePrompt))
		return BusinessSystemPromptBundleCompiled{Body: input.BasePrompt, Metadata: metadata}, nil
	}

	sections := make([]businessSystemPromptBundleSection, 0, len(c.bundle.Manifest.CoreFiles)+len(routes))
	for _, corePath := range c.corePaths() {
		text, err := c.requiredText(corePath)
		if err != nil {
			return BusinessSystemPromptBundleCompiled{}, err
		}
		sections = append(sections, businessSystemPromptBundleSection{
			label: "core/" + corePath, body: sanitizeBusinessSystemPromptText(text), mandatory: true, path: corePath,
		})
	}
	for _, route := range routes {
		text, err := c.requiredText(route.Entry)
		if err != nil {
			return BusinessSystemPromptBundleCompiled{}, err
		}
		sections = append(sections, businessSystemPromptBundleSection{
			label: "route/" + route.ID + "/" + route.Entry, body: sanitizeBusinessSystemPromptText(text), path: route.Entry,
		})
	}
	refs := c.selectReferences(routes, input, previousDocs, previousOK)
	for _, ref := range refs {
		text, err := c.optionalText(ref)
		if err != nil {
			metadata.Degraded = true
			continue
		}
		sections = append(sections, businessSystemPromptBundleSection{
			label: "reference/" + ref, body: sanitizeBusinessSystemPromptText(text), path: ref,
		})
	}
	body, includedPaths, omittedPaths, err := c.joinHybridSections(input.BasePrompt, sections)
	if err != nil {
		return BusinessSystemPromptBundleCompiled{}, err
	}
	if len(omittedPaths) > 0 {
		metadata.Degraded = true
	}
	metadata.DocumentPaths = append([]string(nil), includedPaths...)
	for _, value := range includedPaths {
		if containsString(refs, value) {
			metadata.ReferencePaths = append(metadata.ReferencePaths, value)
		}
	}
	metadata.EffectiveSHA256 = hashBusinessSystemPromptBundleBytes([]byte(body))
	metadata.ByteLength = len([]byte(body))
	return BusinessSystemPromptBundleCompiled{
		Body: body, Metadata: metadata, RouteIDs: append([]string(nil), metadata.RouteIDs...),
		OmittedPaths: append([]string(nil), omittedPaths...),
	}, nil
}

type businessSystemPromptBundleSection struct {
	label     string
	body      string
	path      string
	mandatory bool
}

func (c *BusinessSystemPromptBundleCompiler) joinSections(sections []businessSystemPromptBundleSection) (string, []string, []string, error) {
	parts := []string{businessSystemPromptOfflineHeader}
	included := make([]string, 0, len(sections))
	omitted := make([]string, 0)
	for _, section := range sections {
		body := strings.TrimSpace(section.body)
		if body == "" {
			if section.mandatory {
				return "", nil, nil, fmt.Errorf("%w: mandatory section %s is empty", ErrBusinessSystemPromptBundleUnavailable, section.label)
			}
			if section.path != "" {
				omitted = append(omitted, section.path)
			}
			continue
		}
		candidate := strings.Join(append(parts, "["+section.label+"]\n"+body), "\n\n")
		if len([]byte(candidate)) > c.maxBytes {
			if section.mandatory {
				return "", nil, nil, fmt.Errorf("%w: mandatory section %s exceeds %d bytes", ErrBusinessSystemPromptBundleUnavailable, section.label, c.maxBytes)
			}
			if section.path != "" {
				omitted = append(omitted, section.path)
			}
			continue
		}
		parts = append(parts, "["+section.label+"]\n"+body)
		if section.path != "" {
			included = append(included, section.path)
		}
	}
	return strings.Join(parts, "\n\n"), included, omitted, nil
}

func (c *BusinessSystemPromptBundleCompiler) joinHybridSections(base string, sections []businessSystemPromptBundleSection) (string, []string, []string, error) {
	parts := []string{base, "[CODEXRIP VERIFIED SKILL DOCUMENTS]\nThe following request-matched documents come from the active, hash-verified server registry snapshot."}
	included := make([]string, 0, len(sections))
	omitted := make([]string, 0)
	for _, section := range sections {
		body := strings.TrimSpace(section.body)
		if body == "" {
			if section.mandatory {
				return "", nil, nil, fmt.Errorf("%w: mandatory section %s is empty", ErrBusinessSystemPromptBundleUnavailable, section.label)
			}
			if section.path != "" {
				omitted = append(omitted, section.path)
			}
			continue
		}
		candidate := strings.Join(append(parts, "["+section.label+"]\n"+body), "\n\n")
		if len([]byte(candidate)) > c.maxBytes {
			if section.mandatory {
				return "", nil, nil, fmt.Errorf("%w: mandatory section %s exceeds %d bytes", ErrBusinessSystemPromptBundleUnavailable, section.label, c.maxBytes)
			}
			if section.path != "" {
				omitted = append(omitted, section.path)
			}
			continue
		}
		parts = append(parts, "["+section.label+"]\n"+body)
		if section.path != "" {
			included = append(included, section.path)
		}
	}
	return strings.Join(parts, "\n\n"), included, omitted, nil
}

func (c *BusinessSystemPromptBundleCompiler) corePaths() []string {
	paths := append([]string(nil), c.bundle.Manifest.CoreFiles...)
	if strings.TrimSpace(c.bundle.Manifest.Core) != "" {
		paths = append(paths, c.bundle.Manifest.Core)
	}
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		result = append(result, p)
	}
	return result
}

func (c *BusinessSystemPromptBundleCompiler) requiredText(rel string) (string, error) {
	entry, ok := c.bundle.file(rel)
	if !ok {
		return "", fmt.Errorf("%w: required file %q is not declared", ErrBusinessSystemPromptBundleUnavailable, rel)
	}
	if bundleFileKind(entry) != "text" {
		return "", fmt.Errorf("%w: required file %q is not text", ErrBusinessSystemPromptBundleInvalid, rel)
	}
	text, err := c.bundle.ReadText(rel)
	if err != nil {
		return "", fmt.Errorf("%w: required file %q: %v", ErrBusinessSystemPromptBundleUnavailable, rel, err)
	}
	return stripBusinessSystemPromptNetworkMarkers(text), nil
}

func (c *BusinessSystemPromptBundleCompiler) optionalText(rel string) (string, error) {
	entry, ok := c.bundle.file(rel)
	if !ok {
		return "", fmt.Errorf("%w: reference %q is not declared", ErrBusinessSystemPromptBundleInvalid, rel)
	}
	if bundleFileKind(entry) != "text" {
		return "", fmt.Errorf("%w: reference %q is not text", ErrBusinessSystemPromptBundleInvalid, rel)
	}
	text, err := c.bundle.ReadText(rel)
	if err != nil {
		return "", err
	}
	return stripBusinessSystemPromptNetworkMarkers(text), nil
}

func (c *BusinessSystemPromptBundleCompiler) selectRoutes(input BusinessSystemPromptBundleCompileInput) ([]BusinessSystemPromptBundleDomain, []string, bool) {
	if input.Continuation && input.PreviousMetadata != nil && input.PreviousMetadata.BundleID == c.bundle.Manifest.BundleID && input.PreviousMetadata.ManifestSHA256 == c.bundle.ManifestSHA256 {
		result := make([]BusinessSystemPromptBundleDomain, 0, len(input.PreviousMetadata.RouteIDs))
		for _, id := range input.PreviousMetadata.RouteIDs {
			for _, domain := range c.bundle.Manifest.Domains {
				if domain.ID == id {
					result = append(result, domain)
					break
				}
			}
		}
		return result, append([]string(nil), input.PreviousMetadata.ReferencePaths...), true
	}
	type scored struct {
		domain BusinessSystemPromptBundleDomain
		score  int
	}
	text := strings.ToLower(limitBusinessSystemPromptBundleRouteText(input.RequestText))
	scoredDomains := make([]scored, 0, len(c.bundle.Manifest.Domains))
	for _, domain := range c.bundle.Manifest.Domains {
		score := 0
		for _, keyword := range domain.Keywords {
			keyword = strings.ToLower(strings.TrimSpace(keyword))
			if keyword != "" {
				score += businessSystemPromptBundleRouteScoreThreshold * strings.Count(text, keyword)
			}
		}
		if score >= businessSystemPromptBundleRouteScoreThreshold {
			scoredDomains = append(scoredDomains, scored{domain: domain, score: score})
		}
	}
	sort.SliceStable(scoredDomains, func(i, j int) bool {
		if scoredDomains[i].score != scoredDomains[j].score {
			return scoredDomains[i].score > scoredDomains[j].score
		}
		if c.preserveManifestOrder {
			return false
		}
		if scoredDomains[i].domain.Priority != scoredDomains[j].domain.Priority {
			return scoredDomains[i].domain.Priority > scoredDomains[j].domain.Priority
		}
		return scoredDomains[i].domain.ID < scoredDomains[j].domain.ID
	})
	maximum := c.maxDomains
	if maximum <= 0 {
		maximum = BusinessSystemPromptBundleMaxDomains
	}
	if len(scoredDomains) > maximum {
		scoredDomains = scoredDomains[:maximum]
	}
	result := make([]BusinessSystemPromptBundleDomain, len(scoredDomains))
	for i := range scoredDomains {
		result[i] = scoredDomains[i].domain
	}
	return result, nil, false
}

func (c *BusinessSystemPromptBundleCompiler) selectReferences(routes []BusinessSystemPromptBundleDomain, input BusinessSystemPromptBundleCompileInput, previous []string, previousOK bool) []string {
	if input.Continuation && previousOK && len(previous) > 0 {
		maximum := c.maxReferences
		if maximum <= 0 {
			maximum = BusinessSystemPromptBundleMaxReferences
		}
		result := make([]string, 0, maximum)
		for _, p := range previous {
			if len(result) >= maximum {
				break
			}
			if !containsString(result, p) {
				result = append(result, p)
			}
		}
		return result
	}
	maximum := c.maxReferences
	if maximum <= 0 {
		maximum = BusinessSystemPromptBundleMaxReferences
	}
	result := make([]string, 0, maximum)
	for _, route := range routes {
		for _, ref := range route.References {
			if containsString(result, ref) {
				continue
			}
			result = append(result, ref)
			if len(result) >= maximum {
				return result
			}
		}
	}
	return result
}

func stripBusinessSystemPromptNetworkMarkers(body string) string {
	for {
		changed := false
		for _, marker := range [][2]string{
			{businessSystemPromptSeedMarkerStart, businessSystemPromptSeedMarkerEnd},
			{businessSystemPromptRemoteMarkerStart, businessSystemPromptRemoteMarkerEnd},
		} {
			start := strings.Index(body, marker[0])
			if start < 0 {
				continue
			}
			relEnd := strings.Index(body[start+len(marker[0]):], marker[1])
			if relEnd < 0 {
				body = body[:start]
				changed = true
				continue
			}
			end := start + len(marker[0]) + relEnd + len(marker[1])
			body = body[:start] + body[end:]
			changed = true
		}
		if !changed {
			break
		}
	}
	lines := strings.Split(body, "\n")
	filtered := lines[:0]
	for _, line := range lines {
		line = strings.ReplaceAll(line, "https://moxinggang.com/skills/security-research/current", "LOCAL_BUNDLE_ROOT")
		line = strings.ReplaceAll(line, `C:\Users\Administrator\AppData\Local\模型港\reverse-skill`, "LOCAL_BUNDLE_ROOT")
		line = strings.ReplaceAll(line, "REMOTE_ROOT", "LOCAL_BUNDLE_ROOT")
		filtered = append(filtered, line)
	}
	return strings.TrimSpace(strings.Join(filtered, "\n"))
}

func sanitizeBusinessSystemPromptText(body string) string {
	// Handle escaped JSON-style schemes before the generic replacements. The
	// resulting labels are deliberately non-routable and contain no source URL.
	body = businessSystemPromptEscapedURLPattern.ReplaceAllString(body, "LOCAL_BUNDLE_URL")
	body = businessSystemPromptURLPattern.ReplaceAllString(body, "LOCAL_BUNDLE_URL")
	body = businessSystemPromptWindowsPathPattern.ReplaceAllString(body, "LOCAL_BUNDLE_PATH")
	body = businessSystemPromptDomainPattern.ReplaceAllString(body, "LOCAL_BUNDLE_DOMAIN")
	return body
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func limitBusinessSystemPromptBundleRouteText(value string) string {
	if len(value) <= businessSystemPromptBundleRouteTextMaxBytes {
		return value
	}
	value = value[len(value)-businessSystemPromptBundleRouteTextMaxBytes:]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[1:]
	}
	return value
}
