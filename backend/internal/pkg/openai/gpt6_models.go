package openai

import "strings"

// GPT6CodexReferenceSource pins the subscription client's fallback metadata
// (instructions and client version). Context capacities live in the service
// reference catalog; no reference proves an account's live limits.
const GPT6CodexReferenceCommit = "4b664e0ef0397f82e68c60088a90fcd035deb796"
const GPT6CodexReferenceSource = "https://github.com/openai/codex/blob/" + GPT6CodexReferenceCommit + "/codex-rs/models-manager/models.json"
const GPT6CodexReferenceVerifiedAt = "2026-09-23"
const GPT6CodexMinimumClientVersion = "0.155.0"

// GPT6NamedModel recognizes the two published named models and finite local
// effort/compact aliases. Arbitrary future GPT-6 names and unverified snapshots
// are not aliases for Astra, Sol or Luna.
func GPT6NamedModel(model string) string {
	canonical := CanonicalizeOpenAIModelAliasSpelling(model)
	for _, name := range []string{"gpt-6-sol", "gpt-6-luna"} {
		if canonical == name {
			return name
		}
		if suffix, ok := strings.CutPrefix(canonical, name+"-"); ok {
			switch suffix {
			case "none", "low", "medium", "high", "xhigh", "extrahigh", "max", "openai-compact":
				return name
			}
		}
	}
	return ""
}

// GPT6APIReasoningEfforts contains wire values, not Codex's Ultra workflow.
func GPT6APIReasoningEfforts() []string {
	return []string{"none", "low", "medium", "high", "xhigh", "max"}
}
