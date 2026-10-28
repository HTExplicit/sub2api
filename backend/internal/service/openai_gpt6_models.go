package service

import "github.com/Wei-Shaw/sub2api/internal/pkg/openai"

// gpt6APIModelMetadata supplies release-owned API capability defaults only when
// an API-key catalog omits them. It is never persisted as an upstream
// observation and carries no capacity. It supplies Pelican capability checks.
func gpt6APIModelMetadata(account *Account, model string) (UpstreamModelMetadata, bool) {
	if account == nil || account.Platform != PlatformOpenAI || !account.IsOpenAIApiKey() || !isOpenAIGPT6SolOrLunaModel(model) {
		return UpstreamModelMetadata{}, false
	}
	reasoning := true
	return UpstreamModelMetadata{
		ID: model, Reasoning: &reasoning, DefaultReasoningLevel: "medium",
		SupportedReasoningLevels: openai.GPT6APIReasoningEfforts(),
		InputModalities:          []string{"text", "image"},
	}, true
}

func newConfiguredCodexModelDescriptorForAccount(model string, account *Account) configuredCodexModelDescriptor {
	descriptor := newConfiguredCodexModelDescriptor(model)
	if metadata, ok := gpt6APIModelMetadata(account, model); ok {
		applyUpstreamModelMetadataToCodexDescriptor(&descriptor, codexModelMetadataOverride{UpstreamModelMetadata: metadata})
	}
	return descriptor
}
