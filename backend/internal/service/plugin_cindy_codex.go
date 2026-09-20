package service

import (
	"encoding/json"
	"slices"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

// The provider owns descriptor policy. The host validates the protocol plan,
// keeps catalog identity/capacity authoritative, and supplies native text.
func newCindyCodexModel(capability CindyCapability, priority int) (cindyCodexModel, error) {
	plan := capability.CodexPresentation
	invalid := func() (cindyCodexModel, error) { return cindyCodexModel{}, ErrExtensionOperationUnavailable }
	if plan == nil || plan.Slug != capability.PublicID || plan.BaseInstructions != "" ||
		len(plan.DisplayName) > 256 || plan.ShellType != "shell_command" ||
		(plan.Visibility != "list" && plan.Visibility != "hide") ||
		(plan.TruncationPolicy.Mode != "tokens" && plan.TruncationPolicy.Mode != "bytes") ||
		plan.TruncationPolicy.Limit <= 0 || plan.TruncationPolicy.Limit > extensionv1.MaxModelContextTokens ||
		plan.EffectiveContextWindowPercent <= 0 || plan.EffectiveContextWindowPercent > 100 ||
		!slices.Equal(plan.InputModalities, capability.InputModalities) || len(plan.SupportedReasoningLevels) > 16 {
		return invalid()
	}
	if plan.Description != nil && len(*plan.Description) > 4096 {
		return invalid()
	}
	if limit := plan.AutoCompactTokenLimit; limit != nil && (*limit <= 0 || *limit > int64(capability.EffectiveCodexContextWindow())) {
		return invalid()
	}
	for _, window := range []*int{plan.ContextWindow, plan.MaxContextWindow} {
		if window == nil {
			if capability.EffectiveCodexContextWindow() > 0 {
				return invalid()
			}
		} else if *window <= 0 || int64(*window) > extensionv1.MaxModelContextTokens || *window != capability.EffectiveCodexContextWindow() {
			return invalid()
		}
	}
	efforts := capability.CodexReasoningEfforts()
	if plan.DefaultReasoningLevel != nil && !slices.Contains(efforts, *plan.DefaultReasoningLevel) {
		return invalid()
	}
	if len(efforts) != len(plan.SupportedReasoningLevels) {
		return invalid()
	}
	for index, level := range plan.SupportedReasoningLevels {
		if level.Effort != efforts[index] || len(level.Description) > 256 {
			return invalid()
		}
	}
	encoded, err := json.Marshal(plan)
	if err != nil || len(encoded) > 32*1024 {
		return invalid()
	}
	model := *plan
	model.Priority = priority
	model.BaseInstructions = openai.CodexBaseInstructionsForModel(capability.PublicID)
	return model, nil
}
