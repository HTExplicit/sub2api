package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const BusinessSystemPromptBundleRootEnv = "SUB2API_SYSTEM_PROMPT_BUNDLE_ROOT"
const BusinessSystemPromptBundleDefaultRoot = "/app/skill-bundles"

// BusinessSystemPromptBundleRegistry resolves content-addressed bundle
// directories as <root>/<bundle_id>/<manifest_sha256>. A failed reload never
// replaces the verified in-memory value, which provides request-level
// last-known-good behavior without retaining prompt bodies in Redis.
type BusinessSystemPromptBundleRegistry struct {
	root string

	mu      sync.RWMutex
	bundles map[string]*BusinessSystemPromptBundle
}

func DefaultBusinessSystemPromptBundleRoot() string {
	if value := strings.TrimSpace(os.Getenv(BusinessSystemPromptBundleRootEnv)); value != "" {
		return value
	}
	return BusinessSystemPromptBundleDefaultRoot
}

func NewBusinessSystemPromptBundleRegistry(root string) *BusinessSystemPromptBundleRegistry {
	root = strings.TrimSpace(root)
	if root == "" {
		root = DefaultBusinessSystemPromptBundleRoot()
	}
	return &BusinessSystemPromptBundleRegistry{root: root, bundles: make(map[string]*BusinessSystemPromptBundle)}
}

func (r *BusinessSystemPromptBundleRegistry) Load(bundleID, manifestSHA256 string) (*BusinessSystemPromptBundle, error) {
	key, bundlePath, err := r.resolve(bundleID, manifestSHA256)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	current := r.bundles[key]
	r.mu.RUnlock()
	if current != nil {
		return current, nil
	}
	loaded, err := loadBusinessSystemPromptBundleIdentity(bundlePath, bundleID, manifestSHA256)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if current = r.bundles[key]; current == nil {
		r.bundles[key] = loaded
		current = loaded
	}
	r.mu.Unlock()
	return current, nil
}

func (r *BusinessSystemPromptBundleRegistry) Reload(bundleID, manifestSHA256 string) (*BusinessSystemPromptBundle, error) {
	key, bundlePath, err := r.resolve(bundleID, manifestSHA256)
	if err != nil {
		return nil, err
	}
	loaded, err := loadBusinessSystemPromptBundleIdentity(bundlePath, bundleID, manifestSHA256)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.bundles[key] = loaded
	r.mu.Unlock()
	return loaded, nil
}

func (r *BusinessSystemPromptBundleRegistry) Current(bundleID, manifestSHA256 string) (*BusinessSystemPromptBundle, bool) {
	key, _, err := r.resolve(bundleID, manifestSHA256)
	if err != nil {
		return nil, false
	}
	r.mu.RLock()
	current := r.bundles[key]
	r.mu.RUnlock()
	return current, current != nil
}

func (r *BusinessSystemPromptBundleRegistry) resolve(bundleID, manifestSHA256 string) (string, string, error) {
	if r == nil {
		return "", "", fmt.Errorf("%w: registry is nil", ErrBusinessSystemPromptBundleUnavailable)
	}
	bundleID = strings.TrimSpace(bundleID)
	manifestSHA256 = strings.ToLower(strings.TrimSpace(manifestSHA256))
	if !validBundleID(bundleID) || len(manifestSHA256) != 64 || !isHex(manifestSHA256) {
		return "", "", fmt.Errorf("%w: invalid bundle identity", ErrBusinessSystemPromptBundleInvalid)
	}
	root, err := filepath.Abs(strings.TrimSpace(r.root))
	if err != nil || strings.HasPrefix(root, `\\`) {
		return "", "", fmt.Errorf("%w: invalid registry root", ErrBusinessSystemPromptBundleInvalid)
	}
	root = filepath.Clean(root)
	bundlePath := filepath.Join(root, bundleID, manifestSHA256)
	rel, err := filepath.Rel(root, bundlePath)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("%w: bundle identity escapes registry root", ErrBusinessSystemPromptBundleInvalid)
	}
	return bundleID + ":" + manifestSHA256, bundlePath, nil
}

func loadBusinessSystemPromptBundleIdentity(path, expectedBundleID, expectedManifestSHA256 string) (*BusinessSystemPromptBundle, error) {
	bundle, err := LoadBusinessSystemPromptBundle(path)
	if err != nil {
		return nil, err
	}
	if bundle.Manifest.BundleID != strings.TrimSpace(expectedBundleID) {
		return nil, fmt.Errorf("%w: bundle id mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	if !strings.EqualFold(bundle.ManifestSHA256, strings.TrimSpace(expectedManifestSHA256)) {
		return nil, fmt.Errorf("%w: manifest sha256 mismatch", ErrBusinessSystemPromptBundleInvalid)
	}
	return bundle, nil
}
