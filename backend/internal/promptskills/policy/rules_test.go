package policy

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

func ruleSnapshot(role, position string) BusinessSystemPromptSnapshot {
	rule := extensionv1.PromptRule{ID: "rule", Name: "Rule", Enabled: true, TemplateID: 1, VersionID: 2, Role: role, Platforms: []string{"openai"}, Position: position, ModelMatch: "upstream"}
	hash, _, _ := extensionv1.ValidateTextDocument("site", 100)
	return BusinessSystemPromptSnapshot{Enabled: true, Revision: 1, RulePolicy: &extensionv1.PromptRulePolicy{Version: 2, Rules: []extensionv1.PromptRule{rule}, DefaultRuleIDs: []string{"rule"}}, ResolvedRules: []extensionv1.ResolvedPromptRule{{Rule: rule, Body: "site", SHA256: hash}}}
}

func TestPromptRulesV2RolePositionProtocolMatrix(t *testing.T) {
	all, _ := promptProtocolCapability("responses")
	for _, role := range all.Roles {
		for _, position := range all.PositionsByRole[role] {
			for _, capability := range PromptProtocolCapabilities() {
				t.Run(role+"/"+position+"/"+capability.Protocol, func(t *testing.T) {
					snapshot := ruleSnapshot(role, position)
					platform := "openai"
					if capability.Protocol == "messages" {
						platform = "anthropic"
					}
					if capability.Protocol == "gemini" {
						platform = "gemini"
					}
					snapshot.RulePolicy.Rules[0].Platforms = []string{platform}
					snapshot.RulePolicy.Rules[0].Models = []string{"claude-opus-4-8"}
					target := BusinessSystemPromptTarget{Platform: platform, AccountType: "apikey", Protocol: capability.Protocol, UpstreamModel: "claude-opus-4-8"}
					application, err := Plan(snapshot, target, true)
					allowed := false
					for _, p := range capability.PositionsByRole[role] {
						if p == position {
							allowed = true
						}
					}
					if !allowed {
						if !errors.Is(err, ErrPromptDeliveryUnsupported) {
							t.Fatalf("expected unsupported, got %v", err)
						}
						return
					}
					if err != nil || !application.Applied {
						t.Fatalf("expected application, got %+v, %v", application, err)
					}
					placement := application.RulesPlan.Placements[0]
					if placement.Protocol != capability.Protocol {
						t.Fatal("missing final protocol")
					}
					switch capability.Protocol {
					case "responses":
						if role == "auto" && isControlPosition(position) {
							if placement.Carrier != "instructions" || placement.Role != "system" {
								t.Fatal(placement)
							}
						} else if placement.Carrier != "input" || placement.Role == "auto" {
							t.Fatal(placement)
						}
					case "chat":
						if placement.Carrier != "messages" || placement.Role == "auto" {
							t.Fatal(placement)
						}
					case "messages":
						if placement.Role != "system" {
							t.Fatal(placement)
						}
					case "gemini":
						if placement.Carrier != "systemInstruction" || placement.Role != "system" {
							t.Fatal(placement)
						}
					}
				})
			}
		}
	}
}

func TestPromptRulesV2AccountModelProviderAndOAuthScope(t *testing.T) {
	snapshot := ruleSnapshot("system", "conversation_tail")
	target := BusinessSystemPromptTarget{Platform: "openai", AccountType: "oauth", Protocol: "responses"}
	if _, err := Plan(snapshot, target, false); !errors.Is(err, ErrPromptDeliveryUnsupported) {
		t.Fatal(err)
	}
	target.BindingJSON = `{"mode":"off","rule_ids":[]}`
	if result, err := Plan(snapshot, target, false); err != nil || result.Applied {
		t.Fatal("off must bypass incompatible targets", err)
	}
	target.BindingJSON, target.AccountType = "", "apikey"
	snapshot.RulePolicy.Rules[0].Models = []string{"mapped"}
	target.RequestedModel, target.UpstreamModel = "alias", "other"
	if result, err := Plan(snapshot, target, false); err != nil || result.Applied {
		t.Fatal(err)
	}
	target.UpstreamModel = "mapped"
	if result, err := Plan(snapshot, target, false); err != nil || !result.Applied {
		t.Fatal(err)
	}
	target.ProviderPlatform = "cindy"
	if result, err := Plan(snapshot, target, false); err != nil || result.Applied || result.RulesPlan.Skipped[0].Reason != "platform_scope" {
		t.Fatal("wire OpenAI must not erase real Cindy scope", err)
	}
	snapshot.RulePolicy.Rules[0].Platforms = []string{"cindy"}
	snapshot.RulePolicy.Rules[0].AccountTypes = []string{"apikey"}
	snapshot.RulePolicy.Rules[0].RequestProfiles = []string{"generic-mimic"}
	target.RequestProfile = "claude-code"
	if result, err := Plan(snapshot, target, false); err != nil || result.Applied || result.RulesPlan.Skipped[0].Reason != "request_profile_scope" {
		t.Fatal(err)
	}
	target.RequestProfile = "generic-mimic"
	snapshot.RulePolicy.Rules[0].Models = nil
	snapshot.RulePolicy.Rules[0].ExcludeModelContains = []string{"fable"}
	target.UpstreamModel = "provider/Claude-Fable-5"
	if result, err := Plan(snapshot, target, false); err != nil || result.Applied || result.RulesPlan.Skipped[0].Reason != "model_scope" {
		t.Fatal("old Fable family exclusion changed", err)
	}
}

func TestPromptRulesV2DisabledAndCustomSelection(t *testing.T) {
	snapshot := ruleSnapshot("system", "conversation_tail")
	snapshot.Enabled, snapshot.ResolvedRules = false, nil
	application, err := Plan(snapshot, BusinessSystemPromptTarget{Platform: "openai", AccountType: "oauth", Protocol: "responses", BindingJSON: "malformed-old-binding"}, false)
	if err != nil || application.Applied || application.RulesPlan.Skipped[0].Reason != "global_disabled" {
		t.Fatalf("disabled policy affected forwarding: %v", err)
	}
	snapshot = ruleSnapshot("auto", "control_append")
	second := snapshot.RulePolicy.Rules[0]
	second.ID, second.Name = "custom", "Custom"
	snapshot.RulePolicy.Rules = append(snapshot.RulePolicy.Rules, second)
	hash, _, _ := extensionv1.ValidateTextDocument("custom", 100)
	snapshot.ResolvedRules = append(snapshot.ResolvedRules, extensionv1.ResolvedPromptRule{Rule: second, Body: "custom", SHA256: hash})
	application, err = Plan(snapshot, BusinessSystemPromptTarget{Platform: "openai", Protocol: "responses", BindingJSON: `{"mode":"custom","rule_ids":["custom"]}`}, false)
	if err != nil || len(application.RulesPlan.Placements) != 1 || application.RulesPlan.Placements[0].RuleID != "custom" {
		t.Fatal("custom selection must replace defaults", err)
	}
}

func TestPromptRulesV2PublicEchoDoesNotExposePrivateSibling(t *testing.T) {
	snapshot := ruleSnapshot("auto", "control_append")
	snapshot.ResolvedRules[0].PreserveEcho = true
	privateRule := snapshot.RulePolicy.Rules[0]
	privateRule.ID, privateRule.Name = "private", "Private rule"
	snapshot.RulePolicy.Rules = append(snapshot.RulePolicy.Rules, privateRule)
	snapshot.RulePolicy.DefaultRuleIDs = append(snapshot.RulePolicy.DefaultRuleIDs, privateRule.ID)
	hash, _, _ := extensionv1.ValidateTextDocument("private", 100)
	snapshot.ResolvedRules = append(snapshot.ResolvedRules, extensionv1.ResolvedPromptRule{Rule: privateRule, Body: "private", SHA256: hash})
	application, err := Plan(snapshot, BusinessSystemPromptTarget{Platform: "openai", AccountType: "apikey", Protocol: "responses"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if application.PreserveInstructionsEcho {
		t.Fatal("a public rule exposed a private sibling")
	}
	if !application.RulesPlan.Placements[0].PreserveEcho || application.RulesPlan.Placements[1].PreserveEcho {
		t.Fatal("individual echo scopes changed")
	}
}

func TestPromptRulesV2ValidationDraftAndImmutableOrder(t *testing.T) {
	snapshot := ruleSnapshot("auto", "control_append")
	policy := *snapshot.RulePolicy
	rule := policy.Rules[0]
	rule.ID, rule.Name = "alpha", "Alpha"
	policy.Rules = append(policy.Rules, rule)
	normalized, err := ValidateRulePolicy(policy)
	if err != nil || normalized.Rules[0].ID != "rule" {
		t.Fatal("equal order must retain list order", err)
	}
	normalized.Rules[0].Platforms[0] = "gemini"
	if policy.Rules[0].Platforms[0] != "openai" {
		t.Fatal("validation mutated input")
	}
	policy.Rules[0].TemplateID, policy.Rules[0].VersionID = 0, 0
	if _, err := ValidateRulePolicy(policy); err == nil {
		t.Fatal("live policy accepted draft refs")
	}
	if _, err := ValidateRulePolicyDraft(policy, []string{"rule"}); err != nil {
		t.Fatal(err)
	}
	policy.Rules[0].TemplateID, policy.Rules[0].VersionID = 1, 2
	policy.Rules[0].FollowActive = true
	if _, err := ValidateRulePolicy(policy); err == nil {
		t.Fatal("follow_active remained executable")
	}
	policy.Rules[0].FollowActive = false
	policy.Rules[0].Role, policy.Rules[0].Delivery = "", "system"
	if _, err := ValidateRulePolicy(policy); err == nil {
		t.Fatal("delivery silently supplied v2 role")
	}
	policy.Rules[0].Role, policy.Rules[0].Platforms = "auto", nil
	if _, err := ValidateRulePolicy(policy); err == nil {
		t.Fatal("empty scope expanded to all platforms")
	}
	if !reflect.DeepEqual(snapshot.RulePolicy.DefaultRuleIDs, []string{"rule"}) {
		t.Fatal("snapshot selectors changed")
	}
}

func TestPromptRulesV2ClaudeModelCapabilityAndContentIntegrity(t *testing.T) {
	snapshot := ruleSnapshot("system", "after_last_user")
	snapshot.RulePolicy.Rules[0].Platforms = []string{"anthropic"}
	snapshot.RulePolicy.Rules[0].Models = []string{"claude-opus-4-8"}
	target := BusinessSystemPromptTarget{Platform: "anthropic", Protocol: "messages", UpstreamModel: "claude-opus-4-8"}
	if result, err := Plan(snapshot, target, false); err != nil || !result.Applied {
		t.Fatal(err)
	}
	snapshot.RulePolicy.Rules[0].Models = []string{"claude-sonnet-5"}
	if _, err := ValidateRulePolicy(*snapshot.RulePolicy); !errors.Is(err, ErrPromptDeliveryUnsupported) {
		t.Fatal("unsupported Claude model accepted", err)
	}
	snapshot = ruleSnapshot("auto", "control_append")
	snapshot.ResolvedRules[0].Rule.VersionID++
	if _, err := Plan(snapshot, BusinessSystemPromptTarget{Platform: "openai", Protocol: "responses"}, false); !errors.Is(err, ErrBusinessSystemPromptUnavailable) {
		t.Fatal("resolved content was not pinned", err)
	}
	snapshot = ruleSnapshot("auto", "control_append")
	snapshot.ResolvedRules[0].Body = "tampered"
	if _, err := Plan(snapshot, BusinessSystemPromptTarget{Platform: "openai", Protocol: "responses"}, false); !errors.Is(err, ErrBusinessSystemPromptUnavailable) {
		t.Fatal("changed content bypassed digest", err)
	}
	snapshot = ruleSnapshot("auto", "control_append")
	snapshot.RulePolicy.Version = 1
	if _, err := Plan(snapshot, BusinessSystemPromptTarget{Platform: "openai", Protocol: "responses"}, false); err == nil {
		t.Fatal("legacy policy remained executable")
	}
}

func TestPromptRulesV2UnavailableSourceOnlyBlocksSelectedRule(t *testing.T) {
	snapshot := ruleSnapshot("auto", "control_append")
	source := snapshot.RulePolicy.Rules[0]
	source.ID, source.Name = "optional-source", "Optional source"
	source.Models = []string{"source-model"}
	snapshot.RulePolicy.Rules = append(snapshot.RulePolicy.Rules, source)
	snapshot.RulePolicy.DefaultRuleIDs = append(snapshot.RulePolicy.DefaultRuleIDs, source.ID)
	snapshot.ResolvedRules = append(snapshot.ResolvedRules, extensionv1.ResolvedPromptRule{Rule: source, Unavailable: true})
	target := BusinessSystemPromptTarget{Platform: "openai", Protocol: "responses", UpstreamModel: "inline-model"}
	application, err := PlanRules(snapshot, target)
	if err != nil || !application.Applied || len(application.RulesPlan.Placements) != 1 || application.RulesPlan.Placements[0].RuleID != "rule" {
		t.Fatalf("unselected source blocked inline content: %+v, %v", application, err)
	}
	if len(application.RulesPlan.Skipped) != 1 || application.RulesPlan.Skipped[0].Reason != "model_scope" {
		t.Fatalf("unselected source lost its scope decision: %+v", application.RulesPlan.Skipped)
	}
	target.UpstreamModel = "source-model"
	if _, err := PlanRules(snapshot, target); !errors.Is(err, ErrBusinessSystemPromptUnavailable) {
		t.Fatalf("selected unavailable source did not fail explicitly: %v", err)
	}
	target.BindingJSON = `{"mode":"off","rule_ids":[]}`
	if application, err := PlanRules(snapshot, target); err != nil || application.Applied {
		t.Fatalf("off account was blocked by unavailable source: %+v, %v", application, err)
	}
}

func TestPromptRulesV2AntigravityUpstreamMessagesCapabilities(t *testing.T) {
	snapshot := ruleSnapshot("system", "after_last_user")
	rule := &snapshot.RulePolicy.Rules[0]
	rule.Platforms = []string{"antigravity"}
	rule.AccountTypes = []string{"upstream"}
	rule.Models = []string{"claude-opus-4-8"}
	capability, ok := promptProtocolCapability("messages")
	if !ok || !slices.Contains(capability.Platforms, "antigravity") {
		t.Fatal("Antigravity native Messages capability is missing")
	}
	target := BusinessSystemPromptTarget{Platform: "antigravity", AccountType: "upstream", Protocol: "messages", UpstreamModel: "claude-opus-4-8"}
	application, err := PlanRules(snapshot, target)
	if err != nil || !application.Applied || application.RulesPlan.Placements[0].Carrier != "messages" || application.RulesPlan.Placements[0].Role != "system" {
		t.Fatalf("upstream account lost supported Messages placement: %+v, %v", application, err)
	}
	rule.AccountTypes = nil
	if _, err := ValidateRulePolicy(*snapshot.RulePolicy); err != nil {
		t.Fatalf("unspecified account scope cannot select its supported Messages target: %v", err)
	}
	if _, err := PlanRules(snapshot, BusinessSystemPromptTarget{Platform: "antigravity", AccountType: "oauth", Protocol: "gemini", UpstreamModel: "claude-opus-4-8"}); !errors.Is(err, ErrPromptDeliveryUnsupported) {
		t.Fatalf("runtime Gemini target must reject conversation position: %v", err)
	}
	for _, accountTypes := range [][]string{{"oauth"}, {"upstream", "oauth"}} {
		rule.AccountTypes = accountTypes
		if _, err := ValidateRulePolicy(*snapshot.RulePolicy); !errors.Is(err, ErrPromptDeliveryUnsupported) {
			t.Fatalf("explicit non-Messages scope accepted unsupported conversation position %v: %v", accountTypes, err)
		}
	}
	rule.Position = "control_append"
	if _, err := ValidateRulePolicy(*snapshot.RulePolicy); err != nil {
		t.Fatalf("mixed Antigravity types lost their common system carrier: %v", err)
	}
}

func TestPromptRulesV2StrictChatDeveloperRole(t *testing.T) {
	snapshot := ruleSnapshot("developer", "control_append")
	target := BusinessSystemPromptTarget{Platform: "openai", Protocol: "chat", ChatSystemRoleOnly: true}
	if _, err := PlanRules(snapshot, target); !errors.Is(err, ErrPromptDeliveryUnsupported) {
		t.Fatalf("strict Chat destination accepted developer role: %v", err)
	}
	target.Protocol = "responses"
	application, err := PlanRules(snapshot, target)
	if err != nil || !application.Applied || application.RulesPlan.Placements[0].Role != "developer" {
		t.Fatalf("Chat-only restriction affected Responses: %+v, %v", application, err)
	}
	target.Protocol, target.ChatSystemRoleOnly = "chat", false
	application, err = PlanRules(snapshot, target)
	if err != nil || !application.Applied || application.RulesPlan.Placements[0].Role != "developer" {
		t.Fatalf("ordinary Chat developer role was changed: %+v, %v", application, err)
	}
	snapshot.RulePolicy.Rules[0].Role = "auto"
	target.ChatSystemRoleOnly = true
	application, err = PlanRules(snapshot, target)
	if err != nil || !application.Applied || application.RulesPlan.Placements[0].Role != "system" {
		t.Fatalf("strict Chat auto role lost its system carrier: %+v, %v", application, err)
	}
}
