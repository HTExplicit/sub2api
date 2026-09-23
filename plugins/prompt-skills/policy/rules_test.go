package policy

import (
	"errors"
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func ruleSnapshot(delivery, position string) BusinessSystemPromptSnapshot {
	rule := extensionv1.PromptRule{ID: "rule", Name: "Rule", Enabled: true, TemplateID: 1, VersionID: 2, Delivery: delivery, Position: position, ModelMatch: "upstream"}
	hash, _, _ := extensionv1.ValidateTextDocument("site", 100)
	return BusinessSystemPromptSnapshot{Enabled: true, Revision: 1, RulePolicy: &extensionv1.PromptRulePolicy{Version: 1, Rules: []extensionv1.PromptRule{rule}, DefaultRuleIDs: []string{"rule"}}, ResolvedRules: []extensionv1.ResolvedPromptRule{{Rule: rule, Body: "site", SHA256: hash}}}
}

func TestPromptRulesDeliveryPositionMatrix(t *testing.T) {
	for _, delivery := range []string{"native_control", "system", "developer"} {
		for _, position := range []string{"control_prepend", "control_append", "conversation_head", "conversation_tail"} {
			for _, protocol := range []string{"responses", "chat"} {
				t.Run(delivery+"/"+position+"/"+protocol, func(t *testing.T) {
					application, err := Plan(ruleSnapshot(delivery, position), BusinessSystemPromptTarget{Platform: "openai", AccountType: "apikey", Protocol: protocol}, true)
					if delivery == "native_control" && (position == "conversation_head" || position == "conversation_tail") {
						if !errors.Is(err, ErrPromptDeliveryUnsupported) {
							t.Fatalf("expected explicit unsupported error, got %v", err)
						}
						return
					}
					if err != nil || !application.Applied {
						t.Fatalf("expected plan: %+v, %v", application, err)
					}
					placement := application.RulesPlan.Placements[0]
					if protocol == "chat" && (placement.Carrier != "messages" || placement.Role == "native_control") {
						t.Fatal("Chat must use a real role even if instructions exists")
					}
				})
			}
		}
	}
}

func TestPromptRulesAccountModelAndOAuthScope(t *testing.T) {
	snapshot := ruleSnapshot("system", "conversation_tail")
	target := BusinessSystemPromptTarget{Platform: "openai", AccountType: "oauth", Protocol: "responses"}
	if _, err := Plan(snapshot, target, false); !errors.Is(err, ErrPromptDeliveryUnsupported) {
		t.Fatal(err)
	}
	target.BindingJSON = `{"mode":"off","rule_ids":[]}`
	if result, err := Plan(snapshot, target, false); err != nil || result.Applied {
		t.Fatal("off account must bypass incompatible rules", err)
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
}

func TestPromptRulesDisabledDoesNotResolveInvalidAccountOrContent(t *testing.T) {
	snapshot := ruleSnapshot("system", "conversation_tail")
	snapshot.Enabled, snapshot.ResolvedRules = false, nil
	application, err := Plan(snapshot, BusinessSystemPromptTarget{Platform: "openai", AccountType: "oauth", Protocol: "responses", BindingJSON: "malformed-old-binding"}, false)
	if err != nil || application.Applied {
		t.Fatalf("disabled policy must not affect forwarding: %v", err)
	}
	if application.RulesPlan.Skipped[0].Reason != "global_disabled" {
		t.Fatal("missing explicit skip reason")
	}
}

func TestPromptRulesPublicEchoDoesNotExposePrivateSibling(t *testing.T) {
	snapshot := ruleSnapshot("native_control", "control_append")
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
		t.Fatal("one public skill must not expose a private sibling rule")
	}
	public := 0
	for _, placement := range application.RulesPlan.Placements {
		if placement.PreserveEcho {
			public++
		}
	}
	if public != 1 {
		t.Fatalf("expected one individually public rule, got %d", public)
	}
}
