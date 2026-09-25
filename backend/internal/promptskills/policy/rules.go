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

	"github.com/Wei-Shaw/sub2api/internal/domain"
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

var ruleIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
var ErrPromptDeliveryUnsupported = errors.New("prompt_delivery_unsupported")

// PromptPlatforms describes the actual selected provider, not its wire protocol.
func PromptPlatforms() []string {
	return []string{domain.PlatformOpenAI, domain.PlatformCindy, domain.PlatformAnthropic, domain.PlatformGemini, domain.PlatformAntigravity, domain.PlatformGrok, domain.PlatformKimi, domain.PlatformZhipu, domain.PlatformDeepseek, domain.PlatformMiniMax, domain.PlatformOpenCodeGo}
}

func PromptProtocolCapabilities() []extensionv1.PromptProtocolCapability {
	all := []string{extensionv1.PromptPositionControlPrepend, extensionv1.PromptPositionControlAppend, extensionv1.PromptPositionConversationHead, extensionv1.PromptPositionConversationTail, extensionv1.PromptPositionBeforeLastUser, extensionv1.PromptPositionAfterLastUser}
	control := slices.Clone(all[:2])
	claude := append(slices.Clone(control), extensionv1.PromptPositionConversationTail, extensionv1.PromptPositionAfterLastUser)
	compatible := []string{domain.PlatformOpenAI, domain.PlatformCindy, domain.PlatformGrok, domain.PlatformKimi, domain.PlatformZhipu, domain.PlatformDeepseek, domain.PlatformMiniMax, domain.PlatformOpenCodeGo}
	roles := []string{extensionv1.PromptRoleAuto, extensionv1.PromptRoleSystem, extensionv1.PromptRoleDeveloper}
	positions := func(allowedRoles, allowedPositions []string) map[string][]string {
		result := make(map[string][]string, len(allowedRoles))
		for _, role := range allowedRoles {
			result[role] = slices.Clone(allowedPositions)
		}
		return result
	}
	return []extensionv1.PromptProtocolCapability{
		{Protocol: "responses", Platforms: slices.Clone(compatible), Roles: slices.Clone(roles), PositionsByRole: positions(roles, all), Limitations: []string{"codex_oauth_system_unsupported", "message_positions_visible_request_only", "missing_last_user_skips_rule", "unsafe_tool_boundary_skips_rule"}},
		{Protocol: "chat", Platforms: slices.Clone(compatible), Roles: slices.Clone(roles), PositionsByRole: positions(roles, all), Limitations: []string{"message_positions_visible_request_only", "missing_last_user_skips_rule", "unsafe_tool_boundary_skips_rule"}},
		{Protocol: "messages", Platforms: []string{domain.PlatformAnthropic, domain.PlatformAntigravity, domain.PlatformKimi, domain.PlatformZhipu, domain.PlatformDeepseek, domain.PlatformMiniMax, domain.PlatformOpenCodeGo}, Roles: slices.Clone(roles[:2]), PositionsByRole: positions(roles[:2], claude), Limitations: []string{"conversation_system_requires_supported_upstream_model", "conversation_system_requires_user_then_assistant_or_end", "antigravity_messages_requires_upstream_account", "missing_last_user_skips_rule", "unsafe_tool_boundary_skips_rule"}, ConversationSystemModels: []string{"claude-fable-5-1", "claude-mythos-5-1", "claude-fable-5", "claude-mythos-5", "claude-opus-5-5", "claude-opus-4-8", "claude-opus-5"}},
		{Protocol: "gemini", Platforms: []string{domain.PlatformGemini, domain.PlatformAntigravity}, Roles: slices.Clone(roles[:2]), PositionsByRole: positions(roles[:2], control), Limitations: []string{"dedicated_system_instruction_only"}},
	}
}

func promptProtocolCapability(protocol string) (extensionv1.PromptProtocolCapability, bool) {
	for _, capability := range PromptProtocolCapabilities() {
		if capability.Protocol == protocol {
			return capability, true
		}
	}
	return extensionv1.PromptProtocolCapability{}, false
}

func isControlPosition(position string) bool {
	return position == extensionv1.PromptPositionControlPrepend || position == extensionv1.PromptPositionControlAppend
}

// ClaudeConversationSystemSupported deliberately recognizes only published model
// IDs. Unknown aliases must first be resolved to the actual upstream model.
func ClaudeConversationSystemSupported(model string) bool {
	capability, _ := promptProtocolCapability("messages")
	return slices.Contains(capability.ConversationSystemModels, model)
}

func ValidateRulePolicy(input extensionv1.PromptRulePolicy) (extensionv1.PromptRulePolicy, error) {
	return validateRulePolicy(input, nil)
}

// ValidateRulePolicyDraft permits only named, unsaved content references. It
// performs the same capability and scope checks before a transaction allocates IDs.
func ValidateRulePolicyDraft(input extensionv1.PromptRulePolicy, draftRuleIDs []string) (extensionv1.PromptRulePolicy, error) {
	return validateRulePolicy(input, draftRuleIDs)
}

func validateRulePolicy(input extensionv1.PromptRulePolicy, draftRuleIDs []string) (extensionv1.PromptRulePolicy, error) {
	if input.Version != extensionv1.PromptRulePolicyVersion || len(input.Rules) > extensionv1.PromptRulesMaxCount {
		return input, errors.New("invalid prompt rule policy version or rule count")
	}
	// Normalization must not mutate an immutable request snapshot.
	input.Rules = append([]extensionv1.PromptRule{}, input.Rules...)
	input.DefaultRuleIDs = append([]string{}, input.DefaultRuleIDs...)
	seen := map[string]bool{}
	for i := range input.Rules {
		r := &input.Rules[i]
		r.Models = append([]string{}, r.Models...)
		r.Platforms = slices.Clone(r.Platforms)
		r.AccountTypes = slices.Clone(r.AccountTypes)
		r.ExcludeModelContains = slices.Clone(r.ExcludeModelContains)
		r.RequestProfiles = slices.Clone(r.RequestProfiles)
		if !ruleIDPattern.MatchString(r.ID) || seen[r.ID] || strings.TrimSpace(r.Name) == "" || len(r.Name) > 200 {
			return input, errors.New("invalid or duplicate prompt rule")
		}
		seen[r.ID] = true
		if r.FollowActive {
			return input, errors.New("v2 rules cannot follow the global active template")
		}
		if r.TemplateID < 1 || r.VersionID < 1 {
			if !slices.Contains(draftRuleIDs, r.ID) || r.TemplateID != 0 || r.VersionID != 0 {
				return input, errors.New("rule requires an immutable template version")
			}
		}
		r.Delivery = ""
		if !slices.Contains([]string{extensionv1.PromptRoleAuto, extensionv1.PromptRoleSystem, extensionv1.PromptRoleDeveloper}, r.Role) {
			return input, errors.New("invalid prompt role")
		}
		generic, _ := promptProtocolCapability("responses")
		if !slices.Contains(generic.PositionsByRole[r.Role], r.Position) {
			return input, errors.New("invalid prompt position")
		}
		if len(r.Platforms) == 0 || validatePromptSelectors(r.Platforms, 32) != nil {
			return input, errors.New("rule requires explicit provider platforms")
		}
		for _, platform := range r.Platforms {
			if !slices.Contains(PromptPlatforms(), platform) {
				return input, errors.New("unsupported prompt provider platform")
			}
		}
		if validatePromptSelectors(r.AccountTypes, 16) != nil || validatePromptSelectors(r.RequestProfiles, 16) != nil || validatePromptSelectors(r.ExcludeModelContains, 128) != nil {
			return input, errors.New("invalid prompt rule scope")
		}
		for _, accountType := range r.AccountTypes {
			if !slices.Contains([]string{domain.AccountTypeOAuth, domain.AccountTypeSetupToken, domain.AccountTypeAPIKey, domain.AccountTypeUpstream, domain.AccountTypeBedrock, domain.AccountTypeServiceAccount}, accountType) {
				return input, errors.New("unsupported prompt account type")
			}
		}
		if r.Role == extensionv1.PromptRoleSystem && (slices.Contains(r.Platforms, domain.PlatformOpenAI) || slices.Contains(r.Platforms, domain.PlatformCindy)) && (slices.Contains(r.AccountTypes, domain.AccountTypeOAuth) || slices.Contains(r.AccountTypes, domain.AccountTypeSetupToken)) {
			return input, fmt.Errorf("%w: explicit Codex OAuth scope cannot use system role", ErrPromptDeliveryUnsupported)
		}
		for _, profile := range r.RequestProfiles {
			if profile != "generic-mimic" && profile != "claude-code" {
				return input, errors.New("unsupported request profile")
			}
		}
		if r.ModelMatch == "" {
			r.ModelMatch = "upstream"
		}
		if r.ModelMatch != "upstream" && r.ModelMatch != "requested" {
			return input, errors.New("invalid model matching basis")
		}
		if err := validatePromptSelectors(r.Models, 128); err != nil {
			return input, err
		}
		// These platforms have a dedicated system carrier; the other providers
		// select a final protocol dynamically and are checked again at dispatch.
		for _, platform := range r.Platforms {
			protocol := ""
			switch platform {
			case domain.PlatformAnthropic:
				protocol = "messages"
			case domain.PlatformGemini:
				protocol = "gemini"
			case domain.PlatformAntigravity:
				protocol = "gemini"
				// Antigravity passthrough accounts send native Messages. A
				// broad account scope may select this supported destination;
				// runtime planning still rejects incompatible OAuth targets.
				// Explicit mixed types must satisfy both wire protocols.
				if (len(r.AccountTypes) == 1 && r.AccountTypes[0] == domain.AccountTypeUpstream) || (len(r.AccountTypes) == 0 && !isControlPosition(r.Position)) {
					protocol = "messages"
				}
			}
			if protocol == "" {
				continue
			}
			capability, _ := promptProtocolCapability(protocol)
			if !slices.Contains(capability.PositionsByRole[r.Role], r.Position) {
				return input, fmt.Errorf("%w: role or position is not supported by %s", ErrPromptDeliveryUnsupported, protocol)
			}
			if protocol == "messages" && !isControlPosition(r.Position) {
				if r.ModelMatch != "upstream" || len(r.Models) == 0 {
					return input, fmt.Errorf("%w: conversation system messages require explicit supported upstream models", ErrPromptDeliveryUnsupported)
				}
				for _, model := range r.Models {
					if !ClaudeConversationSystemSupported(model) {
						return input, fmt.Errorf("%w: model does not support conversation system messages", ErrPromptDeliveryUnsupported)
					}
				}
			}
		}
	}
	selected := map[string]bool{}
	for _, id := range input.DefaultRuleIDs {
		if !seen[id] || selected[id] {
			return input, errors.New("invalid default rule reference")
		}
		selected[id] = true
	}
	for _, id := range draftRuleIDs {
		if !seen[id] {
			return input, errors.New("draft content references an unknown rule")
		}
	}
	// Equal order preserves the visible list order rather than sorting IDs.
	sort.SliceStable(input.Rules, func(i, j int) bool { return input.Rules[i].Order < input.Rules[j].Order })
	return input, nil
}

func validatePromptSelectors(values []string, limit int) error {
	if len(values) > limit {
		return errors.New("too many prompt selectors")
	}
	seen := map[string]bool{}
	for _, value := range values {
		if strings.TrimSpace(value) != value || value == "" || len(value) > 256 || seen[value] {
			return errors.New("invalid or duplicate prompt selector")
		}
		seen[value] = true
	}
	return nil
}

func ValidateAccountBinding(binding extensionv1.PromptAccountBinding, policy extensionv1.PromptRulePolicy) (extensionv1.PromptAccountBinding, error) {
	if binding.Mode == "" {
		binding.Mode = "inherit"
	}
	binding.RuleIDs = append([]string{}, binding.RuleIDs...)
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

// PlanRules is the only executable prompt policy. Legacy data must be migrated
// to a pinned v2 rule before it can participate in forwarding.
func PlanRules(snapshot BusinessSystemPromptSnapshot, target BusinessSystemPromptTarget) (BusinessSystemPromptApplication, error) {
	application := BusinessSystemPromptApplication{Revision: snapshot.Revision, ExposeServerPrompt: snapshot.ExposeServerPrompt, CompactEnabled: snapshot.CompactEnabled, RulesPlan: &extensionv1.PromptRulesPlan{Placements: []extensionv1.PromptRulePlacement{}, Skipped: []extensionv1.PromptRuleDecision{}}}
	if snapshot.RulePolicy == nil {
		if !snapshot.Enabled {
			return FinishRulesPlan(application), nil
		}
		return application, fmt.Errorf("%w: v2 prompt policy is required", ErrBusinessSystemPromptUnavailable)
	}
	var draftRuleIDs []string
	if snapshot.Draft {
		for _, content := range snapshot.ResolvedRules {
			if content.Rule.TemplateID == 0 && content.Rule.VersionID == 0 {
				draftRuleIDs = append(draftRuleIDs, content.Rule.ID)
			}
		}
	}
	policy, err := validateRulePolicy(*snapshot.RulePolicy, draftRuleIDs)
	if err != nil {
		return application, err
	}
	scopeReason := ""
	if !snapshot.Enabled {
		scopeReason = "global_disabled"
	} else if target.Compact && !snapshot.CompactEnabled {
		scopeReason = "compact_disabled"
	}
	if scopeReason != "" {
		for _, rule := range policy.Rules {
			application.RulesPlan.Skipped = append(application.RulesPlan.Skipped, extensionv1.PromptRuleDecision{RuleID: rule.ID, Reason: scopeReason})
		}
		return FinishRulesPlan(application), nil
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
	switch binding.Mode {
	case "off":
		selected = nil
	case "custom":
		selected = binding.RuleIDs
	}
	resolved := map[string]extensionv1.ResolvedPromptRule{}
	for _, rule := range snapshot.ResolvedRules {
		if _, duplicate := resolved[rule.Rule.ID]; duplicate {
			return application, fmt.Errorf("%w: duplicate resolved rule", ErrBusinessSystemPromptUnavailable)
		}
		resolved[rule.Rule.ID] = rule
	}
	platform := target.ProviderPlatform
	if platform == "" {
		platform = target.Platform
	}
	total := 0
	for _, rule := range policy.Rules {
		reason := ""
		switch {
		case !slices.Contains(selected, rule.ID):
			reason = "account_scope"
		case !rule.Enabled:
			reason = "rule_disabled"
		case !slices.Contains(rule.Platforms, platform):
			reason = "platform_scope"
		case len(rule.AccountTypes) > 0 && !slices.Contains(rule.AccountTypes, target.AccountType):
			reason = "account_type_scope"
		case len(rule.RequestProfiles) > 0 && !slices.Contains(rule.RequestProfiles, target.RequestProfile):
			reason = "request_profile_scope"
		}
		model := target.UpstreamModel
		if rule.ModelMatch == "requested" {
			model = target.RequestedModel
		}
		if reason == "" && len(rule.Models) > 0 && !slices.Contains(rule.Models, model) {
			reason = "model_scope"
		}
		if reason == "" && slices.ContainsFunc(rule.ExcludeModelContains, func(part string) bool {
			return strings.Contains(strings.ToLower(target.UpstreamModel), strings.ToLower(part))
		}) {
			reason = "model_scope"
		}
		if reason != "" {
			application.RulesPlan.Skipped = append(application.RulesPlan.Skipped, extensionv1.PromptRuleDecision{RuleID: rule.ID, Reason: reason})
			continue
		}
		placement, err := planPromptPlacement(rule, target, platform)
		if err != nil {
			return application, err
		}
		content, ok := resolved[rule.ID]
		if !ok || content.Rule.TemplateID != rule.TemplateID || content.Rule.VersionID != rule.VersionID {
			return application, fmt.Errorf("%w: unresolved immutable template version", ErrBusinessSystemPromptUnavailable)
		}
		if content.Unavailable {
			return application, fmt.Errorf("%w: selected prompt source is unavailable", ErrBusinessSystemPromptUnavailable)
		}
		hash, size, validationErr := extensionv1.ValidateTextDocument(content.Body, extensionv1.PromptRulesMaxBytes)
		if validationErr != nil || hash != content.SHA256 {
			return application, fmt.Errorf("%w: invalid rule content", ErrBusinessSystemPromptUnavailable)
		}
		total += size
		if total > extensionv1.PromptRulesMaxBytes {
			return application, errors.New("compiled prompt rules exceed limit")
		}
		placement.Body, placement.SHA256, placement.PreserveEcho = content.Body, hash, content.PreserveEcho
		placement.ContentFormat = content.ContentFormat
		if content.ContentFormat != "" && content.ContentFormat != "text" {
			if content.ContentFormat != extensionv1.PromptContentAnthropicSystemBlocks || placement.Carrier != "system" || target.Protocol != "messages" {
				return application, fmt.Errorf("%w: structured content requires the Claude system carrier", ErrPromptDeliveryUnsupported)
			}
			if !json.Valid(content.StructuredContent) || !jsonEqualPromptContent(content.Body, content.StructuredContent) {
				return application, fmt.Errorf("%w: structured content digest mismatch", ErrBusinessSystemPromptUnavailable)
			}
			placement.StructuredContent = slices.Clone(content.StructuredContent)
		}
		application.RulesPlan.Placements = append(application.RulesPlan.Placements, placement)
	}
	return FinishRulesPlan(application), nil
}

func jsonEqualPromptContent(body string, structured json.RawMessage) bool {
	var first, second any
	if json.Unmarshal([]byte(body), &first) != nil || json.Unmarshal(structured, &second) != nil {
		return false
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	return string(a) == string(b)
}

func planPromptPlacement(rule extensionv1.PromptRule, target BusinessSystemPromptTarget, platform string) (extensionv1.PromptRulePlacement, error) {
	placement := extensionv1.PromptRulePlacement{RuleID: rule.ID, TemplateID: rule.TemplateID, VersionID: rule.VersionID, Position: rule.Position, Protocol: target.Protocol, Role: rule.Role}
	capability, ok := promptProtocolCapability(target.Protocol)
	if !ok || !slices.Contains(capability.PositionsByRole[rule.Role], rule.Position) {
		return placement, fmt.Errorf("%w: protocol cannot express role or position", ErrPromptDeliveryUnsupported)
	}
	switch target.Protocol {
	case "responses":
		if rule.Role == extensionv1.PromptRoleAuto && isControlPosition(rule.Position) {
			placement.Carrier, placement.Role = "instructions", "system"
		} else {
			placement.Carrier = "input"
			if rule.Role == extensionv1.PromptRoleAuto {
				placement.Role = "developer"
			}
		}
	case "chat":
		if target.ChatSystemRoleOnly && rule.Role == extensionv1.PromptRoleDeveloper {
			return placement, fmt.Errorf("%w: this Chat destination requires system messages", ErrPromptDeliveryUnsupported)
		}
		placement.Carrier = "messages"
		if rule.Role == extensionv1.PromptRoleAuto {
			placement.Role = "system"
		}
	case "messages":
		placement.Role, placement.Carrier = "system", "system"
		if !isControlPosition(rule.Position) {
			if !ClaudeConversationSystemSupported(target.UpstreamModel) {
				return placement, fmt.Errorf("%w: upstream model cannot accept conversation system messages", ErrPromptDeliveryUnsupported)
			}
			placement.Carrier = "messages"
		}
	case "gemini":
		placement.Role, placement.Carrier = "system", "systemInstruction"
	}
	if (platform == domain.PlatformOpenAI || platform == domain.PlatformCindy) && (target.AccountType == domain.AccountTypeOAuth || target.AccountType == domain.AccountTypeSetupToken) && rule.Role == extensionv1.PromptRoleSystem {
		return placement, fmt.Errorf("%w: Codex OAuth requires auto or developer role", ErrPromptDeliveryUnsupported)
	}
	return placement, nil
}

// FinishRulesPlan is called again after anchor validation, so cache identities
// and echo behavior describe only insertions that actually reached the wire.
func FinishRulesPlan(application BusinessSystemPromptApplication) BusinessSystemPromptApplication {
	if application.RulesPlan == nil {
		return application
	}
	application.EffectiveByteLength = 0
	application.PreserveInstructionsEcho = application.ExposeServerPrompt
	if len(application.RulesPlan.Placements) > 0 && !application.PreserveInstructionsEcho {
		application.PreserveInstructionsEcho = true
		for _, placement := range application.RulesPlan.Placements {
			if !placement.PreserveEcho {
				application.PreserveInstructionsEcho = false
				break
			}
		}
	}
	for _, placement := range application.RulesPlan.Placements {
		application.EffectiveByteLength += len(placement.Body)
	}
	// Runtime indices intentionally do not alter the semantic prompt identity.
	placements := slices.Clone(application.RulesPlan.Placements)
	for i := range placements {
		placements[i].Index, placements[i].BlockIndex = nil, nil
	}
	raw, _ := json.Marshal(placements)
	digest := sha256.Sum256(raw)
	application.RulesPlan.SHA256 = hex.EncodeToString(digest[:])
	application.Applied = len(placements) > 0
	application.SHA256, application.EffectiveSHA256 = application.RulesPlan.SHA256, application.RulesPlan.SHA256
	application.Carrier = "rule_plan"
	return application
}
