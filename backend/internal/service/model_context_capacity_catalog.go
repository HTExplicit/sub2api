package service

// This release-pinned catalog describes capacity, not model availability or routing.
// Stored IDs/aliases are exact; matching-only namespace and GPT spelling variants
// are handled by the resolver. MatchHosts and MatchAccountModes
// constrain product-specific evidence; an equal self-hosted model ID is insufficient.
// Zero input/output fields mean unknown, never a copy of the total context window.
const officialModelContextCapacityVerifiedAt = "2026-09-07"

const (
	GPTContextCapacityReferenceRelease    = "rust-v0.153.4"
	gptContextCapacityReferenceSource     = "https://github.com/openai/codex/blob/3d2ee51ca2d5db578f328aa75e20aa22c0197c9a/codex-rs/models-manager/models.json"
	gptContextCapacityReferenceVerifiedAt = "2026-09-08"
	gptLongContextSelection               = "本项目将订阅参考最大 872000 的 GPT 档选用 API 规格 1050000 作为规划基准；订阅参考原值未改写，不表示订阅服务端已返回 1050000。"
	gptSubscriptionMaximumSelection       = "本项目按官方 Codex 内置目录的最大窗口规划；不是单个订阅账号的实时容量或可用性承诺。"
)

const (
	claudeContextSource   = "https://platform.claude.com/docs/en/build-with-claude/context-windows"
	claudeIntegerSource   = "https://www-cdn.anthropic.com/78073f739564e986ff3e28522761a7a0b4484f84.pdf#page=30"
	claudeOutputSource    = "https://platform.claude.com/docs/en/about-claude/models/optimizing-for-cost-and-intelligence#set-budgets-and-output-caps"
	claudeUnitsBasis      = "按官方同平台单位归一：Sonnet 4.6 System Card 明确 1M=1,000,000；成本指南明确 Fable 5.1 的 128K=128,000；其他型号以各自规格及该单位体系归一，非逐型号整数原文。"
	claudeLegacyBasis     = "官方上下文指南以 200000 说明 200K；64K 输出按同平台十进制单位归一为 64000，不宣称已找到逐型号 64000 的独立整数上限原文。"
	deepseekModelsSource  = "https://api-docs.deepseek.com/quick_start/pricing/"
	deepseekIntegerSource = "https://api-docs.deepseek.com/quick_start/agent_integrations/pi_mono/"
	qwenEndpointSource    = "https://docs.modelstudio.console.alibabacloud.com/en/model-studio/base-url"
	kimiIntegerSource     = "https://www.kimi.ai/help/kimi-api/api-troubleshooting"
	kimiCodingSource      = "https://www.kimi.com/code/docs/third-party-tools/claude-code.html"
	glmOverviewSource     = "https://docs.bigmodel.cn/cn/guide/start/model-overview"
	glmOutputSource       = "https://docs.bigmodel.cn/cn/guide/start/concept-param"
	glmMillionSource      = "https://docs.bigmodel.cn/cn/guide/develop/openclaw"
	glm200KSource         = "https://docs.z.ai/devpack/using5.1"
	minimaxSource         = "https://platform.minimax.io/docs/api-reference/api-overview"
	doubaoModelsSource    = "https://www.volcengine.com/docs/82379/1330310?lang=zh"
	doubaoIntegerSource   = "https://www.volcengine.com/docs/82379/1494384?lang=zh"
	doubaoEndpointSource  = "https://www.volcengine.com/docs/82379/1399009?lang=zh"
	doubaoUnitsBasis      = "跨官方页面单位推导：模型表默认回答 4k，官方 API 的 max_tokens 默认值 4096，据此按 k=1024 归一；1024k=1048576，256k=262144，224k=229376。不是上下文原文直接整数证据。"
)

func codexModelContextCapacityReference(window, maximum int64) *ModelContextCapacityReference {
	return &ModelContextCapacityReference{
		Product: "codex_subscription", SourceURL: gptContextCapacityReferenceSource,
		Release: GPTContextCapacityReferenceRelease, VerifiedAt: gptContextCapacityReferenceVerifiedAt,
		ContextWindow: window, MaxContextWindow: maximum,
	}
}

// Host sets contain only the documented regional API hosts applicable to the
// corresponding entries. They deliberately do not use suffix or wildcard rules.
var (
	qwenBailianCapacityHosts      = []string{"dashscope.aliyuncs.com", "dashscope-intl.aliyuncs.com"}
	qwenBailianMainCapacityHosts  = []string{"dashscope.aliyuncs.com", "dashscope-intl.aliyuncs.com", "dashscope-us.aliyuncs.com", "cn-hongkong.dashscope.aliyuncs.com"}
	qwenBailianCoderCapacityHosts = []string{"dashscope.aliyuncs.com", "dashscope-intl.aliyuncs.com", "dashscope-us.aliyuncs.com"}
	doubaoBeijingCapacityHosts    = []string{"ark.cn-beijing.volces.com"}
)

var officialModelContextCapacityCatalog = []OfficialModelContextCapacity{
	// Selected GPT planning baselines. API entries remain the fallback for models
	// not covered by the pinned Codex reference; no model availability is changed.
	{
		ModelID: "gpt-6-astra", Aliases: []string{"gpt-6"}, Provider: "openai", Product: "api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1050000, MaxOutputTokens: 128000, CapacityBasis: "total_context"},
		SourceURL:            "https://developers.openai.com/api/docs/models/gpt-6-astra",
		VerifiedAt:           officialModelContextCapacityVerifiedAt,
		OriginalText:         "1,050,000 context window; 128,000 max output tokens",
		Reference:            codexModelContextCapacityReference(272000, 872000),
		Conditions:           gptLongContextSelection,
	},
	{
		ModelID: "gpt-5.6-sol", Aliases: []string{"gpt-5.6"}, Provider: "openai", Product: "api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1050000, MaxOutputTokens: 128000, CapacityBasis: "total_context"},
		SourceURL:            "https://developers.openai.com/api/docs/models/gpt-5.6-sol",
		VerifiedAt:           officialModelContextCapacityVerifiedAt,
		OriginalText:         "1,050,000 context window; 128,000 max output tokens",
		Reference:            codexModelContextCapacityReference(272000, 872000),
		Conditions:           gptLongContextSelection + " gpt-5.6 是 Sol 的明确别名；不写回账号模型映射。",
	},
	{
		ModelID: "gpt-5.6-terra", Provider: "openai", Product: "api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1050000, MaxOutputTokens: 128000, CapacityBasis: "total_context"},
		SourceURL:            "https://developers.openai.com/api/docs/models/gpt-5.6-terra",
		VerifiedAt:           officialModelContextCapacityVerifiedAt,
		OriginalText:         "1,050,000 context window; 128,000 max output tokens",
		Reference:            codexModelContextCapacityReference(272000, 872000),
		Conditions:           gptLongContextSelection,
	},
	{
		ModelID: "gpt-5.6-luna", Provider: "openai", Product: "api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1050000, MaxOutputTokens: 128000, CapacityBasis: "total_context"},
		SourceURL:            "https://developers.openai.com/api/docs/models/gpt-5.6-luna",
		VerifiedAt:           officialModelContextCapacityVerifiedAt,
		OriginalText:         "1,050,000 context window; 128,000 max output tokens",
		Reference:            codexModelContextCapacityReference(272000, 872000),
		Conditions:           gptLongContextSelection,
	},
	{
		ModelID: "gpt-5.5", Provider: "openai", Product: "codex_subscription",
		ModelContextCapacity: ModelContextCapacity{MaxContextWindow: 272000, CapacityBasis: ModelContextCapacityBasisMaximum},
		SourceURL:            gptContextCapacityReferenceSource,
		VerifiedAt:           gptContextCapacityReferenceVerifiedAt,
		OriginalText:         "context_window: 272000; max_context_window: 272000",
		Reference:            codexModelContextCapacityReference(272000, 272000),
		Conditions:           gptSubscriptionMaximumSelection,
	},
	{
		ModelID: "gpt-5.4", Provider: "openai", Product: "codex_subscription",
		ModelContextCapacity: ModelContextCapacity{MaxContextWindow: 1000000, CapacityBasis: ModelContextCapacityBasisMaximum},
		SourceURL:            gptContextCapacityReferenceSource,
		VerifiedAt:           gptContextCapacityReferenceVerifiedAt,
		OriginalText:         "context_window: 272000; max_context_window: 1000000",
		Reference:            codexModelContextCapacityReference(272000, 1000000),
		Conditions:           gptSubscriptionMaximumSelection,
	},
	{
		ModelID: "gpt-5.4-mini", Provider: "openai", Product: "codex_subscription",
		ModelContextCapacity: ModelContextCapacity{MaxContextWindow: 272000, CapacityBasis: ModelContextCapacityBasisMaximum},
		SourceURL:            gptContextCapacityReferenceSource,
		VerifiedAt:           gptContextCapacityReferenceVerifiedAt,
		OriginalText:         "context_window: 272000; max_context_window: 272000",
		Reference:            codexModelContextCapacityReference(272000, 272000),
		Conditions:           gptSubscriptionMaximumSelection,
	},
	{
		ModelID: "gpt-5.4-nano", Provider: "openai", Product: "api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 400000, MaxOutputTokens: 128000, CapacityBasis: "total_context"},
		SourceURL:            "https://developers.openai.com/api/docs/models/gpt-5.4-nano",
		VerifiedAt:           officialModelContextCapacityVerifiedAt,
		OriginalText:         "400,000 context window; 128,000 max output tokens",
	},
	{
		ModelID: "gpt-5.3-codex", Provider: "openai", Product: "api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 400000, MaxOutputTokens: 128000, CapacityBasis: "total_context"},
		SourceURL:            "https://developers.openai.com/api/docs/models/gpt-5.3-codex",
		VerifiedAt:           officialModelContextCapacityVerifiedAt,
		OriginalText:         "400,000 context window; 128,000 max output tokens",
	},
	{
		ModelID: "gpt-5.2", Provider: "openai", Product: "codex_subscription",
		ModelContextCapacity: ModelContextCapacity{MaxContextWindow: 272000, CapacityBasis: ModelContextCapacityBasisMaximum},
		SourceURL:            gptContextCapacityReferenceSource,
		VerifiedAt:           gptContextCapacityReferenceVerifiedAt,
		OriginalText:         "context_window: 272000; max_context_window: 272000",
		Reference:            codexModelContextCapacityReference(272000, 272000),
		Conditions:           gptSubscriptionMaximumSelection,
	},
	{
		ModelID: "gpt-daybreak-blue-latest", Provider: "openai", Product: "api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1050000, MaxOutputTokens: 128000, CapacityBasis: ModelContextCapacityBasisTotal},
		SourceURL:            "https://developers.openai.com/api/docs/models/gpt-daybreak-blue-latest",
		VerifiedAt:           gptContextCapacityReferenceVerifiedAt,
		OriginalText:         "1,050,000 context window; 128,000 max output tokens",
		Reference:            codexModelContextCapacityReference(272000, 872000),
		Conditions:           gptLongContextSelection + " Daybreak 仍需单独授权，不据容量目录开放模型。",
	},
	{
		ModelID: "gpt-daybreak-red-latest", Provider: "openai", Product: "codex_subscription",
		ModelContextCapacity: ModelContextCapacity{MaxContextWindow: 372000, CapacityBasis: ModelContextCapacityBasisMaximum},
		SourceURL:            gptContextCapacityReferenceSource,
		VerifiedAt:           gptContextCapacityReferenceVerifiedAt,
		OriginalText:         "context_window: 372000; max_context_window: 372000",
		Reference:            codexModelContextCapacityReference(372000, 372000),
		Conditions:           gptSubscriptionMaximumSelection + " Daybreak 仍需单独授权，不据容量目录开放模型。",
	},
	{
		ModelID: "gpt-4.1", Provider: "openai", Product: "api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1047576, MaxOutputTokens: 32768, CapacityBasis: "total_context"},
		SourceURL:            "https://developers.openai.com/api/docs/models/gpt-4.1",
		VerifiedAt:           officialModelContextCapacityVerifiedAt,
		OriginalText:         "1,047,576 context window; 32,768 max output tokens",
	},
	{
		ModelID: "gpt-4o", Provider: "openai", Product: "api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 128000, MaxOutputTokens: 16384, CapacityBasis: "total_context"},
		SourceURL:            "https://developers.openai.com/api/docs/models/gpt-4o",
		VerifiedAt:           officialModelContextCapacityVerifiedAt,
		OriginalText:         "128,000 context window; 16,384 max output tokens",
	},
	{
		ModelID: "gpt-4o-mini", Provider: "openai", Product: "api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 128000, MaxOutputTokens: 16384, CapacityBasis: "total_context"},
		SourceURL:            "https://developers.openai.com/api/docs/models/gpt-4o-mini",
		VerifiedAt:           officialModelContextCapacityVerifiedAt,
		OriginalText:         "128,000 context window; 16,384 max output tokens",
	},

	// Claude API: ordinary Messages limits, excluding Batch-only beta output caps.
	{
		ModelID: "claude-fable-5-1", Provider: "anthropic", Product: "messages_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxOutputTokens: 128000, CapacityBasis: "total_context"},
		SourceURL:            "https://platform.claude.com/docs/en/models/fable-5-1/overview",
		SourceURLs:           []string{claudeContextSource, claudeIntegerSource, claudeOutputSource},
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "1M; 128K",
		NormalizationBasis: claudeUnitsBasis,
	},
	{
		ModelID: "claude-opus-5", Provider: "anthropic", Product: "messages_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxOutputTokens: 128000, CapacityBasis: "total_context"},
		SourceURL:            "https://platform.claude.com/docs/en/models/overview",
		SourceURLs:           []string{claudeContextSource, claudeIntegerSource, claudeOutputSource},
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "1M; 128K",
		NormalizationBasis: claudeUnitsBasis,
	},
	{
		ModelID: "claude-sonnet-5", Provider: "anthropic", Product: "messages_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxOutputTokens: 128000, CapacityBasis: "total_context"},
		SourceURL:            "https://platform.claude.com/docs/en/models/overview",
		SourceURLs:           []string{claudeContextSource, claudeIntegerSource, claudeOutputSource},
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "1M; 128K",
		NormalizationBasis: claudeUnitsBasis,
	},
	{
		ModelID: "claude-fable-5", Provider: "anthropic", Product: "messages_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxOutputTokens: 128000, CapacityBasis: "total_context"},
		SourceURL:            "https://platform.claude.com/docs/en/models/fable-5/overview",
		SourceURLs:           []string{claudeContextSource, claudeIntegerSource, claudeOutputSource},
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "1M; 128K",
		NormalizationBasis: claudeUnitsBasis,
	},
	{
		ModelID: "claude-opus-4-8", Provider: "anthropic", Product: "messages_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxOutputTokens: 128000, CapacityBasis: "total_context"},
		SourceURL:            claudeContextSource,
		SourceURLs:           []string{"https://platform.claude.com/docs/en/about-claude/model-deprecations", claudeIntegerSource, claudeOutputSource},
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "1M; 128K",
		NormalizationBasis: claudeUnitsBasis,
	},
	{
		ModelID: "claude-opus-4-7", Provider: "anthropic", Product: "messages_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxOutputTokens: 128000, CapacityBasis: "total_context"},
		SourceURL:            claudeContextSource,
		SourceURLs:           []string{"https://platform.claude.com/docs/en/about-claude/model-deprecations", claudeIntegerSource, claudeOutputSource},
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "1M; 128K",
		NormalizationBasis: claudeUnitsBasis,
	},
	{
		ModelID: "claude-opus-4-6", Provider: "anthropic", Product: "messages_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxOutputTokens: 128000, CapacityBasis: "total_context"},
		SourceURL:            "https://platform.claude.com/docs/en/models/opus-4-6/overview",
		SourceURLs:           []string{claudeContextSource, claudeIntegerSource, claudeOutputSource},
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "1M; 128K",
		NormalizationBasis: claudeUnitsBasis,
		Conditions:         "普通 Messages 输出上限；不采用 Batch API beta 的 300K 输出上限。",
	},
	{
		ModelID: "claude-sonnet-4-6", Provider: "anthropic", Product: "messages_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxOutputTokens: 128000, CapacityBasis: "total_context"},
		SourceURL:            "https://platform.claude.com/docs/en/models/sonnet-4-6/overview",
		SourceURLs:           []string{claudeContextSource, claudeIntegerSource, claudeOutputSource},
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "1M; 128K",
		NormalizationBasis: claudeUnitsBasis,
		Conditions:         "普通 Messages 输出上限；不采用 Batch API beta 的 300K 输出上限。",
	},
	{
		ModelID: "claude-opus-4-5-20251101", Aliases: []string{"claude-opus-4-5"}, Provider: "anthropic", Product: "messages_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 200000, MaxOutputTokens: 64000, CapacityBasis: "total_context"},
		SourceURL:            "https://platform.claude.com/docs/en/models/opus-4-5/overview",
		SourceURLs:           []string{claudeContextSource, claudeOutputSource},
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "200K; 64K",
		NormalizationBasis: claudeLegacyBasis,
	},
	{
		ModelID: "claude-sonnet-4-5-20250929", Aliases: []string{"claude-sonnet-4-5"}, Provider: "anthropic", Product: "messages_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 200000, MaxOutputTokens: 64000, CapacityBasis: "total_context"},
		SourceURL:            "https://platform.claude.com/docs/zh-CN/models/sonnet-4-5/overview",
		SourceURLs:           []string{claudeContextSource, claudeOutputSource},
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "200K; 64K",
		NormalizationBasis: claudeLegacyBasis,
	},
	{
		ModelID: "claude-haiku-4-5-20251001", Aliases: []string{"claude-haiku-4-5"}, Provider: "anthropic", Product: "messages_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 200000, MaxOutputTokens: 64000, CapacityBasis: "total_context"},
		SourceURL:            "https://platform.claude.com/docs/en/models/haiku-4-5/overview",
		SourceURLs:           []string{claudeContextSource, claudeOutputSource},
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "200K; 64K",
		NormalizationBasis: claudeLegacyBasis,
	},

	// Gemini publishes independent input/output limits, not an additive total.
	{
		ModelID: "gemini-3.8-flash", Provider: "gemini", Product: "gemini_api",
		ModelContextCapacity: ModelContextCapacity{MaxInputTokens: 1048576, MaxOutputTokens: 65536, CapacityBasis: "input_limit"},
		SourceURL:            "https://ai.google.dev/gemini-api/docs/models/gemini-3.8-flash",
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "Input token limit 1,048,576; Output token limit 65,536",
	},
	{
		ModelID: "gemini-3.5-flash-lite", Provider: "gemini", Product: "gemini_api",
		ModelContextCapacity: ModelContextCapacity{MaxInputTokens: 1048576, MaxOutputTokens: 65536, CapacityBasis: "input_limit"},
		SourceURL:            "https://ai.google.dev/gemini-api/docs/models/gemini-3.5-flash-lite",
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "Input token limit 1,048,576; Output token limit 65,536",
	},
	{
		ModelID: "gemini-3.1-pro-preview", Aliases: []string{"gemini-3.1-pro-preview-customtools"}, Provider: "gemini", Product: "gemini_api",
		ModelContextCapacity: ModelContextCapacity{MaxInputTokens: 1048576, MaxOutputTokens: 65536, CapacityBasis: "input_limit"},
		SourceURL:            "https://ai.google.dev/gemini-api/docs/models/gemini-3.1-pro-preview",
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "Input token limit 1,048,576; Output token limit 65,536",
		Conditions: "Preview；customtools 为官方列出的同规格版本。目录仅匹配容量，不新增模型或改变可用性。",
	},
	{
		ModelID: "gemini-3.5-flash", Provider: "gemini", Product: "gemini_api",
		ModelContextCapacity: ModelContextCapacity{MaxInputTokens: 1048576, MaxOutputTokens: 65536, CapacityBasis: "input_limit"},
		SourceURL:            "https://ai.google.dev/gemini-api/docs/models/gemini-3.5-flash",
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "Input token limit 1,048,576; Output token limit 65,536",
	},
	{
		ModelID: "gemini-3-flash-preview", Provider: "gemini", Product: "gemini_api",
		ModelContextCapacity: ModelContextCapacity{MaxInputTokens: 1048576, MaxOutputTokens: 65536, CapacityBasis: "input_limit"},
		SourceURL:            "https://ai.google.dev/gemini-api/docs/models/gemini-3-flash-preview",
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "Input token limit 1,048,576; Output token limit 65,536",
		Conditions: "Preview；容量记录不代表账号已获访问权限。",
	},
	{
		ModelID: "gemini-2.5-pro", Provider: "gemini", Product: "gemini_api",
		ModelContextCapacity: ModelContextCapacity{MaxInputTokens: 1048576, MaxOutputTokens: 65536, CapacityBasis: "input_limit"},
		SourceURL:            "https://ai.google.dev/gemini-api/docs/models/gemini-2.5-pro",
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "Input token limit 1,048,576; Output token limit 65,536",
	},
	{
		ModelID: "gemini-2.5-flash", Provider: "gemini", Product: "gemini_api",
		ModelContextCapacity: ModelContextCapacity{MaxInputTokens: 1048576, MaxOutputTokens: 65536, CapacityBasis: "input_limit"},
		SourceURL:            "https://ai.google.dev/gemini-api/docs/models/gemini-2.5-flash",
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "Input token limit 1,048,576; Output token limit 65,536",
	},
	{
		ModelID: "gemini-2.0-flash", Provider: "gemini", Product: "gemini_api",
		ModelContextCapacity: ModelContextCapacity{MaxInputTokens: 1048576, MaxOutputTokens: 8192, CapacityBasis: "input_limit"},
		SourceURL:            "https://ai.google.dev/gemini-api/docs/models/gemini-2.0-flash",
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "Input token limit 1,048,576; Output token limit 8,192",
		Conditions: "历史规格，官方于 2026-06-01 关闭；只保留容量证据，不恢复可用性。",
	},
	{
		ModelID: "gemini-3-pro-preview", Provider: "gemini", Product: "gemini_api",
		ModelContextCapacity: ModelContextCapacity{MaxInputTokens: 1048576, MaxOutputTokens: 65536, CapacityBasis: "input_limit"},
		SourceURL:            "https://ai.google.dev/gemini-api/docs/models/gemini-3-pro-preview",
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "Input token limit 1,048,576; Output token limit 65,536",
		Conditions: "历史规格，官方于 2026-03-09 关闭；只保留容量证据，不恢复可用性。",
	},

	// Grok independent input/output hard limits are not guessed from context size.
	{
		ModelID: "grok-4.6", Provider: "grok", Product: "api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 500000, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.x.ai/developers/models/grok-4.6",
		SourceURLs:           []string{"https://docs.x.ai/developers/grok-4-6"},
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "500,000",
		Conditions: "官方无独立文本输出上限；仍受总上下文约束。",
	},
	{
		ModelID: "grok-4.5", Provider: "grok", Product: "api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 500000, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.x.ai/developers/models/grok-4.5",
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "500,000",
		Conditions: "不把 grok-build-latest 登记为本行别名；沿项目真实目标解析，保留既有 grok-build-0.1 路由。",
	},
	{
		ModelID: "grok-4.3", Provider: "grok", Product: "api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.x.ai/developers/models/grok-4.3",
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "1,000,000",
	},
	{
		ModelID: "grok-build-0.1", Provider: "grok", Product: "api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 256000, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.x.ai/developers/models/grok-build-0.1",
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "256,000",
		Conditions: "Early access；容量记录不代表账号已获访问权限。",
	},
	{
		ModelID: "grok-4.20-0309-reasoning", Provider: "grok", Product: "api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.x.ai/developers/models/grok-4.20-0309-reasoning",
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "1,000,000",
	},
	{
		ModelID: "grok-4.20-0309-non-reasoning", Provider: "grok", Product: "api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.x.ai/developers/models/grok-4.20-0309-non-reasoning",
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "1,000,000",
	},
	{
		ModelID: "grok-4.20-multi-agent-0309", Provider: "grok", Product: "api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.x.ai/developers/models/grok-4.20-multi-agent-0309",
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "1,000,000",
		Conditions: "Beta；容量记录不代表账号已获访问权限。",
	},

	// DeepSeek current exact IDs. Retired moving aliases are deliberately absent.
	{
		ModelID: "deepseek-v4-pro", Provider: "deepseek", Product: "api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxOutputTokens: 384000, CapacityBasis: "total_context"},
		SourceURL:            deepseekModelsSource, SourceURLs: []string{deepseekIntegerSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "1M; 384K",
		NormalizationBasis: "官方 Pi 接入配置直接给出 contextWindow=1000000、maxTokens=384000。",
		Conditions:         "当前价目表标注版本 DeepSeek-V4-Pro-0813；不把 deepseek-chat/reasoner 自动映射为 V4。",
	},
	{
		ModelID: "deepseek-v4-flash", Provider: "deepseek", Product: "api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxOutputTokens: 384000, CapacityBasis: "total_context"},
		SourceURL:            deepseekModelsSource, SourceURLs: []string{deepseekIntegerSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "1M; 384K",
		NormalizationBasis: "官方 Pi 接入配置直接给出 contextWindow=1000000、maxTokens=384000。",
		Conditions:         "当前价目表标注版本 DeepSeek-V4-Flash-0731；不把 deepseek-chat/reasoner 自动映射为 V4。",
	},
	{
		ModelID: "deepseek-v4-flash-vision-exp", Provider: "deepseek", Product: "api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxOutputTokens: 384000, CapacityBasis: "total_context"},
		SourceURL:            deepseekModelsSource, SourceURLs: []string{deepseekIntegerSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "1M; 384K",
		NormalizationBasis: "官方模型表与 Pro/Flash 共用容量规格，按官方 Pi 接入配置的整数单位归一；不是该实验型号独立整数配置。",
		Conditions:         "实验视觉版本；容量记录不修改模型可用性。",
	},

	// Qwen: official Bailian pay-as-you-go API only. Input caps shared by both
	// reasoning modes are the lower declared limit, with both originals retained.
	{
		ModelID: "qwen3.8-max", Aliases: []string{"qwen3.8-max-0902", "qwen3.8-max-2026-09-02"}, Provider: "qwen", Product: "bailian_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxInputTokens: 983616, MaxOutputTokens: 131072, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.modelstudio.console.alibabacloud.com/en/model-studio/qwen3-8-max",
		SourceURLs:           []string{qwenEndpointSource},
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "1,000,000; 991,808 / 983,616; 131,072",
		NormalizationBasis: "官方直接整数；最大输入取非思考 991808 与思考 983616 的共同下限 983616。",
		Conditions:         "百炼 API；北京、新加坡、法兰克福、弗吉尼亚、东京、香港。精确快照 ID 为官方明确列举，不推断滚动指向。",
		MatchHosts:         qwenBailianMainCapacityHosts, MatchAccountModes: []string{"payg"},
	},
	{
		ModelID: "qwen3.8-flash", Provider: "qwen", Product: "bailian_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxInputTokens: 983616, MaxOutputTokens: 131072, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.modelstudio.console.alibabacloud.com/en/model-studio/qwen3-8-flash",
		SourceURLs:           []string{qwenEndpointSource},
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "1,000,000; 991,808 / 983,616; 131,072",
		NormalizationBasis: "官方直接整数；最大输入取非思考 991808 与思考 983616 的共同下限 983616。",
		Conditions:         "百炼 API；北京、新加坡、法兰克福、弗吉尼亚、东京、香港；不覆盖独立 Coding Plan/Token Plan 或自部署权重。",
		MatchHosts:         qwenBailianMainCapacityHosts, MatchAccountModes: []string{"payg"},
	},
	{
		ModelID: "qwen3.7-plus", Aliases: []string{"qwen3.7-plus-2026-05-26"}, Provider: "qwen", Product: "bailian_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxInputTokens: 983616, MaxOutputTokens: 131072, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.modelstudio.console.alibabacloud.com/en/model-studio/qwen3-7-plus",
		SourceURLs:           []string{qwenEndpointSource},
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "1,000,000; 991,808 / 983,616; 131,072",
		NormalizationBasis: "官方直接整数；最大输入取非思考 991808 与思考 983616 的共同下限 983616。",
		Conditions:         "百炼 API；北京、新加坡、法兰克福、弗吉尼亚、东京、香港；不覆盖独立 Coding Plan/Token Plan 或自部署权重。",
		MatchHosts:         qwenBailianMainCapacityHosts, MatchAccountModes: []string{"payg"},
	},
	{
		ModelID: "qwen3.7-flash", Aliases: []string{"qwen3.7-flash-2026-07-15"}, Provider: "qwen", Product: "bailian_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxInputTokens: 983616, MaxOutputTokens: 131072, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.modelstudio.console.alibabacloud.com/en/model-studio/qwen3-7-flash",
		SourceURLs:           []string{qwenEndpointSource},
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "1,000,000; 991,808 / 983,616; 131,072",
		NormalizationBasis: "官方直接整数；最大输入取非思考 991808 与思考 983616 的共同下限 983616。",
		Conditions:         "百炼 API；北京、新加坡、法兰克福、弗吉尼亚、东京、香港；不覆盖独立 Coding Plan/Token Plan 或自部署权重。",
		MatchHosts:         qwenBailianMainCapacityHosts, MatchAccountModes: []string{"payg"},
	},
	{
		ModelID: "qwen3.8-27b", Provider: "qwen", Product: "bailian_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxInputTokens: 983616, MaxOutputTokens: 131072, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.modelstudio.console.alibabacloud.com/en/model-studio/qwen3-8-27b",
		SourceURLs:           []string{qwenEndpointSource},
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "1,000,000; 991,808 / 983,616; 131,072",
		NormalizationBasis: "官方直接整数；最大输入取非思考 991808 与思考 983616 的共同下限 983616。",
		Conditions:         "仅核实百炼 API 北京、新加坡；不套用于自部署同名权重。",
		MatchHosts:         qwenBailianCapacityHosts, MatchAccountModes: []string{"payg"},
	},
	{
		ModelID: "qwen3.8-2.4t-a95b", Provider: "qwen", Product: "bailian_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxInputTokens: 983616, MaxOutputTokens: 131072, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.modelstudio.console.alibabacloud.com/en/model-studio/qwen3-8-2-4t-a95b",
		SourceURLs:           []string{qwenEndpointSource},
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "1,000,000; 991,808 / 983,616; 131,072",
		NormalizationBasis: "官方直接整数；最大输入取非思考 991808 与思考 983616 的共同下限 983616。",
		Conditions:         "仅核实百炼 API 北京、新加坡；不套用于自部署同名权重。",
		MatchHosts:         qwenBailianCapacityHosts, MatchAccountModes: []string{"payg"},
	},
	{
		ModelID: "qwen3-coder-plus", Provider: "qwen", Product: "bailian_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxInputTokens: 997952, MaxOutputTokens: 65536, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.modelstudio.console.alibabacloud.com/en/model-studio/qwen3-coder-plus",
		SourceURLs:           []string{qwenEndpointSource},
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "1,000,000; 997,952; 65,536",
		Conditions: "百炼 API；北京、新加坡、法兰克福、弗吉尼亚；不覆盖独立 Coding Plan/Token Plan。",
		MatchHosts: qwenBailianCoderCapacityHosts, MatchAccountModes: []string{"payg"},
	},
	{
		ModelID: "qwen3-coder-flash", Aliases: []string{"qwen3-coder-flash-2025-07-28"}, Provider: "qwen", Product: "bailian_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxInputTokens: 997952, MaxOutputTokens: 65536, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.modelstudio.console.alibabacloud.com/en/model-studio/qwen3-coder-flash",
		SourceURLs:           []string{qwenEndpointSource},
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "1,000,000; 997,952; 65,536",
		Conditions: "百炼 API；北京、新加坡、法兰克福、弗吉尼亚；不覆盖独立 Coding Plan/Token Plan。",
		MatchHosts: qwenBailianCoderCapacityHosts, MatchAccountModes: []string{"payg"},
	},
	{
		ModelID: "qwen3-coder-next", Provider: "qwen", Product: "bailian_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 262144, MaxInputTokens: 204800, MaxOutputTokens: 65536, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.modelstudio.console.alibabacloud.com/en/model-studio/qwen3-coder-next",
		SourceURLs:           []string{qwenEndpointSource},
		VerifiedAt:           officialModelContextCapacityVerifiedAt, OriginalText: "262,144; 204,800; 65,536",
		Conditions: "百炼 API；北京、新加坡、法兰克福；不覆盖自部署或独立 Coding Plan/Token Plan。",
		MatchHosts: qwenBailianCapacityHosts, MatchAccountModes: []string{"payg"},
	},

	// Kimi Open Platform and Coding are separate products. Defaults such as
	// max_completion_tokens=131072 or 32768 are not independent output hard caps.
	{
		ModelID: "kimi-k3", Provider: "kimi", Product: "open_platform",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1048576, CapacityBasis: "total_context"},
		SourceURL:            kimiIntegerSource, SourceURLs: []string{"https://platform.kimi.com/docs/models"},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "1024*1024 - prompt_tokens",
		NormalizationBasis: "官方输出余额公式明确总窗口为 1024×1024=1048576；131072 是默认输出预算而非独立硬上限。",
		Conditions:         "Kimi 开放平台，不套用于 Coding 产品 k3。",
		MatchAccountModes:  []string{"payg"},
	},
	{
		ModelID: "kimi-k2.6", Provider: "kimi", Product: "open_platform",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 262144, CapacityBasis: "total_context"},
		SourceURL:            kimiIntegerSource, SourceURLs: []string{"https://platform.kimi.com/docs/models"},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "256*1024 - prompt_tokens",
		NormalizationBasis: "官方输出余额公式明确总窗口为 256×1024=262144；32768 是默认输出预算而非独立硬上限。",
		Conditions:         "Kimi 开放平台；不将已停用 K2/K2.5/moonshot-v1 旧名自动映射为该模型。",
		MatchAccountModes:  []string{"payg"},
	},
	{
		ModelID: "kimi-k2.7-code", Provider: "kimi", Product: "open_platform",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 262144, CapacityBasis: "total_context"},
		SourceURL:            "https://platform.kimi.ai/docs/guide/kimi-k2-7-code-quickstart", SourceURLs: []string{kimiIntegerSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "256K",
		NormalizationBasis: "型号官方规格为 256K，按同平台官方 256×1024 公式归一；不宣称该型号有独立输出硬上限整数证据。",
		Conditions:         "Kimi 开放平台；不同于 Coding 的 kimi-for-coding。",
		MatchAccountModes:  []string{"payg"},
	},
	{
		ModelID: "kimi-k2.7-code-highspeed", Provider: "kimi", Product: "open_platform",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 262144, CapacityBasis: "total_context"},
		SourceURL:            "https://platform.kimi.ai/docs/guide/kimi-k2-7-code-quickstart", SourceURLs: []string{kimiIntegerSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "256K",
		NormalizationBasis: "官方与 K2.7 Code 列为同模型、同 256K，按同平台官方 256×1024 公式归一；独立输出硬上限未知。",
		Conditions:         "Kimi 开放平台高速版，不使用 Coding 套餐权益推断容量。",
		MatchAccountModes:  []string{"payg"},
	},
	{
		ModelID: "kimi-for-coding", Provider: "kimi", Product: "coding",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 262144, CapacityBasis: "total_context"},
		SourceURL:            kimiCodingSource, SourceURLs: []string{"https://www.kimi.com/code/docs/kimi-code/models.html"},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "262144",
		Conditions:        "Coding 所有会员；当前对应 K2.7 Code。关闭 thinking 可路由至 K2.6，不改变账号现有路由。",
		MatchAccountModes: []string{"coding"},
	},
	{
		ModelID: "kimi-for-coding-highspeed", Provider: "kimi", Product: "coding",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 262144, CapacityBasis: "total_context"},
		SourceURL:            kimiCodingSource, SourceURLs: []string{"https://www.kimi.com/code/docs/kimi-code/models.html"},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "262144",
		Conditions:        "Coding Allegretto 及以上；容量记录不代表账号拥有该型号使用权。",
		MatchAccountModes: []string{"coding"},
	},
	{
		ModelID: "k3-256k", Provider: "kimi", Product: "coding",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 262144, CapacityBasis: "total_context"},
		SourceURL:            kimiCodingSource, SourceURLs: []string{"https://www.kimi.com/code/docs/kimi-code/models.html"},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "262144",
		Conditions:        "Coding Moderato 及以上；此 ID 在各可用套餐均固定 256K。",
		MatchAccountModes: []string{"coding"},
	},
	{
		ModelID: "k3", Provider: "kimi", Product: "coding",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 262144, CapacityBasis: "total_context"},
		SourceURL:            kimiCodingSource, SourceURLs: []string{"https://www.kimi.com/code/docs/kimi-code/models.html"},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "262144; 1048576",
		NormalizationBasis: "官方套餐表直接整数：Moderato=262144；Allegretto 及以上思考路径=1048576。权益/请求路径未知时采用共同下限 262144。",
		Conditions:         "仅 Coding；不把开放平台 kimi-k3 的 1M 套入；k3[1m] 是 Claude Code 客户端配置语法，不是 API model ID。",
		MatchAccountModes:  []string{"coding"},
	},

	// GLM: keep exact official integers separate from same-platform normalization.
	{
		ModelID: "glm-5.3", Provider: "zhipu", Product: "model_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxOutputTokens: 131072, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.bigmodel.cn/cn/guide/models/text/glm-5.3", SourceURLs: []string{glmOverviewSource, glmMillionSource, glmOutputSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "1M; 128K",
		NormalizationBasis: "上下文按同平台 GLM-5.2 官方整数配置 1000000 归一 1M；不是 GLM-5.3 直接整数原文。输出 131072 来自官方逐型号参数表。",
		Conditions:         "容量不代表任意端点均可用；Coding Plan 的协议限制保持原账号配置。",
	},
	{
		ModelID: "glm-5.3-flash", Provider: "zhipu", Product: "model_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxOutputTokens: 131072, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.bigmodel.cn/cn/guide/models/vlm/glm-5.3-flash", SourceURLs: []string{glmOverviewSource, glmMillionSource, glmOutputSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "1M; 128K",
		NormalizationBasis: "上下文按同平台 GLM-5.2 官方整数配置 1000000 归一 1M；不是 GLM-5.3-Flash 直接整数原文。输出 131072 来自官方逐型号参数表。",
		Conditions:         "容量不代表任意端点均可用；不改原生多模态或 Coding Plan 协议能力。",
	},
	{
		ModelID: "glm-5.2", Provider: "zhipu", Product: "model_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, MaxOutputTokens: 131072, CapacityBasis: "total_context"},
		SourceURL:            glmMillionSource, SourceURLs: []string{glmOverviewSource, glmOutputSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "1000000; 131072",
		NormalizationBasis: "官方 GLM-5.2 接入配置明确上下文整数，输出为逐型号参数表的硬上限。",
	},
	{
		ModelID: "glm-5.1", Provider: "zhipu", Product: "model_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 204800, MaxOutputTokens: 131072, CapacityBasis: "total_context"},
		SourceURL:            glm200KSource, SourceURLs: []string{glmOverviewSource, glmOutputSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "204800; 131072",
		NormalizationBasis: "官方按型号整数配置明确 204800；不采用旧客户端建议配置中的 200000。",
	},
	{
		ModelID: "glm-5", Provider: "zhipu", Product: "model_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 204800, MaxOutputTokens: 131072, CapacityBasis: "total_context"},
		SourceURL:            glm200KSource, SourceURLs: []string{glmOverviewSource, glmOutputSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "204800; 131072",
		NormalizationBasis: "官方按型号整数配置明确 204800；不采用旧客户端建议配置中的 200000。",
	},
	{
		ModelID: "glm-4.7", Provider: "zhipu", Product: "model_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 204800, MaxOutputTokens: 131072, CapacityBasis: "total_context"},
		SourceURL:            glm200KSource, SourceURLs: []string{glmOverviewSource, glmOutputSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "204800; 131072",
		NormalizationBasis: "官方按型号整数配置明确 204800；不采用旧客户端建议配置中的 200000。",
	},
	{
		ModelID: "glm-5-turbo", Provider: "zhipu", Product: "model_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 204800, MaxOutputTokens: 131072, CapacityBasis: "total_context"},
		SourceURL:            glmOverviewSource, SourceURLs: []string{glm200KSource, glmOutputSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "200K; 131072",
		NormalizationBasis: "上下文 200K 按同平台官方 GLM-5/5.1/4.7 整数 204800 归一；不是该型号独立上下文整数原文。输出为逐型号参数表整数。",
	},
	{
		ModelID: "glm-4.6", Provider: "zhipu", Product: "model_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 204800, MaxOutputTokens: 131072, CapacityBasis: "total_context"},
		SourceURL:            glmOverviewSource, SourceURLs: []string{glm200KSource, glmOutputSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "200K; 131072",
		NormalizationBasis: "上下文 200K 按同平台官方 GLM-5/5.1/4.7 整数 204800 归一；不是该型号独立上下文整数原文。输出为逐型号参数表整数。",
	},
	{
		ModelID: "glm-4.5", Provider: "zhipu", Product: "model_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 131072, MaxOutputTokens: 98304, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.z.ai/guides/llm/glm-4.5", SourceURLs: []string{glm200KSource, glmOutputSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "128K; 98304",
		NormalizationBasis: "官方 128K 上下文按同平台 200K=204800 的单位体系归一为 131072；输出 98304 为逐型号参数表整数。",
	},
	{
		ModelID: "glm-4.5-air", Provider: "zhipu", Product: "model_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 131072, MaxOutputTokens: 98304, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.z.ai/guides/llm/glm-4.5", SourceURLs: []string{glm200KSource, glmOutputSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "128K; 98304",
		NormalizationBasis: "官方 128K 上下文按同平台 200K=204800 的单位体系归一为 131072；输出 98304 为逐型号参数表整数。",
	},
	{
		ModelID: "glm-4.5-x", Provider: "zhipu", Product: "model_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 131072, MaxOutputTokens: 98304, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.z.ai/guides/llm/glm-4.5", SourceURLs: []string{glm200KSource, glmOutputSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "128K; 98304",
		NormalizationBasis: "官方 128K 上下文按同平台 200K=204800 的单位体系归一为 131072；输出 98304 为逐型号参数表整数。",
	},
	{
		ModelID: "glm-4.5-airx", Provider: "zhipu", Product: "model_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 131072, MaxOutputTokens: 98304, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.z.ai/guides/llm/glm-4.5", SourceURLs: []string{glm200KSource, "https://docs.z.ai/guides/overview/concept-param"},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "128K; 98304",
		NormalizationBasis: "官方 128K 上下文按同平台 200K=204800 的单位体系归一为 131072；输出 98304 为国际版逐型号参数表整数。",
	},
	{
		ModelID: "glm-4.5-flash", Provider: "zhipu", Product: "model_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 131072, MaxOutputTokens: 98304, CapacityBasis: "total_context"},
		SourceURL:            "https://docs.z.ai/guides/llm/glm-4.5", SourceURLs: []string{glm200KSource, glmOutputSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "128K; 98304",
		NormalizationBasis: "官方 128K 上下文按同平台 200K=204800 的单位体系归一为 131072；输出 98304 为逐型号参数表整数。",
	},

	// MiniMax model IDs deliberately preserve the official mixed case.
	{
		ModelID: "MiniMax-M3", Provider: "minimax", Product: "text_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1000000, CapacityBasis: "total_context"},
		SourceURL:            minimaxSource, VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "1,000,000",
		Conditions: "官方窗口为输入与输出总和；未公布的独立输入/输出硬上限留空。",
	},
	{
		ModelID: "MiniMax-M2.7", Provider: "minimax", Product: "text_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 204800, CapacityBasis: "total_context"},
		SourceURL:            minimaxSource, VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "204,800",
		Conditions: "官方窗口为输入与输出总和；未公布的独立输入/输出硬上限留空。",
	},
	{
		ModelID: "MiniMax-M2.7-highspeed", Provider: "minimax", Product: "text_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 204800, CapacityBasis: "total_context"},
		SourceURL:            minimaxSource, VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "204,800",
		Conditions: "官方高速版；未公布的独立输入/输出硬上限留空。",
	},
	{
		ModelID: "MiniMax-M2.5", Provider: "minimax", Product: "text_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 204800, CapacityBasis: "total_context"},
		SourceURL:            minimaxSource, VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "204,800",
		Conditions: "官方窗口为输入与输出总和；未公布的独立输入/输出硬上限留空。",
	},
	{
		ModelID: "MiniMax-M2.5-highspeed", Provider: "minimax", Product: "text_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 204800, CapacityBasis: "total_context"},
		SourceURL:            minimaxSource, VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "204,800",
		Conditions: "官方高速版；未公布的独立输入/输出硬上限留空。",
	},
	{
		ModelID: "MiniMax-M2.1", Provider: "minimax", Product: "text_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 204800, CapacityBasis: "total_context"},
		SourceURL:            minimaxSource, VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "204,800",
		Conditions: "官方窗口为输入与输出总和；未公布的独立输入/输出硬上限留空。",
	},
	{
		ModelID: "MiniMax-M2.1-highspeed", Provider: "minimax", Product: "text_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 204800, CapacityBasis: "total_context"},
		SourceURL:            minimaxSource, VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "204,800",
		Conditions: "官方高速版；未公布的独立输入/输出硬上限留空。",
	},
	{
		ModelID: "MiniMax-M2", Provider: "minimax", Product: "text_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 204800, CapacityBasis: "total_context"},
		SourceURL:            minimaxSource, VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "204,800",
		Conditions: "另页 128k 输出含 CoT 但无已核实整数依据；本行不从示例或第三方推定输出硬上限。",
	},

	// Doubao's accepted integers are explicitly labelled cross-page inference.
	// The answer-only and thinking limits are not collapsed into max_output_tokens.
	{
		ModelID: "doubao-seed-evolving", Provider: "doubao", Product: "ark_beijing_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 1048576, MaxInputTokens: 1048576, CapacityBasis: "total_context"},
		SourceURL:            doubaoModelsSource, SourceURLs: []string{doubaoIntegerSource, doubaoEndpointSource, "https://www.volcengine.com/docs/82379/2549861?lang=zh"},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "1024k; 1024k",
		NormalizationBasis: doubaoUnitsBasis,
		Conditions:         "仅核实北京方舟公开 API；滚动升级型号。最大回答/思维链分别给出，不合并为独立总输出上限。",
		MatchHosts:         doubaoBeijingCapacityHosts, MatchAccountModes: []string{"payg"},
	},
	{
		ModelID: "doubao-seed-2-1-pro-260628", Provider: "doubao", Product: "ark_beijing_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 262144, MaxInputTokens: 262144, CapacityBasis: "total_context"},
		SourceURL:            doubaoModelsSource, SourceURLs: []string{doubaoIntegerSource, doubaoEndpointSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "256k; 256k",
		NormalizationBasis: doubaoUnitsBasis,
		Conditions:         "仅核实北京方舟公开 API；精确版本后缀为 260628。最大回答/思维链不合并为总输出上限。",
		MatchHosts:         doubaoBeijingCapacityHosts, MatchAccountModes: []string{"payg"},
	},
	{
		ModelID: "doubao-seed-2-1-turbo-260628", Provider: "doubao", Product: "ark_beijing_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 262144, MaxInputTokens: 262144, CapacityBasis: "total_context"},
		SourceURL:            doubaoModelsSource, SourceURLs: []string{doubaoIntegerSource, doubaoEndpointSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "256k; 256k",
		NormalizationBasis: doubaoUnitsBasis,
		Conditions:         "仅核实北京方舟公开 API；精确版本后缀为 260628。最大回答/思维链不合并为总输出上限。",
		MatchHosts:         doubaoBeijingCapacityHosts, MatchAccountModes: []string{"payg"},
	},
	{
		ModelID: "doubao-seed-2-0-pro-260215", Provider: "doubao", Product: "ark_beijing_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 262144, MaxInputTokens: 229376, CapacityBasis: "total_context"},
		SourceURL:            doubaoModelsSource, SourceURLs: []string{doubaoIntegerSource, doubaoEndpointSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "256k; 224k",
		NormalizationBasis: doubaoUnitsBasis,
		Conditions:         "北京方舟往期模型规格；仅记录容量，不恢复或推断模型可用性。",
		MatchHosts:         doubaoBeijingCapacityHosts, MatchAccountModes: []string{"payg"},
	},
	{
		ModelID: "doubao-seed-2-0-lite-260215", Provider: "doubao", Product: "ark_beijing_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 262144, MaxInputTokens: 229376, CapacityBasis: "total_context"},
		SourceURL:            doubaoModelsSource, SourceURLs: []string{doubaoIntegerSource, doubaoEndpointSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "256k; 224k",
		NormalizationBasis: doubaoUnitsBasis,
		Conditions:         "北京方舟往期模型规格；仅记录容量，不恢复或推断模型可用性。",
		MatchHosts:         doubaoBeijingCapacityHosts, MatchAccountModes: []string{"payg"},
	},
	{
		ModelID: "doubao-seed-2-0-lite-260428", Provider: "doubao", Product: "ark_beijing_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 262144, MaxInputTokens: 229376, CapacityBasis: "total_context"},
		SourceURL:            doubaoModelsSource, SourceURLs: []string{doubaoIntegerSource, doubaoEndpointSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "256k; 224k",
		NormalizationBasis: doubaoUnitsBasis,
		Conditions:         "北京方舟往期模型规格；仅记录容量，不恢复或推断模型可用性。",
		MatchHosts:         doubaoBeijingCapacityHosts, MatchAccountModes: []string{"payg"},
	},
	{
		ModelID: "doubao-seed-2-0-mini-260215", Provider: "doubao", Product: "ark_beijing_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 262144, MaxInputTokens: 229376, CapacityBasis: "total_context"},
		SourceURL:            doubaoModelsSource, SourceURLs: []string{doubaoIntegerSource, doubaoEndpointSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "256k; 224k",
		NormalizationBasis: doubaoUnitsBasis,
		Conditions:         "北京方舟往期模型规格；仅记录容量，不恢复或推断模型可用性。",
		MatchHosts:         doubaoBeijingCapacityHosts, MatchAccountModes: []string{"payg"},
	},
	{
		ModelID: "doubao-seed-2-0-mini-260428", Provider: "doubao", Product: "ark_beijing_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 262144, MaxInputTokens: 229376, CapacityBasis: "total_context"},
		SourceURL:            doubaoModelsSource, SourceURLs: []string{doubaoIntegerSource, doubaoEndpointSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "256k; 224k",
		NormalizationBasis: doubaoUnitsBasis,
		Conditions:         "北京方舟往期模型规格；仅记录容量，不恢复或推断模型可用性。",
		MatchHosts:         doubaoBeijingCapacityHosts, MatchAccountModes: []string{"payg"},
	},
	{
		ModelID: "doubao-seed-2-0-code-preview-260215", Provider: "doubao", Product: "ark_beijing_api",
		ModelContextCapacity: ModelContextCapacity{ContextWindow: 262144, MaxInputTokens: 229376, CapacityBasis: "total_context"},
		SourceURL:            doubaoModelsSource, SourceURLs: []string{doubaoIntegerSource, doubaoEndpointSource},
		VerifiedAt: officialModelContextCapacityVerifiedAt, OriginalText: "256k; 224k",
		NormalizationBasis: doubaoUnitsBasis,
		Conditions:         "北京方舟往期 Preview 模型规格；仅记录容量，不恢复或推断模型可用性。",
		MatchHosts:         doubaoBeijingCapacityHosts, MatchAccountModes: []string{"payg"},
	},
}
