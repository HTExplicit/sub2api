package service

import (
	"regexp"
	"strings"
)

const capabilityUnclassifiedGroup = "待确认名称"

// These patterns recognize a concrete model name without upgrading its version
// or trimming unknown suffixes. Editorial recommendations remain a separate
// list. An unknown product or suffix is kept verbatim for name confirmation.
var capabilityFamilyPatterns = []struct {
	group   string
	pattern *regexp.Regexp
}{
	{"gpt", regexp.MustCompile(`(?i)^gpt-(?:[0-9]+(?:\.[0-9]+)*|[0-9]+o)(?:-(?:astra|sol|terra|luna|mini|nano|pro|turbo|codex|codex-spark|codex-mini|chat-latest|instant|thinking))?(?:-[0-9]{4}-[0-9]{2}-[0-9]{2})?$`)},
	{"claude(非逆向渠道)", regexp.MustCompile(`(?i)^claude-(?:(?:fable|opus|sonnet|haiku)-[0-9]+(?:[.-][0-9]+)*|[0-9]+(?:-[0-9]+)?-(?:opus|sonnet|haiku)(?:-[0-9]{8})?)$`)},
	{"gemini", regexp.MustCompile(`(?i)^gemini-[0-9]+(?:\.[0-9]+)*-(?:pro|flash|flash-lite|ultra|nano)(?:-(?:preview|latest|experimental|exp))?(?:-[0-9]{2}-[0-9]{2})?$`)},
	{"grok", regexp.MustCompile(`(?i)^grok-[0-9]+(?:[.-][0-9]+)*(?:-(?:fast|fast-reasoning|fast-non-reasoning|beta|mini|reasoning))?$`)},
	{"kimi", regexp.MustCompile(`(?i)^kimi-(?:k[0-9]+(?:\.[0-9]+)*(?:-(?:thinking|code|instruct|preview))?|for-coding)$`)},
	{"glm", regexp.MustCompile(`(?i)^glm-[0-9]+(?:\.[0-9]+)*(?:-(?:air|airx|flash|flashx|plus|coder|long))?$`)},
	{"deepseek", regexp.MustCompile(`(?i)^deepseek-(?:v[0-9]+(?:\.[0-9]+)*(?:-(?:pro|flash|chat|reasoner))?|r[0-9]+(?:\.[0-9]+)*|chat|reasoner|coder)(?:-[0-9]{4}(?:-[0-9]{2}-[0-9]{2})?)?$`)},
	{"Qwen", regexp.MustCompile(`(?i)^qwen[0-9]+(?:\.[0-9]+)*(?:-(?:max|plus|turbo|flash|coder|coder-plus|coder-next|coder-flash|[0-9]+b(?:-a[0-9]+b)?))?(?:-(?:thinking|instruct))?(?:-[0-9]{4}(?:-[0-9]{2}-[0-9]{2})?)?$`)},
	{"MiniMax", regexp.MustCompile(`(?i)^minimax-(?:m[0-9]+(?:\.[0-9]+)*(?:-(?:highspeed|lightning))?|text-01)$`)},
}

func capabilityResolveRecognizedFamily(name string) (capabilityModelDefinition, bool) {
	for _, family := range capabilityFamilyPatterns {
		if family.pattern.MatchString(name) {
			return capabilityModelDefinition{ID: name, Group: family.group}, true
		}
	}
	return capabilityModelDefinition{}, false
}

func capabilityModelGroup(model string) string {
	tail := strings.ToLower(model[strings.LastIndex(model, "/")+1:])
	for _, family := range []struct{ prefix, group string }{
		{"gpt-", "gpt"}, {"claude-", "claude(非逆向渠道)"}, {"gemini-", "gemini"},
		{"grok-4.6", "grok(仅4.6)"}, {"grok-", "grok"}, {"kimi-", "kimi"},
		{"glm-", "glm"}, {"deepseek-", "deepseek"}, {"qwen", "Qwen"}, {"minimax-", "MiniMax"},
	} {
		if strings.HasPrefix(tail, family.prefix) {
			return family.group
		}
	}
	return capabilityUnclassifiedGroup
}

func capabilityIsRecommendedModel(model string) bool {
	for _, definition := range capabilityLaunchModels {
		if strings.EqualFold(definition.ID, model) {
			return true
		}
	}
	return false
}

type capabilityPublishedModelHint struct {
	Group       *Group
	PublicModel string
	Aliases     []string
}

type capabilityCandidateModel struct {
	capabilityModelDefinition
	Tier        string
	Recognized  bool
	Recommended bool
}

func capabilityCandidateModels(account *Account, upstream string, published []capabilityPublishedModelHint) []capabilityCandidateModel {
	definition, tier, recognized := resolveCapabilityAccountModel(account, upstream)
	if !recognized {
		definition = capabilityModelDefinition{ID: upstream, Group: capabilityModelGroup(upstream)}
	}
	if definition.Group == "gpt" && tier != "standard" {
		definition.Group = "gpt-vip"
	}
	models := []capabilityCandidateModel{{capabilityModelDefinition: definition, Tier: tier, Recognized: recognized, Recommended: recognized && capabilityIsRecommendedModel(definition.ID)}}
	seen := map[string]bool{definition.Group + "\x00" + definition.ID: true}
	for _, hint := range published {
		if hint.Group == nil || hint.PublicModel == "" || IsManagedModelSelector(hint.PublicModel) {
			continue
		}
		key := hint.Group.Name + "\x00" + hint.PublicModel
		if seen[key] {
			continue
		}
		seen[key] = true
		publicationTier := "standard"
		if hint.Group.Name == "gpt-vip" {
			publicationTier = "vip"
		}
		nameConfirmed := recognized && CapabilityCandidateMatches(account, upstream, hint.PublicModel, hint.Aliases, publicationTier)
		models = append(models, capabilityCandidateModel{
			capabilityModelDefinition: capabilityModelDefinition{ID: hint.PublicModel, Group: hint.Group.Name, Aliases: append([]string(nil), hint.Aliases...)},
			Tier:                      tier, Recognized: nameConfirmed, Recommended: nameConfirmed && capabilityIsRecommendedModel(hint.PublicModel),
		})
	}
	return models
}

func capabilityCandidateProtocols(account *Account, upstream string, evidence []AccountCapabilityItem) []string {
	protocols := capabilityAccountProtocols(account)
	seen := make(map[string]bool, len(protocols))
	for _, protocol := range protocols {
		seen[protocol] = true
	}
	// Historical observations stay visible even when a protocol flag changes.
	// Compatibility is checked separately before offering a new probe or route.
	for _, item := range evidence {
		if item.UpstreamModel == upstream && capabilityIsBasicTextEvidence(item) && !seen[item.Protocol] {
			protocols = append(protocols, item.Protocol)
			seen[item.Protocol] = true
		}
	}
	if len(protocols) == 0 {
		// Unsupported account platforms still contribute directory/configuration
		// clues to the overview; no callable protocol is invented for them.
		return []string{""}
	}
	return protocols
}
