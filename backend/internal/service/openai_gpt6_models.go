package service

import (
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

// gpt6APIModelMetadata supplies release-owned API capability defaults only when
// an API-key catalog omits them. It is never persisted as an upstream
// observation and carries no capacity: windows come from the capacity resolver.
func gpt6APIModelMetadata(account *Account, model string) (UpstreamModelMetadata, bool) {
	if account == nil || account.Platform != PlatformOpenAI || !account.IsOpenAIApiKey() || !isOpenAIGPT6SolOrLunaModel(model) {
		return UpstreamModelMetadata{}, false
	}
	reasoning := true
	return UpstreamModelMetadata{
		ID: model, Reasoning: &reasoning, DefaultReasoningLevel: "medium",
		SupportedReasoningLevels: openai.GPT6APIReasoningEfforts(),
		InputModalities:          []string{"text", "image"},
		CodexToolCapabilities: map[string]json.RawMessage{
			"use_responses_lite":           json.RawMessage("false"),
			"tool_mode":                    json.RawMessage("null"),
			"multi_agent_reasoning_effort": json.RawMessage("null"),
		},
	}, true
}

func newConfiguredCodexModelDescriptorForAccount(model string, account *Account) configuredCodexModelDescriptor {
	descriptor := newConfiguredCodexModelDescriptor(model)
	if metadata, ok := gpt6APIModelMetadata(account, model); ok {
		applyUpstreamModelMetadataToCodexDescriptor(&descriptor, codexModelMetadataOverride{UpstreamModelMetadata: metadata})
	}
	return descriptor
}

func gpt6AccountModelMetadata(account *Account, model string) (UpstreamModelMetadata, bool) {
	if metadata, ok := gpt6APIModelMetadata(account, model); ok {
		return metadata, true
	}
	if account == nil || account.Platform != PlatformOpenAI || !account.IsOpenAIOAuthLike() || !isOpenAIGPT6SolOrLunaModel(model) {
		return UpstreamModelMetadata{}, false
	}
	reasoning := true
	levels := configuredCodexGPTReasoningLevels(model)
	efforts := make([]string, 0, len(levels))
	for _, level := range levels {
		efforts = append(efforts, level.Effort)
	}
	return UpstreamModelMetadata{
		ID: model, Reasoning: &reasoning, DefaultReasoningLevel: "medium",
		SupportedReasoningLevels: efforts, InputModalities: []string{"text", "image"},
	}, true
}
