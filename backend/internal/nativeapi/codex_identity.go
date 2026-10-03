package nativeapi

// Profile fields are persisted in accounts.extra. codexruntime/profile generates
// and interprets them; no credential is part of a profile.
type CodexClientProfile struct {
	Version     int    `json:"v"`
	OSType      string `json:"os_type"`
	OSVersion   string `json:"os_version"`
	Arch        string `json:"arch"`
	Terminal    string `json:"terminal"`
	Sandbox     string `json:"sandbox"`
	GeneratedAt string `json:"generated_at,omitempty"`
}
