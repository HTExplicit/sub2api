package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

func NormalizeOpenAICompatRequestedModel(model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return ""
	}

	normalized, _, ok := splitOpenAICompatReasoningModel(trimmed)
	if !ok || normalized == "" {
		return trimmed
	}
	return normalized
}

func applyOpenAICompatModelNormalization(req *apicompat.AnthropicRequest) {
	if req == nil {
		return
	}

	originalModel := strings.TrimSpace(req.Model)
	if originalModel == "" {
		return
	}

	normalizedModel, derivedEffort, hasReasoningSuffix := splitOpenAICompatReasoningModel(originalModel)
	if hasReasoningSuffix && normalizedModel != "" {
		req.Model = normalizedModel
	}

	if req.OutputConfig != nil && strings.TrimSpace(req.OutputConfig.Effort) != "" {
		return
	}

	if derivedEffort == "" {
		return
	}

	if req.OutputConfig == nil {
		req.OutputConfig = &apicompat.AnthropicOutputConfig{}
	}
	// This path targets an OpenAI-compatible endpoint, not native Anthropic.
	// Keep the alias's exact effort: xhigh and max are distinct on modern
	// models, so round-tripping through an Anthropic max alias loses intent.
	req.OutputConfig.Effort = derivedEffort
}

func splitOpenAICompatReasoningModel(model string) (normalizedModel string, reasoningEffort string, ok bool) {
	if base, effort, recognized := resolveOpenAIModelReasoningAlias(model); recognized {
		return base, effort, true
	}
	return strings.TrimSpace(model), "", false
}

// openAICompatAnthropicReasoningEffort resolves the effort emitted by the
// Anthropic bridge after the final upstream model is known. Anthropic's max is
// translated to xhigh on the legacy scale. Only a destination with a known
// distinct max level may retain max; native parameter normalization is not a
// capability check.
func openAICompatAnthropicReasoningEffort(req *apicompat.AnthropicRequest, upstreamModel, convertedEffort string) string {
	if req == nil || req.OutputConfig == nil || !strings.EqualFold(strings.TrimSpace(req.OutputConfig.Effort), "max") {
		return convertedEffort
	}
	if !supportsOpenAIReasoningEffortMax(upstreamModel) {
		return convertedEffort
	}
	if normalized := normalizeOpenAIReasoningEffortForModel(req.OutputConfig.Effort, upstreamModel); normalized != "" {
		return normalized
	}
	return convertedEffort
}
