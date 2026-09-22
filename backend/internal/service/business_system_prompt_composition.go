package service

import (
	"context"
	"errors"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

const (
	BusinessSystemPromptCompositionInline           = "inline"
	BusinessSystemPromptCompositionCodexSkillHybrid = "codex_skill_hybrid"

	BusinessSystemPromptRemoteSkillBundleID = "codexrip-reverse-skill"
)

// BusinessSystemPromptComposition identifies how an immutable template
// version is assembled. Bundle references are content-addressed and never
// contain prompt bodies.
type BusinessSystemPromptComposition = extensionv1.PromptComposition

// NormalizeBusinessSystemPromptComposition validates and canonicalizes a
// version's composition reference before it reaches durable storage.
func NormalizeBusinessSystemPromptComposition(mode, bundleID, manifestSHA256 string) (BusinessSystemPromptComposition, error) {
	input := BusinessSystemPromptComposition{Mode: mode, BundleID: bundleID, BundleManifestSHA256: manifestSHA256}
	var output BusinessSystemPromptComposition
	err := invokePromptManagement(context.Background(), "prompt.composition", input, &output)
	return output, err
}

// NormalizeBusinessSystemPromptCompositionStructure validates only the storage
// shape. Policy ownership and bundle identity remain in the prompt plugin.
func NormalizeBusinessSystemPromptCompositionStructure(mode, bundleID, manifestSHA256 string) (BusinessSystemPromptComposition, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	bundleID = strings.TrimSpace(bundleID)
	manifestSHA256 = strings.ToLower(strings.TrimSpace(manifestSHA256))
	if mode == "" {
		mode = BusinessSystemPromptCompositionInline
	}
	if mode != BusinessSystemPromptCompositionInline && mode != BusinessSystemPromptCompositionCodexSkillHybrid {
		return BusinessSystemPromptComposition{}, errors.New("unsupported composition mode")
	}
	if mode == BusinessSystemPromptCompositionInline && (bundleID != "" || manifestSHA256 != "") {
		return BusinessSystemPromptComposition{}, errors.New("inline composition cannot reference a bundle")
	}
	if mode == BusinessSystemPromptCompositionCodexSkillHybrid && bundleID == "" {
		return BusinessSystemPromptComposition{}, errors.New("bundle composition requires a bundle id")
	}
	if len(bundleID) > 128 {
		return BusinessSystemPromptComposition{}, errors.New("bundle id is too long")
	}
	if manifestSHA256 != "" && !isLowerHexSHA256(manifestSHA256) {
		return BusinessSystemPromptComposition{}, errors.New("bundle manifest digest is invalid")
	}
	for _, char := range bundleID {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			continue
		}
		return BusinessSystemPromptComposition{}, errors.New("bundle id is invalid")
	}
	return BusinessSystemPromptComposition{Mode: mode, BundleID: bundleID, BundleManifestSHA256: manifestSHA256}, nil
}

func isLowerHexSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') {
			continue
		}
		return false
	}
	return true
}
