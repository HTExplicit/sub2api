package claude

import (
	"slices"
	"strings"
	"unicode"
)

var (
	effortLowMediumHigh         = []string{"low", "medium", "high"}
	effortLowMediumHighMax      = []string{"low", "medium", "high", "max"}
	effortLowMediumHighXHighMax = []string{"low", "medium", "high", "xhigh", "max"}
)

type effortFamily struct {
	family string
	levels []string
	// adaptive pairs output_config.effort with thinking {type: "adaptive"}.
	// Opus 4.5 thinks only through enabled+budget_tokens; Mythos Preview's
	// thinking mode is unverified.
	adaptive bool
}

var effortFamilies = []effortFamily{
	{family: "claude-mythos-preview", levels: effortLowMediumHighMax},
	{family: "claude-mythos-5", levels: effortLowMediumHighXHighMax, adaptive: true},
	{family: "claude-fable-5", levels: effortLowMediumHighXHighMax, adaptive: true},
	{family: "claude-sonnet-4-6", levels: effortLowMediumHighMax, adaptive: true},
	{family: "claude-sonnet-5-5", levels: effortLowMediumHighXHighMax, adaptive: true},
	{family: "claude-sonnet-5", levels: effortLowMediumHighXHighMax, adaptive: true},
	{family: "claude-opus-4-8", levels: effortLowMediumHighXHighMax, adaptive: true},
	{family: "claude-opus-4-7", levels: effortLowMediumHighXHighMax, adaptive: true},
	{family: "claude-opus-4-6", levels: effortLowMediumHighMax, adaptive: true},
	{family: "claude-opus-4-5", levels: effortLowMediumHigh},
	{family: "claude-opus-5-5", levels: effortLowMediumHighXHighMax, adaptive: true},
	{family: "claude-opus-5", levels: effortLowMediumHighXHighMax, adaptive: true},
}

func findEffortFamily(model string) *effortFamily {
	id := normalizeEffortModelID(model)
	for i := range effortFamilies {
		if entry := &effortFamilies[i]; id == entry.family || strings.HasPrefix(id, entry.family+"-") {
			return entry
		}
	}
	return nil
}

// EffortLevelsForModel returns the output_config.effort values accepted by a
// Claude model, ordered from the lightest to the deepest reasoning level.
func EffortLevelsForModel(model string) []string {
	if entry := findEffortFamily(model); entry != nil {
		return append([]string(nil), entry.levels...)
	}
	return nil
}

// EffortLevelForModel returns the output_config.effort level a Claude model
// accepts for effort: effort itself when the model lists it, otherwise the
// nearest deeper level, otherwise the model's deepest level. It returns ""
// when the model has no effort levels or effort is not an effort level.
func EffortLevelForModel(model, effort string) string {
	entry := findEffortFamily(model)
	rank := slices.Index(effortLowMediumHighXHighMax, effort)
	if entry == nil || rank < 0 {
		return ""
	}
	for _, level := range entry.levels {
		if slices.Index(effortLowMediumHighXHighMax, level) >= rank {
			return level
		}
	}
	return entry.levels[len(entry.levels)-1]
}

// EffortUsesAdaptiveThinking reports whether a Claude model pairs
// output_config.effort with thinking {type: "adaptive"}.
func EffortUsesAdaptiveThinking(model string) bool {
	entry := findEffortFamily(model)
	return entry != nil && entry.adaptive
}

// IsClaudeModel reports whether model is a Claude model ID after
// provider/local suffix normalization, including models without effort levels.
func IsClaudeModel(model string) bool {
	return strings.HasPrefix(normalizeEffortModelID(model), "claude-")
}

// DefaultEffortForModel returns the effort a Claude model applies when
// output_config.effort is omitted, or "" when the model has no effort levels.
func DefaultEffortForModel(model string) string {
	entry := findEffortFamily(model)
	switch {
	case entry == nil:
		return ""
	case entry.family == "claude-opus-5-5":
		// Opus 5.5 defaults one level below every other effort family.
		return "medium"
	default:
		return "high"
	}
}

// IsOpus55 identifies the fixed Opus 5.5 ID after provider/local suffix normalization.
func IsOpus55(model string) bool {
	return normalizeEffortModelID(model) == "claude-opus-5-5"
}

// IsSonnet55 identifies the fixed Sonnet 5.5 ID after provider/local suffix normalization.
func IsSonnet55(model string) bool {
	return normalizeEffortModelID(model) == "claude-sonnet-5-5"
}

func normalizeEffortModelID(model string) string {
	id := strings.ToLower(strings.TrimSpace(model))
	id = strings.TrimPrefix(id, "models/")
	if slash := strings.IndexByte(id, '/'); slash >= 0 {
		id = strings.TrimPrefix(strings.TrimSpace(id[slash+1:]), "models/")
	}
	for _, prefix := range []string{"us.", "eu.", "apac.", "jp.", "au.", "us-gov.", "global."} {
		id = strings.TrimPrefix(id, prefix)
	}
	id = strings.TrimPrefix(id, "anthropic.")
	id = strings.TrimSuffix(id, "-thinking")
	// OpenRouter spells minor versions with a dot (claude-opus-4.7) where
	// Anthropic IDs use a hyphen. Normalize them before effort, thinking, and
	// billing family lookups.
	if strings.Contains(id, ".") {
		b := []byte(id)
		for i := 1; i+1 < len(b); i++ {
			if b[i] == '.' && '0' <= b[i-1] && b[i-1] <= '9' && '0' <= b[i+1] && b[i+1] <= '9' {
				b[i] = '-'
			}
		}
		id = string(b)
	}
	if mapped, ok := ModelIDReverseOverrides[id]; ok {
		id = mapped
	}
	// Dated snapshots: "-YYYYMMDD" (Anthropic API) and "@YYYYMMDD" (Vertex AI).
	if len(id) >= 9 {
		suffix := id[len(id)-9:]
		if suffix[0] == '-' || suffix[0] == '@' {
			digits := true
			for _, r := range suffix[1:] {
				if !unicode.IsDigit(r) {
					digits = false
					break
				}
			}
			if digits {
				id = id[:len(id)-9]
			}
		}
	}
	return id
}
