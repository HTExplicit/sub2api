package extensionv1

// Publication actions are policy inputs. The host remains responsible for
// authorization, content integrity and the durable atomic switch.
const (
	PublicationActionPublish  = "publish"
	PublicationActionRollback = "rollback"
)

type PromptPublicationVersionSummary struct {
	ID                   int64  `json:"id"`
	Version              int64  `json:"version"`
	SHA256               string `json:"sha256"`
	ByteLength           int    `json:"byte_length"`
	CompositionMode      string `json:"composition_mode"`
	BundleID             string `json:"bundle_id,omitempty"`
	BundleManifestSHA256 string `json:"bundle_manifest_sha256,omitempty"`
}

type PromptPublicationPolicyRequest struct {
	Action           string                          `json:"action"`
	ManagedSource    string                          `json:"managed_source,omitempty"`
	CurrentVersionID int64                           `json:"current_version_id,omitempty"`
	Target           PromptPublicationVersionSummary `json:"target"`
}

type PromptPublicationPolicyPlan struct {
	Action      string            `json:"action"`
	Allowed     bool              `json:"allowed"`
	ReasonCode  string            `json:"reason_code,omitempty"`
	Composition PromptComposition `json:"composition"`
}

type SkillPublicationBundleSummary struct {
	BundleVersionID       int64  `json:"bundle_version_id"`
	PromptVersionID       int64  `json:"prompt_version_id"`
	UpstreamSourceID      string `json:"upstream_source_id"`
	RawTreeSHA256         string `json:"raw_tree_sha256"`
	EffectiveTreeSHA256   string `json:"effective_tree_sha256"`
	RawPromptSHA256       string `json:"raw_prompt_sha256"`
	EffectivePromptSHA256 string `json:"effective_prompt_sha256"`
	FileCount             int    `json:"file_count"`
}

type SkillPublicationPolicyRequest struct {
	Action                 string                        `json:"action"`
	CurrentBundleVersionID int64                         `json:"current_bundle_version_id,omitempty"`
	Target                 SkillPublicationBundleSummary `json:"target"`
}

type SkillPublicationPolicyPlan struct {
	Action     string `json:"action"`
	Allowed    bool   `json:"allowed"`
	ReasonCode string `json:"reason_code,omitempty"`
}

type SkillRegistryPolicyProfile struct {
	SourceID             string   `json:"source_id"`
	UpstreamRoot         string   `json:"upstream_root"`
	PublicRoot           string   `json:"public_root"`
	RequiredPaths        []string `json:"required_paths"`
	RequiredModules      []string `json:"required_modules"`
	ScriptExtensions     []string `json:"script_extensions"`
	BinaryExtensions     []string `json:"binary_extensions"`
	MaxFileCount         int      `json:"max_file_count"`
	MaxTotalBytes        int64    `json:"max_total_bytes"`
	StorageLayoutVersion int      `json:"storage_layout_version"`
}

type SkillStorageLayoutRequest struct {
	BundleVersionID       int64  `json:"bundle_version_id"`
	PromptVersionID       int64  `json:"prompt_version_id"`
	EffectiveTreeSHA256   string `json:"effective_tree_sha256"`
	EffectivePromptSHA256 string `json:"effective_prompt_sha256"`
}

type SkillStorageLayoutPlan struct {
	LayoutVersion int      `json:"layout_version"`
	Namespace     string   `json:"namespace"`
	CandidateKey  string   `json:"candidate_key"`
	Slots         []string `json:"slots"`
}
