// Versioned model-reference contracts shared by the host and domain plugins.
package extensionv1

import "context"

const (
	ModelContextCapacityBasisTotal         = "total_context"
	ModelContextCapacityBasisInput         = "input_limit"
	ModelContextCapacityBasisMaximum       = "max_context_window"
	MaxModelContextTokens            int64 = 9007199254740991
)

type ModelContextCapacity struct {
	ContextWindow    int64  `json:"context_window,omitempty"`
	MaxContextWindow int64  `json:"max_context_window,omitempty"`
	MaxInputTokens   int64  `json:"max_input_tokens,omitempty"`
	MaxOutputTokens  int64  `json:"max_output_tokens,omitempty"`
	CapacityBasis    string `json:"capacity_basis,omitempty"`
	ObservedAt       string `json:"observed_at,omitempty"`
}

type ModelContextCapacityReference struct {
	Product          string `json:"product"`
	SourceURL        string `json:"source_url"`
	Release          string `json:"release"`
	VerifiedAt       string `json:"verified_at"`
	ContextWindow    int64  `json:"context_window"`
	MaxContextWindow int64  `json:"max_context_window"`
}

type OfficialModelContextCapacity struct {
	ModelContextCapacity
	ModelID            string                         `json:"model_id"`
	Aliases            []string                       `json:"aliases,omitempty"`
	Provider           string                         `json:"provider"`
	Product            string                         `json:"product"`
	SourceURL          string                         `json:"source_url"`
	SourceURLs         []string                       `json:"source_urls,omitempty"`
	VerifiedAt         string                         `json:"verified_at"`
	OriginalText       string                         `json:"original_text"`
	NormalizationBasis string                         `json:"normalization_basis,omitempty"`
	Conditions         string                         `json:"conditions,omitempty"`
	Reference          *ModelContextCapacityReference `json:"reference,omitempty"`
	MatchHosts         []string                       `json:"-"`
	MatchAccountModes  []string                       `json:"-"`
}

type CatalogQuery struct {
	// AccountID comes from the host account being resolved. Zero preserves
	// non-account shared-reference queries; account lookups must carry their ID.
	AccountID         int64    `json:"account_id,omitempty"`
	Candidates        []string `json:"candidates"`
	Platform          string   `json:"platform"`
	AccountType       string   `json:"account_type"`
	AccountMode       string   `json:"account_mode,omitempty"`
	Scheme            string   `json:"scheme,omitempty"`
	Host              string   `json:"host,omitempty"`
	Port              string   `json:"port,omitempty"`
	Path              string   `json:"path,omitempty"`
	HasURLCredentials bool     `json:"has_url_credentials,omitempty"`
}
type CatalogMatch struct {
	Matched bool                          `json:"matched"`
	Entry   *OfficialModelContextCapacity `json:"entry,omitempty"`
}
type CatalogResolver interface {
	ResolveCatalog(context.Context, CatalogQuery) (CatalogMatch, error)
}
