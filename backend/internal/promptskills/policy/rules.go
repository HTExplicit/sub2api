package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

var ruleIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
var ErrPromptDeliveryUnsupported = errors.New("prompt_delivery_unsupported")

func ValidateRulePolicy(input extensionv1.PromptRulePolicy) (extensionv1.PromptRulePolicy, error) {
	if input.Version != 1 || len(input.Rules) > extensionv1.PromptRulesMaxCount {
		return input, errors.New("invalid prompt rule policy version or rule count")
	}
	if input.Rules == nil {
		input.Rules = []extensionv1.PromptRule{}
	}
	if input.DefaultRuleIDs == nil {
		input.DefaultRuleIDs = []string{}
	}
	seen := map[string]bool{}
	for i := range input.Rules {
		r := &input.Rules[i]
		if r.Models == nil {
			r.Models = []string{}
		}
		if !ruleIDPattern.MatchString(r.ID) || seen[r.ID] || strings.TrimSpace(r.Name) == "" || len(r.Name) > 200 {
			return input, errors.New("invalid or duplicate prompt rule")
		}
		seen[r.ID] = true
		if !r.FollowActive && (r.TemplateID < 1 || r.VersionID < 1) {
			return input, errors.New("rule requires an immutable template version")
		}
		if !slices.Contains([]string{extensionv1.PromptDeliveryNative, extensionv1.PromptDeliverySystem, extensionv1.PromptDeliveryDeveloper}, r.Delivery) {
			return input, errors.New("invalid prompt delivery")
		}
		if !slices.Contains([]string{extensionv1.PromptPositionControlPrepend, extensionv1.PromptPositionControlAppend, extensionv1.PromptPositionConversationHead, extensionv1.PromptPositionConversationTail}, r.Position) {
			return input, errors.New("invalid prompt position")
		}
		if r.Delivery == extensionv1.PromptDeliveryNative && strings.HasPrefix(r.Position, "conversation_") {
			return input, fmt.Errorf("%w: native control has no conversation position", ErrPromptDeliveryUnsupported)
		}
		if r.ModelMatch == "" {
			r.ModelMatch = "upstream"
		}
		if r.ModelMatch != "upstream" && r.ModelMatch != "requested" {
			return input, errors.New("invalid model matching basis")
		}
		if len(r.Models) > 128 {
			return input, errors.New("too many model selectors")
		}
		models := map[string]bool{}
		for _, model := range r.Models {
			if strings.TrimSpace(model) != model || model == "" || len(model) > 256 || models[model] {
				return input, errors.New("invalid model selector")
			}
			models[model] = true
		}
	}
	selected := map[string]bool{}
	for _, id := range input.DefaultRuleIDs {
		if !seen[id] || selected[id] {
			return input, errors.New("invalid default rule reference")
		}
		selected[id] = true
	}
	sort.SliceStable(input.Rules, func(i, j int) bool {
		if input.Rules[i].Order == input.Rules[j].Order {
			return input.Rules[i].ID < input.Rules[j].ID
		}
		return input.Rules[i].Order < input.Rules[j].Order
	})
	return input, nil
}

func ValidateAccountBinding(binding extensionv1.PromptAccountBinding, policy extensionv1.PromptRulePolicy) (extensionv1.PromptAccountBinding, error) {
	if binding.Mode == "" {
		binding.Mode = "inherit"
	}
	if binding.RuleIDs == nil {
		binding.RuleIDs = []string{}
	}
	if binding.Mode != "inherit" && binding.Mode != "off" && binding.Mode != "custom" {
		return binding, errors.New("invalid account prompt mode")
	}
	if binding.Mode != "custom" && len(binding.RuleIDs) > 0 {
		return binding, errors.New("only custom binding can select rules")
	}
	seen := map[string]bool{}
	for _, id := range binding.RuleIDs {
		if seen[id] || !slices.ContainsFunc(policy.Rules, func(rule extensionv1.PromptRule) bool { return rule.ID == id }) {
			return binding, errors.New("unknown or duplicate account prompt rule")
		}
		seen[id] = true
	}
	return binding, nil
}

func planRules(snapshot BusinessSystemPromptSnapshot, target BusinessSystemPromptTarget) (BusinessSystemPromptApplication, error) {
	application := BusinessSystemPromptApplication{Revision: snapshot.Revision, ExposeServerPrompt: snapshot.ExposeServerPrompt, CompactEnabled: snapshot.CompactEnabled, RulesPlan: &extensionv1.PromptRulesPlan{Placements: []extensionv1.PromptRulePlacement{}, Skipped: []extensionv1.PromptRuleDecision{}}}
	policy, err := ValidateRulePolicy(*snapshot.RulePolicy)
	if err != nil {
		return application, err
	}
	scopeReason := ""
	if !snapshot.Enabled {
		scopeReason = "global_disabled"
	} else if target.Platform != PlatformOpenAI {
		scopeReason = "platform_excluded"
	} else if target.Compact && !snapshot.CompactEnabled {
		scopeReason = "compact_disabled"
	}
	if scopeReason != "" {
		for _, rule := range policy.Rules {
			application.RulesPlan.Skipped = append(application.RulesPlan.Skipped, extensionv1.PromptRuleDecision{RuleID: rule.ID, Reason: scopeReason})
		}
		return finishRulesPlan(application, 0), nil
	}
	binding := extensionv1.PromptAccountBinding{Mode: "inherit"}
	if target.BindingJSON != "" && json.Unmarshal([]byte(target.BindingJSON), &binding) != nil {
		return application, errors.New("invalid account prompt binding")
	}
	binding, err = ValidateAccountBinding(binding, policy)
	if err != nil {
		return application, err
	}
	selected := policy.DefaultRuleIDs
	if binding.Mode == "off" {
		selected = nil
	} else if binding.Mode == "custom" {
		selected = binding.RuleIDs
	}
	resolved := map[string]extensionv1.ResolvedPromptRule{}
	for _, rule := range snapshot.ResolvedRules {
		resolved[rule.Rule.ID] = rule
	}
	total := 0
	for _, rule := range policy.Rules {
		reason := ""
		switch {
		case !snapshot.Enabled:
			reason = "global_disabled"
		case target.Platform != PlatformOpenAI:
			reason = "platform_excluded"
		case target.Compact && !snapshot.CompactEnabled:
			reason = "compact_disabled"
		case !slices.Contains(selected, rule.ID):
			reason = "account_scope"
		case !rule.Enabled:
			reason = "rule_disabled"
		}
		model := target.UpstreamModel
		if rule.ModelMatch == "requested" {
			model = target.RequestedModel
		}
		if reason == "" && len(rule.Models) > 0 && !slices.Contains(rule.Models, model) {
			reason = "model_scope"
		}
		if reason != "" {
			application.RulesPlan.Skipped = append(application.RulesPlan.Skipped, extensionv1.PromptRuleDecision{RuleID: rule.ID, Reason: reason})
			continue
		}
		content, ok := resolved[rule.ID]
		if !ok {
			return application, fmt.Errorf("%w: unresolved template version", ErrBusinessSystemPromptUnavailable)
		}
		hash, size, validationErr := extensionv1.ValidateTextDocument(content.Body, extensionv1.PromptRulesMaxBytes)
		if validationErr != nil || hash != content.SHA256 {
			return application, fmt.Errorf("%w: invalid rule content", ErrBusinessSystemPromptUnavailable)
		}
		total += size
		if total > extensionv1.PromptRulesMaxBytes {
			return application, errors.New("compiled prompt rules exceed limit")
		}
		placement := extensionv1.PromptRulePlacement{RuleID: rule.ID, TemplateID: content.Rule.TemplateID, VersionID: content.Rule.VersionID, Delivery: rule.Delivery, Position: rule.Position, Body: strings.TrimSpace(content.Body), SHA256: hash, PreserveEcho: content.PreserveEcho}
		switch target.Protocol {
		case BusinessSystemPromptProtocolResponses:
			if rule.Delivery == extensionv1.PromptDeliveryNative {
				placement.Carrier = "instructions"
			} else {
				placement.Carrier, placement.Role = "input", rule.Delivery
			}
		case BusinessSystemPromptProtocolChat:
			placement.Carrier, placement.Role = "messages", rule.Delivery
			if rule.Delivery == extensionv1.PromptDeliveryNative {
				placement.Role = "system"
			}
		default:
			return application, fmt.Errorf("%w: protocol does not have a supported control carrier", ErrPromptDeliveryUnsupported)
		}
		if (target.AccountType == "oauth" || target.AccountType == "setup-token") && rule.Delivery == extensionv1.PromptDeliverySystem {
			return application, fmt.Errorf("%w: Codex OAuth requires native instructions or developer messages", ErrPromptDeliveryUnsupported)
		}
		application.RulesPlan.Placements = append(application.RulesPlan.Placements, placement)
	}
	application.PreserveInstructionsEcho = application.PreserveInstructionsEcho || snapshot.ExposeServerPrompt
	if len(application.RulesPlan.Placements) > 0 && !application.PreserveInstructionsEcho {
		application.PreserveInstructionsEcho = true
		for _, placement := range application.RulesPlan.Placements {
			if !placement.PreserveEcho {
				application.PreserveInstructionsEcho = false
				break
			}
		}
	}
	return finishRulesPlan(application, total), nil
}

func finishRulesPlan(application BusinessSystemPromptApplication, total int) BusinessSystemPromptApplication {
	raw, _ := json.Marshal(application.RulesPlan.Placements)
	digest := sha256.Sum256(raw)
	application.RulesPlan.SHA256 = hex.EncodeToString(digest[:])
	application.Applied = len(application.RulesPlan.Placements) > 0
	application.SHA256, application.EffectiveSHA256 = application.RulesPlan.SHA256, application.RulesPlan.SHA256
	application.EffectiveByteLength = total
	application.Carrier = "rule_plan"
	return application
}
