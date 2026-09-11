package service

// cindyCapabilityCatalog is the complete Cindy inventory. Cindy accounts are
// permanently free-only: the catalog itself contains exactly the authenticated
// free inventory instead of retaining a paid candidate layer and filtering it
// at runtime.
var cindyCapabilityCatalog = []CindyCapability{
	newCindyFreeChatCapability("deepseek-v4-flash", "deepseek/deepseek-v4-flash", "DeepSeek V4 Flash", "DeepSeek V4 Flash efficiency-optimized MoE; supports low/high/max effort", 1048576, 384000, []string{"text"}, []string{"low", "high", "max"}, "high", 0, CindyTextPricing{InputCostPerToken: 0.3e-6, OutputCostPerToken: 1.2e-6, CacheReadInputTokenCost: 0.006e-6, CacheCreationInputTokenCostPresent: true}),
	newCindyFreeChatCapability("deepseek-v4-flash-vision-exp", "deepseek/deepseek-v4-flash-vision-exp", "DeepSeek V4 Flash Vision Exp", "", 1048576, 384000, []string{"text", "image"}, []string{"low", "high", "max"}, "high", 0, CindyTextPricing{InputCostPerToken: 0.3e-6, OutputCostPerToken: 1.2e-6, CacheReadInputTokenCost: 0.006e-6, CacheCreationInputTokenCostPresent: true}),
	newCindyFreeChatCapability("deepseek-v4-pro", "deepseek/deepseek-v4-pro", "DeepSeek V4 Pro", "DeepSeek V4 Pro reasoning model", 1048576, 384000, []string{"text"}, []string{"low", "high", "max"}, "high", 0, CindyTextPricing{InputCostPerToken: 1.32e-6, OutputCostPerToken: 3.96e-6, CacheReadInputTokenCost: 0.044e-6, CacheCreationInputTokenCostPresent: true}),
	newCindyFreeChatCapability("gemini-3.6-flash", "google/gemini-3.6-flash", "Gemini 3.6 Flash", "Google Gemini 3.6 Flash — frontier intelligence optimized for speed and cost", 1000000, 65536, []string{"text", "image", "audio", "video"}, []string{"minimal", "low", "medium", "high"}, "medium", 0, CindyTextPricing{InputCostPerToken: 0.75e-6, OutputCostPerToken: 3.75e-6, CacheReadInputTokenCost: 0.075e-6}),
	newCindyFreeChatCapability("gpt-5.6-luna", "openai/gpt-5.6-luna", "GPT-5.6-Luna", "", 1050000, 128000, []string{"text", "image"}, []string{"low", "medium", "high", "xhigh", "max"}, "medium", 0, CindyTextPricing{InputCostPerToken: 0.2e-6, OutputCostPerToken: 1.2e-6, InputCostPerTokenPriority: 0.4e-6, OutputCostPerTokenPriority: 2.4e-6, CacheReadInputTokenCost: 0.02e-6, CacheReadInputTokenCostPriority: 0.04e-6, CacheCreationInputTokenCost: 0.25e-6, CacheCreationInputTokenCostPriority: 0.5e-6, CacheCreationInputTokenCostPresent: true, LongContextInputTokenThreshold: 272000, LongContextInputCostPerToken: 0.4e-6, LongContextOutputCostPerToken: 1.8e-6, LongContextCacheReadInputTokenCost: 0.04e-6, LongContextCacheCreationTokenCost: 0.5e-6, LongContextInputCostPerTokenPriority: 0.8e-6, LongContextOutputCostPerTokenPriority: 3.6e-6, LongContextCacheReadInputTokenCostPriority: 0.08e-6}),
	newCindyFreeChatCapability("qwen3.8-27b", "qwen/qwen3.8-27b", "Qwen3.8 27B", "", 991808, 131072, []string{"text", "image", "video"}, []string{"low", "medium", "high", "xhigh"}, "medium", 0, CindyTextPricing{InputCostPerToken: 0.425e-6, OutputCostPerToken: 2.55e-6, CacheReadInputTokenCost: 0.085e-6, CacheCreationInputTokenCost: 0.53125e-6, CacheCreationInputTokenCostPresent: true}),
	newCindyFreeChatCapability("qwen3.8-flash", "qwen/qwen3.8-flash", "Qwen 3.8 Flash", "Alibaba Qwen 3.8 Flash multimodal model with provider-managed thinking", 991808, 131072, []string{"text", "image", "video"}, []string{"low", "medium", "xhigh"}, "medium", 0, CindyTextPricing{InputCostPerToken: 0.16e-6, OutputCostPerToken: 0.47e-6, CacheReadInputTokenCost: 0.016e-6, CacheCreationInputTokenCost: 0.2e-6, CacheCreationInputTokenCostPresent: true}),
	newCindyFreeChatCapability("hy3", "tencent/hy3", "Hy3", "", 262144, 128000, []string{"text"}, []string{"low", "medium", "high"}, "medium", 0.9, CindyTextPricing{InputCostPerToken: 0.132e-6, OutputCostPerToken: 0.528e-6, CacheReadInputTokenCost: 0.033e-6, CacheCreationInputTokenCostPresent: true}),
	newCindyFreeChatCapability("glm-5.3-flash", "z-ai/glm-5.3-flash", "GLM-5.3-Flash", "Zhipu GLM-5.3-Flash native multimodal coding model; 1M context", 1000000, 131072, []string{"text", "image"}, []string{"low", "high", "max"}, "high", 0.5, CindyTextPricing{InputCostPerToken: 0.15e-6, OutputCostPerToken: 0.5e-6, CacheReadInputTokenCost: 0.03e-6, CacheCreationInputTokenCostPresent: true}),
	newCindyFreeSpecialCapability(CindyWebSearchModel, "Cindy Web Search", "Internal model for the independently gated /v1/alpha/search bridge", []CindyEndpoint{CindyEndpointAlphaSearch}),
	newCindyFreeSpecialCapability(CindyAutoReviewModel, "Cindy Auto Review", "Reserved management-visible model without a public schema or routing handler", nil),
	newCindyMetadataPendingCapability("deepseek-flash", "deepseek/deepseek-flash", "DeepSeek Flash", 1000000, 384000, nil),
	newCindyMetadataPendingCapability("gemini-3.8-flash", "google/gemini-3.8-flash", "Gemini 3.8 Flash", 0, 0, []CindyEndpoint{CindyEndpointResponses, CindyEndpointMessages, CindyEndpointAlphaSearch}),
	newCindyMetadataPendingCapability("muse-spark-1.3", "meta/muse-spark-1.3", "Muse Spark 1.3", 0, 0, []CindyEndpoint{CindyEndpointResponses, CindyEndpointMessages, CindyEndpointAlphaSearch}),
	newCindyMetadataPendingCapability("hy4-preview", "tencent/hy4-preview", "HY4 Preview", 960000, 64000, []CindyEndpoint{CindyEndpointMessages}),
	newCindyInventoryImageCapability("gpt-image-2.5-flare", "openai/gpt-image-2.5-flare", "GPT Image 2.5 Flare"),
	newCindyInventoryImageCapability("gpt-image-2.5-sunburst", "openai/gpt-image-2.5-sunburst", "GPT Image 2.5 Sunburst"),
}

func newCindyFreeChatCapability(publicID, liveID, displayName, description string, contextWindow, maxOutputTokens int, inputModalities, efforts []string, defaultEffort string, costDiscount float64, pricing CindyTextPricing) CindyCapability {
	return CindyCapability{
		PublicID: publicID, LiveUpstreamID: liveID, RegistryID: liveID,
		DisplayName: displayName, Description: description, Kind: CindyModelKindText,
		InputModalities: inputModalities, OutputModalities: []string{"text"},
		VerifiedEndpoints:  []CindyEndpoint{CindyEndpointResponses, CindyEndpointMessages, CindyEndpointAlphaSearch},
		ClientSurfaces:     []string{CindyClientSurfaceCodex, CindyClientSurfacePi, CindyClientSurfaceOpenAI, CindyClientSurfaceClaude, CindyClientSurfaceAnthropic},
		AgentWireProtocols: map[string]string{"claude-code": "anthropic-messages", "codex": "openai-responses", "pi": "openai-responses"},
		MaxInputTokens:     contextWindow, CodexContextWindow: contextWindow, MaxOutputTokens: maxOutputTokens,
		ReasoningEfforts: efforts, DefaultReasoningEffort: defaultEffort,
		MetadataSourceRevision: CindyModelMetadataSourceRevision, PricingSource: CindyModelMetadataSourceRevision,
		CostDiscount: costDiscount, TextPricing: &pricing, PublicModel: true,
	}
}

func newCindyMetadataPendingCapability(publicID, liveID, displayName string, contextWindow, maxOutputTokens int, endpoints []CindyEndpoint) CindyCapability {
	return CindyCapability{
		PublicID: publicID, LiveUpstreamID: liveID, RegistryID: liveID,
		DisplayName: displayName, Description: "Metadata pending verification", Kind: CindyModelKindText,
		InputModalities: []string{"text"}, OutputModalities: []string{"text"},
		VerifiedEndpoints: endpoints, MetadataSourceRevision: CindyFreeModelCatalogSourceRevision,
		MaxInputTokens: contextWindow, CodexContextWindow: contextWindow, MaxOutputTokens: maxOutputTokens,
		PublicModel: false,
	}
}

func newCindyInventoryImageCapability(publicID, liveID, displayName string) CindyCapability {
	return CindyCapability{
		PublicID: publicID, LiveUpstreamID: liveID, RegistryID: liveID,
		DisplayName: displayName, Description: "Metadata pending verification", Kind: CindyModelKindImage,
		InputModalities: []string{"text", "image"}, OutputModalities: []string{"image"},
		MetadataSourceRevision: CindyModelMetadataSourceRevision,
		PublicModel: false,
	}
}

func newCindyFreeSpecialCapability(publicID, displayName, description string, endpoints []CindyEndpoint) CindyCapability {
	return CindyCapability{
		PublicID: publicID, LiveUpstreamID: publicID, RegistryID: publicID,
		DisplayName: displayName, Description: description, Kind: CindyModelKindSpecial,
		InputModalities: []string{"text"}, OutputModalities: []string{"text"},
		VerifiedEndpoints: endpoints, MetadataSourceRevision: CindyFreeModelCatalogSourceRevision,
		PublicModel: false,
	}
}
