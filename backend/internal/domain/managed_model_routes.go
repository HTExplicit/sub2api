package domain

// ManagedModelRoutesConfig records the verified routes published for one group.
// The zero value represents an unmanaged group. Enabled configurations are
// enforced by the gateway even when Routes is empty.
type ManagedModelRoutesConfig struct {
	Version int                 `json:"version"`
	Enabled bool                `json:"enabled"`
	Routes  []ManagedModelRoute `json:"routes,omitempty"`
}

// ManagedModelRoute binds a public model to a group-specific internal selector.
type ManagedModelRoute struct {
	PublicModel    string                     `json:"public_model"`
	Aliases        []string                   `json:"aliases,omitempty"`
	Selector       string                     `json:"selector"`
	TargetPlatform string                     `json:"target_platform"`
	Endpoints      []string                   `json:"endpoints"`
	Accounts       []ManagedModelRouteAccount `json:"accounts"`
}

// ManagedModelRouteAccount identifies one verified account/model combination.
// Fingerprints and account membership are internal/admin-only information.
type ManagedModelRouteAccount struct {
	AccountID          int64    `json:"account_id"`
	UpstreamModel      string   `json:"upstream_model"`
	AccountFingerprint string   `json:"account_fingerprint"`
	Endpoints          []string `json:"endpoints"`
}
