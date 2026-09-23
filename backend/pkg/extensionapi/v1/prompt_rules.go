package extensionv1

// PromptRule references immutable template content. Account bindings contain
// only rule IDs; customer history and account credentials are never policy RPCs.
type PromptRule struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Enabled      bool     `json:"enabled"`
	TemplateID   int64    `json:"template_id"`
	VersionID    int64    `json:"version_id"`
	FollowActive bool     `json:"follow_active,omitempty"`
	Order        int      `json:"order"`
	Delivery     string   `json:"delivery"`
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
	Rule         PromptRule `json:"rule"`
	Body         string     `json:"body"`
	SHA256       string     `json:"sha256"`
	PreserveEcho bool       `json:"preserve_echo,omitempty"`
}

type PromptRulePlacement struct {
	PreserveEcho bool   `json:"preserve_echo,omitempty"`
	RuleID       string `json:"rule_id"`
	TemplateID   int64  `json:"template_id"`
	VersionID    int64  `json:"version_id"`
	Delivery     string `json:"delivery"`
	Position     string `json:"position"`
	Carrier      string `json:"carrier"`
	Role         string `json:"role,omitempty"`
	Body         string `json:"body"`
	SHA256       string `json:"sha256"`
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
	PromptDeliveryNative           = "native_control"
	PromptDeliverySystem           = "system"
	PromptDeliveryDeveloper        = "developer"
	PromptPositionControlPrepend   = "control_prepend"
	PromptPositionControlAppend    = "control_append"
	PromptPositionConversationHead = "conversation_head"
	PromptPositionConversationTail = "conversation_tail"
	PromptRulesMaxCount            = 32
	PromptRulesMaxBytes            = 256 << 10
)
