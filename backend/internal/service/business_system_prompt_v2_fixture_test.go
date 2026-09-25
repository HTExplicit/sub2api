package service

import (
	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

// Old storage fixtures still describe an immutable version. Gateway tests make
// its explicit migrated rule before exercising the v2-only send lifecycle.
func promptRulesSnapshotForTest(snapshot BusinessSystemPromptSnapshot) BusinessSystemPromptSnapshot {
	if snapshot.RulePolicy != nil {
		return snapshot
	}
	if snapshot.TemplateID == 0 {
		snapshot.TemplateID = 1
	}
	if snapshot.VersionID == 0 {
		snapshot.VersionID = 1
	}
	rule := extensionv1.PromptRule{ID: "fixture", Name: "Fixture prompt", Enabled: true,
		TemplateID: snapshot.TemplateID, VersionID: snapshot.VersionID,
		Role: extensionv1.PromptRoleAuto, Position: extensionv1.PromptPositionControlAppend,
		Platforms: []string{PlatformOpenAI}, ModelMatch: "upstream", Models: []string{}}
	snapshot.RulePolicy = &extensionv1.PromptRulePolicy{Version: 2, Rules: []extensionv1.PromptRule{rule}, DefaultRuleIDs: []string{rule.ID}}
	if snapshot.Body != "" {
		hash, _, _ := extensionv1.ValidateTextDocument(snapshot.Body, extensionv1.PromptRulesMaxBytes)
		snapshot.ResolvedRules = []extensionv1.ResolvedPromptRule{{Rule: rule, Body: snapshot.Body, SHA256: hash,
			PreserveEcho: snapshot.CompositionMode == BusinessSystemPromptCompositionCodexSkillHybrid}}
	}
	return snapshot
}
