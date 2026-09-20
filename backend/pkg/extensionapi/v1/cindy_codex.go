package extensionv1

// CindyCodexModel is a typed presentation plan for the supported native Codex
// descriptor. The host supplies source isolation and native base instructions.
type CindyCodexReasoningEffort struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

type CindyCodexTruncationPolicy struct {
	Mode  string `json:"mode"`
	Limit int64  `json:"limit"`
}

// CindyCodexModel mirrors every serialized field in the official
// openai/codex rust-v0.147.0 ModelInfo. Fields represented by Rust Option are
// deliberately emitted as null to keep the local manifest stable and explicit.
type CindyCodexModel struct {
	Slug                              string                      `json:"slug"`
	DisplayName                       string                      `json:"display_name"`
	Description                       *string                     `json:"description"`
	DefaultReasoningLevel             *string                     `json:"default_reasoning_level"`
	SupportedReasoningLevels          []CindyCodexReasoningEffort `json:"supported_reasoning_levels"`
	ShellType                         string                      `json:"shell_type"`
	Visibility                        string                      `json:"visibility"`
	SupportedInAPI                    bool                        `json:"supported_in_api"`
	Priority                          int                         `json:"priority"`
	AdditionalSpeedTiers              []string                    `json:"additional_speed_tiers"`
	ServiceTiers                      []any                       `json:"service_tiers"`
	DefaultServiceTier                any                         `json:"default_service_tier"`
	AvailabilityNUX                   any                         `json:"availability_nux"`
	Upgrade                           any                         `json:"upgrade"`
	BaseInstructions                  string                      `json:"base_instructions"`
	ModelMessages                     any                         `json:"model_messages"`
	IncludeSkillsUsageInstructions    bool                        `json:"include_skills_usage_instructions"`
	IncludePluginUsageInstructions    bool                        `json:"include_plugin_usage_instructions"`
	IncludeAppsUsageInstructions      bool                        `json:"include_apps_usage_instructions"`
	SupportsReasoningSummaryParameter bool                        `json:"supports_reasoning_summary_parameter"`
	DefaultReasoningSummary           string                      `json:"default_reasoning_summary"`
	SupportVerbosity                  bool                        `json:"support_verbosity"`
	DefaultVerbosity                  any                         `json:"default_verbosity"`
	ApplyPatchToolType                any                         `json:"apply_patch_tool_type"`
	WebSearchToolType                 string                      `json:"web_search_tool_type"`
	TruncationPolicy                  CindyCodexTruncationPolicy  `json:"truncation_policy"`
	SupportsParallelToolCalls         bool                        `json:"supports_parallel_tool_calls"`
	SupportsImageDetailOriginal       bool                        `json:"supports_image_detail_original"`
	ContextWindow                     *int                        `json:"context_window"`
	MaxContextWindow                  *int                        `json:"max_context_window"`
	AutoCompactTokenLimit             *int64                      `json:"auto_compact_token_limit"`
	CompHash                          any                         `json:"comp_hash"`
	EffectiveContextWindowPercent     int                         `json:"effective_context_window_percent"`
	ExperimentalSupportedTools        []string                    `json:"experimental_supported_tools"`
	InputModalities                   []string                    `json:"input_modalities"`
	SupportsSearchTool                bool                        `json:"supports_search_tool"`
	UseResponsesLite                  bool                        `json:"use_responses_lite"`
	AutoReviewModelOverride           any                         `json:"auto_review_model_override"`
	ModelSpecialty                    any                         `json:"model_specialty"`
	ToolMode                          any                         `json:"tool_mode"`
	MultiAgentVersion                 any                         `json:"multi_agent_version"`
}
