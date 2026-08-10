package service

import (
	"fmt"
	"strings"
)

const (
	BusinessSystemPromptCompositionInline           = "inline"
	BusinessSystemPromptCompositionCodexSkillHybrid = "codex_skill_hybrid"

	BusinessSystemPromptSeedBundleID             = "moxinggang-reverse-skill"
	BusinessSystemPromptSeedBundleManifestSHA256 = "22c227128165afbbcbda0175eb5e991ddb51d105b7d1e704572c625c64b626d7"
	BusinessSystemPromptRemoteSkillBundleID      = "codexrip-reverse-skill"
)

// BusinessSystemPromptComposition identifies how an immutable template
// version is assembled. Bundle references are content-addressed and never
// contain prompt bodies.
type BusinessSystemPromptComposition struct {
	Mode                 string `json:"composition_mode"`
	BundleID             string `json:"bundle_id,omitempty"`
	BundleManifestSHA256 string `json:"bundle_manifest_sha256,omitempty"`
}

// NormalizeBusinessSystemPromptComposition validates and canonicalizes a
// version's composition reference before it reaches durable storage.
func NormalizeBusinessSystemPromptComposition(mode, bundleID, manifestSHA256 string) (BusinessSystemPromptComposition, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	bundleID = strings.TrimSpace(bundleID)
	manifestSHA256 = strings.ToLower(strings.TrimSpace(manifestSHA256))
	if mode == "" {
		mode = BusinessSystemPromptCompositionInline
	}

	composition := BusinessSystemPromptComposition{
		Mode:                 mode,
		BundleID:             bundleID,
		BundleManifestSHA256: manifestSHA256,
	}
	switch mode {
	case BusinessSystemPromptCompositionInline:
		if bundleID != "" || manifestSHA256 != "" {
			return BusinessSystemPromptComposition{}, fmt.Errorf("%w: inline composition cannot reference a bundle", ErrBusinessSystemPromptInvalid)
		}
	case BusinessSystemPromptCompositionCodexSkillHybrid:
		if bundleID != BusinessSystemPromptRemoteSkillBundleID {
			return BusinessSystemPromptComposition{}, fmt.Errorf("%w: unknown CodexRip skill bundle", ErrBusinessSystemPromptInvalid)
		}
		if manifestSHA256 != "" {
			return BusinessSystemPromptComposition{}, fmt.Errorf("%w: registry-backed composition follows the published registry and cannot pin a manifest", ErrBusinessSystemPromptInvalid)
		}
	default:
		return BusinessSystemPromptComposition{}, fmt.Errorf("%w: unsupported composition mode %q", ErrBusinessSystemPromptInvalid, mode)
	}
	return composition, nil
}

func isLowerHexSHA256(value string) bool {
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return false
	}
	return true
}
