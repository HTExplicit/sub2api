package claude

import (
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

// EffortUsesAdaptiveThinking reports whether a Claude model pairs
// output_config.effort with thinking {type: "adaptive"}.
func EffortUsesAdaptiveThinking(model string) bool {
	entry := findEffortFamily(model)
	return entry != nil && entry.adaptive
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
	// OpenRouter uses dotted minor versions for some models. Normalize them
	// before effort, thinking, and billing family lookups.
	if id == "claude-opus-5.5" {
		id = "claude-opus-5-5"
	}
	if id == "claude-sonnet-5.5" {
		id = "claude-sonnet-5-5"
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
