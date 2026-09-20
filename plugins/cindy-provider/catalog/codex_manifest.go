package catalog

import (
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
	"strings"
)

type cindyCodexReasoningEffort = extensionv1.CindyCodexReasoningEffort
type cindyCodexTruncationPolicy = extensionv1.CindyCodexTruncationPolicy
type cindyCodexModel = extensionv1.CindyCodexModel

func newCodexPresentation(capability CindyCapability) *cindyCodexModel {
	displayName := strings.TrimSpace(capability.DisplayName)
	if displayName == "" {
		displayName = capability.PublicID
	}
	var description *string
	if value := strings.TrimSpace(capability.Description); value != "" {
		description = &value
	}
	var defaultReasoningLevel *string
	if value := strings.TrimSpace(capability.DefaultReasoningEffort); value != "" {
		defaultReasoningLevel = &value
	}
	reasoningLevels := make([]cindyCodexReasoningEffort, 0, len(capability.CodexReasoningEfforts()))
	for _, effort := range capability.CodexReasoningEfforts() {
		reasoningLevels = append(reasoningLevels, cindyCodexReasoningEffort{
			Effort:      effort,
			Description: cindyCodexReasoningEffortDescription(effort),
		})
	}
	var contextWindow *int
	if value := capability.EffectiveCodexContextWindow(); value > 0 {
		contextWindow = &value
	}
	return &cindyCodexModel{
		Slug:                              capability.PublicID,
		DisplayName:                       displayName,
		Description:                       description,
		DefaultReasoningLevel:             defaultReasoningLevel,
		SupportedReasoningLevels:          reasoningLevels,
		ShellType:                         "shell_command",
		Visibility:                        "list",
		SupportedInAPI:                    true,
		Priority:                          0,
		AdditionalSpeedTiers:              []string{},
		ServiceTiers:                      []any{},
		BaseInstructions:                  "",
		IncludeSkillsUsageInstructions:    false,
		IncludePluginUsageInstructions:    false,
		IncludeAppsUsageInstructions:      false,
		SupportsReasoningSummaryParameter: true,
		DefaultReasoningSummary:           "auto",
		SupportVerbosity:                  false,
		WebSearchToolType:                 "text",
		TruncationPolicy:                  cindyCodexTruncationPolicyForModel(capability.PublicID),
		SupportsParallelToolCalls:         false,
		SupportsImageDetailOriginal:       false,
		ContextWindow:                     contextWindow,
		MaxContextWindow:                  contextWindow,
		AutoCompactTokenLimit:             cindyCodexAutoCompactTokenLimitForModel(capability.PublicID),
		EffectiveContextWindowPercent:     95,
		ExperimentalSupportedTools:        []string{},
		InputModalities:                   append([]string(nil), capability.InputModalities...),
		SupportsSearchTool:                false,
		UseResponsesLite:                  false,
	}
}

func cindyCodexTruncationPolicyForModel(modelID string) cindyCodexTruncationPolicy {
	switch modelID {
	case "gpt-5.6-luna":
		// Exact openai/codex rust-v0.147.0 models.json contract.
		return cindyCodexTruncationPolicy{Mode: "tokens", Limit: 10000}
	default:
		// Official rust-v0.147.0 unknown-model fallback.
		return cindyCodexTruncationPolicy{Mode: "bytes", Limit: 10000}
	}
}

func cindyCodexAutoCompactTokenLimitForModel(modelID string) *int64 {
	switch modelID {
	case "gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra":
		limit := int64(900000)
		return &limit
	default:
		return nil
	}
}

func cindyCodexReasoningEffortDescription(effort string) string {
	switch effort {
	case "minimal":
		return "Minimal reasoning"
	case "low":
		return "Low reasoning"
	case "medium":
		return "Medium reasoning"
	case "high":
		return "High reasoning"
	case "xhigh":
		return "Extra high reasoning"
	case "max":
		return "Maximum reasoning"
	case "ultra":
		return "Ultra reasoning"
	default:
		return effort
	}
}
