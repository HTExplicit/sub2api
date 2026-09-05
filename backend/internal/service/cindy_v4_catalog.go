package service

// cindyCapabilityCatalog is the complete Cindy inventory. Cindy accounts are
// permanently free-only: the catalog itself contains exactly the authenticated
// free inventory instead of retaining a paid candidate layer and filtering it
// at runtime.
var cindyCapabilityCatalog = []CindyCapability{
	newCindyFreeChatCapability("deepseek-v4-flash", "deepseek/deepseek-v4-flash", "DeepSeek V4 Flash", "DeepSeek V4 Flash efficiency-optimized MoE; supports low/high/max effort", 1000000, 384000, []string{"text"}, []string{"medium", "high", "max"}, "high", 0, CindyTextPricing{InputCostPerToken: 0.44e-6, OutputCostPerToken: 1.32e-6, CacheReadInputTokenCost: 0.014e-6, CacheCreationInputTokenCostPresent: true}),
	newCindyFreeChatCapability("deepseek-v4-flash-vision-exp", "deepseek/deepseek-v4-flash-vision-exp", "DeepSeek V4 Flash Vision Exp", "", 1000000, 384000, []string{"text", "image"}, []string{"medium", "high", "max"}, "high", 0, CindyTextPricing{InputCostPerToken: 0.44e-6, OutputCostPerToken: 1.32e-6, CacheReadInputTokenCost: 0.014e-6, CacheCreationInputTokenCostPresent: true}),
	newCindyFreeChatCapability("deepseek-v4-pro", "deepseek/deepseek-v4-pro", "DeepSeek V4 Pro", "DeepSeek V4 Pro reasoning model; only high/max effective (low/medium→high, xhigh→max)", 1000000, 384000, []string{"text"}, []string{"medium", "high", "max"}, "high", 0, CindyTextPricing{InputCostPerToken: 1.32e-6, OutputCostPerToken: 3.96e-6, CacheReadInputTokenCost: 0.044e-6, CacheCreationInputTokenCostPresent: true}),
	newCindyFreeChatCapability("gemini-3.6-flash", "google/gemini-3.6-flash", "Gemini 3.6 Flash", "Google Gemini 3.6 Flash — frontier intelligence optimized for speed and cost", 1048576, 65536, []string{"text", "image", "audio", "video"}, []string{"medium", "high"}, "medium", 0, CindyTextPricing{InputCostPerToken: 1.5e-6, OutputCostPerToken: 7.5e-6, InputCostPerTokenPriority: 2.7e-6, OutputCostPerTokenPriority: 13.5e-6, CacheReadInputTokenCost: 0.15e-6, CacheReadInputTokenCostPriority: 0.27e-6}),
	newCindyFreeChatCapability("gpt-5.6-luna", "openai/gpt-5.6-luna", "GPT-5.6 Luna", "", 1050000, 128000, []string{"text", "image"}, []string{"medium", "high", "xhigh"}, "medium", 0, CindyTextPricing{InputCostPerToken: 0.2e-6, OutputCostPerToken: 1.2e-6, InputCostPerTokenPriority: 2e-6, OutputCostPerTokenPriority: 12e-6, CacheReadInputTokenCost: 0.02e-6, CacheReadInputTokenCostPriority: 0.2e-6, LongContextInputTokenThreshold: 272000, LongContextInputCostPerToken: 2e-6, LongContextOutputCostPerToken: 9e-6, LongContextCacheReadInputTokenCost: 0.2e-6, LongContextInputCostPerTokenPriority: 4e-6, LongContextOutputCostPerTokenPriority: 18e-6, LongContextCacheReadInputTokenCostPriority: 0.4e-6}),
	newCindyFreeChatCapability("qwen3.8-27b", "qwen/qwen3.8-27b", "Qwen3.8 27B", "", 991808, 131072, []string{"text", "image", "video"}, []string{"low", "medium", "high", "xhigh"}, "xhigh", 0, CindyTextPricing{InputCostPerToken: 0.425e-6, OutputCostPerToken: 2.55e-6, CacheReadInputTokenCost: 0.085e-6, CacheCreationInputTokenCost: 0.53125e-6, CacheCreationInputTokenCostPresent: true}),
	newCindyFreeChatCapability("qwen3.8-flash", "qwen/qwen3.8-flash", "Qwen3.8 Flash", "", 991808, 131072, []string{"text", "image", "video"}, []string{"low", "medium", "high", "xhigh"}, "xhigh", 0, CindyTextPricing{InputCostPerToken: 0.16e-6, OutputCostPerToken: 0.47e-6, CacheReadInputTokenCost: 0.016e-6, CacheCreationInputTokenCost: 0.2e-6, CacheCreationInputTokenCostPresent: true}),
	newCindyFreeChatCapability("hy3", "tencent/hy3", "Hy3", "", 262144, 128000, []string{"text"}, []string{"low", "medium", "high"}, "high", 0.9, CindyTextPricing{InputCostPerToken: 0.132e-6, OutputCostPerToken: 0.528e-6, CacheReadInputTokenCost: 0.033e-6, CacheCreationInputTokenCostPresent: true}),
	newCindyFreeChatCapability("glm-5.3-flash", "z-ai/glm-5.3-flash", "GLM 5.3 Flash", "Zhipu GLM-5.3-Flash native multimodal coding model; 1M context", 1000000, 131072, []string{"text", "image"}, []string{"low", "medium", "high", "max"}, "max", 0.5, CindyTextPricing{InputCostPerToken: 0.15e-6, OutputCostPerToken: 0.5e-6, CacheReadInputTokenCost: 0.03e-6, CacheCreationInputTokenCostPresent: true}),
	newCindyFreeSpecialCapability(CindyWebSearchModel, "Cindy Web Search", "Internal model for the independently gated /v1/alpha/search bridge", []CindyEndpoint{CindyEndpointAlphaSearch}),
	newCindyFreeSpecialCapability(CindyAutoReviewModel, "Cindy Auto Review", "Reserved management-visible model without a public schema or routing handler", nil),
	newCindyAstraCapability(),
	newCindyMetadataPendingCapability("gemini-3.8-flash", "google/gemini-3.8-flash", "Gemini 3.8 Flash", 0, 0, []CindyEndpoint{CindyEndpointResponses, CindyEndpointMessages, CindyEndpointAlphaSearch}),
	newCindyMetadataPendingCapability("muse-spark-1.3", "meta/muse-spark-1.3", "Muse Spark 1.3", 0, 0, []CindyEndpoint{CindyEndpointResponses, CindyEndpointMessages, CindyEndpointAlphaSearch}),
	newCindyMetadataPendingCapability("hy4-preview", "tencent/hy4-preview", "HY4 Preview", 960000, 64000, []CindyEndpoint{CindyEndpointMessages}),
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

func newCindyAstraCapability() CindyCapability {
	capability := newCindyFreeChatCapability("gpt-6-astra", "openai/gpt-6-astra", "GPT-6 Astra",
		"OpenAI GPT-6 Astra for complex reasoning, coding, computer use, research, and document creation",
		272000, 128000, []string{"text"}, []string{"low", "medium", "high", "xhigh", "max"}, "medium", 0,
		CindyTextPricing{InputCostPerToken: 10e-6, OutputCostPerToken: 50e-6,
			CacheReadInputTokenCost: 1e-6, CacheCreationInputTokenCost: 12.5e-6, CacheCreationInputTokenCostPresent: true,
			InputCostPerTokenPriority: 20e-6, OutputCostPerTokenPriority: 100e-6,
			CacheReadInputTokenCostPriority: 2e-6, CacheCreationInputTokenCostPriority: 25e-6})
	capability.MetadataSourceRevision = CindyCatalogAdditionsMetadataSourceRevision
	capability.PricingSource = CindyCatalogAdditionsMetadataSourceRevision
	capability.CodexReasoningEffortLevels = []string{"low", "medium", "high", "xhigh", "max", "ultra"}
	capability.VerifiedEndpoints = []CindyEndpoint{CindyEndpointResponses, CindyEndpointAlphaSearch}
	capability.ClientSurfaces = []string{CindyClientSurfaceCodex, CindyClientSurfacePi, CindyClientSurfaceOpenAI}
	capability.AgentWireProtocols = map[string]string{"codex": "openai-responses", "pi": "openai-responses"}
	return capability
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

func newCindyFreeSpecialCapability(publicID, displayName, description string, endpoints []CindyEndpoint) CindyCapability {
	return CindyCapability{
		PublicID: publicID, LiveUpstreamID: publicID, RegistryID: publicID,
		DisplayName: displayName, Description: description, Kind: CindyModelKindSpecial,
		InputModalities: []string{"text"}, OutputModalities: []string{"text"},
		VerifiedEndpoints: endpoints, MetadataSourceRevision: CindyFreeModelCatalogSourceRevision,
		PublicModel: false,
	}
}
