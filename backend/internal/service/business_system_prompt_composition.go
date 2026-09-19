package service

import (
	"context"

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
