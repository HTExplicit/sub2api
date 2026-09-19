package extensionv1

type PromptComposition struct {
	Mode                 string `json:"composition_mode"`
	BundleID             string `json:"bundle_id,omitempty"`
	BundleManifestSHA256 string `json:"bundle_manifest_sha256,omitempty"`
}

type PromptSeed struct {
	Slug                 string
	Name                 string
	Description          string
	ManagedSource        string
	Body                 string
	Note                 string
	SHA256               string
	ByteLength           int
	CompositionMode      string
	BundleID             string
	BundleManifestSHA256 string
	SourceRepository     string
	SourceCommit         string
	SourceVersion        string
	SourceArtifact       string
	SourceArtifactSHA256 string
	SourceLicenseSHA256  string
	UpgradeExistingSeed  bool
	AutoActivateFromSHA  []string
}

type PromptSourceCandidate struct {
	ManagedSource        string `json:"managed_source"`
	SourceRepository     string `json:"source_repository"`
	SourceCommit         string `json:"source_commit"`
	SourceVersion        string `json:"source_version"`
	SourceArtifact       string `json:"source_artifact"`
	SourceArtifactSHA256 string `json:"source_artifact_sha256"`
	SourceLicenseSHA256  string `json:"source_license_sha256"`
	Body                 string `json:"-"`
	SHA256               string `json:"sha256"`
	ByteLength           int    `json:"byte_length"`
}
type PromptSourceEnvelope struct {
	Candidate PromptSourceCandidate `json:"candidate"`
	Body      string                `json:"body"`
}

type SkillPromptCapture struct {
	RawBody         []byte `json:"raw_body"`
	EffectiveBody   []byte `json:"effective_body"`
	RawSHA256       string `json:"raw_sha256"`
	EffectiveSHA256 string `json:"effective_sha256"`
	Diff            string `json:"diff"`
}

type PromptTemplatePolicyRequest struct {
	Action        string  `json:"action"`
	ManagedSource string  `json:"managed_source"`
	Slug          string  `json:"slug"`
	Name          *string `json:"name"`
	Description   *string `json:"description"`
}

type PromptTemplatePolicyPlan struct {
	Slug        string  `json:"slug"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

type SkillRegistryProfile struct {
	SourceID         string   `json:"source_id"`
	UpstreamRoot     string   `json:"upstream_root"`
	PublicRoot       string   `json:"public_root"`
	RequiredPaths    []string `json:"required_paths"`
	RequiredModules  []string `json:"required_modules"`
	ScriptExtensions []string `json:"script_extensions"`
	BinaryExtensions []string `json:"binary_extensions"`
}

type SkillManifest struct {
	SchemaVersion     int                  `json:"schema_version"`
	BundleID          string               `json:"bundle_id"`
	UpstreamSourceID  string               `json:"upstream_source_id"`
	UpstreamRoot      string               `json:"upstream_root"`
	ExpectedFileCount int                  `json:"expected_file_count"`
	UpstreamFileCount int                  `json:"upstream_file_count"`
	PinnedFileCount   int                  `json:"pinned_file_count"`
	Files             []SkillManifestEntry `json:"files"`
}
type SkillManifestEntry struct {
	Path         string                `json:"path"`
	SourceKind   string                `json:"source_kind"`
	EmbeddedPath string                `json:"embedded_path,omitempty"`
	ByteLength   int                   `json:"byte_length"`
	SHA256       string                `json:"sha256"`
	Provenance   *SkillAssetProvenance `json:"provenance,omitempty"`
}
type SkillAssetProvenance struct {
	SourceCommit             string `json:"source_commit"`
	HistoricalManifestSHA256 string `json:"historical_manifest_sha256"`
	HistoricalArchiveSHA256  string `json:"historical_archive_sha256"`
}
type SkillSeedChunkRequest struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}
type SkillSeedChunk struct {
	Data       []byte `json:"data"`
	TotalBytes int    `json:"total_bytes"`
}
type SkillFileInspection struct {
	Path   string `json:"path"`
	Prefix []byte `json:"prefix"`
	UTF8   bool   `json:"utf8"`
}
type SkillFilePlan struct {
	Kind        string `json:"kind"`
	ReplaceFrom string `json:"replace_from,omitempty"`
	ReplaceTo   string `json:"replace_to,omitempty"`
}
type SkillTreeEntry struct {
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
	UTF8   bool   `json:"utf8"`
}
type SkillTreeCheck struct {
	Entries []SkillTreeEntry `json:"entries"`
	Current bool             `json:"current"`
}
