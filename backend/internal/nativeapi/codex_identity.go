package nativeapi

// Profile fields are persisted by the host. Their generation and interpretation
// belong to the Codex domain process; no credential is part of this contract.
type CodexClientProfile struct {
	Source        string `json:"source,omitempty"`
	Originator    string `json:"originator,omitempty"`
	ClientVersion string `json:"client_version,omitempty"`
	Version       int    `json:"v"`
	OSType        string `json:"os_type"`
	OSVersion     string `json:"os_version"`
	Arch          string `json:"arch"`
	Terminal      string `json:"terminal"`
	Sandbox       string `json:"sandbox"`
	GeneratedAt   string `json:"generated_at,omitempty"`
}

type CodexIdentityQuery struct {
	Preset    string             `json:"preset,omitempty"`
	Seed      string             `json:"seed,omitempty"`
	Profile   CodexClientProfile `json:"profile,omitempty"`
	Version   string             `json:"version,omitempty"`
	UserAgent string             `json:"user_agent,omitempty"`
}

type CodexIdentityResult struct {
	DefaultFingerprintMode string             `json:"default_fingerprint_mode,omitempty"`
	Profile                CodexClientProfile `json:"profile,omitempty"`
	Valid                  bool               `json:"valid"`
	UserAgent              string             `json:"user_agent,omitempty"`
	Sandbox                string             `json:"sandbox,omitempty"`
}
