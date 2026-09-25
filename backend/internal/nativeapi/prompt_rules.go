package nativeapi

import "encoding/json"

// PromptRule references immutable template content. Account bindings contain
// only rule IDs; customer history and account credentials are never policy RPCs.
type PromptRule struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Enabled              bool     `json:"enabled"`
	TemplateID           int64    `json:"template_id"`
	VersionID            int64    `json:"version_id"`
	Role                 string   `json:"role"`
	Platforms            []string `json:"platforms"`
	AccountTypes         []string `json:"account_types,omitempty"`
	ExcludeModelContains []string `json:"exclude_model_contains,omitempty"`
	RequestProfiles      []string `json:"request_profiles,omitempty"`
	// Deprecated import fields. V2 rules always pin immutable content and use Role.
	FollowActive bool     `json:"follow_active,omitempty"`
	Order        int      `json:"order"`
	Delivery     string   `json:"delivery,omitempty"`
	Position     string   `json:"position"`
	ModelMatch   string   `json:"model_match"`
	Models       []string `json:"models"`
}

type PromptRulePolicy struct {
	Version        int          `json:"version"`
	Rules          []PromptRule `json:"rules"`
	DefaultRuleIDs []string     `json:"default_rule_ids"`
}

type PromptAccountBinding struct {
	Mode    string   `json:"mode"`
	RuleIDs []string `json:"rule_ids"`
}

type ResolvedPromptRule struct {
	Rule              PromptRule      `json:"rule"`
	Body              string          `json:"body"`
	SHA256            string          `json:"sha256"`
	PreserveEcho      bool            `json:"preserve_echo,omitempty"`
	Unavailable       bool            `json:"unavailable,omitempty"`
	ContentFormat     string          `json:"content_format,omitempty"`
	StructuredContent json.RawMessage `json:"structured_content,omitempty"`
}

type PromptRulePlacement struct {
	PreserveEcho      bool            `json:"preserve_echo,omitempty"`
	RuleID            string          `json:"rule_id"`
	TemplateID        int64           `json:"template_id"`
	VersionID         int64           `json:"version_id"`
	Delivery          string          `json:"delivery,omitempty"`
	Protocol          string          `json:"protocol"`
	Position          string          `json:"position"`
	Carrier           string          `json:"carrier"`
	Role              string          `json:"role,omitempty"`
	Body              string          `json:"body"`
	SHA256            string          `json:"sha256"`
	ContentFormat     string          `json:"content_format,omitempty"`
	StructuredContent json.RawMessage `json:"structured_content,omitempty"`
	Index             *int            `json:"index,omitempty"`
	BlockIndex        *int            `json:"block_index,omitempty"`
}

// PromptProtocolCapability is shared by the editor and policy validation.
// ConversationSystemModels is an explicit capability allowlist, not a model selector.
type PromptProtocolCapability struct {
	Protocol                 string              `json:"protocol"`
	Platforms                []string            `json:"platforms"`
	Roles                    []string            `json:"roles"`
	PositionsByRole          map[string][]string `json:"positions_by_role"`
	Limitations              []string            `json:"limitations"`
	ConversationSystemModels []string            `json:"conversation_system_models,omitempty"`
}

type PromptRuleDecision struct {
	RuleID string `json:"rule_id"`
	Reason string `json:"reason"`
}

type PromptRulesPlan struct {
	SHA256     string                `json:"sha256"`
	Placements []PromptRulePlacement `json:"placements"`
	Skipped    []PromptRuleDecision  `json:"skipped"`
}

const (
	PromptRulePolicyVersion            = 2
	PromptRoleAuto                     = "auto"
	PromptRoleSystem                   = "system"
	PromptRoleDeveloper                = "developer"
	PromptContentAnthropicSystemBlocks = "anthropic_system_blocks"
	PromptDeliveryNative               = "native_control"
	PromptDeliverySystem               = "system"
	PromptDeliveryDeveloper            = "developer"
	PromptPositionControlPrepend       = "control_prepend"
	PromptPositionControlAppend        = "control_append"
	PromptPositionConversationHead     = "conversation_head"
	PromptPositionConversationTail     = "conversation_tail"
	PromptPositionBeforeLastUser       = "before_last_user"
	PromptPositionAfterLastUser        = "after_last_user"
	// The configuration contains rules for different providers. Its capacity
	// must accommodate the union of the former independent prompt domains.
	PromptRulesMaxCount = 64
	PromptRulesMaxBytes = 256 << 10
)
