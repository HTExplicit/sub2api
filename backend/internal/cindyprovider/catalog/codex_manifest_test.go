package catalog

import "testing"

func TestCodexPresentationOwnsRulesAndClonesEachRead(t *testing.T) {
	registry := Registry{}
	var luna CindyCapability
	for _, capability := range registry.CindyCapabilities() {
		if capability.PublicID == "gpt-5.6-luna" {
			luna = capability
		}
	}
	if luna.CodexPresentation == nil || luna.CodexPresentation.TruncationPolicy.Mode != "tokens" || luna.CodexPresentation.AutoCompactTokenLimit == nil || *luna.CodexPresentation.AutoCompactTokenLimit != 900000 {
		t.Fatalf("missing independent Codex presentation: %+v", luna.CodexPresentation)
	}
	if luna.CodexPresentation.BaseInstructions != "" {
		t.Fatal("native protocol base instructions must be supplied by the host")
	}
	luna.CodexPresentation.TruncationPolicy.Limit = 1
	luna.CodexPresentation.SupportedReasoningLevels[0].Description = "changed"
	for _, capability := range registry.CindyCapabilities() {
		if capability.PublicID == "gpt-5.6-luna" && (capability.CodexPresentation.TruncationPolicy.Limit != 10000 || capability.CodexPresentation.SupportedReasoningLevels[0].Description == "changed") {
			t.Fatal("descriptor mutation escaped its returned snapshot")
		}
	}
}
