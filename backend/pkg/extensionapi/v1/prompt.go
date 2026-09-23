package extensionv1

import "time"

type BusinessSystemPromptSnapshot struct {
	RulePolicy                    *PromptRulePolicy    `json:"rule_policy,omitempty"`
	ResolvedRules                 []ResolvedPromptRule `json:"resolved_rules,omitempty"`
	Enabled                       bool                 `json:"enabled"`
	ExposeServerPrompt            bool                 `json:"expose_server_prompt"`
	CompactEnabled                bool                 `json:"compact_enabled"`
	TemplateID                    int64                `json:"template_id"`
	VersionID                     int64                `json:"version_id"`
	TemplateVersion               int64                `json:"template_version"`
	Revision                      int64                `json:"revision"`
	Body                          string               `json:"body,omitempty"`
	SHA256                        string               `json:"sha256"`
	ByteLength                    int                  `json:"byte_length"`
	CompositionMode               string               `json:"composition_mode"`
	BundleID                      string               `json:"bundle_id,omitempty"`
	BundleManifestSHA256          string               `json:"bundle_manifest_sha256,omitempty"`
	RegistryRevision              int64                `json:"registry_revision,omitempty"`
	RegistryRawTreeSHA256         string               `json:"registry_raw_tree_sha256,omitempty"`
	RegistryEffectiveTreeSHA256   string               `json:"registry_effective_tree_sha256,omitempty"`
	RegistryPromptRawSHA256       string               `json:"registry_prompt_raw_sha256,omitempty"`
	RegistryPromptEffectiveSHA256 string               `json:"registry_prompt_effective_sha256,omitempty"`
	RegistryUpstreamSourceID      string               `json:"registry_upstream_source_id,omitempty"`
	RegistryUpstreamRoot          string               `json:"registry_upstream_root,omitempty"`
	RegistryPublicRoot            string               `json:"registry_public_root,omitempty"`
	BundleAvailable               bool                 `json:"bundle_available"`
	BundleDegraded                bool                 `json:"bundle_degraded"`
	DegradedReason                string               `json:"degraded_reason,omitempty"`
	Degraded                      bool                 `json:"degraded"`
	UpdatedAt                     time.Time            `json:"updated_at"`

	// Effective fields are attached to an immutable request-scoped copy. They
	// are never persisted or returned by the runtime API.
	BaseSHA256          string `json:"-"`
	EffectiveSHA256     string `json:"-"`
	EffectiveByteLength int    `json:"-"`
}

type BusinessSystemPromptTarget struct {
	RequestedModel   string `json:"requested_model,omitempty"`
	UpstreamModel    string `json:"upstream_model,omitempty"`
	ProviderPlatform string `json:"provider_platform,omitempty"`
	ProviderProfile  string `json:"provider_profile,omitempty"`
	BindingJSON      string `json:"binding_json,omitempty"`
	AccountID        int64  `json:"account_id,omitempty"`
	Platform         string `json:"platform"`
	AccountType      string `json:"account_type,omitempty"`
	Protocol         string `json:"protocol"`
	Compact          bool   `json:"compact"`
}

type BusinessSystemPromptApplication struct {
	RulesPlan                   *PromptRulesPlan `json:"rules_plan,omitempty"`
	OriginalInstructionsExists  bool             `json:"-"`
	FinalInstructions           string           `json:"-"`
	PublicInstructions          string           `json:"-"`
	PublicInstructionsExists    bool             `json:"-"`
	PreserveInstructionsEcho    bool             `json:"preserve_instructions_echo,omitempty"`
	Applied                     bool             `json:"applied"`
	Carrier                     string           `json:"carrier"`
	ClientInstructions          string           `json:"client_instructions"`
	ServerInstructions          string           `json:"server_instructions"`
	ExposeServerPrompt          bool             `json:"expose_server_prompt"`
	CompactEnabled              bool             `json:"compact_enabled"`
	TemplateID                  int64            `json:"template_id"`
	VersionID                   int64            `json:"version_id"`
	TemplateVersion             int64            `json:"template_version"`
	Revision                    int64            `json:"revision"`
	SHA256                      string           `json:"sha256"`
	BaseSHA256                  string           `json:"base_sha256,omitempty"`
	EffectiveSHA256             string           `json:"effective_sha256,omitempty"`
	EffectiveByteLength         int              `json:"effective_byte_length,omitempty"`
	CompositionMode             string           `json:"composition_mode,omitempty"`
	BundleID                    string           `json:"bundle_id,omitempty"`
	BundleManifestSHA256        string           `json:"bundle_manifest_sha256,omitempty"`
	BundleRevision              int64            `json:"bundle_revision,omitempty"`
	BundleRawTreeSHA256         string           `json:"bundle_raw_tree_sha256,omitempty"`
	BundleEffectiveTreeSHA256   string           `json:"bundle_effective_tree_sha256,omitempty"`
	BundlePromptRawSHA256       string           `json:"bundle_prompt_raw_sha256,omitempty"`
	BundlePromptEffectiveSHA256 string           `json:"bundle_prompt_effective_sha256,omitempty"`
	BundleUpstreamSourceID      string           `json:"bundle_upstream_source_id,omitempty"`
	BundleUpstreamRoot          string           `json:"bundle_upstream_root,omitempty"`
	BundlePublicRoot            string           `json:"bundle_public_root,omitempty"`
	Degraded                    bool             `json:"degraded,omitempty"`
}

type PromptPlanRequest struct {
	Snapshot            BusinessSystemPromptSnapshot `json:"snapshot"`
	Target              BusinessSystemPromptTarget   `json:"target"`
	HasInstructions     bool                         `json:"has_instructions"`
	BaseSHA256          string                       `json:"base_sha256,omitempty"`
	EffectiveSHA256     string                       `json:"effective_sha256,omitempty"`
	EffectiveByteLength int                          `json:"effective_byte_length,omitempty"`
}
